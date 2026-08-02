package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	agentruntime "oops/internal/agent/runtime"
	"oops/internal/agent/runtime/model"
	"oops/internal/agent/runtime/session"
	"oops/internal/config"
	runtimestore "oops/internal/store/runtime"
)

func TestRunManagerReplaysHistoryAndRunDone(t *testing.T) {
	manager := newRunManagerForTest(t)
	run := activateRunForTest(t, manager, "run-1", "sess-1")
	run.publish(runStreamItem{
		name:    string(protocol.AgentEventAgentStart),
		payload: protocol.AgentEvent{Type: protocol.AgentEventAgentStart},
	})
	run.publishTerminal(runStreamItem{
		name: "run_done",
		payload: runDoneEvent{
			Type:    "run_done",
			Session: agentruntime.SessionSnapshot{SessionID: "sess-1"},
		},
	})

	subscription, err := manager.subscribe("run-1")
	if err != nil || subscription == nil {
		t.Fatalf("subscribe() = %v, %v", subscription, err)
	}
	defer subscription.unsubscribe()

	first, _, ok := subscription.next(t.Context())
	if !ok || !strings.Contains(string(first.data), "event: agent_start\n") {
		t.Fatalf("first frame = %q, ok=%v", first.data, ok)
	}
	second, _, ok := subscription.next(t.Context())
	if !ok || !strings.Contains(string(second.data), "event: run_done\n") {
		t.Fatalf("second frame = %q, ok=%v", second.data, ok)
	}
	if _, _, ok := subscription.next(t.Context()); ok {
		t.Fatal("subscription still has events after terminal replay")
	}
}

func TestHandleRunEventsReplaysBoundedHistoryTailAndTerminal(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxRetainedEvents = 1
	manager := newRunManagerForTest(t, limits)
	run := activateRunForTest(t, manager, "run-overflow-replay", "sess-1")
	run.publish(testRunItem(1, "evicted"))
	run.publish(testRunItem(2, "retained"))
	run.publishTerminal(runStreamItem{
		name: "run_done",
		payload: runDoneEvent{
			Type:    "run_done",
			Session: agentruntime.SessionSnapshot{SessionID: "sess-1"},
		},
	})
	run.finishExecution()

	server := &Server{runManager: manager}
	request := httptest.NewRequest(http.MethodGet, "/api/runs/run-overflow-replay/events", nil)
	request.SetPathValue("id", run.id)
	recorder := httptest.NewRecorder()
	server.handleRunEvents(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		":ok\n\n",
		"event: agent_start\n",
		`"type":"agent_start"`,
		`"index":2`,
		"event: run_done\n",
		`"sessionId":"sess-1"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q: %s", want, body)
		}
	}
	for _, excluded := range []string{`"index":1`, "event: run_error\n", "run event history limit reached"} {
		if strings.Contains(body, excluded) {
			t.Fatalf("SSE body contains %q: %s", excluded, body)
		}
	}
}

func TestHandleRunEventsWritesTypedRuntimeEvent(t *testing.T) {
	manager := newRunManagerForTest(t)
	run := activateRunForTest(t, manager, "run-typed-event", "sess-1")
	var event agentruntime.RunEvent = agentruntime.ToolsUpdateEvent{
		Type:                    "tools_update",
		ToolNames:               []string{"read", "write"},
		PreviousToolNames:       []string{"read"},
		ActiveToolNames:         []string{"write"},
		PreviousActiveToolNames: []string{"read"},
		Source:                  "set_tools",
	}
	run.publish(runStreamItem{name: event.EventName(), payload: event})
	run.publishTerminal(runStreamItem{
		name: "run_done",
		payload: runDoneEvent{
			Type:    "run_done",
			Session: agentruntime.SessionSnapshot{SessionID: "sess-1"},
		},
	})
	run.finishExecution()

	server := &Server{runManager: manager}
	request := httptest.NewRequest(http.MethodGet, "/api/runs/run-typed-event/events", nil)
	request.SetPathValue("id", run.id)
	recorder := httptest.NewRecorder()
	server.handleRunEvents(recorder, request)

	body := recorder.Body.String()
	for _, want := range []string{
		"event: tools_update\n",
		`"type":"tools_update"`,
		`"toolNames":["read","write"]`,
		`"activeToolNames":["write"]`,
		`"source":"set_tools"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q: %s", want, body)
		}
	}
}

func TestRunManagerAbortCancelsActiveRun(t *testing.T) {
	manager := newRunManagerForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	reservation, err := manager.reserve()
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	run, err := reservation.activate("run-1", "sess-1", "", cancel)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	t.Cleanup(func() {
		run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
		run.finishExecution()
	})

	aborted, ok := manager.abort("run-1")
	if !ok || !aborted {
		t.Fatalf("abort() = %v, %v; want true, true", aborted, ok)
	}
	if ctx.Err() == nil {
		t.Fatal("context was not canceled")
	}

	run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
	aborted, ok = manager.abort("run-1")
	if !ok || aborted {
		t.Fatalf("abort() after done = %v, %v; want false, true", aborted, ok)
	}
}

func activateRunForTest(t *testing.T, manager *runManager, runID, sessionID string) *runState {
	run, _ := activateRunWithContextForTest(t, manager, runID, sessionID)
	return run
}

func activateRunWithContextForTest(t *testing.T, manager *runManager, runID, sessionID string) (*runState, context.Context) {
	t.Helper()
	reservation, err := manager.reserve()
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	run, err := reservation.activate(runID, sessionID, "", cancel)
	if err != nil {
		cancel()
		t.Fatalf("activate: %v", err)
	}
	t.Cleanup(func() {
		run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
		run.finishExecution()
	})
	return run, ctx
}

func newRunManagerForTest(t *testing.T, limits ...config.RunLimits) *runManager {
	t.Helper()
	manager := newRunManager(limits...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.close(ctx); err != nil {
			t.Errorf("close run manager: %v", err)
		}
	})
	return manager
}

func TestHandleRunCreateRejectsInvalidSkillCommandBeforeRun(t *testing.T) {
	manager := newRunManagerForTest(t)
	server := &Server{
		llmClient:  &model.Client{},
		runManager: manager,
		skillStore: newTestSkillStore(t),
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/runs",
		strings.NewReader(`{"text":"/skill:missing"}`),
	)
	recorder := httptest.NewRecorder()

	server.handleRunCreate(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "skill not found or disabled") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if len(manager.runs) != 0 {
		t.Fatalf("runs created = %d, want 0", len(manager.runs))
	}
}

func TestHandleRunCreateRejectsAtCapacityWithRetryAfter(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxActiveRuns = 1
	manager := newRunManagerForTest(t, limits)
	reservation, err := manager.reserve()
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	defer reservation.release()

	server := &Server{llmClient: &model.Client{}, runManager: manager}
	request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"text":"hello"}`))
	recorder := httptest.NewRecorder()

	server.handleRunCreate(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}

func TestHandleRunCreateRejectsBusySession(t *testing.T) {
	manager := newRunManagerForTest(t)
	repo := session.NewRepository(nil)
	runtime := agentruntime.NewRuntime(agentruntime.RuntimeOptions{Repo: repo})
	lease, err := runtime.AcquireSession("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	server := &Server{
		llmClient:    &model.Client{},
		runManager:   manager,
		agentRepo:    repo,
		agentRuntime: runtime,
	}
	request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"text":"hello","session_id":"sess-1"}`))
	recorder := httptest.NewRecorder()

	server.handleRunCreate(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "session is busy") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestHandleRunCreateRejectsInvalidSessionID(t *testing.T) {
	for _, sessionID := range []string{"../outside", "SESS-1"} {
		t.Run(sessionID, func(t *testing.T) {
			server := &Server{
				llmClient:  &model.Client{},
				runManager: newRunManagerForTest(t),
			}
			request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(fmt.Sprintf(`{"text":"hello","session_id":%q}`, sessionID)))
			recorder := httptest.NewRecorder()

			server.handleRunCreate(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), "invalid session id") {
				t.Fatalf("body = %s", recorder.Body.String())
			}
		})
	}
}

func TestHandleRunCreateRejectsPersistedProjectConflict(t *testing.T) {
	manager := newRunManagerForTest(t)
	repo := session.NewRepository(nil)
	createRuntimeSession(t, repo, "sess-project", "project-a", "hello")
	server := &Server{
		llmClient:  &model.Client{},
		runManager: manager,
		agentRepo:  repo,
	}
	request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"text":"hello","session_id":"sess-project","project_id":"project-b"}`))
	recorder := httptest.NewRecorder()

	server.handleRunCreate(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "session belongs to another project") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	lease, err := server.ensureAgentRuntime().AcquireSession("sess-project")
	if err != nil {
		t.Fatalf("session lease was not released: %v", err)
	}
	lease.Release()
}

func TestHandleRunCreateRejectsMissingSessionWithoutCreatingFile(t *testing.T) {
	dir := t.TempDir()
	storage, err := session.NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	manager := newRunManagerForTest(t)
	repo := session.NewRepository(storage)
	server := &Server{
		llmClient:  &model.Client{},
		runManager: manager,
		agentRepo:  repo,
	}
	request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"text":"hello","session_id":"missing-session"}`))
	recorder := httptest.NewRecorder()

	server.handleRunCreate(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "session not found") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if _, ok := repo.Get("missing-session"); ok {
		t.Fatal("missing session remained in repository cache")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing-session.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("session file stat error = %v, want not exist", err)
	}
	lease, err := server.ensureAgentRuntime().AcquireSession("missing-session")
	if err != nil {
		t.Fatalf("session lease was not released: %v", err)
	}
	lease.Release()
}

func TestResolveRunProjectIDUsesPersistedProject(t *testing.T) {
	repo := session.NewRepository(nil)
	createRuntimeSession(t, repo, "sess-project", "project-a", "hello")
	server := &Server{agentRepo: repo}

	projectID, err := server.resolveRunProjectID("sess-project", "")
	if err != nil {
		t.Fatal(err)
	}
	if projectID != "project-a" {
		t.Fatalf("project id = %q, want project-a", projectID)
	}
}

func TestHandleRunEventsRejectsSubscriberLimitAndExpiredRun(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxSubscribers = 1
	limits.CompletedTTL = 10 * time.Millisecond
	manager := newRunManagerForTest(t, limits)
	run := activateRunForTest(t, manager, "run-1", "sess-1")
	first, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("first subscribe: %v", err)
	}
	defer first.unsubscribe()
	server := &Server{runManager: manager}

	request := httptest.NewRequest(http.MethodGet, "/api/runs/run-1/events", nil)
	request.SetPathValue("id", run.id)
	recorder := httptest.NewRecorder()
	server.handleRunEvents(recorder, request)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("subscriber limit status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}

	run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
	run.finishExecution()
	deadline := time.Now().Add(time.Second)
	for {
		if _, exists := manager.get(run.id); !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed run did not expire")
		}
		time.Sleep(time.Millisecond)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/runs/run-1/events", nil)
	request.SetPathValue("id", run.id)
	recorder = httptest.NewRecorder()
	server.handleRunEvents(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expired run status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestRunAgentMaxTurnsUsesRuntimeSettings(t *testing.T) {
	ctx := context.Background()
	store, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer store.Close()
	if err := store.UpdateAgentSettings(ctx, runtimestore.AgentSettingsRecord{MaxTurns: 9}); err != nil {
		t.Fatalf("UpdateAgentSettings: %v", err)
	}

	server := &Server{runtimeStore: store}
	maxTurns, err := server.runAgentMaxTurns(ctx)
	if err != nil {
		t.Fatalf("runAgentMaxTurns: %v", err)
	}
	if maxTurns != 9 {
		t.Fatalf("MaxTurns = %d, want 9", maxTurns)
	}
}

func TestRunAgentMaxTurnsRequiresRuntimeStore(t *testing.T) {
	_, err := (&Server{}).runAgentMaxTurns(context.Background())
	if err == nil {
		t.Fatal("expected runtime store error")
	}
}
