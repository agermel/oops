package nodelet

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Provider 提供当前 Nodelet 管理的机器和容器信息。
type Provider interface {
	// Host 返回当前机器和 Docker daemon 的基础信息。
	Host(r *http.Request) (Host, error)

	// Containers 返回当前机器上的容器列表。
	Containers(r *http.Request) ([]Container, error)

	// ContainerInspect 返回指定容器的详细信息（环境变量、端口等）。
	ContainerInspect(r *http.Request, containerID string) (ContainerInspect, error)

	// ContainerLogs 返回指定容器的历史日志。
	ContainerLogs(r *http.Request, containerID string) ([]LogEntry, error)

	// ContainerLogsStream 返回指定容器的实时日志流。
	ContainerLogsStream(r *http.Request, containerID string) (<-chan LogEntry, error)
}

// Server 暴露 Nodelet 的 HTTP 协议。
type Server struct {
	provider Provider
	token    string
}

// NewServer 创建 Nodelet HTTP 服务。
func NewServer(provider Provider) *Server {
	return NewServerWithToken(provider, "")
}

// NewServerWithToken 创建带鉴权的 Nodelet HTTP 服务。
func NewServerWithToken(provider Provider, token string) *Server {
	return &Server{provider: provider, token: token}
}

// Routes 返回 Nodelet 的 HTTP 路由。
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(HealthPath, s.handleHealth)
	mux.HandleFunc(HostPath, s.authorize(s.handleHost))
	mux.HandleFunc(ContainersPath, s.authorize(s.handleContainers))
	mux.HandleFunc("/containers/", s.authorize(s.handleContainer))
	return mux
}

// authorize 校验中心端调用 Nodelet 的 Bearer Token。
func (s *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next(w, r)
			return
		}

		expected := "Bearer " + s.token
		actual := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// handleHealth 返回 Nodelet 存活状态。
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Health{Status: "ok"})
}

// handleHost 返回 Nodelet 所在机器的信息。
func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	host, err := s.provider.Host(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, host)
}

// handleContainers 返回 Nodelet 所在机器上的容器列表。
func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	containers, err := s.provider.Containers(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, containers)
}

// handleContainer 处理单个容器下的子资源。
func (s *Server) handleContainer(w http.ResponseWriter, r *http.Request) {
	containerID, action, ok := splitContainerPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	switch action {
	case "inspect":
		detail, err := s.provider.ContainerInspect(r, containerID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	case "logs":
		logs, err := s.provider.ContainerLogs(r, containerID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, logs)
	case "logs/stream":
		s.handleContainerLogsStream(w, r, containerID)
	default:
		http.NotFound(w, r)
	}
}

// handleContainerLogsStream 以 SSE 持续返回容器日志。
func (s *Server) handleContainerLogsStream(w http.ResponseWriter, r *http.Request, containerID string) {
	logs, err := s.provider.ContainerLogsStream(r, containerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	for entry := range logs {
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
		flusher.Flush()
	}
}

// splitContainerPath 拆分 /containers/{id}/{action} 路径。
func splitContainerPath(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/containers/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[0] != "" {
		containerID, err := url.PathUnescape(parts[0])
		if err != nil {
			return "", "", false
		}
		action := parts[1]
		if action == "logs" || action == "inspect" {
			return containerID, action, true
		}
	}
	if len(parts) == 3 && parts[0] != "" && parts[1] == "logs" && parts[2] == "stream" {
		containerID, err := url.PathUnescape(parts[0])
		if err != nil {
			return "", "", false
		}
		return containerID, "logs/stream", true
	}
	return "", "", false
}

// writeJSON 写入 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
