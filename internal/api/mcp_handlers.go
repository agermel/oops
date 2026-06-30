package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"oops/internal/connection"
	"oops/internal/llm"
	"oops/internal/logutil"
	"oops/internal/mcp"

	"github.com/cloudwego/eino/components/tool"
	"go.uber.org/zap"
)

type statusItem struct {
	Connection connection.Connection `json:"connection"`
	Result     connection.Result     `json:"result"`
	Error      string                `json:"error,omitempty"`
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
		writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
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
		writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "output": output})
}

