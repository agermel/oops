package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	agentauth "oops/internal/agent/ai/auth"
	agentruntime "oops/internal/agent/runtime"
	"oops/internal/agent/runtime/model"
	"oops/internal/agent/runtime/session"
	"oops/internal/agent/runtime/skills"
	"oops/internal/auth"
	"oops/internal/config"
	"oops/internal/console"
	"oops/internal/httprate"
	"oops/internal/logutil"
	"oops/internal/mcp"
	"oops/internal/nodelet"
	"oops/internal/project"
	runtimestore "oops/internal/store/runtime"

	"go.uber.org/zap"
)

// NodeletClient 是中心端访问 oops-nodelet 的最小接口。
type NodeletClient interface {
	// Host 读取远端 Nodelet 所在机器信息。
	Host(context.Context, string, string) (nodelet.Host, error)

	// Containers 读取远端 Nodelet 上的容器列表。
	Containers(context.Context, string, string) ([]nodelet.Container, error)

	// InspectContainer 读取远端 Nodelet 上某个容器的详细信息（环境变量、端口等）。
	InspectContainer(context.Context, string, string, string) (nodelet.ContainerInspect, error)

	// ContainerLogs 读取远端 Nodelet 上某个容器的历史日志。
	ContainerLogs(context.Context, string, string, string, string) ([]nodelet.LogEntry, error)

	// ContainerLogsStream 读取远端 Nodelet 上某个容器的实时日志 SSE 流。
	ContainerLogsStream(context.Context, string, string, string, string) (io.ReadCloser, error)

	// ContainerExec 在远端 Nodelet 上执行容器内命令。
	ContainerExec(context.Context, string, string, string, []string) (nodelet.ExecResult, error)
}

// Options 保存中心端 API 服务依赖。
type Options struct {
	NodeletManager    *nodelet.NodeletManager
	NodeletClient     NodeletClient
	RuntimeStore      *runtimestore.Store
	LLMEnabled        bool
	LLMConfig         config.LLMConfig
	LLMCredentials    agentauth.CredentialStore
	LLMAuthContext    agentauth.AuthContext
	RunLimits         config.RunLimits
	UserStore         *auth.Store
	TokenService      *auth.TokenService
	TokenTTL          time.Duration
	HTTPRateLimiter   *httprate.Limiter
	TrustedProxyCIDRs []string
	ConsoleHub        *console.Hub
	PromptTemplates   []agentruntime.PromptTemplate
}

type serverClosePhase uint8

const (
	serverCloseWaitRequests serverClosePhase = iota
	serverCloseWaitStreams
	serverCloseRuns
	serverCloseMCP
	serverCloseHTTPRateLimiter
	serverCloseLoginLimiter
	serverCloseSkills
	serverCloseNodeletProber
	serverCloseRuntimeStore
	serverCloseConsoleHub
	serverCloseDone
)

// Server 保存中心端 API 服务运行所需的配置和依赖。
type Server struct {
	nodeletManager  *nodelet.NodeletManager
	nodeletProber   *nodelet.NodeletProber
	nodeletClient   NodeletClient
	llmClient       *model.Client
	llmConfig       config.LLMConfig
	skillStore      *skills.Store
	promptTemplates []agentruntime.PromptTemplate
	agentRuntime    *agentruntime.Runtime
	mcpManager      *mcp.Manager
	projectStore    *project.Store
	dsnStore        *project.DSNStore
	runtimeStore    *runtimestore.Store
	agentRepo       *session.Repository
	runManager      *runManager
	UserStore       *auth.Store
	TokenService    *auth.TokenService
	tokenTTL        time.Duration
	httpRateLimiter *httprate.Limiter
	loginLimiter    *loginLimiter
	consoleHub      *console.Hub
	ownsConsoleHub  bool

	lifecycleMu     sync.Mutex
	lifecycle       context.Context
	cancelLifecycle context.CancelFunc
	quiescing       bool
	requestWG       sync.WaitGroup
	streamWG        sync.WaitGroup
	quiesceOnce     sync.Once

	closeMu       sync.Mutex
	closeGate     chan struct{}
	closePhase    serverClosePhase
	closeErr      error
	closeStepDone chan struct{}
	closeStepErr  error
}

// NewFromConfigWithConsoleHub builds the API service with the process-owned
// console hub supplied by the composition root.
func NewFromConfigWithConsoleHub(cfg config.Config, consoleHub *console.Hub) (*Server, error) {
	runtimeStore, err := runtimestore.OpenRuntime()
	if err != nil {
		return nil, fmt.Errorf("open runtime store: %w", err)
	}

	nm, err := nodelet.NewNodeletManagerWithRuntime(runtimeStore)
	if err != nil {
		_ = runtimeStore.Close()
		return nil, fmt.Errorf("create nodelet manager: %w", err)
	}

	projectStore, err := project.NewStore(runtimeStore)
	if err != nil {
		_ = runtimeStore.Close()
		return nil, fmt.Errorf("create project store: %w", err)
	}

	dsnStore, err := project.NewDSNStore(runtimeStore)
	if err != nil {
		_ = runtimeStore.Close()
		return nil, fmt.Errorf("create container DSN store: %w", err)
	}
	httpRateLimiter, err := httprate.New(httprate.Options{TrustedProxyCIDRs: cfg.HTTP.TrustedProxyCIDRs})
	if err != nil {
		_ = runtimeStore.Close()
		return nil, fmt.Errorf("create HTTP rate limiter: %w", err)
	}

	promptTemplates := agentruntime.LoadPromptTemplates("config/prompts")
	for _, diagnostic := range promptTemplates.Diagnostics {
		logutil.Warn("agent prompt template: load",
			zap.String("code", string(diagnostic.Code)),
			zap.String("path", diagnostic.Path),
			zap.String("message", diagnostic.Message),
		)
	}

	s := New(Options{
		NodeletManager:  nm,
		NodeletClient:   nodelet.NewClient(nil),
		RuntimeStore:    runtimeStore,
		LLMEnabled:      cfg.LLM.Enabled,
		LLMConfig:       cfg.LLM,
		RunLimits:       cfg.Run,
		HTTPRateLimiter: httpRateLimiter,
		ConsoleHub:      consoleHub,
		PromptTemplates: promptTemplates.Templates,
	})

	mgr, err := mcp.NewManagerWithRuntimeAndConsole(runtimeStore, s.consoleHub, nil)
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), cfg.Run.WithDefaults().CloseTimeout)
		_ = s.Close(closeCtx)
		cancel()
		return nil, fmt.Errorf("create mcp manager: %w", err)
	}
	s.mcpManager = mgr
	s.projectStore = projectStore
	s.dsnStore = dsnStore

	// 用户认证。
	userStore, err := auth.NewStoreFromRuntime(runtimeStore)
	if err != nil {
		logutil.Error("auth: user store", zap.Error(err))
	}
	s.UserStore = userStore
	s.tokenTTL = 24 * time.Hour

	if userStore != nil && userStore.IsSetup() {
		s.TokenService = auth.NewTokenService(userStore.User.Password, s.tokenTTL)
	} else {
		// 尚未设置用户：使用随机密钥创建 TokenService，setup 完成后重建。
		ts, err := auth.NewTokenServiceRandom(s.tokenTTL)
		if err != nil {
			logutil.Error("auth: random token service", zap.Error(err))
		} else {
			s.TokenService = ts
		}
	}

	prober := nodelet.NewNodeletProber(nm)
	s.nodeletProber = prober
	prober.Start()

	return s, nil
}

// New 创建中心端 API 服务。
func New(options Options) *Server {
	if options.NodeletClient == nil {
		options.NodeletClient = nodelet.NewClient(nil)
	}
	lifecycle, cancelLifecycle := context.WithCancel(context.Background())
	consoleHub := options.ConsoleHub
	ownsConsoleHub := false
	if consoleHub == nil {
		consoleHub = console.NewHub()
		ownsConsoleHub = true
	}
	httpRateLimiter := options.HTTPRateLimiter
	if httpRateLimiter == nil {
		var err error
		httpRateLimiter, err = httprate.New(httprate.Options{TrustedProxyCIDRs: options.TrustedProxyCIDRs})
		if err != nil {
			logutil.Warn("api: invalid trusted proxy configuration", zap.Error(err))
			httpRateLimiter, _ = httprate.New(httprate.Options{})
		}
	}

	promptTemplates := append([]agentruntime.PromptTemplate(nil), options.PromptTemplates...)
	s := &Server{
		nodeletManager:  options.NodeletManager,
		nodeletClient:   options.NodeletClient,
		runtimeStore:    options.RuntimeStore,
		llmConfig:       options.LLMConfig,
		UserStore:       options.UserStore,
		TokenService:    options.TokenService,
		tokenTTL:        options.TokenTTL,
		lifecycle:       lifecycle,
		cancelLifecycle: cancelLifecycle,
		httpRateLimiter: httpRateLimiter,
		loginLimiter:    newLoginLimiter(nil),
		consoleHub:      consoleHub,
		ownsConsoleHub:  ownsConsoleHub,
		promptTemplates: promptTemplates,
	}
	if storage, err := session.NewFileStorage("data/agent-sessions"); err != nil {
		logutil.Warn("agent session: open store, falling back to memory-only", zap.Error(err))
		s.agentRepo = session.NewRepository(nil)
	} else {
		s.agentRepo = session.NewRepository(storage)
	}
	s.agentRuntime = agentruntime.NewRuntime(agentruntime.RuntimeOptions{
		Repo:            s.agentRepo,
		PromptTemplates: promptTemplates,
	})
	s.runManager = newRunManager(options.RunLimits)

	// 技能存储必须在模型客户端之前就绪，保证无 MCP 连接时也会注册 skill 工具。
	ss, err := skills.NewStore("config/skills")
	if err != nil {
		logutil.Warn("llm: skill store", zap.Error(err))
	} else {
		s.skillStore = ss
	}

	if options.LLMEnabled {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client, err := model.New(ctx, options.LLMConfig, model.Options{
			Credentials: options.LLMCredentials,
			AuthContext: options.LLMAuthContext,
		})
		if err != nil {
			// LLM 不可用时不影响其他功能，仅日志输出。
			logutil.Error("llm: create client", zap.Error(err))
		} else {
			s.llmClient = client
		}
	}

	return s
}

// Quiesce stops admission and active streams while the HTTP server drains.
func (s *Server) Quiesce() {
	s.quiesceOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.quiescing = true
		cancel := s.cancelLifecycle
		s.lifecycleMu.Unlock()
		if cancel != nil {
			cancel()
		}
		if s.runManager != nil {
			s.runManager.quiesce()
		}
	})
}

// Close releases the server-owned resources after Quiesce and HTTP shutdown.
func (s *Server) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.Quiesce()

	s.closeMu.Lock()
	if s.closeGate == nil {
		s.closeGate = make(chan struct{}, 1)
		s.closeGate <- struct{}{}
	}
	gate := s.closeGate
	s.closeMu.Unlock()

	select {
	case <-gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { gate <- struct{}{} }()

	return s.closeResources(ctx)
}

func (s *Server) closeResources(ctx context.Context) error {
	for s.closePhase != serverCloseDone {
		if err := ctx.Err(); err != nil {
			return errors.Join(s.closeErr, err)
		}

		switch s.closePhase {
		case serverCloseWaitRequests:
			if err := s.waitForRequests(ctx); err != nil {
				return errors.Join(s.closeErr, err)
			}
		case serverCloseWaitStreams:
			if err := s.waitForStreams(ctx); err != nil {
				return errors.Join(s.closeErr, err)
			}
		case serverCloseRuns:
			if s.runManager != nil {
				if err := s.runManager.close(ctx); err != nil {
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						return errors.Join(s.closeErr, err)
					}
					s.closeErr = errors.Join(s.closeErr, err)
				}
			}
		case serverCloseMCP:
			if s.mcpManager != nil {
				if err := s.mcpManager.Shutdown(ctx); err != nil {
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						return errors.Join(s.closeErr, err)
					}
					s.closeErr = errors.Join(s.closeErr, err)
				}
			}
		case serverCloseHTTPRateLimiter:
			if s.httpRateLimiter != nil {
				s.httpRateLimiter.Close()
			}
		case serverCloseLoginLimiter:
			if s.loginLimiter != nil {
				s.loginLimiter.Close()
			}
		case serverCloseSkills:
			if s.skillStore != nil {
				if err := s.runCloseStep(ctx, func() error {
					s.skillStore.Close()
					return nil
				}); err != nil {
					return errors.Join(s.closeErr, err)
				}
			}
		case serverCloseNodeletProber:
			if s.nodeletProber != nil {
				if err := s.runCloseStep(ctx, func() error {
					s.nodeletProber.Stop()
					return nil
				}); err != nil {
					return errors.Join(s.closeErr, err)
				}
			}
		case serverCloseRuntimeStore:
			if s.runtimeStore != nil {
				if err := s.runCloseStep(ctx, s.runtimeStore.Close); err != nil {
					if ctx.Err() != nil {
						return errors.Join(s.closeErr, err)
					}
					s.closeErr = errors.Join(s.closeErr, err)
				}
			}
		case serverCloseConsoleHub:
			if s.ownsConsoleHub && s.consoleHub != nil {
				s.consoleHub.Close()
			}
		}
		s.closePhase++
	}
	return s.closeErr
}

// runCloseStep starts one blocking resource close at most once. A caller
// deadline stops only the wait; a later Close call resumes on the same step.
func (s *Server) runCloseStep(ctx context.Context, closeFn func() error) error {
	if s.closeStepDone == nil {
		done := make(chan struct{})
		s.closeStepDone = done
		go func() {
			s.closeStepErr = closeFn()
			close(done)
		}()
	}

	select {
	case <-s.closeStepDone:
		err := s.closeStepErr
		s.closeStepDone = nil
		s.closeStepErr = nil
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) beginRequest(requestCtx context.Context) (context.Context, func(), bool) {
	s.lifecycleMu.Lock()
	if s.quiescing {
		s.lifecycleMu.Unlock()
		return nil, nil, false
	}
	lifecycle := s.lifecycle
	s.requestWG.Add(1)
	s.lifecycleMu.Unlock()

	if requestCtx == nil {
		requestCtx = context.Background()
	}
	if lifecycle == nil {
		lifecycle = context.Background()
	}
	requestCtx, cancel := context.WithCancel(requestCtx)
	stopLifecycleCancel := context.AfterFunc(lifecycle, cancel)
	return requestCtx, func() {
		stopLifecycleCancel()
		cancel()
		s.requestWG.Done()
	}, true
}

func (s *Server) beginStream(requestCtx context.Context) (context.Context, func(), bool) {
	s.lifecycleMu.Lock()
	if s.quiescing {
		s.lifecycleMu.Unlock()
		return nil, nil, false
	}
	lifecycle := s.lifecycle
	s.streamWG.Add(1)
	s.lifecycleMu.Unlock()

	if lifecycle == nil {
		lifecycle = context.Background()
	}
	streamCtx, cancel := context.WithCancel(requestCtx)
	stopLifecycleCancel := context.AfterFunc(lifecycle, cancel)
	return streamCtx, func() {
		stopLifecycleCancel()
		cancel()
		s.streamWG.Done()
	}, true
}

func (s *Server) waitForStreams(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.streamWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) waitForRequests(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.requestWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
