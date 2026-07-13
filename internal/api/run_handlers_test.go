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

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"oops/internal/config"
	"oops/internal/llm/agent"
	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/runtime/harness"
	runtimesession "oops/internal/llm/runtime/session"
	runtimestore "oops/internal/store/runtime"
)

type namedRunTool struct {
	name string
}

func (t namedRunTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

func (t namedRunTool) InvokableRun(context.Context, string, ...einotool.Option) (string, error) {
	return "", nil
}

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
			Session: harness.SessionSnapshot{SessionID: "sess-1"},
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

func TestRunToolHelpersPreferReservedNames(t *testing.T) {
	ctx := context.Background()
	unique := uniqueInvokableTools(ctx, []einotool.InvokableTool{
		namedRunTool{name: "read"},
		namedRunTool{name: "read"},
		namedRunTool{name: "repo_read_file"},
	})
	if len(unique) != 2 {
		t.Fatalf("unique len = %d", len(unique))
	}
	enabled := map[string]bool{"read": true, "repo_read_file": true}
	reserved := map[string]bool{"read": true}
	filtered := filterInvokableTools(ctx, unique, enabled, reserved)
	if len(filtered) != 1 {
		t.Fatalf("filtered len = %d", len(filtered))
	}
	info, err := filtered[0].Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "repo_read_file" {
		t.Fatalf("filtered tool = %q", info.Name)
	}
}

func TestHandleRunCreateRejectsInvalidSkillCommandBeforeRun(t *testing.T) {
	manager := newRunManagerForTest(t)
	server := &Server{
		llmClient:  &agent.Client{},
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

	server := &Server{llmClient: &agent.Client{}, runManager: manager}
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
	lease, err := manager.acquireSession("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	server := &Server{
		llmClient:  &agent.Client{},
		runManager: manager,
		agentRepo:  runtimesession.NewRepository(nil),
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
				llmClient:  &agent.Client{},
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
	repo := runtimesession.NewRepository(nil)
	createRuntimeSession(t, repo, "sess-project", "project-a", "hello")
	server := &Server{
		llmClient:  &agent.Client{},
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
	lease, err := manager.acquireSession("sess-project")
	if err != nil {
		t.Fatalf("session lease was not released: %v", err)
	}
	lease.release()
}

func TestHandleRunCreateRejectsMissingSessionWithoutCreatingFile(t *testing.T) {
	dir := t.TempDir()
	storage, err := runtimesession.NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	manager := newRunManagerForTest(t)
	repo := runtimesession.NewRepository(storage)
	server := &Server{
		llmClient:  &agent.Client{},
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
	lease, err := manager.acquireSession("missing-session")
	if err != nil {
		t.Fatalf("session lease was not released: %v", err)
	}
	lease.release()
}

func TestResolveRunProjectIDUsesPersistedProject(t *testing.T) {
	repo := runtimesession.NewRepository(nil)
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

func TestRunAgentLoopConfigUsesRuntimeSettings(t *testing.T) {
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
	config, err := server.runAgentLoopConfig(ctx, nil)
	if err != nil {
		t.Fatalf("runAgentLoopConfig: %v", err)
	}
	if config.MaxTurns != 9 {
		t.Fatalf("MaxTurns = %d, want 9", config.MaxTurns)
	}
}

func TestRunAgentLoopConfigRequiresRuntimeStore(t *testing.T) {
	_, err := (&Server{}).runAgentLoopConfig(context.Background(), nil)
	if err == nil {
		t.Fatal("expected runtime store error")
	}
}
