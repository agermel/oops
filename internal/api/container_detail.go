package api

import (
	"context"
	"encoding/json"
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
	Container      nodelet.ContainerInspect `json:"container"`
	ServiceType    string                   `json:"serviceType"`
	DSN            *docker.DSNInfo          `json:"dsn,omitempty"`
	DSNOverrides   map[string]string        `json:"dsnOverrides,omitempty"`
	HasDSNOverrides bool                    `json:"hasDSNOverrides"`
	Health         *healthResult            `json:"health,omitempty"`
	MCP            *mcpStatus               `json:"mcp,omitempty"`
}

// healthResult 是一次健康探测的结果。
type healthResult struct {
	Status  string `json:"status"` // alive | dead | unknown
	Message string `json:"message,omitempty"`
	Latency int64  `json:"latency"`
}

// mcpStatus 是容器级 MCP 连接的运行时状态。
type mcpStatus struct {
	Connected    bool   `json:"connected"`
	ToolCount    int    `json:"toolCount"`
	Error        string `json:"error,omitempty"`
	ConnectionID string `json:"connectionId,omitempty"`
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

	// 合并用户 DSN 覆盖值。
	var dsnOverrides map[string]string
	if s.dsnStore != nil {
		dsnOverrides = s.dsnStore.Get(nodeletID, containerID)
	}
	if len(dsnOverrides) > 0 {
		if dsn == nil {
			dsn = &docker.DSNInfo{}
		}
		if v, ok := dsnOverrides["host"]; ok {
			dsn.Host = v
		}
		if v, ok := dsnOverrides["port"]; ok {
			if p, err := strconv.Atoi(v); err == nil {
				dsn.Port = p
			}
		}
		if v, ok := dsnOverrides["user"]; ok {
			dsn.User = v
		}
		if v, ok := dsnOverrides["database"]; ok {
			dsn.Database = v
		}
		if v, ok := dsnOverrides["raw"]; ok {
			dsn.Raw = v
		}
	}

	result := &ContainerDetail{
		Container:      detail,
		ServiceType:    string(stype),
		DSN:            dsn,
		DSNOverrides:   dsnOverrides,
		HasDSNOverrides: len(dsnOverrides) > 0,
	}

	// 查找匹配的 MCP 连接：优先按容器 ID 精确匹配，回退按类型匹配。
	if s.mcpManager != nil && stype.IsDatabase() {
		// Primary: match by container binding.
		bound := s.mcpManager.FindByContainer(nodeletID, containerID)
		if bound != nil {
			if bound.Status == "running" {
				result.MCP = &mcpStatus{
					Connected:    true,
					ToolCount:    bound.ToolCount,
					ConnectionID: bound.ID,
				}
			} else {
				result.MCP = &mcpStatus{
					Connected:    false,
					Error:        bound.Error,
					ConnectionID: bound.ID,
				}
			}
		} else {
			// Fallback: type-based matching for backward compat with
			// existing connections that have no container binding.
			for _, conn := range s.mcpManager.List() {
				if conn.Type == string(stype) && conn.Status == "running" {
					result.MCP = &mcpStatus{
						Connected:    true,
						ToolCount:    conn.ToolCount,
						ConnectionID: conn.ID,
					}
					break
				}
			}
			if result.MCP == nil {
				for _, conn := range s.mcpManager.List() {
					if conn.Type == string(stype) {
						result.MCP = &mcpStatus{
							Connected:    false,
							Error:        conn.Error,
							ConnectionID: conn.ID,
						}
						break
					}
				}
			}
		}
	}

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

// handleContainerMCPConnection 处理容器绑定的 MCP 连接。
// GET/DELETE /api/projects/:pid/servers/:sid/containers/:cid/mcp
func (s *Server) handleContainerMCPConnection(w http.ResponseWriter, r *http.Request) {
	_, nodeletID, containerID, ok := splitContainerDetailPath(r.URL.Path)
	if !ok {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	if s.mcpManager == nil {
		writeJSON(w, nil)
		return
	}

	switch r.Method {
	case http.MethodGet:
		bound := s.mcpManager.FindByContainer(nodeletID, containerID)
		writeJSON(w, bound)

	case http.MethodDelete:
		bound := s.mcpManager.FindByContainer(nodeletID, containerID)
		if bound == nil {
			writeJSONError(w, "no connection bound to this container", http.StatusNotFound)
			return
		}
		if err := s.mcpManager.Remove(bound.ID); err != nil {
			writeJSONError(w, err.Error(), mcpErrorStatus(err))
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// dsnConfigResponse is the JSON shape for the DSN config endpoint.
type dsnConfigResponse struct {
	Detected     map[string]string `json:"detected"`
	Overrides    map[string]string `json:"overrides"`
	Merged       map[string]string `json:"merged"`
	HasOverrides bool              `json:"hasOverrides"`
}

// dsnSaveRequest is the JSON shape for saving DSN overrides.
type dsnSaveRequest struct {
	Pairs map[string]string `json:"pairs"`
}

// dsnInfoToMap converts a docker.DSNInfo to a flat KV map.
func dsnInfoToMap(dsn *docker.DSNInfo) map[string]string {
	if dsn == nil {
		return map[string]string{}
	}
	m := map[string]string{}
	if dsn.Host != "" {
		m["host"] = dsn.Host
	}
	if dsn.Port > 0 {
		m["port"] = strconv.Itoa(dsn.Port)
	}
	if dsn.User != "" {
		m["user"] = dsn.User
	}
	if dsn.Database != "" {
		m["database"] = dsn.Database
	}
	if dsn.Raw != "" {
		m["raw"] = dsn.Raw
	}
	return m
}

// mergeDSN merges user overrides on top of detected values.
func mergeDSN(detected, overrides map[string]string) map[string]string {
	merged := make(map[string]string, len(detected)+len(overrides))
	for k, v := range detected {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	return merged
}

// handleContainerDSN handles per-container DSN configuration.
// GET  /api/projects/:pid/servers/:sid/containers/:cid/dsn
// PUT  /api/projects/:pid/servers/:sid/containers/:cid/dsn
// DELETE /api/projects/:pid/servers/:sid/containers/:cid/dsn
func (s *Server) handleContainerDSN(w http.ResponseWriter, r *http.Request) {
	_, nodeletID, containerID, ok := splitContainerDetailPath(r.URL.Path)
	if !ok {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	// 需要容器详情来获取检测到的 DSN（GET 需要，PUT/DELETE 不需要但用于验证容器存在。
	item, itemOK := s.findNodelet(nodeletID)
	if !itemOK {
		writeJSONError(w, "nodelet not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		detail, err := s.nodeletClient.InspectContainer(ctx, item.Address, item.Token, containerID)
		if err != nil {
			writeJSONError(w, "inspect container: "+err.Error(), http.StatusInternalServerError)
			return
		}

		stype := docker.DetectServiceType(detail.Image)
		var detected map[string]string
		if stype.IsDatabase() {
			dsn := docker.ExtractDSN(stype, detail)
			detected = dsnInfoToMap(dsn)
		} else {
			detected = map[string]string{}
		}

		var overrides map[string]string
		if s.dsnStore != nil {
			overrides = s.dsnStore.Get(nodeletID, containerID)
		}
		if overrides == nil {
			overrides = map[string]string{}
		}

		writeJSON(w, dsnConfigResponse{
			Detected:     detected,
			Overrides:    overrides,
			Merged:       mergeDSN(detected, overrides),
			HasOverrides: len(overrides) > 0,
		})

	case http.MethodPut:
		if s.dsnStore == nil {
			writeJSONError(w, "dsn store not available", http.StatusInternalServerError)
			return
		}

		var req dsnSaveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}

		if err := s.dsnStore.Set(nodeletID, containerID, req.Pairs); err != nil {
			writeJSONError(w, "save dsn: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	case http.MethodDelete:
		if s.dsnStore == nil {
			writeJSONError(w, "dsn store not available", http.StatusInternalServerError)
			return
		}

		if err := s.dsnStore.Delete(nodeletID, containerID); err != nil {
			writeJSONError(w, "delete dsn: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// errNotFound 返回一个标记为 404 的错误。
func errNotFound(msg string) error {
	return &notFoundError{msg}
}

type notFoundError struct {
	msg string
}

func (e *notFoundError) Error() string { return e.msg }
