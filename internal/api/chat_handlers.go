package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"oops/internal/llm/budget"
	"oops/internal/llm/ctxbuilder"
	agentevents "oops/internal/llm/events"
	"oops/internal/llm/prompt"
	"oops/internal/llm/session"

	"github.com/cloudwego/eino/components/tool"
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
		writeJSON(w, session.SessionInfo{
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
		writeJSON(w, session.SessionInfo{
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

type chatRunContext struct {
	session     *session.Session
	messages    []*schema.Message
	trimmed     int
	totalTokens int
	maxStep     int
	runID       string
	seq         int
	tools       []tool.InvokableTool
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

	runCtx := s.buildChatRunContext(ctx, req, projectID)
	onMessage := func(_ context.Context, msg *schema.Message) error {
		s.persistAgentMessage(runCtx.session, runCtx.runID, projectID, &runCtx.seq, msg)
		return nil
	}

	events, err := s.llmClient.AskWithTools(ctx, runCtx.tools, runCtx.messages, onMessage, runCtx.maxStep)
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

	if err := streamStepEvents(w, flusher, runCtx.session.ID, events, runCtx.totalTokens, runCtx.trimmed, runCtx.maxStep); err != nil {
		return
	}
}

func (s *Server) buildChatRunContext(ctx context.Context, req chatRequest, projectID string) chatRunContext {
	var sess *session.Session
	if req.SessionID != "" {
		sess = s.sessionStore.GetOrCreate(req.SessionID, projectID)
	} else {
		sess = s.sessionStore.Create(projectID)
	}

	var projectCtx *prompt.ProjectContext
	if projectID != "" && s.projectStore != nil {
		if p := s.projectStore.Get(projectID); p != nil {
			projectCtx = &prompt.ProjectContext{
				ID:          p.ID,
				Name:        p.Name,
				Description: p.Description,
				GitHubRepo:  p.GitHubRepo,
				NodeletIDs:  p.NodeletIDs,
			}
		}
	}

	var messages []*schema.Message
	var trimmed int
	var totalTokens int
	userMsg := schema.UserMessage(req.Question)
	tools, inventory := s.chatToolsAndInventory(ctx, projectID)

	if s.contextBuilder != nil {
		buildResult := s.contextBuilder.Build(ctx, ctxbuilder.BuildOptions{
			Session:  sess,
			Question: req.Question,
			Project:  projectCtx,
		})
		messages = buildResult.Messages
		trimmed = buildResult.Trimmed
		totalTokens = buildResult.Tokens
	} else {
		systemPrompt := prompt.BasePrompt
		if projectCtx != nil {
			systemPrompt = systemPrompt + "\n\n" + prompt.FormatProjectContext(projectCtx)
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
		trimResult := budget.TrimToBudget(msgs, budget.DefaultBudget)
		messages = trimResult.Messages
		trimmed = trimResult.Trimmed
		totalTokens = trimResult.TotalTokens
	}

	if inventory != "" {
		messages = appendSystemPromptSection(messages, inventory)
		trimResult := budget.TrimToBudget(messages, budget.DefaultBudget)
		messages = trimResult.Messages
		trimmed += trimResult.Trimmed
		totalTokens = trimResult.TotalTokens
	}

	s.sessionStore.AppendMessage(sess.ID, userMsg)
	maxStep := 15

	return chatRunContext{
		session:     sess,
		messages:    messages,
		trimmed:     trimmed,
		totalTokens: totalTokens,
		maxStep:     maxStep,
		runID:       sess.ID + "_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		tools:       tools,
	}
}

func (s *Server) persistAgentMessage(sess *session.Session, runID, projectID string, seq *int, msg *schema.Message) {
	s.sessionStore.AppendMessage(sess.ID, msg)
	if s.eventStore == nil {
		return
	}

	evt := agentevents.StepEvent{
		Type:       string(msg.Role),
		Content:    msg.Content,
		ToolName:   msg.ToolName,
		ToolCallID: msg.ToolCallID,
	}
	if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			s.eventStore.AppendEvent(runID, sess.ID, projectID, *seq, agentevents.StepEvent{
				Type:       "tool_call",
				Content:    tc.Function.Name,
				ToolName:   tc.Function.Name,
				ToolArgs:   tc.Function.Arguments,
				ToolCallID: tc.ID,
			})
			(*seq)++
		}
	}
	s.eventStore.AppendEvent(runID, sess.ID, projectID, *seq, evt)
	(*seq)++
}

func streamStepEvents(w io.Writer, flusher http.Flusher, sessionID string, events <-chan agentevents.StepEvent, totalTokens, trimmed, maxStep int) error {
	if _, err := fmt.Fprintf(w, ":ok\n\n"); err != nil {
		return err
	}
	flusher.Flush()

	sessionEvt := agentevents.StepEvent{
		Type:      "session",
		Content:   sessionID,
		AgentType: "default",
		MaxStep:   maxStep,
	}
	if err := writeSSEData(w, flusher, sessionEvt); err != nil {
		return err
	}

	for evt := range events {
		if err := writeSSEData(w, flusher, evt); err != nil {
			return err
		}
	}

	statsEvt := agentevents.StepEvent{
		Type:    "stats",
		Tokens:  totalTokens,
		Trimmed: trimmed,
	}
	if err := writeSSEData(w, flusher, statsEvt); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeSSEData(w io.Writer, flusher http.Flusher, evt agentevents.StepEvent) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
