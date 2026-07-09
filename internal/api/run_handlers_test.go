package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"oops/internal/llm/agent"
	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/runtime/harness"
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

func TestStreamRunItemsUsesNamedSSEOrder(t *testing.T) {
	events := make(chan runStreamItem, 2)
	events <- runStreamItem{
		name:    string(protocol.AgentEventAgentStart),
		payload: protocol.AgentEvent{Type: protocol.AgentEventAgentStart},
	}
	events <- runStreamItem{
		name: "run_done",
		payload: runDoneEvent{
			Type:    "run_done",
			Session: harness.SessionSnapshot{SessionID: "sess-1"},
		},
	}
	close(events)

	recorder := httptest.NewRecorder()
	if err := streamRunItems(recorder, recorder, events); err != nil {
		t.Fatalf("streamRunItems() error = %v", err)
	}

	want := ":ok\n\n" +
		"event: agent_start\n" +
		"data: {\"type\":\"agent_start\"}\n\n" +
		"event: run_done\n" +
		"data: {\"type\":\"run_done\",\"session\":{\"sessionId\":\"sess-1\",\"messages\":[],\"events\":null,\"tools\":null,\"entries\":null}}\n\n"
	if recorder.Body.String() != want {
		t.Fatalf("SSE body = %q, want %q", recorder.Body.String(), want)
	}
}

func TestRunManagerReplaysHistoryAndRunDone(t *testing.T) {
	manager := newRunManager()
	run := manager.create("run-1", "sess-1", "", func() {})
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

	events, unsubscribe, ok := manager.subscribe("run-1")
	if !ok {
		t.Fatal("subscribe() ok = false")
	}
	defer unsubscribe()

	first, ok := <-events
	if !ok || first.name != string(protocol.AgentEventAgentStart) {
		t.Fatalf("first event = %#v, ok=%v", first, ok)
	}
	second, ok := <-events
	if !ok || second.name != "run_done" {
		t.Fatalf("second event = %#v, ok=%v", second, ok)
	}
	if _, ok := <-events; ok {
		t.Fatal("events channel still open after terminal replay")
	}
}

func TestRunManagerAbortCancelsActiveRun(t *testing.T) {
	manager := newRunManager()
	ctx, cancel := context.WithCancel(context.Background())
	run := manager.create("run-1", "sess-1", "", cancel)

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

func TestWriteNamedSSEEscapesPayload(t *testing.T) {
	recorder := httptest.NewRecorder()
	payload := map[string]string{"type": "run_error", "error": "bad\nvalue"}
	if err := writeNamedSSE(recorder, recorder, "run_error", payload); err != nil {
		t.Fatalf("writeNamedSSE() error = %v", err)
	}
	body := recorder.Body.String()
	if !strings.HasPrefix(body, "event: run_error\ndata: ") {
		t.Fatalf("body prefix = %q", body)
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(body, "event: run_error\ndata: "), "\n\n")
	var got map[string]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if got["error"] != "bad\nvalue" {
		t.Fatalf("payload = %#v", got)
	}
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
	manager := newRunManager()
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
