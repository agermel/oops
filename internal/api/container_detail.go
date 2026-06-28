package api

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"oops/internal/docker"
	"oops/internal/nodelet"
)

// ContainerDetail 是容器详情页的聚合视图。
type ContainerDetail struct {
	Container   nodelet.ContainerInspect `json:"container"`
	ServiceType string                   `json:"serviceType"`
	DSN         *docker.DSNInfo          `json:"dsn,omitempty"`
	Health      *healthResult            `json:"health,omitempty"`
	MCP         *mcpStatus               `json:"mcp,omitempty"`
}

// healthResult 是一次健康探测的结果。
type healthResult struct {
	Status  string `json:"status"` // alive | dead | unknown
	Message string `json:"message,omitempty"`
	Latency int64  `json:"latency"`
}

// mcpStatus 是容器级 MCP 连接的运行时状态。
type mcpStatus struct {
	Connected bool   `json:"connected"`
	ToolCount int    `json:"toolCount"`
	Error     string `json:"error,omitempty"`
}

// buildContainerDetail 聚合容器的所有信息。
func (s *Server) buildContainerDetail(ctx context.Context, nodeletID string, containerID string) (*ContainerDetail, error) {
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		return nil, errNotFound("nodelet not found")
	}

	detail, err := s.nodeletClient.InspectContainer(ctx, item.Address, item.Token, containerID)
	if err != nil {
		return nil, err
	}

	stype := docker.DetectServiceType(detail.Image)
	dsn := docker.ExtractDSN(stype, detail)

	result := &ContainerDetail{
		Container:   detail,
		ServiceType: string(stype),
		DSN:         dsn,
	}

	// TODO: MCP 容器级状态查询在 MCP Manager 重构后启用。
	return result, nil
}

// handleContainerDetail 处理容器详情 API 请求。
// GET /api/projects/:pid/servers/:sid/containers/:cid
func (s *Server) handleContainerDetail(w http.ResponseWriter, r *http.Request) {
	_, nodeletID, containerID, ok := splitContainerDetailPath(r.URL.Path)
	if !ok {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	detail, err := s.buildContainerDetail(ctx, nodeletID, containerID)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, detail)
}

// handleProjectLogsStream 处理项目级容器日志 SSE 流。
func (s *Server) handleProjectLogsStream(w http.ResponseWriter, r *http.Request) {
	_, nodeletID, containerID, ok := splitContainerDetailPath(r.URL.Path)
	if !ok {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	item, ok := s.findNodelet(nodeletID)
	if !ok {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

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

// handleProjectHealthCheck 处理容器健康检查 API 请求。
func (s *Server) handleProjectHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	_, nodeletID, containerID, ok := splitContainerDetailPath(r.URL.Path)
	if !ok {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 获取容器详情以确定服务类型。
	detail, err := s.buildContainerDetail(ctx, nodeletID, containerID)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadGateway)
		return
	}

	stype := docker.ServiceType(detail.ServiceType)
	defaultPorts := map[docker.ServiceType]int{
		docker.ServiceMySQL:    3306,
		docker.ServiceRedis:    6379,
		docker.ServicePostgres: 5432,
		docker.ServiceMongo:    27017,
	}

	result := healthResult{Status: "unknown"}
	if port, ok := defaultPorts[stype]; ok {
		result = tcpHealthCheck(ctx, detail.Container.HostID, port)
	} else {
		result.Message = "unsupported service type for health check"
	}

	writeJSON(w, result)
}

// tcpHealthCheck 对指定地址和端口执行 TCP 连接探测。
func tcpHealthCheck(ctx context.Context, host string, port int) healthResult {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return healthResult{
			Status:  "dead",
			Message: err.Error(),
			Latency: latency,
		}
	}
	conn.Close()

	return healthResult{
		Status:  "alive",
		Latency: latency,
	}
}

// splitContainerDetailPath 从路径中提取 nodeletID 和 containerID。
// 路径格式: /api/projects/:pid/servers/:sid/containers/:cid
func splitContainerDetailPath(rawPath string) (string, string, string, bool) {
	rest := strings.TrimPrefix(rawPath, "/api/projects/")
	parts := strings.Split(rest, "/")

	if len(parts) >= 5 && parts[1] == "servers" && parts[3] == "containers" {
		return parts[0], parts[2], parts[4], true
	}
	return "", "", "", false
}

// errNotFound 返回一个标记为 404 的错误。
func errNotFound(msg string) error {
	return &notFoundError{msg}
}

type notFoundError struct {
	msg string
}

func (e *notFoundError) Error() string { return e.msg }
