package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"oops/internal/docker"
	"oops/internal/nodelet"
)

// ContainerDetail 是容器详情页的聚合视图。
type ContainerDetail struct {
	Container       nodelet.ContainerInspect `json:"container"`
	ServiceType     string                   `json:"serviceType"`
	DSN             *docker.DSNInfo          `json:"dsn,omitempty"`
	DSNOverrides    map[string]string        `json:"dsnOverrides,omitempty"`
	HasDSNOverrides bool                     `json:"hasDSNOverrides"`
	MCP             *mcpStatus               `json:"mcp,omitempty"`
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
	dsn := docker.ExtractDSN(stype, detail, nodeletHost(item.Address))

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
		Container:       detail,
		ServiceType:     string(stype),
		DSN:             dsn,
		DSNOverrides:    dsnOverrides,
		HasDSNOverrides: len(dsnOverrides) > 0,
	}

	// 查找绑定到当前容器的 MCP 连接。
	if s.mcpManager != nil && (stype.IsDatabase() || stype.IsMiddleware()) {
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
		}
	}

	return result, nil
}

// handleContainerDetail handles GET /api/projects/{pid}/servers/{sid}/containers/{cid}.
func (s *Server) handleContainerDetail(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("sid")
	containerID := r.PathValue("cid")

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	detail, err := s.buildContainerDetail(ctx, nodeletID, containerID)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, detail)
}

// handleProjectLogsStream handles GET /api/projects/{pid}/servers/{sid}/containers/{cid}/logs/stream.
func (s *Server) handleProjectLogsStream(w http.ResponseWriter, r *http.Request) {
	streamCtx, finishStream, ok := s.beginStream(r.Context())
	if !ok {
		writeJSONError(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	defer finishStream()

	nodeletID := r.PathValue("sid")
	containerID := r.PathValue("cid")

	item, ok := s.findNodelet(nodeletID)
	if !ok {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	stream, err := s.nodeletClient.ContainerLogsStream(streamCtx, item.Address, item.Token, containerID, r.URL.Query().Get("tail"))
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer stream.Close()
	stopClose := context.AfterFunc(streamCtx, func() { _ = stream.Close() })
	defer stopClose()

	flusher, err := requireFlusher(w)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	setSSEHeaders(w)

	copyAndFlush(w, flusher, stream)
}

// handleContainerMCPGet handles GET /api/projects/{pid}/servers/{sid}/containers/{cid}/mcp.
func (s *Server) handleContainerMCPGet(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("sid")
	containerID := r.PathValue("cid")
	if s.mcpManager == nil {
		writeJSON(w, nil)
		return
	}
	bound := s.mcpManager.FindByContainer(nodeletID, containerID)
	writeJSON(w, bound)
}

// handleContainerMCPDelete handles DELETE /api/projects/{pid}/servers/{sid}/containers/{cid}/mcp.
func (s *Server) handleContainerMCPDelete(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("sid")
	containerID := r.PathValue("cid")
	if s.mcpManager == nil {
		writeJSONError(w, "mcp manager not initialized", http.StatusServiceUnavailable)
		return
	}
	bound := s.mcpManager.FindByContainer(nodeletID, containerID)
	if bound == nil {
		writeJSONError(w, "no connection bound to this container", http.StatusNotFound)
		return
	}
	if err := s.mcpManager.Remove(bound.ID); err != nil {
		writeJSONError(w, err.Error(), mcpErrorStatus(err))
		return
	}
	writeJSONOK(w)
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

// handleContainerDSNGet handles GET /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn.
func (s *Server) handleContainerDSNGet(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("sid")
	containerID := r.PathValue("cid")
	item, itemOK := s.findNodelet(nodeletID)
	if !itemOK {
		writeJSONError(w, "nodelet not found", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	detail, err := s.nodeletClient.InspectContainer(ctx, item.Address, item.Token, containerID)
	if err != nil {
		writeJSONError(w, "inspect container: "+err.Error(), http.StatusInternalServerError)
		return
	}
	stype := docker.DetectServiceType(detail.Image)
	dsn := docker.ExtractDSN(stype, detail, nodeletHost(item.Address))
	detected := dsnInfoToMap(dsn)
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
}

// handleContainerDSNPut handles PUT /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn.
func (s *Server) handleContainerDSNPut(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("sid")
	containerID := r.PathValue("cid")
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
	writeJSONOK(w)
}

// handleContainerDSNDelete handles DELETE /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn.
func (s *Server) handleContainerDSNDelete(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("sid")
	containerID := r.PathValue("cid")
	if s.dsnStore == nil {
		writeJSONError(w, "dsn store not available", http.StatusInternalServerError)
		return
	}
	if err := s.dsnStore.Delete(nodeletID, containerID); err != nil {
		writeJSONError(w, "delete dsn: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONOK(w)
}

// nodeletHost 从 nodelet 地址中提取 IP。
// 支持 "http://203.0.113.10:8686"（带 scheme）和 "10.0.0.5:8686"（纯 host:port）两种格式。
func nodeletHost(addr string) string {
	// 带 scheme 的 URL 格式。
	if u, err := url.Parse(addr); err == nil && u.Host != "" {
		h, _, err := net.SplitHostPort(u.Host)
		if err == nil {
			return h
		}
		return u.Host
	}
	// 纯 host:port 格式。
	h, _, err := net.SplitHostPort(addr)
	if err == nil {
		return h
	}
	return addr
}

// errNotFound 返回一个标记为 404 的错误。
func errNotFound(msg string) error {
	return &notFoundError{msg}
}

type notFoundError struct {
	msg string
}

func (e *notFoundError) Error() string { return e.msg }
