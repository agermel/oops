package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"oops/internal/config"
	llmtools "oops/internal/llm/tools"
	"oops/internal/logutil"
	"oops/internal/mcp"

	"github.com/cloudwego/eino/components/tool"
	"go.uber.org/zap"
)

// onMCPToolsChanged 是 MCP Manager 的工具变更回调。
// 合并原生工具和 MCP 工具后热更新 LLM Client。
func (s *Server) onMCPToolsChanged(mcpTools []mcp.ConnectionTool) {
	if s.llmClient == nil {
		return
	}

	nativeTools, err := llmtools.NewTools(s, s.skillStore)
	if err != nil {
		logutil.Error("mcp: create native tools", zap.Error(err))
		return
	}

	mcpTools = s.withMCPToolServerNames(mcpTools)
	mcpTools = namespaceMCPTools(mcpTools, nativeToolNames(context.Background(), nativeTools))
	allTools := make([]tool.InvokableTool, 0, len(nativeTools)+len(mcpTools))
	allTools = append(allTools, nativeTools...)
	for _, mt := range mcpTools {
		if it, ok := mt.Tool.(tool.InvokableTool); ok {
			allTools = append(allTools, namespacedMCPTool{
				modelName: mt.ModelName,
				inner:     it,
			})
		}
	}

	s.llmClient.UpdateTools(allTools)
	logutil.Info("mcp: tools updated",
		zap.Int("total", len(allTools)),
		zap.Int("native", len(nativeTools)),
		zap.Int("mcp", len(mcpTools)),
	)
}

func (s *Server) namespacedMCPToolEntries(ctx context.Context) []mcp.ConnectionTool {
	return s.namespacedMCPToolEntriesForProject(ctx, "")
}

func (s *Server) namespacedMCPToolEntriesForProject(ctx context.Context, projectID string) []mcp.ConnectionTool {
	if s.mcpManager == nil {
		return nil
	}
	entries := s.mcpToolEntriesForProject(projectID)
	nativeTools, err := llmtools.NewTools(s, s.skillStore)
	if err != nil {
		logutil.Error("mcp: create native tools", zap.Error(err))
		return namespaceMCPTools(entries, nil)
	}
	return namespaceMCPTools(entries, nativeToolNames(ctx, nativeTools))
}

func (s *Server) chatToolsAndInventory(ctx context.Context, projectID string) ([]tool.InvokableTool, string) {
	nativeTools, err := llmtools.NewTools(s, s.skillStore)
	if err != nil {
		logutil.Error("mcp: create native tools", zap.Error(err))
		return nil, ""
	}
	entries := s.mcpToolEntriesForProject(projectID)
	namespacedEntries := namespaceMCPTools(entries, nativeToolNames(ctx, nativeTools))

	allTools := make([]tool.InvokableTool, 0, len(nativeTools)+len(namespacedEntries))
	allTools = append(allTools, nativeTools...)
	for _, mt := range namespacedEntries {
		if it, ok := mt.Tool.(tool.InvokableTool); ok {
			allTools = append(allTools, namespacedMCPTool{
				modelName: mt.ModelName,
				inner:     it,
			})
		}
	}
	return allTools, formatMCPToolInventory(namespacedEntries)
}

func (s *Server) mcpToolEntriesForProject(projectID string) []mcp.ConnectionTool {
	if s.mcpManager == nil {
		return nil
	}
	entries := s.withMCPToolServerNames(s.mcpManager.GetConnectionToolEntries())
	return filterMCPToolEntriesForProject(entries, projectID, s.projectStore)
}

func filterMCPToolEntriesForProject(entries []mcp.ConnectionTool, projectID string, projectStore *config.ProjectStore) []mcp.ConnectionTool {
	if projectID == "" || projectStore == nil {
		return entries
	}
	p := projectStore.Get(projectID)
	if p == nil {
		return nil
	}
	nodeletSet := make(map[string]struct{}, len(p.NodeletIDs))
	for _, id := range p.NodeletIDs {
		nodeletSet[id] = struct{}{}
	}
	filtered := make([]mcp.ConnectionTool, 0, len(entries))
	for _, entry := range entries {
		if _, ok := nodeletSet[entry.NodeletID]; ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (s *Server) withMCPToolServerNames(entries []mcp.ConnectionTool) []mcp.ConnectionTool {
	if len(entries) == 0 {
		return nil
	}
	out := slices.Clone(entries)
	if s.nodeletManager == nil {
		return out
	}

	nodeletNames := make(map[string]string)
	for _, n := range s.nodeletManager.List() {
		nodeletNames[n.ID] = n.Name
	}
	for i := range out {
		if name := nodeletNames[out[i].NodeletID]; name != "" {
			out[i].ServerName = name
		}
	}
	return out
}

func namespacedMCPToolsByConnection(entries []mcp.ConnectionTool) map[string][]mcp.ToolInfo {
	byConn := make(map[string][]mcp.ToolInfo)
	for _, entry := range entries {
		byConn[entry.ConnectionID] = append(byConn[entry.ConnectionID], mcp.ToolInfo{
			Name:           entry.ModelName,
			Description:    entry.Description,
			OriginalName:   entry.OriginalName,
			ModelName:      entry.ModelName,
			ConnectionType: entry.ConnectionType,
		})
	}
	return byConn
}

func (s *Server) decorateMCPConnections(ctx context.Context, conns []mcp.ConnectionWithStatus) []mcp.ConnectionWithStatus {
	byConn := namespacedMCPToolsByConnection(s.namespacedMCPToolEntries(ctx))
	out := slices.Clone(conns)
	for i := range out {
		if tools, ok := byConn[out[i].ID]; ok {
			out[i].Tools = tools
		}
	}
	return out
}

// toolItem 是 GET /api/tools 返回的单个工具条目。
type toolItem struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Enabled        bool   `json:"enabled"`
	OriginalName   string `json:"originalName,omitempty"`
	ModelName      string `json:"modelName,omitempty"`
	ConnectionType string `json:"connectionType,omitempty"`
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
	nativeTools, err := llmtools.NewTools(s, s.skillStore)
	nativeNames := map[string]struct{}{}
	if err == nil {
		nativeNames = nativeToolNames(r.Context(), nativeTools)
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
		entries := s.withMCPToolServerNames(s.mcpManager.GetConnectionToolEntries())
		connectionTools := namespacedMCPToolsByConnection(namespaceMCPTools(entries, nativeNames))
		for _, conn := range s.mcpManager.List() {
			mcpTools := connectionTools[conn.ID]
			if len(mcpTools) == 0 {
				continue
			}
			name := conn.Name
			if name == "" {
				name = conn.ID
			}
			groupName := name
			if _, exists := resp.MCP[groupName]; exists {
				groupName = fmt.Sprintf("%s (%s)", name, conn.ID)
			}
			items := make([]toolItem, 0, len(mcpTools))
			for _, mt := range mcpTools {
				items = append(items, toolItem{
					Name:           mt.Name,
					Description:    mt.Description,
					Enabled:        !disabled[mt.Name],
					OriginalName:   mt.OriginalName,
					ModelName:      mt.ModelName,
					ConnectionType: mt.ConnectionType,
				})
			}
			if len(items) > 0 {
				resp.MCP[groupName] = items
			}
		}
	}

	writeJSON(w, resp)
}

// handleToolToggle handles PUT /api/tools/{name}.
func (s *Server) handleToolToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.llmClient == nil {
		writeJSONError(w, "llm not configured", http.StatusServiceUnavailable)
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
	writeJSONOK(w)
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
	if strings.Contains(msg, "id is required") || strings.Contains(msg, "nodeletId is required") || strings.Contains(msg, "not in the allowed list") {
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
	writeJSON(w, s.decorateMCPConnections(r.Context(), s.mcpManager.List()))
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
	writeJSONOK(w)
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
	writeJSONOK(w)
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
	writeJSONOK(w)
}

// handleMCPLogs handles GET /api/mcp/connections/{id}/logs.
func (s *Server) handleMCPLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.mcpManager == nil {
		writeJSONError(w, "mcp manager not initialized", http.StatusServiceUnavailable)
		return
	}
	logs, ok := s.mcpManager.ConnectionLogs(id, mcpLogTail(r))
	if !ok {
		writeJSONError(w, "connection not found", http.StatusNotFound)
		return
	}
	writeJSON(w, logs)
}

// handleMCPLogsStream handles GET /api/mcp/connections/{id}/logs/stream.
func (s *Server) handleMCPLogsStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.mcpManager == nil {
		writeJSONError(w, "mcp manager not initialized", http.StatusServiceUnavailable)
		return
	}

	ch, cancel, ok := s.mcpManager.SubscribeConnectionLogs(id, mcpLogTail(r))
	if !ok {
		writeJSONError(w, "connection not found", http.StatusNotFound)
		return
	}
	defer cancel()

	flusher, err := requireFlusher(w)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	setSSEHeaders(w)
	fmt.Fprintf(w, ":ok\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func mcpLogTail(r *http.Request) int {
	tail, err := strconv.Atoi(r.URL.Query().Get("tail"))
	if err != nil || tail <= 0 {
		return 200
	}
	if tail > 1000 {
		return 1000
	}
	return tail
}

// handleMCPTest handles POST /api/mcp/connections/test.
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
		switch {
		case errors.Is(err, mcp.ErrTestConnectFailed):
			writeJSON(w, map[string]string{"status": "transport_error", "error": err.Error()})
		default:
			writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
		}
		return
	}
	writeJSONOK(w)
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
		switch {
		case errors.Is(err, mcp.ErrConnectionNotRunning):
			writeJSON(w, map[string]string{"status": "unavailable", "error": err.Error()})
		case errors.Is(err, mcp.ErrToolCallFailed):
			writeJSON(w, map[string]string{"status": "transport_error", "error": err.Error()})
		default:
			writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
		}
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "output": output})
}
