package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"oops/internal/auth"
	"oops/internal/config"
	"oops/internal/console"
	"oops/internal/httprate"
	"oops/internal/llm/agent"
	runtimesession "oops/internal/llm/runtime/session"
	"oops/internal/llm/skills"
	llmtools "oops/internal/llm/tools"
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
	RunLimits         config.RunLimits
	UserStore         *auth.Store
	TokenService      *auth.TokenService
	TokenTTL          time.Duration
	HTTPRateLimiter   *httprate.Limiter
	TrustedProxyCIDRs []string
	ConsoleHub        *console.Hub
}

// Server 保存中心端 API 服务运行所需的配置和依赖。
type Server struct {
	nodeletManager  *nodelet.NodeletManager
	nodeletProber   *nodelet.NodeletProber
	nodeletClient   NodeletClient
	llmClient       *agent.Client
	llmConfig       config.LLMConfig
	skillStore      *skills.SkillStore
	mcpManager      *mcp.Manager
	projectStore    *project.Store
	dsnStore        *project.DSNStore
	runtimeStore    *runtimestore.Store
	agentRepo       *runtimesession.Repository
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

	closeMu   sync.Mutex
	closing   bool
	closeDone chan struct{}
	closeErr  error
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

	s := New(Options{
		NodeletManager:  nm,
		NodeletClient:   nodelet.NewClient(nil),
		RuntimeStore:    runtimeStore,
		LLMEnabled:      cfg.LLM.Enabled,
		LLMConfig:       cfg.LLM,
		RunLimits:       cfg.Run,
		HTTPRateLimiter: httpRateLimiter,
		ConsoleHub:      consoleHub,
	})

	// MCP Manager 在 Server 创建后初始化，onChange 回调可引用 s.llmClient。
	mgr, err := mcp.NewManagerWithRuntimeAndConsole(runtimeStore, s.consoleHub, func(mcpTools []mcp.ConnectionTool) {
		s.onMCPToolsChanged(mcpTools)
	})
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
		closeDone:       make(chan struct{}),
		httpRateLimiter: httpRateLimiter,
		loginLimiter:    newLoginLimiter(nil),
		consoleHub:      consoleHub,
		ownsConsoleHub:  ownsConsoleHub,
	}
	if storage, err := runtimesession.NewFileStorage("data/agent-sessions"); err != nil {
		logutil.Warn("agent session: open store, falling back to memory-only", zap.Error(err))
		s.agentRepo = runtimesession.NewRepository(nil)
	} else {
		s.agentRepo = runtimesession.NewRepository(storage)
	}
	s.runManager = newRunManager(options.RunLimits)

	// SkillStore 必须在 LLM Client 之前就绪，保证无 MCP 连接时也会注册 skill 工具。
	ss, err := skills.NewSkillStore("config/skills")
	if err != nil {
		logutil.Warn("llm: skill store", zap.Error(err))
	} else {
		s.skillStore = ss
	}

	if options.LLMEnabled {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		nativeTools, err := llmtools.NewTools(s, s.skillStore)
		if err != nil {
			logutil.Error("llm: create tools", zap.Error(err))
			return s
		}

		client, err := agent.NewClient(ctx, options.LLMConfig, nativeTools)
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
	if !s.closing {
		s.closing = true
		if s.closeDone == nil {
			s.closeDone = make(chan struct{})
		}
		go s.closeResources()
	}
	done := s.closeDone
	s.closeMu.Unlock()

	select {
	case <-done:
		s.closeMu.Lock()
		err := s.closeErr
		s.closeMu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) closeResources() {
	var closeErr error
	if err := s.waitForRequests(context.Background()); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if err := s.waitForStreams(context.Background()); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if s.httpRateLimiter != nil {
		s.httpRateLimiter.Close()
	}
	if s.loginLimiter != nil {
		s.loginLimiter.Close()
	}
	if s.runManager != nil {
		if err := s.runManager.close(context.Background()); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	if s.ownsConsoleHub && s.consoleHub != nil {
		s.consoleHub.Close()
	}
	if s.mcpManager != nil {
		s.mcpManager.Close()
	}
	if s.skillStore != nil {
		s.skillStore.Close()
	}
	if s.nodeletProber != nil {
		s.nodeletProber.Stop()
	}
	if s.runtimeStore != nil {
		if err := s.runtimeStore.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}

	s.closeMu.Lock()
	s.closeErr = closeErr
	close(s.closeDone)
	s.closeMu.Unlock()
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
