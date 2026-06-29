package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"oops/internal/auth"
	"oops/internal/config"
	"oops/internal/connection"
	"oops/internal/connection/checker"
	"oops/internal/console"
	"oops/internal/llm"
	"oops/internal/logutil"
	"oops/internal/mcp"
	"oops/internal/nodelet"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
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
}

// Options 保存中心端 API 服务依赖。
type Options struct {
	Connections    []connection.Connection
	NodeletManager *nodelet.NodeletManager
	NodeletClient  NodeletClient
	Registry       *connection.Registry
	LLMEnabled     bool
	LLMConfig      config.LLMConfig
	UserStore      *auth.Store
	TokenService   *auth.TokenService
	TokenTTL       time.Duration
}

// Server 保存中心端 API 服务运行所需的配置和依赖。
type Server struct {
	connections    []connection.Connection
	nodeletManager *nodelet.NodeletManager
	nodeletProber  *nodelet.NodeletProber
	nodeletClient  NodeletClient
	registry       *connection.Registry
	llmClient      *llm.Client
	mcpManager     *mcp.Manager
	projectStore   *config.ProjectStore
	dsnStore       *config.ContainerDSNStore
	sessionStore   *llm.SessionStore
	UserStore      *auth.Store
	TokenService   *auth.TokenService
	tokenTTL       time.Duration
}

// statusItem 是 GUI 状态接口返回的一行连接状态。
type statusItem struct {
	Connection connection.Connection `json:"connection"`
	Result     connection.Result     `json:"result"`
	Error      string                `json:"error,omitempty"`
}

// nodeletItem 是 GUI 机器列表接口返回的一台 Nodelet 状态。
type nodeletItem struct {
	Nodelet   nodelet.NodeletConfig `json:"nodelet"`
	Host      nodelet.Host         `json:"host"`
	Available bool                 `json:"available"`
	Error     string               `json:"error,omitempty"`
}

// NewFromConfig 使用配置创建中心端 API 服务。
func NewFromConfig(cfg config.Config) *Server {
	nm, err := nodelet.NewNodeletManager("config/nodelets.json")
	if err != nil {
		logutil.Error("nodelet: manager", zap.Error(err))
		nm, _ = nodelet.NewNodeletManager("/dev/null") // fallback: empty
	}

	// 后台保活探测器。
	prober := nodelet.NewNodeletProber(nm)
	prober.Start()

	s := New(Options{
		Connections:    cfg.Connections(),
		NodeletManager: nm,
		NodeletClient:  nodelet.NewClient(nil),
		Registry:      checker.NewDefaultRegistry(),
		LLMEnabled:    cfg.LLM.Enabled,
		LLMConfig:     cfg.LLM,
	})
	s.nodeletProber = prober

	// MCP Manager 在 Server 创建后初始化，onChange 回调可引用 s.llmClient。
	mgr, err := mcp.NewManager("config/mcp_connections.json", func(mcpBaseTools []tool.BaseTool) {
		s.onMCPToolsChanged(mcpBaseTools)
	})
	if err != nil {
		logutil.Error("mcp: manager", zap.Error(err))
	} else {
		s.mcpManager = mgr
		mgr.StartKeepalive(5 * time.Minute)
	}

	// 项目存储。
	projectStore, err := config.NewProjectStore(config.DefaultProjectsPath)
	if err != nil {
		logutil.Error("projects: store", zap.Error(err))
	} else {
		s.projectStore = projectStore
	}

	// 容器 DSN 覆盖值存储。
	dsnStore, err := config.NewContainerDSNStore("config/container_dsn.json")
	if err != nil {
		logutil.Error("dsn: store", zap.Error(err))
	} else {
		s.dsnStore = dsnStore
	}

	// 用户认证。
	userStore, err := auth.NewStore("data/users.yml")
	if err != nil {
		logutil.Error("auth: user store", zap.Error(err))
	} else {
		s.UserStore = userStore
		s.TokenService = auth.NewTokenService(userStore.Users, 24*time.Hour)
		s.tokenTTL = 24 * time.Hour
	}

	return s
}

// New 创建中心端 API 服务。
func New(options Options) *Server {
	if options.NodeletClient == nil {
		options.NodeletClient = nodelet.NewClient(nil)
	}
	if options.Registry == nil {
		options.Registry = checker.NewDefaultRegistry()
	}

	s := &Server{
		connections:    options.Connections,
		nodeletManager: options.NodeletManager,
		nodeletClient:  options.NodeletClient,
		registry:      options.Registry,
		sessionStore:  llm.NewSessionStore(),
		UserStore:     options.UserStore,
		TokenService:  options.TokenService,
		tokenTTL:      options.TokenTTL,
	}

	if options.LLMEnabled {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		nativeTools, err := llm.NewTools(s)
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

	// ---- Connections ----
	mux.HandleFunc("GET /api/connections/status", authed(s.handleConnectionStatus))

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

	// ---- Console SSE ----
	mux.HandleFunc("GET /api/console/stream", securityHeaders(s.authMiddleware(console.Default().SSEHandler)))

	// ---- Projects ----
	mux.HandleFunc("GET /api/projects", authed(s.handleProjectList))
	mux.HandleFunc("POST /api/projects", authed(s.handleProjectCreate))
	mux.HandleFunc("GET /api/projects/{pid}", authed(s.handleProjectGet))
	mux.HandleFunc("PUT /api/projects/{pid}", authed(s.handleProjectUpdate))
	mux.HandleFunc("DELETE /api/projects/{pid}", authed(s.handleProjectDelete))

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
	mux.HandleFunc("POST /api/projects/{pid}/servers/{sid}/containers/{cid}/check", authed(s.handleProjectHealthCheck))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}/mcp", authed(s.handleContainerMCPGet))
	mux.HandleFunc("DELETE /api/projects/{pid}/servers/{sid}/containers/{cid}/mcp", authed(s.handleContainerMCPDelete))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn", authed(s.handleContainerDSNGet))
	mux.HandleFunc("PUT /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn", authed(s.handleContainerDSNPut))
	mux.HandleFunc("DELETE /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn", authed(s.handleContainerDSNDelete))
}

// handleSessions handles GET /api/sessions — lists global or project sessions.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	writeJSON(w, s.sessionStore.List(projectID))
}

// handleSessionGet handles GET /api/sessions/{id}.
func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.sessionStore.Get(id)
	if !ok {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("include_messages") == "true" {
		writeJSON(w, sess.ToDetail())
	} else {
		writeJSON(w, llm.SessionInfo{
			ID:           sess.ID,
			ProjectID:    sess.ProjectID,
			MessageCount: len(sess.Messages),
			CreatedAt:    sess.CreatedAt.UnixMilli(),
			UpdatedAt:    sess.UpdatedAt.UnixMilli(),
		})
	}
}

// handleSessionDelete handles DELETE /api/sessions/{id}.
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.sessionStore.Delete(id) {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleProjectSessions handles GET /api/projects/{pid}/sessions.
func (s *Server) handleProjectSessions(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	writeJSON(w, s.sessionStore.List(pid))
}

// handleProjectSessionGet handles GET /api/projects/{pid}/sessions/{id}.
func (s *Server) handleProjectSessionGet(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	id := r.PathValue("id")
	sess, ok := s.sessionStore.Get(id)
	if !ok || sess.ProjectID != pid {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("include_messages") == "true" {
		writeJSON(w, sess.ToDetail())
	} else {
		writeJSON(w, llm.SessionInfo{
			ID:           sess.ID,
			ProjectID:    sess.ProjectID,
			MessageCount: len(sess.Messages),
			CreatedAt:    sess.CreatedAt.UnixMilli(),
			UpdatedAt:    sess.UpdatedAt.UnixMilli(),
		})
	}
}

// handleProjectSessionDelete handles DELETE /api/projects/{pid}/sessions/{id}.
func (s *Server) handleProjectSessionDelete(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	id := r.PathValue("id")
	sess, ok := s.sessionStore.Get(id)
	if !ok || sess.ProjectID != pid {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	s.sessionStore.Delete(id)
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleConnectionStatus 执行所有连接检查并返回 JSON。
func (s *Server) handleConnectionStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	items := make([]statusItem, 0, len(s.connections))
	results := make([]statusItem, len(s.connections))

	var wg sync.WaitGroup
	for index, conn := range s.connections {
		wg.Add(1)
		go func(index int, conn connection.Connection) {
			defer wg.Done()

			// 每个连接独立超时，避免慢组件拖累其他组件的结果。
			checkCtx, checkCancel := context.WithTimeout(ctx, 6*time.Second)
			defer checkCancel()

			result, err := s.registry.Check(checkCtx, conn)
			item := statusItem{
				Connection: conn,
				Result:     result,
			}
			if err != nil {
				item.Error = err.Error()
				item.Result = connection.Result{
					ConnectionID: conn.ID,
					Status:       connection.StatusUnknown,
					Message:      err.Error(),
					CheckedAt:    time.Now(),
				}
			}
			results[index] = item
		}(index, conn)
	}
	wg.Wait()

	items = append(items, results...)
	writeJSON(w, items)
}

// handleNodelets 返回中心端配置的 Nodelet 状态（读 Prober 缓存，即时响应）。
func (s *Server) handleNodelets(w http.ResponseWriter, r *http.Request) {
	if s.nodeletProber == nil {
		writeJSON(w, []nodeletStatusItem{})
		return
	}

	nodelets := s.nodeletManager.List()
	results := make([]nodeletStatusItem, 0, len(nodelets))
	for _, item := range nodelets {
		pr := s.nodeletProber.StatusByID(item.ID)
		nsi := nodeletStatusItem{
			Nodelet:   item,
			Available: pr != nil && pr.Status == nodelet.StatusHealthy,
		}
		if pr != nil {
			nsi.Status = pr.Status
			nsi.LastProbe = pr.LastProbeAt
			nsi.LatencyMs = pr.LatencyMs
			nsi.Error = pr.LastError
		} else {
			nsi.Status = nodelet.StatusUnknown
		}
		results = append(results, nsi)
	}

	writeJSON(w, results)
}

// nodeletStatusItem 是 GET /api/nodelets/status 的响应项。
type nodeletStatusItem struct {
	Nodelet   nodelet.NodeletConfig `json:"nodelet"`
	Status    nodelet.ProbeStatus   `json:"status"`
	Available bool                  `json:"available"`
	LastProbe time.Time             `json:"lastProbeAt"`
	LatencyMs int64                 `json:"latencyMs"`
	Error     string                `json:"error,omitempty"`
}

// handleNodeletContainers handles GET /api/nodelets/{nodeletID}/containers.
func (s *Server) handleNodeletContainers(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("nodeletID")
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	containers, err := s.nodeletClient.Containers(ctx, item.Address, item.Token)
	if err != nil {
		sanitizedError(w, "nodelet containers", err, http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, containers)
}

// handleNodeletLogs handles GET /api/nodelets/{nodeletID}/containers/{containerID}/logs.
func (s *Server) handleNodeletLogs(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("nodeletID")
	containerID := r.PathValue("containerID")
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	logs, err := s.nodeletClient.ContainerLogs(ctx, item.Address, item.Token, containerID, r.URL.Query().Get("tail"))
	if err != nil {
		sanitizedError(w, "nodelet logs", err, http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, logs)
}

// handleNodeletLogsStreamRoute handles GET /api/nodelets/{nodeletID}/containers/{containerID}/logs/stream.
func (s *Server) handleNodeletLogsStreamRoute(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("nodeletID")
	containerID := r.PathValue("containerID")
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.handleNodeletLogsStream(w, r, item, containerID)
}

// handleNodeletLogsStream 透传远端 Nodelet 的容器日志 SSE。
func (s *Server) handleNodeletLogsStream(w http.ResponseWriter, r *http.Request, item nodelet.NodeletConfig, containerID string) {
	stream, err := s.nodeletClient.ContainerLogsStream(r.Context(), item.Address, item.Token, containerID, r.URL.Query().Get("tail"))
	if err != nil {
		sanitizedError(w, "nodelet logs stream", err, http.StatusServiceUnavailable)
		return
	}
	defer stream.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	copyAndFlush(w, flusher, stream)
}

// copyAndFlush 复制流式响应并在每个块后刷新。
func copyAndFlush(w io.Writer, flusher http.Flusher, reader io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if _, err := w.Write(buffer[:n]); err != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			return
		}
	}
}

// findNodelet 按配置 ID 查找 Nodelet。
func (s *Server) findNodelet(id string) (nodelet.NodeletConfig, bool) {
	if s.nodeletManager == nil {
		return nodelet.NodeletConfig{}, false
	}
	return s.nodeletManager.Find(id)
}

// chatRequest 是 POST /api/chat 的请求体。
type chatRequest struct {
	SessionID string `json:"session_id"`
	Question  string `json:"question"`
}

// handleChat 处理 LLM 对话请求。
// 接受 session_id（可选）和 question，通过 SSE 流式返回每一步执行过程。
// 若不传 session_id，后端自动创建新会话并通过首条 SSE 事件返回 session ID。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	s.handleChatWithProject(w, r, "")
}

// handleProjectChat handles POST /api/projects/{pid}/chat.
func (s *Server) handleProjectChat(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	s.handleChatWithProject(w, r, pid)
}

// handleChatWithProject 是 handleChat 和 handleProjectChat 的共享实现。
// projectID 为空时表示全局会话。
func (s *Server) handleChatWithProject(w http.ResponseWriter, r *http.Request, projectID string) {
	if s.llmClient == nil {
		http.Error(w, `{"error":"LLM not configured. Set llm.enabled=true and llm.api_key in config."}`, http.StatusServiceUnavailable)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Question == "" {
		http.Error(w, `{"error":"question is required"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	// 获取或创建会话。
	var sess *llm.Session
	if req.SessionID != "" {
		sess = s.sessionStore.GetOrCreate(req.SessionID, projectID)
	} else {
		sess = s.sessionStore.Create(projectID)
	}

	// 构建完整消息列表: system prompt + 历史消息 + 当前问题。
	messages := []*schema.Message{
		schema.SystemMessage(llm.SystemPrompt),
	}
	messages = append(messages, sess.Messages...)
	userMsg := schema.UserMessage(req.Question)
	messages = append(messages, userMsg)

	// 用户消息先写入 session。
	s.sessionStore.AppendMessage(sess.ID, userMsg)

	// onMessage 回调：agent 产生的每条新消息都追加到 session。
	onMessage := func(_ context.Context, msg *schema.Message) error {
		s.sessionStore.AppendMessage(sess.ID, msg)
		return nil
	}

	events, err := s.llmClient.Ask(ctx, messages, onMessage)
	if err != nil {
		sanitizedError(w, "chat ask", err, http.StatusInternalServerError)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming unsupported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 发送初始注释，强制浏览器进入流模式。
	fmt.Fprintf(w, ":ok\n\n")
	flusher.Flush()

	// 首条事件：告知客户端 session ID。
	sessionEvt := llm.StepEvent{Type: "session", Content: sess.ID}
	data, _ := json.Marshal(sessionEvt)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	for evt := range events {
		data, err := json.Marshal(evt)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
		flusher.Flush()
	}
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// ListNodelets 实现 llm.OpsData，返回所有 Nodelet 概要。
// 先读 Prober 缓存判断可用性，仅对健康节点实时获取 Docker 详情。
func (s *Server) ListNodelets(ctx context.Context) ([]llm.NodeletSummary, error) {
	nodelets := s.nodeletManager.List()
	results := make([]llm.NodeletSummary, len(nodelets))
	var wg sync.WaitGroup
	for index, item := range nodelets {
		wg.Add(1)
		go func(index int, item nodelet.NodeletConfig) {
			defer wg.Done()

			summary := llm.NodeletSummary{
				ID:      item.ID,
				Name:    item.Name,
				Address: item.Address,
			}

			// 从 Prober 缓存获取可用性
			if s.nodeletProber != nil {
				pr := s.nodeletProber.StatusByID(item.ID)
				if pr != nil && pr.Status == nodelet.StatusHealthy {
					summary.Available = true
				} else if pr != nil {
					summary.Error = pr.LastError
					results[index] = summary
					return
				}
			}

			// 仅健康节点实时获取 Docker 详情
			checkCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()

			host, err := s.nodeletClient.Host(checkCtx, item.Address, item.Token)
			if err != nil {
				summary.Error = err.Error()
				summary.Available = false
			} else {
				summary.Available = true
				summary.DockerVersion = host.DockerVersion
				summary.Runtime = host.Runtime
				summary.NCPU = host.NCPU
				summary.MemTotal = host.MemTotal
			}
			results[index] = summary
		}(index, item)
	}
	wg.Wait()
	return results, nil
}

// ListContainers 实现 llm.OpsData，返回指定 Nodelet 的容器列表。
// status 不为空时仅返回匹配状态的容器。
func (s *Server) ListContainers(ctx context.Context, nodeletID, status string) ([]nodelet.Container, error) {
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		return nil, fmt.Errorf("nodelet %q not found", nodeletID)
	}
	containers, err := s.nodeletClient.Containers(ctx, item.Address, item.Token)
	if err != nil {
		return nil, err
	}
	if status == "" {
		return containers, nil
	}
	filtered := make([]nodelet.Container, 0, len(containers))
	for _, c := range containers {
		if c.State == status {
			filtered = append(filtered, c)
		}
	}
	return filtered, nil
}

// GetLogs 实现 llm.OpsData，返回指定容器的历史日志。
func (s *Server) GetLogs(ctx context.Context, nodeletID, containerID string, tail int) ([]nodelet.LogEntry, error) {
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		return nil, fmt.Errorf("nodelet %q not found", nodeletID)
	}
	return s.nodeletClient.ContainerLogs(ctx, item.Address, item.Token, containerID, strconv.Itoa(tail))
}

// CheckConnections 实现 llm.OpsData，执行所有连接健康检查。
func (s *Server) CheckConnections(ctx context.Context) ([]llm.ConnectionStatus, error) {
	results := make([]llm.ConnectionStatus, len(s.connections))
	var wg sync.WaitGroup
	for index, conn := range s.connections {
		wg.Add(1)
		go func(index int, conn connection.Connection) {
			defer wg.Done()

			checkCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()

			result, err := s.registry.Check(checkCtx, conn)
			cs := llm.ConnectionStatus{
				ID:      conn.ID,
				Name:    conn.Name,
				Type:    conn.Type,
				Address: conn.Address,
				Status:  string(result.Status),
				Message: result.Message,
				Latency: result.Latency,
			}
			if err != nil {
				cs.Error = err.Error()
				cs.Status = string(connection.StatusUnknown)
			}
			results[index] = cs
		}(index, conn)
	}
	wg.Wait()
	return results, nil
}

// onMCPToolsChanged 是 MCP Manager 的工具变更回调。
// 合并原生工具和 MCP 工具后热更新 LLM Client。
func (s *Server) onMCPToolsChanged(mcpBaseTools []tool.BaseTool) {
	if s.llmClient == nil {
		return
	}

	nativeTools, err := llm.NewTools(s)
	if err != nil {
		logutil.Error("mcp: create native tools", zap.Error(err))
		return
	}

	allTools := make([]tool.InvokableTool, 0, len(nativeTools)+len(mcpBaseTools))
	allTools = append(allTools, nativeTools...)
	for _, bt := range mcpBaseTools {
		if it, ok := bt.(tool.InvokableTool); ok {
			allTools = append(allTools, it)
		}
	}

	s.llmClient.UpdateTools(allTools)
	logutil.Info("mcp: tools updated",
		zap.Int("total", len(allTools)),
		zap.Int("native", len(nativeTools)),
		zap.Int("mcp", len(mcpBaseTools)),
	)
}

// toolItem 是 GET /api/tools 返回的单个工具条目。
type toolItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// toolsResponse 是 GET /api/tools 的响应体。
type toolsResponse struct {
	Native []toolItem            `json:"native"`
	MCP    map[string][]toolItem `json:"mcp"`
}

// handleTools handles GET /api/tools — returns all tools (native + MCP) and their enabled state.
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	resp := toolsResponse{
		Native: []toolItem{},
		MCP:    make(map[string][]toolItem),
	}

	// 获取禁用状态。
	disabled := make(map[string]bool)
	if s.llmClient != nil {
		disabled = s.llmClient.DisabledTools()
	}

	// 原生工具。
	nativeTools, err := llm.NewTools(s)
	if err == nil {
		for _, t := range nativeTools {
			info, err := t.Info(r.Context())
			if err != nil {
				continue
			}
			resp.Native = append(resp.Native, toolItem{
				Name:        info.Name,
				Description: info.Desc,
				Enabled:     !disabled[info.Name],
			})
		}
	}

	// MCP 工具（按连接分组）。
	if s.mcpManager != nil {
		// 构建连接 ID → 名称的映射。
		connNames := make(map[string]string)
		for _, conn := range s.mcpManager.List() {
			connNames[conn.ID] = conn.Name
		}

		for connID, mcpTools := range s.mcpManager.GetConnectionTools() {
			name := connNames[connID]
			if name == "" {
				name = connID
			}
			items := make([]toolItem, 0, len(mcpTools))
			for _, mt := range mcpTools {
				items = append(items, toolItem{
					Name:        mt.Name,
					Description: mt.Description,
					Enabled:     !disabled[mt.Name],
				})
			}
			if len(items) > 0 {
				resp.MCP[name] = items
			}
		}
	}

	writeJSON(w, resp)
}

// handleToolToggle handles PUT /api/tools/{name}.
func (s *Server) handleToolToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.llmClient == nil {
		http.Error(w, `{"error":"llm not configured"}`, http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	s.llmClient.SetToolEnabled(name, req.Enabled)
	writeJSON(w, map[string]string{"status": "ok"})
}

// mcpErrorStatus maps MCP manager errors to appropriate HTTP status codes.
// Validation/conflict errors return 4xx so the frontend can display the reason.
func mcpErrorStatus(err error) int {
	msg := err.Error()
	if strings.Contains(msg, "already exists") || strings.Contains(msg, "already has connection") {
		return http.StatusConflict
	}
	if strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	if strings.Contains(msg, "id is required") || strings.Contains(msg, "not in the allowed list") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// handleMCPList handles GET /api/mcp/connections.
func (s *Server) handleMCPList(w http.ResponseWriter, r *http.Request) {
	if s.mcpManager == nil {
		writeJSON(w, []mcp.ConnectionWithStatus{})
		return
	}
	writeJSON(w, s.mcpManager.List())
}

// handleMCPAdd handles POST /api/mcp/connections.
func (s *Server) handleMCPAdd(w http.ResponseWriter, r *http.Request) {
	var cfg mcp.ConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if s.mcpManager == nil {
		writeJSONError(w, "mcp manager not initialized", http.StatusServiceUnavailable)
		return
	}
	if err := s.mcpManager.Add(cfg); err != nil {
		logutil.Error("api: mcp add", zap.Error(err))
		writeJSONError(w, err.Error(), mcpErrorStatus(err))
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleMCPUpdate handles PUT /api/mcp/connections/{id}.
func (s *Server) handleMCPUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var cfg mcp.ConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	cfg.ID = id
	if s.mcpManager == nil {
		writeJSONError(w, "mcp manager not initialized", http.StatusServiceUnavailable)
		return
	}
	if err := s.mcpManager.Update(cfg); err != nil {
		logutil.Error("api: mcp update", zap.Error(err))
		writeJSONError(w, err.Error(), mcpErrorStatus(err))
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleMCPRemove handles DELETE /api/mcp/connections/{id}.
func (s *Server) handleMCPRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.mcpManager == nil {
		writeJSONError(w, "mcp manager not initialized", http.StatusServiceUnavailable)
		return
	}
	if err := s.mcpManager.Remove(id); err != nil {
		logutil.Error("api: mcp remove", zap.Error(err))
		writeJSONError(w, err.Error(), mcpErrorStatus(err))
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleMCPTest handles POST /api/mcp/connections/{id}/test and POST /api/mcp/connections/test.
func (s *Server) handleMCPTest(w http.ResponseWriter, r *http.Request) {
	var cfg mcp.ConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if s.mcpManager == nil {
		writeJSONError(w, "mcp manager not initialized", http.StatusServiceUnavailable)
		return
	}
	if err := s.mcpManager.Test(cfg); err != nil {
		logutil.Error("api: mcp test", zap.Error(err))
		writeJSON(w, map[string]string{"status": "failed", "error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleMCPToolTestRoute handles POST /api/mcp/connections/{id}/tools/{toolName}/test.
func (s *Server) handleMCPToolTestRoute(w http.ResponseWriter, r *http.Request) {
	connID := r.PathValue("id")
	toolName := r.PathValue("toolName")
	if s.mcpManager == nil {
		writeJSONError(w, "mcp manager not initialized", http.StatusServiceUnavailable)
		return
	}
	output, err := s.mcpManager.TestTool(connID, toolName)
	if err != nil {
		logutil.Error("api: mcp tool test failed",
			zap.String("connID", connID),
			zap.String("tool", toolName),
			zap.Error(err),
		)
		writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "output": output})
}

// handleNodeletList handles GET /api/nodelets — 返回纯配置列表（无 Docker 调用，即时响应）。
func (s *Server) handleNodeletList(w http.ResponseWriter, r *http.Request) {
	if s.nodeletManager == nil {
		writeJSON(w, []nodelet.NodeletConfig{})
		return
	}
	writeJSON(w, s.nodeletManager.List())
}

// handleNodeletAdd handles POST /api/nodelets.
func (s *Server) handleNodeletAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	cfg := nodelet.NodeletConfig{ID: req.ID, Name: req.Name, Address: req.Address, Token: req.Token}
	if s.nodeletManager == nil {
		writeJSONError(w, "nodelet manager not initialized", http.StatusServiceUnavailable)
		return
	}
	if err := s.nodeletManager.Add(cfg); err != nil {
		logutil.Error("api: nodelet add", zap.Error(err))
		writeJSONError(w, err.Error(), mcpErrorStatus(err))
		return
	}
	if s.nodeletProber != nil {
		s.nodeletProber.OnConfigChange()
		go s.nodeletProber.ProbeNow(cfg.ID)
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleNodeletUpdate handles PUT /api/nodelets/{id}.
func (s *Server) handleNodeletUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if s.nodeletManager == nil {
		writeJSONError(w, "nodelet manager not initialized", http.StatusServiceUnavailable)
		return
	}
	// 若未提交新 token，保留旧值
	token := req.Token
	if token == "" {
		if old, ok := s.nodeletManager.Find(id); ok {
			token = old.Token
		}
	}
	cfg := nodelet.NodeletConfig{ID: id, Name: req.Name, Address: req.Address, Token: token}
	if err := s.nodeletManager.Update(cfg); err != nil {
		logutil.Error("api: nodelet update", zap.Error(err))
		writeJSONError(w, err.Error(), mcpErrorStatus(err))
		return
	}
	if s.nodeletProber != nil {
		s.nodeletProber.OnConfigChange()
		go s.nodeletProber.ProbeNow(cfg.ID)
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleNodeletRemove handles DELETE /api/nodelets/{id}.
// 同时从所有引用了该 nodelet 的项目中移除。
func (s *Server) handleNodeletRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.nodeletManager == nil {
		writeJSONError(w, "nodelet manager not initialized", http.StatusServiceUnavailable)
		return
	}
	if err := s.nodeletManager.Remove(id); err != nil {
		logutil.Error("api: nodelet remove", zap.Error(err))
		writeJSONError(w, err.Error(), mcpErrorStatus(err))
		return
	}
	// 级联清理项目引用。
	if s.projectStore != nil {
		for _, p := range s.projectStore.List() {
			for _, nid := range p.NodeletIDs {
				if nid == id {
					_ = s.projectStore.RemoveNodelet(p.ID, id)
					break
				}
			}
		}
	}
	if s.nodeletProber != nil {
		s.nodeletProber.OnConfigChange()
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleProjectServersRemove handles DELETE /api/projects/{pid}/servers/{sid}.
func (s *Server) handleProjectServersRemove(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	sid := r.PathValue("sid")
	if s.projectStore == nil {
		writeJSONError(w, "not initialized", http.StatusServiceUnavailable)
		return
	}
	if err := s.projectStore.RemoveNodelet(pid, sid); err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleNodeletTest handles POST /api/nodelets/test（兼容旧前端，内部改为走 Prober）。
func (s *Server) handleNodeletTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if s.nodeletProber == nil {
		writeJSONError(w, "nodelet manager not initialized", http.StatusServiceUnavailable)
		return
	}
	result := s.nodeletProber.ProbeNow(req.ID)
	if result.Status == nodelet.StatusHealthy {
		writeJSON(w, map[string]string{"status": "ok"})
	} else {
		writeJSON(w, map[string]string{"status": "failed", "error": result.LastError})
	}
}

// handleNodeletProbe handles POST /api/nodelets/{id}/probe — 强制探测单个 Nodelet。
func (s *Server) handleNodeletProbe(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("id")
	if s.nodeletProber == nil {
		writeJSONError(w, "nodelet manager not initialized", http.StatusServiceUnavailable)
		return
	}
	logutil.Info("api: probe requested", zap.String("nodeletID", nodeletID))
	result := s.nodeletProber.ProbeNow(nodeletID)
	writeJSON(w, result)
}

// handleNodeletProbeAll handles POST /api/nodelets/probe-all — 强制探测全部 Nodelet。
func (s *Server) handleNodeletProbeAll(w http.ResponseWriter, r *http.Request) {
	if s.nodeletProber == nil {
		writeJSONError(w, "nodelet manager not initialized", http.StatusServiceUnavailable)
		return
	}
	logutil.Info("api: probe-all requested")
	s.nodeletProber.ProbeAll()
	writeJSON(w, map[string]string{"status": "ok"})
}

// writeJSON 写入 JSON 响应。
func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}

// writeJSONError 写入 JSON 格式的错误响应。
func writeJSONError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
