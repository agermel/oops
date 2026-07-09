package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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
	runMaxTurns      = 15
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

type runDoneEvent struct {
	Type    string                  `json:"type"`
	Session harness.SessionSnapshot `json:"session"`
}

type runErrorEvent struct {
	Type    string                  `json:"type"`
	Error   string                  `json:"error"`
	Session harness.SessionSnapshot `json:"session"`
}

type runStreamItem struct {
	name    string
	payload any
}

type runManager struct {
	mu   sync.RWMutex
	runs map[string]*runState
}

type runState struct {
	mu          sync.Mutex
	id          string
	sessionID   string
	projectID   string
	cancel      context.CancelFunc
	history     []runStreamItem
	subscribers map[chan runStreamItem]struct{}
	done        bool
}

func newRunManager() *runManager {
	return &runManager{runs: map[string]*runState{}}
}

func (m *runManager) create(runID, sessionID, projectID string, cancel context.CancelFunc) *runState {
	run := &runState{
		id:          runID,
		sessionID:   sessionID,
		projectID:   projectID,
		cancel:      cancel,
		subscribers: map[chan runStreamItem]struct{}{},
	}
	m.mu.Lock()
	m.runs[runID] = run
	m.mu.Unlock()
	return run
}

func (m *runManager) get(runID string) (*runState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[runID]
	return run, ok
}

func (m *runManager) subscribe(runID string) (<-chan runStreamItem, func(), bool) {
	run, ok := m.get(runID)
	if !ok {
		return nil, nil, false
	}
	return run.subscribe()
}

func (m *runManager) abort(runID string) (bool, bool) {
	run, ok := m.get(runID)
	if !ok {
		return false, false
	}
	return run.abort(), true
}

func (r *runState) subscribe() (<-chan runStreamItem, func(), bool) {
	r.mu.Lock()
	buffer := len(r.history) + 16
	if buffer < 64 {
		buffer = 64
	}
	ch := make(chan runStreamItem, buffer)
	for _, item := range r.history {
		ch <- item
	}
	if r.done {
		close(ch)
		r.mu.Unlock()
		return ch, func() {}, true
	}
	r.subscribers[ch] = struct{}{}
	r.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.subscribers, ch)
			r.mu.Unlock()
		})
	}
	return ch, unsubscribe, true
}

func (r *runState) publish(item runStreamItem) {
	r.publishLocked(item, false)
}

func (r *runState) publishTerminal(item runStreamItem) {
	r.publishLocked(item, true)
}

func (r *runState) publishLocked(item runStreamItem, terminal bool) {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return
	}
	r.history = append(r.history, item)
	subscribers := make([]chan runStreamItem, 0, len(r.subscribers))
	for subscriber := range r.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	if terminal {
		r.done = true
		r.cancel = nil
		r.subscribers = map[chan runStreamItem]struct{}{}
	}
	r.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber <- item
		if terminal {
			close(subscriber)
		}
	}
}

func (r *runState) abort() bool {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
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

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	agentSession, err := s.newRunAgentSession(ctx, req)
	if err != nil {
		cancel()
		sanitizedError(w, "run create", err, http.StatusInternalServerError)
		return
	}
	if err := agentSession.ValidateProviderContext(); err != nil {
		cancel()
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	snapshot := agentSession.Snapshot()
	runID := newRunID()
	run := s.runManager.create(runID, snapshot.SessionID, req.ProjectID, cancel)
	unsubscribe := agentSession.Listen(func(_ context.Context, event protocol.AgentEvent, _ coreagent.AgentState) error {
		run.publish(runStreamItem{name: string(event.Type), payload: event})
		return nil
	})

	go func() {
		defer cancel()
		defer unsubscribe()
		messages := protocol.MessageList{protocol.UserMessage{
			Content:   protocol.ContentList{protocol.NewTextContent(req.Text)},
			Timestamp: time.Now().UnixMilli(),
		}}
		if _, err := agentSession.Prompt(ctx, messages); err != nil {
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
	events, unsubscribe, ok := s.runManager.subscribe(r.PathValue("id"))
	if !ok {
		writeJSONError(w, "run not found", http.StatusNotFound)
		return
	}
	defer unsubscribe()

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

	for {
		select {
		case item, ok := <-events:
			if !ok {
				return
			}
			if err := writeNamedSSE(w, flusher, item.name, item.payload); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
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
	runtime := harness.NewRuntime(harness.RuntimeOptions{
		Repo: s.agentRepo,
		Loader: harness.StaticResourceLoader{Snapshot: harness.ResourceSnapshot{
			SystemPrompt: systemPrompt,
			Tools:        runtimeTools,
		}},
		Config: coreagent.AgentLoopConfig{
			MaxTurns: runMaxTurns,
			Stream:   streamFn,
		},
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

func streamRunItems(w io.Writer, flusher http.Flusher, events <-chan runStreamItem) error {
	if _, err := fmt.Fprintf(w, ":ok\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	for item := range events {
		if err := writeNamedSSE(w, flusher, item.name, item.payload); err != nil {
			return err
		}
	}
	return nil
}
