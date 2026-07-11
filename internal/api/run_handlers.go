package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/ai/provider"
	coreagent "oops/internal/llm/core/agent"
	"oops/internal/llm/core/toolruntime"
	tooladapter "oops/internal/llm/core/toolruntime/einoadapter"
	"oops/internal/llm/prompt"
	"oops/internal/llm/runtime/harness"
	workspacetools "oops/internal/llm/runtime/tools"
)

const (
	runTimeout       = 120 * time.Second
	runProviderLabel = "openai-compatible"
)

var runIDCounter atomic.Uint64

type runCreateRequest struct {
	Text      string `json:"text"`
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id"`
}

type runCreateResponse struct {
	RunID     string `json:"runId"`
	SessionID string `json:"sessionId"`
}

func (s *Server) handleRunCreate(w http.ResponseWriter, r *http.Request) {
	if s.llmClient == nil {
		writeJSONError(w, "LLM not configured. Set llm.enabled=true and llm.api_key in config.", http.StatusServiceUnavailable)
		return
	}
	var req runCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeJSONError(w, "text is required", http.StatusBadRequest)
		return
	}
	expanded, err := s.expandSkillCommand(req.Text)
	if err != nil {
		var commandErr skillCommandError
		if errors.As(err, &commandErr) {
			writeJSONError(w, commandErr.message, commandErr.status)
			return
		}
		sanitizedError(w, "run create", err, http.StatusInternalServerError)
		return
	}
	req.Text = expanded

	reservation, err := s.runManager.reserve()
	if err != nil {
		if errors.Is(err, ErrRunCapacity) || errors.Is(err, ErrRunManagerQuiescing) {
			w.Header().Set("Retry-After", s.runManager.retryAfterHeader())
			writeJSONError(w, "run capacity unavailable", http.StatusServiceUnavailable)
			return
		}
		sanitizedError(w, "run reserve", err, http.StatusInternalServerError)
		return
	}
	activated := false
	defer func() {
		if !activated {
			reservation.release()
		}
	}()

	setupCtx, cancelSetup := context.WithCancel(s.runManager.context())
	stopRequestCancel := context.AfterFunc(r.Context(), cancelSetup)
	defer func() {
		stopRequestCancel()
		cancelSetup()
	}()
	agentSession, err := s.newRunAgentSession(setupCtx, req)
	if err != nil {
		sanitizedError(w, "run create", err, http.StatusInternalServerError)
		return
	}
	if err := agentSession.ValidateProviderContext(); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := setupCtx.Err(); err != nil {
		w.Header().Set("Retry-After", s.runManager.retryAfterHeader())
		writeJSONError(w, "run capacity unavailable", http.StatusServiceUnavailable)
		return
	}
	snapshot := agentSession.Snapshot()
	runID := newRunID()
	runCtx, cancelRun := context.WithTimeout(s.runManager.context(), runTimeout)
	run, err := reservation.activate(runID, snapshot.SessionID, req.ProjectID, cancelRun)
	if err != nil {
		cancelRun()
		if errors.Is(err, ErrRunManagerQuiescing) {
			w.Header().Set("Retry-After", s.runManager.retryAfterHeader())
			writeJSONError(w, "run capacity unavailable", http.StatusServiceUnavailable)
			return
		}
		sanitizedError(w, "run activate", err, http.StatusInternalServerError)
		return
	}
	activated = true
	unsubscribe := agentSession.Listen(func(_ context.Context, event protocol.AgentEvent, _ coreagent.AgentState) error {
		run.publish(runStreamItem{name: string(event.Type), payload: event})
		return nil
	})

	go func() {
		defer run.finishExecution()
		defer unsubscribe()
		messages := protocol.MessageList{protocol.UserMessage{
			Content:   protocol.ContentList{protocol.NewTextContent(req.Text)},
			Timestamp: time.Now().UnixMilli(),
		}}
		if _, err := agentSession.Prompt(runCtx, messages); err != nil {
			run.publishTerminal(runStreamItem{
				name: "run_error",
				payload: runErrorEvent{
					Type:    "run_error",
					Error:   err.Error(),
					Session: agentSession.Snapshot(),
				},
			})
			return
		}
		run.publishTerminal(runStreamItem{
			name: "run_done",
			payload: runDoneEvent{
				Type:    "run_done",
				Session: agentSession.Snapshot(),
			},
		})
	}()

	writeJSON(w, runCreateResponse{RunID: runID, SessionID: snapshot.SessionID})
}

func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	streamCtx, finishStream, ok := s.beginStream(r.Context())
	if !ok {
		writeJSONError(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	defer finishStream()

	subscription, err := s.runManager.subscribe(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, ErrRunSubscriberLimit) {
			writeJSONError(w, "run subscriber limit reached", http.StatusTooManyRequests)
			return
		}
		sanitizedError(w, "run subscribe", err, http.StatusInternalServerError)
		return
	}
	if subscription == nil {
		writeJSONError(w, "run not found", http.StatusNotFound)
		return
	}
	defer subscription.unsubscribe()

	flusher, err := requireFlusher(w)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	setSSEHeaders(w)
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w, ":ok\n\n"); err != nil {
		return
	}
	flusher.Flush()

	_ = streamRunSubscription(w, flusher, subscription, streamCtx)
}

func (s *Server) handleRunAbort(w http.ResponseWriter, r *http.Request) {
	aborted, ok := s.runManager.abort(r.PathValue("id"))
	if !ok {
		writeJSONError(w, "run not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"aborted": aborted})
}

func (s *Server) newRunAgentSession(ctx context.Context, req runCreateRequest) (*harness.AgentSession, error) {
	rawTools, inventory := s.chatToolsAndInventory(ctx, req.ProjectID)
	runtimeTools, modelTools, err := s.runToolSets(ctx, rawTools)
	if err != nil {
		return nil, err
	}
	streamFn, err := provider.NewEinoStreamFn(ctx, s.llmClient.Model(), modelTools)
	if err != nil {
		return nil, err
	}
	systemPrompt := s.runSystemPrompt(req.ProjectID, inventory)
	loopConfig, err := s.runAgentLoopConfig(ctx, streamFn)
	if err != nil {
		return nil, err
	}
	runtime := harness.NewRuntime(harness.RuntimeOptions{
		Repo: s.agentRepo,
		Loader: harness.StaticResourceLoader{Snapshot: harness.ResourceSnapshot{
			SystemPrompt: systemPrompt,
			Tools:        runtimeTools,
		}},
		Config:   loopConfig,
		Model:    s.llmConfig.Model,
		Provider: runProviderLabel,
	})
	if req.SessionID != "" {
		return runtime.Resume(ctx, req.SessionID)
	}
	return runtime.NewSession(ctx, harness.NewSessionOptions{
		Model:     s.llmConfig.Model,
		Provider:  runProviderLabel,
		ProjectID: req.ProjectID,
	})
}

func (s *Server) runAgentLoopConfig(ctx context.Context, streamFn coreagent.StreamFn) (coreagent.AgentLoopConfig, error) {
	if s.runtimeStore == nil {
		return coreagent.AgentLoopConfig{}, fmt.Errorf("runtime store not available")
	}
	settings, err := s.runtimeStore.GetAgentSettings(ctx)
	if err != nil {
		return coreagent.AgentLoopConfig{}, fmt.Errorf("read agent settings: %w", err)
	}
	return coreagent.AgentLoopConfig{
		MaxTurns: settings.MaxTurns,
		Stream:   streamFn,
	}, nil
}

func (s *Server) runToolSets(ctx context.Context, platformTools []einotool.InvokableTool) ([]toolruntime.Tool, []einotool.InvokableTool, error) {
	workspaceRuntimeTools, err := workspacetools.NewWorkspaceTools(workspacetools.Options{})
	if err != nil {
		return nil, nil, err
	}
	workspaceModelTools, err := tooladapter.ToInvokableTools(workspaceRuntimeTools)
	if err != nil {
		return nil, nil, err
	}
	workspaceNames := runtimeToolNameSet(workspaceRuntimeTools)
	allModelTools := make([]einotool.InvokableTool, 0, len(platformTools)+len(workspaceModelTools))
	allModelTools = append(allModelTools, workspaceModelTools...)
	allModelTools = append(allModelTools, platformTools...)
	allModelTools = uniqueInvokableTools(ctx, allModelTools)
	enabledModelTools := s.llmClient.EnabledTools(allModelTools)
	enabledNames := invokableToolNameSet(ctx, enabledModelTools)

	enabledPlatformTools := filterInvokableTools(ctx, platformTools, enabledNames, workspaceNames)
	runtimeTools, _, err := tooladapter.FromInvokableTools(ctx, enabledPlatformTools)
	if err != nil {
		return nil, nil, err
	}
	for _, item := range workspaceRuntimeTools {
		if item == nil {
			continue
		}
		if enabledNames[item.Definition().Name] {
			runtimeTools = append(runtimeTools, item)
		}
	}
	return runtimeTools, enabledModelTools, nil
}

func runtimeToolNameSet(tools []toolruntime.Tool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		if name := item.Definition().Name; name != "" {
			names[name] = true
		}
	}
	return names
}

func invokableToolNameSet(ctx context.Context, tools []einotool.InvokableTool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		info, err := item.Info(ctx)
		if err != nil || info == nil || info.Name == "" {
			continue
		}
		names[info.Name] = true
	}
	return names
}

func uniqueInvokableTools(ctx context.Context, tools []einotool.InvokableTool) []einotool.InvokableTool {
	if len(tools) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]einotool.InvokableTool, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		info, err := item.Info(ctx)
		if err != nil || info == nil || info.Name == "" {
			continue
		}
		if seen[info.Name] {
			continue
		}
		seen[info.Name] = true
		out = append(out, item)
	}
	return out
}

func filterInvokableTools(ctx context.Context, tools []einotool.InvokableTool, enabled, reserved map[string]bool) []einotool.InvokableTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]einotool.InvokableTool, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		info, err := item.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		if reserved[info.Name] {
			continue
		}
		if enabled[info.Name] {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) runSystemPrompt(projectID, inventory string) string {
	systemPrompt := prompt.BasePrompt
	if projectID != "" && s.projectStore != nil {
		if p := s.projectStore.Get(projectID); p != nil {
			systemPrompt += "\n\n" + prompt.FormatProjectContext(&prompt.ProjectContext{
				ID:          p.ID,
				Name:        p.Name,
				Description: p.Description,
				GitHubRepo:  p.GitHubRepo,
				NodeletIDs:  p.NodeletIDs,
			})
		}
	}
	if s.skillStore != nil {
		if available := s.skillStore.RenderAvailable(); available != "" {
			systemPrompt += "\n\n" + available
		}
	}
	if inventory != "" {
		systemPrompt += "\n\n" + inventory
	}
	return systemPrompt
}

func newRunID() string {
	return fmt.Sprintf("run_%d_%d", time.Now().UnixNano(), runIDCounter.Add(1))
}
