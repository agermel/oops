package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"oops/internal/llm"

	"github.com/cloudwego/eino/schema"
)

// handleSessions handles GET /api/sessions — lists global or project sessions.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	writeJSON(w, s.sessionStore.List(projectID))
}

// handleSessionGet handles GET /api/sessions/{id}.
func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.sessionStore.Get(id)
	if !ok {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("include_messages") == "true" {
		writeJSON(w, sess.ToDetail())
	} else {
		writeJSON(w, llm.SessionInfo{
			ID:           sess.ID,
			ProjectID:    sess.ProjectID,
			MessageCount: len(sess.Messages),
			CreatedAt:    sess.CreatedAt.UnixMilli(),
			UpdatedAt:    sess.UpdatedAt.UnixMilli(),
		})
	}
}

// handleSessionDelete handles DELETE /api/sessions/{id}.
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.sessionStore.Delete(id) {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSONOK(w)
}

// handleProjectSessions handles GET /api/projects/{pid}/sessions.
func (s *Server) handleProjectSessions(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	writeJSON(w, s.sessionStore.List(pid))
}

// handleProjectSessionGet handles GET /api/projects/{pid}/sessions/{id}.
func (s *Server) handleProjectSessionGet(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	id := r.PathValue("id")
	sess, ok := s.sessionStore.Get(id)
	if !ok || sess.ProjectID != pid {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("include_messages") == "true" {
		writeJSON(w, sess.ToDetail())
	} else {
		writeJSON(w, llm.SessionInfo{
			ID:           sess.ID,
			ProjectID:    sess.ProjectID,
			MessageCount: len(sess.Messages),
			CreatedAt:    sess.CreatedAt.UnixMilli(),
			UpdatedAt:    sess.UpdatedAt.UnixMilli(),
		})
	}
}

// handleProjectSessionDelete handles DELETE /api/projects/{pid}/sessions/{id}.
func (s *Server) handleProjectSessionDelete(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	id := r.PathValue("id")
	sess, ok := s.sessionStore.Get(id)
	if !ok || sess.ProjectID != pid {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	s.sessionStore.Delete(id)
	writeJSONOK(w)
}

// chatRequest 是 POST /api/chat 的请求体。
type chatRequest struct {
	SessionID string `json:"session_id"`
	Question  string `json:"question"`
}

// handleChat 处理 LLM 对话请求。
// 接受 session_id（可选）和 question，通过 SSE 流式返回每一步执行过程。
// 若不传 session_id，后端自动创建新会话并通过首条 SSE 事件返回 session ID。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	s.handleChatWithProject(w, r, "")
}

// handleProjectChat handles POST /api/projects/{pid}/chat.
func (s *Server) handleProjectChat(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	s.handleChatWithProject(w, r, pid)
}

// handleChatWithProject 是 handleChat 和 handleProjectChat 的共享实现。
// projectID 为空时表示全局会话。
func (s *Server) handleChatWithProject(w http.ResponseWriter, r *http.Request, projectID string) {
	if s.llmClient == nil {
		writeJSONError(w, "LLM not configured. Set llm.enabled=true and llm.api_key in config.", http.StatusServiceUnavailable)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Question == "" {
		writeJSONError(w, "question is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	// 获取或创建会话。
	var sess *llm.Session
	if req.SessionID != "" {
		sess = s.sessionStore.GetOrCreate(req.SessionID, projectID)
	} else {
		sess = s.sessionStore.Create(projectID)
	}

	// 查项目元数据，注入 system prompt。
	var projectCtx *llm.ProjectContext
	if projectID != "" && s.projectStore != nil {
		if p := s.projectStore.Get(projectID); p != nil {
			projectCtx = &llm.ProjectContext{
				ID:          p.ID,
				Name:        p.Name,
				Description: p.Description,
				GitHubRepo:  p.GitHubRepo,
				NodeletIDs:  p.NodeletIDs,
			}
		}
	}

	// 使用 ContextBuilder 构建上下文（compaction + system prompt + skills）。
	// BuildContext → compactIfNeeded → 注入摘要 → 组装。
	var messages []*schema.Message
	var trimmed int
	var totalTokens int

	userMsg := schema.UserMessage(req.Question)

	if s.contextBuilder != nil {
		buildResult := s.contextBuilder.Build(ctx, llm.BuildOptions{
			Session:  sess,
			Question: req.Question,
			Project:  projectCtx,
		})
		messages = buildResult.Messages
		trimmed = buildResult.Trimmed
		totalTokens = buildResult.Tokens
	} else {
		// 回退：手动组装（contextBuilder 未初始化时，如 skillStore 加载失败）。
		systemPrompt := llm.BasePrompt
		if projectCtx != nil {
			systemPrompt = systemPrompt + "\n\n" + llm.FormatProjectContext(projectCtx)
		}
		if s.skillStore != nil {
			if available := s.skillStore.RenderAvailable(); available != "" {
				systemPrompt = systemPrompt + "\n\n" + available
			}
		}
		systemMsg := schema.SystemMessage(systemPrompt)
		msgs := []*schema.Message{systemMsg}
		msgs = append(msgs, sess.Messages...)
		msgs = append(msgs, userMsg)
		trimResult := llm.TrimToBudget(msgs, llm.DefaultBudget)
		messages = trimResult.Messages
		trimmed = trimResult.Trimmed
		totalTokens = trimResult.TotalTokens
	}

	// 用户消息写入 session（Build 不修改 session，这里追加）。
	s.sessionStore.AppendMessage(sess.ID, userMsg)
	maxStep := 15

	// 生成 run ID（用于事件持久化）。
	runID := sess.ID + "_" + fmt.Sprintf("%d", time.Now().UnixNano())

	// onMessage 回调：agent 产生的每条新消息追加到 session + event store。
	seq := 0
	onMessage := func(_ context.Context, msg *schema.Message) error {
		s.sessionStore.AppendMessage(sess.ID, msg)
		if s.eventStore != nil {
			evt := llm.StepEvent{
				Type:       string(msg.Role),
				Content:    msg.Content,
				ToolName:   msg.ToolName,
				ToolCallID: msg.ToolCallID,
			}
			// 对 tool_call 类型也记录参数。
			if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					s.eventStore.AppendEvent(runID, sess.ID, projectID, seq, llm.StepEvent{
						Type:       "tool_call",
						Content:    tc.Function.Name,
						ToolName:   tc.Function.Name,
						ToolArgs:   tc.Function.Arguments,
						ToolCallID: tc.ID,
					})
					seq++
				}
			}
			s.eventStore.AppendEvent(runID, sess.ID, projectID, seq, evt)
			seq++
		}
		return nil
	}

	events, err := s.llmClient.Ask(ctx, messages, onMessage, maxStep)
	if err != nil {
		sanitizedError(w, "chat ask", err, http.StatusInternalServerError)
		return
	}

	flusher, err := requireFlusher(w)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	setSSEHeaders(w)
	w.WriteHeader(http.StatusOK)

	// 发送初始注释，强制浏览器进入流模式。
	fmt.Fprintf(w, ":ok\n\n")
	flusher.Flush()

	// 首条事件：告知客户端 session ID + Agent 类型 + 最大步数。
	sessionEvt := llm.StepEvent{
		Type:      "session",
		Content:   sess.ID,
		AgentType: "default",
		MaxStep:   maxStep,
	}
	data, _ := json.Marshal(sessionEvt)
	fmt.Fprintf(w, "data: %s\n\n", data)
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

	// 末尾事件：token 用量统计。
	statsEvt := llm.StepEvent{
		Type:    "stats",
		Tokens:  totalTokens,
		Trimmed: trimmed,
	}
	statsData, _ := json.Marshal(statsEvt)
	fmt.Fprintf(w, "data: %s\n\n", statsData)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

