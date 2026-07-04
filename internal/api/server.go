package api

import (
	"context"
	"io"
	"net/http"
	"time"

	"oops/internal/auth"
	"oops/internal/config"
	"oops/internal/console"
	"oops/internal/llm"
	"oops/internal/logutil"
	"oops/internal/mcp"
	"oops/internal/nodelet"

	"github.com/cloudwego/eino/components/tool"
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
	NodeletManager *nodelet.NodeletManager
	NodeletClient  NodeletClient
	LLMEnabled     bool
	LLMConfig      config.LLMConfig
	UserStore      *auth.Store
	TokenService   *auth.TokenService
	TokenTTL       time.Duration
}

// Server 保存中心端 API 服务运行所需的配置和依赖。
type Server struct {
	nodeletManager *nodelet.NodeletManager
	nodeletProber  *nodelet.NodeletProber
	nodeletClient  NodeletClient
	llmClient      *llm.Client
	skillStore     *llm.SkillStore
	contextBuilder *llm.ContextBuilder
	eventStore     *llm.EventStore
	mcpManager     *mcp.Manager
	projectStore   *config.ProjectStore
	dsnStore       *config.ContainerDSNStore
	sessionStore   *llm.SessionStore
	UserStore      *auth.Store
	TokenService   *auth.TokenService
	tokenTTL       time.Duration
}

// NewFromConfig 使用配置创建中心端 API 服务。
func NewFromConfig(cfg config.Config) *Server {
	nm, err := nodelet.NewNodeletManager(nodelet.DefaultConfigPath)
	if err != nil {
		logutil.Error("nodelet: manager", zap.Error(err))
		nm, _ = nodelet.NewNodeletManager(nodelet.FallbackConfigPath) // fallback: empty
	}

	// 后台保活探测器。
	prober := nodelet.NewNodeletProber(nm)
	prober.Start()

	s := New(Options{
		NodeletManager: nm,
		NodeletClient:  nodelet.NewClient(nil),
		LLMEnabled:    cfg.LLM.Enabled,
		LLMConfig:     cfg.LLM,
	})
	s.nodeletProber = prober

	// MCP Manager 在 Server 创建后初始化，onChange 回调可引用 s.llmClient。
	mgr, err := mcp.NewManager(mcp.DefaultConfigPath, func(mcpBaseTools []tool.BaseTool) {
		s.onMCPToolsChanged(mcpBaseTools)
	})
	if err != nil {
		logutil.Error("mcp: manager", zap.Error(err))
	} else {
		s.mcpManager = mgr
	}

	// 项目存储。
	projectStore, err := config.NewProjectStore(config.DefaultProjectsPath)
	if err != nil {
		logutil.Error("projects: store", zap.Error(err))
	} else {
		s.projectStore = projectStore
	}

	// 容器 DSN 覆盖值存储。
	dsnStore, err := config.NewContainerDSNStore(config.DefaultDSNPath)
	if err != nil {
		logutil.Error("dsn: store", zap.Error(err))
	} else {
		s.dsnStore = dsnStore
	}

	// 用户认证。
	userStore, err := auth.NewStore(auth.DefaultPath)
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

	return s
}

// New 创建中心端 API 服务。
func New(options Options) *Server {
	if options.NodeletClient == nil {
		options.NodeletClient = nodelet.NewClient(nil)
	}

	// JSONL-backed session store（重启后会话可恢复）。
	sessionStore, err := llm.OpenSessionStore("data/sessions")
	if err != nil {
		logutil.Warn("session: open store, falling back to memory-only", zap.Error(err))
		sessionStore = llm.NewSessionStore()
	}

	s := &Server{
		nodeletManager: options.NodeletManager,
		nodeletClient:  options.NodeletClient,
		sessionStore:   sessionStore,
		UserStore:      options.UserStore,
		TokenService:   options.TokenService,
		tokenTTL:       options.TokenTTL,
	}

	if options.LLMEnabled {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		nativeTools, err := llm.NewTools(s, nil) // SkillStore 在后续初始化，此处传 nil，skill 工具后续由 onMCPToolsChanged 补上
		if err != nil {
			logutil.Error("llm: create tools", zap.Error(err))
			return s
		}

		client, err := llm.NewClient(ctx, options.LLMConfig, nativeTools)
		if err != nil {
			// LLM 不可用时不影响其他功能，仅日志输出。
			logutil.Error("llm: create client", zap.Error(err))
		} else {
			s.llmClient = client
		}
	}

	// SkillStore 管理 Agent 技能（替换旧 PromptStore + Router）。
	// 即使 LLM 未启用也初始化，供后续启用时使用。
	ss, err := llm.NewSkillStore("config/skills")
	if err != nil {
		logutil.Warn("llm: skill store", zap.Error(err))
	} else {
		s.skillStore = ss
	}

	// ContextBuilder：上下文工程引擎（compaction + system prompt + skills 注入）。
	if s.llmClient != nil && s.skillStore != nil {
		s.contextBuilder = llm.NewContextBuilder(s.sessionStore, s.skillStore, s.llmClient)
		logutil.Info("context: builder ready")
	}

	// SQLite 事件持久化。
	es, err := llm.OpenEventStore("data/events.db")
	if err != nil {
		logutil.Warn("llm: event store", zap.Error(err))
	} else {
		s.eventStore = es
	}

	return s
}

// Routes 返回中心端 API 路由。
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

// Mount 把中心端 API 路由挂载到指定 mux。
// Go 1.22+ 原生支持方法和路径参数匹配，不再需要手工 TrimPrefix+Split 解析。
func (s *Server) Mount(mux *http.ServeMux) {
	// 所有 API 路由统一经过: securityHeaders → rateLimit → authMiddleware → requireAuth → limitBody → handler
	authed := func(f http.HandlerFunc) http.HandlerFunc {
		return securityHeaders(rateLimit(s.authMiddleware(s.requireAuth(limitBody(f)))))
	}
	authedChat := func(f http.HandlerFunc) http.HandlerFunc {
		return securityHeaders(rateLimitChat(s.authMiddleware(s.requireAuth(limitBody(f)))))
	}
	publicWrap := func(f http.HandlerFunc) http.HandlerFunc {
		return securityHeaders(rateLimit(s.authMiddleware(f)))
	}

	// ---- Auth ----
	mux.HandleFunc("POST /api/token", publicWrap(s.handleCreateToken))
	mux.HandleFunc("DELETE /api/token", publicWrap(s.handleDeleteToken))
	mux.HandleFunc("GET /api/auth/me", authed(s.handleAuthMe))
	mux.HandleFunc("GET /api/auth/status", publicWrap(s.handleAuthStatus))
	mux.HandleFunc("POST /api/setup", publicWrap(s.handleSetup))

	// ---- Nodelets ----
	mux.HandleFunc("GET /api/nodelets", authed(s.handleNodeletList))
	mux.HandleFunc("GET /api/nodelets/status", authed(s.handleNodelets))
	mux.HandleFunc("POST /api/nodelets", authed(s.handleNodeletAdd))
	mux.HandleFunc("PUT /api/nodelets/{id}", authed(s.handleNodeletUpdate))
	mux.HandleFunc("DELETE /api/nodelets/{id}", authed(s.handleNodeletRemove))
	mux.HandleFunc("POST /api/nodelets/test", authed(s.handleNodeletTest))
	mux.HandleFunc("POST /api/nodelets/{id}/probe", authed(s.handleNodeletProbe))
	mux.HandleFunc("POST /api/nodelets/probe-all", authed(s.handleNodeletProbeAll))
	mux.HandleFunc("GET /api/nodelets/{nodeletID}/containers", authed(s.handleNodeletContainers))
	mux.HandleFunc("GET /api/nodelets/{nodeletID}/containers/{containerID}/logs", authed(s.handleNodeletLogs))
	mux.HandleFunc("GET /api/nodelets/{nodeletID}/containers/{containerID}/logs/stream", authed(s.handleNodeletLogsStreamRoute))

	// ---- Chat & Sessions ----
	mux.HandleFunc("POST /api/chat", authedChat(s.handleChat))
	mux.HandleFunc("GET /api/sessions", authed(s.handleSessions))
	mux.HandleFunc("GET /api/sessions/{id}", authed(s.handleSessionGet))
	mux.HandleFunc("DELETE /api/sessions/{id}", authed(s.handleSessionDelete))

	// ---- MCP Connections ----
	mux.HandleFunc("GET /api/mcp/connections", authed(s.handleMCPList))
	mux.HandleFunc("POST /api/mcp/connections", authed(s.handleMCPAdd))
	mux.HandleFunc("PUT /api/mcp/connections/{id}", authed(s.handleMCPUpdate))
	mux.HandleFunc("DELETE /api/mcp/connections/{id}", authed(s.handleMCPRemove))
	mux.HandleFunc("POST /api/mcp/connections/{id}/test", authed(s.handleMCPTest))
	mux.HandleFunc("POST /api/mcp/connections/{id}/tools/{toolName}/test", authed(s.handleMCPToolTestRoute))
	mux.HandleFunc("POST /api/mcp/connections/test", authed(s.handleMCPTest))

	// ---- Tools ----
	mux.HandleFunc("GET /api/tools", authed(s.handleTools))
	mux.HandleFunc("PUT /api/tools/{name}", authed(s.handleToolToggle))

	// ---- Skills ----
	mux.HandleFunc("GET /api/skills", authed(s.handleSkillsList))
	mux.HandleFunc("PUT /api/skills/{name}", authed(s.handleSkillsUpdate))
	mux.HandleFunc("DELETE /api/skills/{name}", authed(s.handleSkillsDelete))

	// ---- Console SSE ----
	mux.HandleFunc("GET /api/console/stream", securityHeaders(s.authMiddleware(console.Default().SSEHandler)))

	// ---- Projects ----
	mux.HandleFunc("GET /api/projects", authed(s.handleProjectList))
	mux.HandleFunc("POST /api/projects", authed(s.handleProjectCreate))
	mux.HandleFunc("GET /api/projects/{pid}", authed(s.handleProjectGet))
	mux.HandleFunc("PUT /api/projects/{pid}", authed(s.handleProjectUpdate))
	mux.HandleFunc("DELETE /api/projects/{pid}", authed(s.handleProjectDelete))

	// ---- Project Container Exclusions ----
	mux.HandleFunc("POST /api/projects/{pid}/excluded-containers", authed(s.handleProjectExcludeContainer))
	mux.HandleFunc("DELETE /api/projects/{pid}/excluded-containers", authed(s.handleProjectIncludeContainer))

	// ---- Project Chat ----
	mux.HandleFunc("POST /api/projects/{pid}/chat", authedChat(s.handleProjectChat))

	// ---- Project Sessions ----
	mux.HandleFunc("GET /api/projects/{pid}/sessions", authed(s.handleProjectSessions))
	mux.HandleFunc("GET /api/projects/{pid}/sessions/{id}", authed(s.handleProjectSessionGet))
	mux.HandleFunc("DELETE /api/projects/{pid}/sessions/{id}", authed(s.handleProjectSessionDelete))

	// ---- Project Servers ----
	mux.HandleFunc("GET /api/projects/{pid}/servers", authed(s.handleProjectServersList))
	mux.HandleFunc("POST /api/projects/{pid}/servers", authed(s.handleProjectServersAdd))
	mux.HandleFunc("DELETE /api/projects/{pid}/servers/{sid}", authed(s.handleProjectServersRemove))

	// ---- Project Containers ----
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers", authed(s.handleProjectContainers))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}", authed(s.handleContainerDetail))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}/logs/stream", authed(s.handleProjectLogsStream))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}/mcp", authed(s.handleContainerMCPGet))
	mux.HandleFunc("DELETE /api/projects/{pid}/servers/{sid}/containers/{cid}/mcp", authed(s.handleContainerMCPDelete))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn", authed(s.handleContainerDSNGet))
	mux.HandleFunc("PUT /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn", authed(s.handleContainerDSNPut))
	mux.HandleFunc("DELETE /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn", authed(s.handleContainerDSNDelete))
}


// findNodelet 按配置 ID 查找 Nodelet。
func (s *Server) findNodelet(id string) (nodelet.NodeletConfig, bool) {
	if s.nodeletManager == nil {
		return nodelet.NodeletConfig{}, false
	}
	return s.nodeletManager.Find(id)
}


