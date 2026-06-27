package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"oops/internal/config"
	"oops/internal/connection"
	"oops/internal/connection/checker"
	"oops/internal/llm"
	"oops/internal/nodelet"
)

// NodeletClient 是中心端访问 oops-nodelet 的最小接口。
type NodeletClient interface {
	// Host 读取远端 Nodelet 所在机器信息。
	Host(context.Context, string, string) (nodelet.Host, error)

	// Containers 读取远端 Nodelet 上的容器列表。
	Containers(context.Context, string, string) ([]nodelet.Container, error)

	// ContainerLogs 读取远端 Nodelet 上某个容器的历史日志。
	ContainerLogs(context.Context, string, string, string, string) ([]nodelet.LogEntry, error)

	// ContainerLogsStream 读取远端 Nodelet 上某个容器的实时日志 SSE 流。
	ContainerLogsStream(context.Context, string, string, string, string) (io.ReadCloser, error)
}

// Options 保存中心端 API 服务依赖。
type Options struct {
	Connections   []connection.Connection
	Nodelets      []config.NodeletConfig
	NodeletClient NodeletClient
	Registry      *connection.Registry
	LLMEnabled    bool
	LLMConfig     config.LLMConfig
}

// Server 保存中心端 API 服务运行所需的配置和依赖。
type Server struct {
	connections   []connection.Connection
	nodelets      []config.NodeletConfig
	nodeletClient NodeletClient
	registry      *connection.Registry
	llmClient     *llm.Client
}

// statusItem 是 GUI 状态接口返回的一行连接状态。
type statusItem struct {
	Connection connection.Connection `json:"connection"`
	Result     connection.Result     `json:"result"`
	Error      string                `json:"error,omitempty"`
}

// nodeletItem 是 GUI 机器列表接口返回的一台 Nodelet 状态。
type nodeletItem struct {
	Nodelet   config.NodeletConfig `json:"nodelet"`
	Host      nodelet.Host         `json:"host"`
	Available bool                 `json:"available"`
	Error     string               `json:"error,omitempty"`
}

// NewFromConfig 使用配置创建中心端 API 服务。
func NewFromConfig(cfg config.Config) *Server {
	return New(Options{
		Connections:   cfg.Connections(),
		Nodelets:      cfg.Nodelets,
		NodeletClient: nodelet.NewClient(nil),
		Registry:      checker.NewDefaultRegistry(),
		LLMEnabled:    cfg.LLM.Enabled,
		LLMConfig:     cfg.LLM,
	})
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
		connections:   options.Connections,
		nodelets:      options.Nodelets,
		nodeletClient: options.NodeletClient,
		registry:      options.Registry,
	}

	if options.LLMEnabled {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client, err := llm.NewClient(ctx, options.LLMConfig, s)
		if err != nil {
			// LLM 不可用时不影响其他功能，仅日志输出。
			log.Printf("llm: create client: %v", err)
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
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/api/connections/status", s.handleConnectionStatus)
	mux.HandleFunc("/api/nodelets", s.handleNodelets)
	mux.HandleFunc("/api/nodelets/", s.handleNodeletResource)
	mux.HandleFunc("/api/chat", s.handleChat)
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

// handleNodelets 返回中心端配置的 Nodelet 机器列表。
func (s *Server) handleNodelets(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/nodelets" {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	results := make([]nodeletItem, len(s.nodelets))
	var wg sync.WaitGroup
	for index, item := range s.nodelets {
		wg.Add(1)
		go func(index int, item config.NodeletConfig) {
			defer wg.Done()

			checkCtx, checkCancel := context.WithTimeout(ctx, 6*time.Second)
			defer checkCancel()

			host, err := s.nodeletClient.Host(checkCtx, item.Address, item.Token)
			result := nodeletItem{
				Nodelet:   item,
				Host:      host,
				Available: err == nil,
			}
			if err != nil {
				result.Error = err.Error()
				result.Host = nodelet.Host{
					ID:        item.ID,
					Name:      item.Name,
					Address:   item.Address,
					Available: false,
				}
			}
			results[index] = result
		}(index, item)
	}
	wg.Wait()

	writeJSON(w, results)
}

// handleNodeletResource 返回指定 Nodelet 的容器列表或容器日志。
func (s *Server) handleNodeletResource(w http.ResponseWriter, r *http.Request) {
	nodeletID, containerID, action, ok := splitNodeletResourcePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	item, ok := s.findNodelet(nodeletID)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if action == "containers" {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		containers, err := s.nodeletClient.Containers(ctx, item.Address, item.Token)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, containers)
		return
	}

	if action == "logs" {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		logs, err := s.nodeletClient.ContainerLogs(ctx, item.Address, item.Token, containerID, r.URL.Query().Get("tail"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, logs)
		return
	}

	if action == "logs/stream" {
		s.handleNodeletLogsStream(w, r, item, containerID)
		return
	}

	http.NotFound(w, r)
}

// handleNodeletLogsStream 透传远端 Nodelet 的容器日志 SSE。
func (s *Server) handleNodeletLogsStream(w http.ResponseWriter, r *http.Request, item config.NodeletConfig, containerID string) {
	stream, err := s.nodeletClient.ContainerLogsStream(r.Context(), item.Address, item.Token, containerID, r.URL.Query().Get("tail"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
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
func (s *Server) findNodelet(id string) (config.NodeletConfig, bool) {
	for _, item := range s.nodelets {
		if item.ID == id {
			return item, true
		}
	}
	return config.NodeletConfig{}, false
}

// splitNodeletResourcePath 拆分中心端 Nodelet 子资源路径。
func splitNodeletResourcePath(rawPath string) (string, string, string, bool) {
	rest := strings.TrimPrefix(rawPath, "/api/nodelets/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "containers" {
		return parts[0], "", "containers", true
	}
	if len(parts) == 4 && parts[0] != "" && parts[1] == "containers" && parts[2] != "" && parts[3] == "logs" {
		return parts[0], parts[2], "logs", true
	}
	if len(parts) == 5 && parts[0] != "" && parts[1] == "containers" && parts[2] != "" && parts[3] == "logs" && parts[4] == "stream" {
		return parts[0], parts[2], "logs/stream", true
	}
	return "", "", "", false
}

// handleChat 处理 LLM 对话请求。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// 暂未配置大模型
	if s.llmClient == nil {
		http.Error(w, `{"error":"LLM not configured. Set llm.enabled=true and llm.api_key in config."}`, http.StatusServiceUnavailable)
		return
	}

	// 请求构建
	var req struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Question == "" {
		http.Error(w, `{"error":"question is required"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	// 向大模型请求，流式返回每一步。
	events, err := s.llmClient.Ask(ctx, req.Question)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
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
func (s *Server) ListNodelets(ctx context.Context) ([]llm.NodeletSummary, error) {
	results := make([]llm.NodeletSummary, len(s.nodelets))
	var wg sync.WaitGroup
	for index, item := range s.nodelets {
		wg.Add(1)
		go func(index int, item config.NodeletConfig) {
			defer wg.Done()

			checkCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()

			summary := llm.NodeletSummary{
				ID:      item.ID,
				Name:    item.Name,
				Address: item.Address,
			}

			host, err := s.nodeletClient.Host(checkCtx, item.Address, item.Token)
			if err != nil {
				summary.Error = err.Error()
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
func (s *Server) ListContainers(ctx context.Context, nodeletID string) ([]nodelet.Container, error) {
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		return nil, fmt.Errorf("nodelet %q not found", nodeletID)
	}
	return s.nodeletClient.Containers(ctx, item.Address, item.Token)
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

// writeJSON 写入 JSON 响应。
func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}
