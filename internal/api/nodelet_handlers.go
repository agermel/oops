package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"oops/internal/logutil"
	"oops/internal/nodelet"

	"go.uber.org/zap"
)

// handleNodelets 返回中心端配置的 Nodelet 状态（读 Prober 缓存，即时响应）。
func (s *Server) handleNodelets(w http.ResponseWriter, r *http.Request) {
	if s.nodeletProber == nil || s.nodeletManager == nil {
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
	streamCtx, finishStream, ok := s.beginStream(r.Context())
	if !ok {
		writeJSONError(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	defer finishStream()

	stream, err := s.nodeletClient.ContainerLogsStream(streamCtx, item.Address, item.Token, containerID, r.URL.Query().Get("tail"))
	if err != nil {
		sanitizedError(w, "nodelet logs stream", err, http.StatusServiceUnavailable)
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
		Name    string `json:"name"`
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	cfg := nodelet.NodeletConfig{Name: req.Name, Address: req.Address, Token: req.Token}
	if s.nodeletManager == nil {
		writeJSONError(w, "nodelet manager not initialized", http.StatusServiceUnavailable)
		return
	}
	if err := s.nodeletManager.Add(&cfg); err != nil {
		logutil.Error("api: nodelet add", zap.Error(err))
		writeJSONError(w, err.Error(), mcpErrorStatus(err))
		return
	}
	if s.nodeletProber != nil {
		s.nodeletProber.OnConfigChange()
		go s.nodeletProber.ProbeNow(cfg.ID)
	}
	w.WriteHeader(http.StatusCreated)
	writeJSONOK(w)
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
		go s.nodeletProber.ProbeNow(id)
	}
	writeJSONOK(w)
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
			if slices.Contains(p.NodeletIDs, id) {
				_ = s.projectStore.RemoveNodelet(p.ID, id)
			}
		}
	}
	if s.nodeletProber != nil {
		s.nodeletProber.OnConfigChange()
	}
	writeJSONOK(w)
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
	writeJSONOK(w)
}

// handleNodeletTest handles POST /api/nodelets/test.
// 直接对目标地址做 GET /host（带 Bearer token）验证连通性和 token 有效性。
// 不依赖 manager/prober 中是否已保存该节点。
func (s *Server) handleNodeletTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Address == "" {
		writeJSONError(w, "address is required", http.StatusBadRequest)
		return
	}
	if req.Token == "" {
		writeJSONError(w, "token is required", http.StatusBadRequest)
		return
	}

	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, req.Address+"/host", nil)
	if err != nil {
		writeJSONError(w, "bad address: "+err.Error(), http.StatusBadRequest)
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+req.Token)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		writeJSONOK(w)
		return
	}
	if resp.StatusCode == http.StatusUnauthorized {
		writeJSON(w, map[string]string{"status": "error", "error": "unauthorized: token mismatch"})
		return
	}
	writeJSON(w, map[string]string{"status": "error", "error": fmt.Sprintf("returned %d", resp.StatusCode)})
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
	writeJSONOK(w)
}
