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

	"github.com/google/uuid"

	agentruntime "oops/internal/agent/runtime"
)

const (
	runTimeout = 120 * time.Second
)

var runIDCounter atomic.Uint64

var (
	errSessionNotFound        = errors.New("session not found")
	errSessionProjectConflict = errors.New("session belongs to another project")
)

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
		writeJSONError(w, "LLM not configured. Enable llm and configure a provider credential.", http.StatusServiceUnavailable)
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
	if req.SessionID != "" && !validateRuntimeSessionID(w, req.SessionID) {
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
	leaseSessionID := req.SessionID
	newSessionID := ""
	if leaseSessionID == "" {
		newSessionID = "session_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		leaseSessionID = newSessionID
	}
	sessionLease, err := s.ensureAgentRuntime().AcquireSession(leaseSessionID)
	if err != nil {
		if errors.Is(err, ErrSessionBusy) {
			writeJSONError(w, ErrSessionBusy.Error(), http.StatusConflict)
			return
		}
		sanitizedError(w, "claim run session", err, http.StatusInternalServerError)
		return
	}
	defer func() {
		if !activated {
			sessionLease.Release()
		}
	}()
	if req.SessionID != "" {
		req.ProjectID, err = s.resolveRunProjectID(req.SessionID, req.ProjectID)
		if err != nil {
			if errors.Is(err, errSessionNotFound) {
				writeJSONError(w, err.Error(), http.StatusNotFound)
				return
			}
			if errors.Is(err, errSessionProjectConflict) {
				writeJSONError(w, err.Error(), http.StatusConflict)
				return
			}
			sanitizedError(w, "resolve run project", err, http.StatusInternalServerError)
			return
		}
	}

	setupCtx, cancelSetup := context.WithCancel(s.runManager.context())
	stopRequestCancel := context.AfterFunc(r.Context(), cancelSetup)
	defer func() {
		stopRequestCancel()
		cancelSetup()
	}()
	agentSession, err := s.newRunAgentSession(setupCtx, req, newSessionID)
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
		if errors.Is(err, ErrSessionBusy) {
			writeJSONError(w, ErrSessionBusy.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, ErrRunManagerQuiescing) {
			w.Header().Set("Retry-After", s.runManager.retryAfterHeader())
			writeJSONError(w, "run capacity unavailable", http.StatusServiceUnavailable)
			return
		}
		sanitizedError(w, "run activate", err, http.StatusInternalServerError)
		return
	}
	activated = true
	unsubscribe := agentSession.ListenRunEvents(func(_ context.Context, event agentruntime.RunEvent) error {
		run.publish(runStreamItem{name: event.Name, payload: event.Payload})
		return nil
	})

	go func() {
		defer sessionLease.Release()
		defer run.finishExecution()
		defer unsubscribe()
		if err := agentSession.PromptText(runCtx, req.Text, time.Now().UnixMilli()); err != nil {
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

func (s *Server) resolveRunProjectID(sessionID, requestedProjectID string) (string, error) {
	if sessionID == "" {
		return requestedProjectID, nil
	}
	info, ok, err := s.runtimeSessionInfoByID(sessionID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errSessionNotFound
	}
	if requestedProjectID != "" && requestedProjectID != info.ProjectID {
		return "", errSessionProjectConflict
	}
	return info.ProjectID, nil
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

func (s *Server) newRunAgentSession(ctx context.Context, req runCreateRequest, newSessionID string) (*agentruntime.AgentSession, error) {
	rawTools, inventory := s.chatToolsAndInventory(ctx, req.ProjectID)
	maxTurns, err := s.runAgentMaxTurns(ctx)
	if err != nil {
		return nil, err
	}
	systemPrompt := s.runSystemPrompt(req.ProjectID, inventory)
	return s.ensureAgentRuntime().PreparePromptSession(ctx, agentruntime.PreparePromptOptions{
		SessionID:     req.SessionID,
		NewSessionID:  newSessionID,
		Model:         s.llmConfig.Model,
		Provider:      s.llmClient.Provider(),
		ProjectID:     req.ProjectID,
		SystemPrompt:  systemPrompt,
		PlatformTools: rawTools,
		MaxTurns:      maxTurns,
		Client:        s.llmClient,
	})
}

func (s *Server) runAgentMaxTurns(ctx context.Context) (int, error) {
	if s.runtimeStore == nil {
		return 0, fmt.Errorf("runtime store not available")
	}
	settings, err := s.runtimeStore.GetAgentSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("read agent settings: %w", err)
	}
	return settings.MaxTurns, nil
}

func (s *Server) runSystemPrompt(projectID, inventory string) string {
	systemPrompt := agentruntime.BasePrompt
	if projectID != "" && s.projectStore != nil {
		if p := s.projectStore.Get(projectID); p != nil {
			systemPrompt += "\n\n" + agentruntime.FormatProjectContext(&agentruntime.ProjectContext{
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
