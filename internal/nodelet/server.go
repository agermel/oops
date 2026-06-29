package nodelet

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"oops/internal/logutil"
	"go.uber.org/zap"
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

// Server 暴露 Nodelet 的 HTTP 协议。鉴权为强制要求。
type Server struct {
	provider Provider
	token    string
	limiter  *rateLimiter
}

// NewServer 创建不带鉴权的 Nodelet HTTP 服务（仅用于测试）。
func NewServer(provider Provider) *Server {
	rl := newRateLimiter()
	go rl.cleanup(5 * time.Minute)
	return &Server{provider: provider, limiter: rl}
}

// NewServerWithToken 创建带强制鉴权的 Nodelet HTTP 服务。token 为空时所有受保护接口返回 503。
func NewServerWithToken(provider Provider, token string) *Server {
	rl := newRateLimiter()
	go rl.cleanup(5 * time.Minute)
	return &Server{provider: provider, token: token, limiter: rl}
}

// Routes 返回 Nodelet 的 HTTP 路由。
// 受保护路由: securityHeaders → requestLogger → rateLimit → authorize → handler
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(HealthPath, securityHeaders(s.handleHealth))
	mux.HandleFunc(HostPath, requestLogger(securityHeaders(s.rateLimit(s.authorize(s.handleHost)))))
	mux.HandleFunc(ContainersPath, requestLogger(securityHeaders(s.rateLimit(s.authorize(s.handleContainers)))))
	mux.HandleFunc("/containers/", requestLogger(securityHeaders(s.rateLimit(s.authorize(s.handleContainer)))))
	return mux
}

// authorize 校验中心端调用 Nodelet 的 Bearer Token。Token 为空时直接拒绝。
func (s *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			logutil.Warn("nodelet: request without token configured", zap.String("path", r.URL.Path))
			writeJSONError(w, http.StatusServiceUnavailable, "auth not configured")
			return
		}

		expected := "Bearer " + s.token
		actual := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			logutil.Warn("nodelet: authorize failed",
				zap.String("ip", extractIP(r)),
				zap.String("path", r.URL.Path),
			)
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// rateLimit Nodelet 接口限流（50 req/s，突发 100）。中心端是已知调用方，比 Web API 更严格。
func (s *Server) rateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !s.limiter.allow(ip, 50, 100) {
			logutil.Warn("nodelet: rate limited",
				zap.String("ip", ip),
				zap.String("path", r.URL.Path),
			)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// Shutdown 安全关闭后台 goroutine（限流器清理等）。
func (s *Server) Shutdown() {
	s.limiter.shutdown()
}

// handleHealth 返回 Nodelet 存活状态。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	logutil.Debug("nodelet: health check", zap.String("ip", extractIP(r)))
	writeJSON(w, http.StatusOK, Health{Status: "ok"})
}

// handleHost 返回 Nodelet 所在机器的信息。
func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	host, err := s.provider.Host(r)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		logutil.Errorf("nodelet: host: %v", err)
		writeJSONError(w, http.StatusServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
		return
	}
	logutil.Info("nodelet: host", zap.Int64("latencyMs", latency))
	writeJSON(w, http.StatusOK, host)
}

// handleContainers 返回 Nodelet 所在机器上的容器列表。
func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	containers, err := s.provider.Containers(r)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		logutil.Errorf("nodelet: containers: %v", err)
		writeJSONError(w, http.StatusServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
		return
	}
	logutil.Info("nodelet: containers",
		zap.Int("count", len(containers)),
		zap.Int64("latencyMs", latency),
	)
	writeJSON(w, http.StatusOK, containers)
}

// handleContainer 处理单个容器下的子资源。
func (s *Server) handleContainer(w http.ResponseWriter, r *http.Request) {
	containerID, action, ok := splitContainerPath(r.URL.Path)
	if !ok {
		logutil.Debug("nodelet: invalid container path", zap.String("path", r.URL.Path))
		http.NotFound(w, r)
		return
	}

	switch action {
	case "inspect":
		start := time.Now()
		detail, err := s.provider.ContainerInspect(r, containerID)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			logutil.Errorf("nodelet: inspect %s: %v", containerID, err)
			writeJSONError(w, http.StatusServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
			return
		}
		logutil.Info("nodelet: inspect",
			zap.String("containerID", containerID),
			zap.Int64("latencyMs", latency),
		)
		writeJSON(w, http.StatusOK, detail)
	case "logs":
		start := time.Now()
		logs, err := s.provider.ContainerLogs(r, containerID)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			logutil.Errorf("nodelet: logs %s: %v", containerID, err)
			writeJSONError(w, http.StatusServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
			return
		}
		logutil.Info("nodelet: logs",
			zap.String("containerID", containerID),
			zap.Int("entryCount", len(logs)),
			zap.Int64("latencyMs", latency),
		)
		writeJSON(w, http.StatusOK, logs)
	case "logs/stream":
		logutil.Debug("nodelet: logs/stream start", zap.String("containerID", containerID))
		s.handleContainerLogsStream(w, r, containerID)
	default:
		http.NotFound(w, r)
	}
}

// handleContainerLogsStream 以 SSE 持续返回容器日志。
func (s *Server) handleContainerLogsStream(w http.ResponseWriter, r *http.Request, containerID string) {
	logs, err := s.provider.ContainerLogsStream(r, containerID)
	if err != nil {
		logutil.Errorf("nodelet: logs/stream %s: %v", containerID, err)
		writeJSONError(w, http.StatusServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
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

	for {
		select {
		case <-r.Context().Done():
			return
		case entry, ok := <-logs:
			if !ok {
				return
			}
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
}

// containerIDPattern 匹配 Docker 容器 ID（64 字符 hex）。
var containerIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// isValidContainerID 校验容器 ID 格式，防止路径遍历和非法输入。
func isValidContainerID(id string) bool {
	return containerIDPattern.MatchString(id)
}

// splitContainerPath 拆分 /containers/{id}/{action} 路径。
func splitContainerPath(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/containers/")
	parts := strings.Split(rest, "/")

	extractID := func(raw string) (string, bool) {
		id, err := url.PathUnescape(raw)
		if err != nil {
			return "", false
		}
		// 先解码再检查 ..，防止 %2e%2e 绕过。
		if strings.Contains(id, "..") {
			return "", false
		}
		if !isValidContainerID(id) {
			return "", false
		}
		return id, true
	}

	if len(parts) == 2 && parts[0] != "" {
		containerID, ok := extractID(parts[0])
		if !ok {
			return "", "", false
		}
		action := parts[1]
		if action == "logs" || action == "inspect" {
			return containerID, action, true
		}
	}
	if len(parts) == 3 && parts[0] != "" && parts[1] == "logs" && parts[2] == "stream" {
		containerID, ok := extractID(parts[0])
		if !ok {
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

// writeJSONError 写入 JSON 格式的错误响应。
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
