package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"oops/internal/agent"
	"oops/internal/config"
	"oops/internal/connection"
	"oops/internal/connection/checker"
)

// AgentClient 是中心端访问 oops-agent 的最小接口。
type AgentClient interface {
	// Host 读取远端 Agent 所在机器信息。
	Host(context.Context, string, string) (agent.Host, error)

	// Containers 读取远端 Agent 上的容器列表。
	Containers(context.Context, string, string) ([]agent.Container, error)

	// ContainerLogs 读取远端 Agent 上某个容器的历史日志。
	ContainerLogs(context.Context, string, string, string, string) ([]agent.LogEntry, error)
}

// Options 保存中心端 API 服务依赖。
type Options struct {
	Connections []connection.Connection
	Agents      []config.AgentConfig
	AgentClient AgentClient
	Registry    *connection.Registry
	StaticDir   string
}

// Server 保存中心端 API 服务运行所需的配置和依赖。
type Server struct {
	connections []connection.Connection
	agents      []config.AgentConfig
	agentClient AgentClient
	registry    *connection.Registry
	staticDir   string
}

// statusItem 是 GUI 状态接口返回的一行连接状态。
type statusItem struct {
	Connection connection.Connection `json:"connection"`
	Result     connection.Result     `json:"result"`
	Error      string                `json:"error,omitempty"`
}

// agentItem 是 GUI 机器列表接口返回的一台 Agent 状态。
type agentItem struct {
	Agent     config.AgentConfig `json:"agent"`
	Host      agent.Host         `json:"host"`
	Available bool               `json:"available"`
	Error     string             `json:"error,omitempty"`
}

// NewFromConfig 使用配置创建中心端 API 服务。
func NewFromConfig(cfg config.Config, staticDir string) *Server {
	return New(Options{
		Connections: cfg.Connections(),
		Agents:      cfg.Agents,
		AgentClient: agent.NewClient(nil),
		Registry:    checker.NewDefaultRegistry(),
		StaticDir:   staticDir,
	})
}

// New 创建中心端 API 服务。
func New(options Options) *Server {
	if options.AgentClient == nil {
		options.AgentClient = agent.NewClient(nil)
	}
	if options.Registry == nil {
		options.Registry = checker.NewDefaultRegistry()
	}
	if options.StaticDir == "" {
		options.StaticDir = "web/dist"
	}

	return &Server{
		connections: options.Connections,
		agents:      options.Agents,
		agentClient: options.AgentClient,
		registry:    options.Registry,
		staticDir:   options.StaticDir,
	}
}

// Routes 返回中心端 API 和静态文件路由。
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/connections/status", s.handleConnectionStatus)
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/", s.handleAgentResource)
	mux.HandleFunc("/", s.handleStatic)
	return mux
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

// handleAgents 返回中心端配置的 Agent 机器列表。
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/agents" {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	results := make([]agentItem, len(s.agents))
	var wg sync.WaitGroup
	for index, item := range s.agents {
		wg.Add(1)
		go func(index int, item config.AgentConfig) {
			defer wg.Done()

			checkCtx, checkCancel := context.WithTimeout(ctx, 6*time.Second)
			defer checkCancel()

			host, err := s.agentClient.Host(checkCtx, item.Address, item.Token)
			result := agentItem{
				Agent:     item,
				Host:      host,
				Available: err == nil,
			}
			if err != nil {
				result.Error = err.Error()
				result.Host = agent.Host{
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

// handleAgentResource 返回指定 Agent 的容器列表或容器日志。
func (s *Server) handleAgentResource(w http.ResponseWriter, r *http.Request) {
	agentID, containerID, action, ok := splitAgentResourcePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	item, ok := s.findAgent(agentID)
	if !ok {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if action == "containers" {
		containers, err := s.agentClient.Containers(ctx, item.Address, item.Token)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, containers)
		return
	}

	if action == "logs" {
		logs, err := s.agentClient.ContainerLogs(ctx, item.Address, item.Token, containerID, r.URL.Query().Get("tail"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, logs)
		return
	}

	http.NotFound(w, r)
}

// findAgent 按配置 ID 查找 Agent。
func (s *Server) findAgent(id string) (config.AgentConfig, bool) {
	for _, item := range s.agents {
		if item.ID == id {
			return item, true
		}
	}
	return config.AgentConfig{}, false
}

// splitAgentResourcePath 拆分中心端 Agent 子资源路径。
func splitAgentResourcePath(rawPath string) (string, string, string, bool) {
	rest := strings.TrimPrefix(rawPath, "/api/agents/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "containers" {
		return parts[0], "", "containers", true
	}
	if len(parts) == 4 && parts[0] != "" && parts[1] == "containers" && parts[2] != "" && parts[3] == "logs" {
		return parts[0], parts[2], "logs", true
	}
	return "", "", "", false
}

// handleStatic 在生产模式下提供 React 构建产物。
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}

	indexPath := filepath.Join(s.staticDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		http.Error(w, "React build is missing. Run: npm --prefix web install && npm --prefix web run build", http.StatusServiceUnavailable)
		return
	}
	http.ServeFile(w, r, indexPath)
}

// writeJSON 写入 JSON 响应。
func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}
