package api

import (
	"net/http/httptest"
	"reflect"
	"testing"

	agentevents "oops/internal/llm/events"
	"oops/internal/llm/session"

	"github.com/cloudwego/eino/schema"
)

func TestStreamStepEventsKeepsSSEOrder(t *testing.T) {
	events := make(chan agentevents.StepEvent, 3)
	events <- agentevents.StepEvent{Type: "thinking", Content: "checking"}
	events <- agentevents.StepEvent{
		Type:       "tool_call",
		Content:    "lookup",
		ToolName:   "lookup",
		ToolArgs:   `{"id":1}`,
		ToolCallID: "call-1",
	}
	events <- agentevents.StepEvent{
		Type:       "tool_result",
		Content:    "semantic result",
		ToolName:   "lookup",
		ToolCallID: "call-1",
	}
	close(events)

	recorder := httptest.NewRecorder()
	if err := streamStepEvents(recorder, recorder, "sess-1", events, 42, 1, 15); err != nil {
		t.Fatalf("streamStepEvents() error = %v", err)
	}

	want := ":ok\n\n" +
		"data: {\"type\":\"session\",\"content\":\"sess-1\",\"agentType\":\"default\",\"maxStep\":15}\n\n" +
		"data: {\"type\":\"thinking\",\"content\":\"checking\"}\n\n" +
		"data: {\"type\":\"tool_call\",\"content\":\"lookup\",\"toolName\":\"lookup\",\"toolArgs\":\"{\\\"id\\\":1}\",\"toolCallId\":\"call-1\"}\n\n" +
		"data: {\"type\":\"tool_result\",\"content\":\"semantic result\",\"toolName\":\"lookup\",\"toolCallId\":\"call-1\"}\n\n" +
		"data: {\"type\":\"stats\",\"content\":\"\",\"tokens\":42,\"trimmed\":1}\n\n" +
		"data: [DONE]\n\n"
	if recorder.Body.String() != want {
		t.Fatalf("SSE body = %q, want %q", recorder.Body.String(), want)
	}
}

func TestPersistAgentMessageKeepsRawArgsWithSemanticResult(t *testing.T) {
	eventStore, err := agentevents.OpenEventStore(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatalf("OpenEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = eventStore.Close() })

	sessionStore := session.NewSessionStore()
	sess := sessionStore.Create("proj-1")
	server := &Server{
		sessionStore: sessionStore,
		eventStore:   eventStore,
	}

	seq := 0
	runID := "run-semantic"
	server.persistAgentMessage(sess, runID, "proj-1", &seq, schema.AssistantMessage("checking", []schema.ToolCall{{
		ID: "call-1",
		Function: schema.FunctionCall{
			Name:      "lookup",
			Arguments: `{"raw":true}`,
		},
	}}))
	server.persistAgentMessage(sess, runID, "proj-1", &seq, schema.ToolMessage("semantic result", "call-1", schema.WithToolName("lookup")))

	events, err := eventStore.GetEvents(runID)
	if err != nil {
		t.Fatalf("GetEvents() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	wantEvents := []agentevents.StepEvent{
		{Type: "tool_call", Content: "lookup", ToolName: "lookup", ToolArgs: `{"raw":true}`, ToolCallID: "call-1"},
		{Type: string(schema.Assistant), Content: "checking"},
		{Type: string(schema.Tool), Content: "semantic result", ToolName: "lookup", ToolCallID: "call-1"},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
	assertStoredMessageOrder(t, eventStore, sess.ID, []storedMessage{
		{Seq: 0, Role: "tool_call", Content: "lookup", ToolName: "lookup", ToolCallID: "call-1", RunID: runID},
		{Seq: 1, Role: string(schema.Assistant), Content: "checking", RunID: runID},
		{Seq: 2, Role: string(schema.Tool), Content: "semantic result", ToolName: "lookup", ToolCallID: "call-1", RunID: runID},
	})
	if seq != 3 {
		t.Fatalf("seq = %d, want 3", seq)
	}
}

func TestPersistAgentMessageKeepsToolCallEventOrderAndRawArgs(t *testing.T) {
	eventStore, err := agentevents.OpenEventStore(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatalf("OpenEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = eventStore.Close() })

	sessionStore := session.NewSessionStore()
	sess := sessionStore.Create("proj-1")
	server := &Server{
		sessionStore: sessionStore,
		eventStore:   eventStore,
	}

	seq := 0
	runID := "run-1"
	server.persistAgentMessage(sess, runID, "proj-1", &seq, schema.AssistantMessage("checking", []schema.ToolCall{{
		ID: "call-1",
		Function: schema.FunctionCall{
			Name:      "lookup",
			Arguments: `{"id":1}`,
		},
	}}))
	server.persistAgentMessage(sess, runID, "proj-1", &seq, schema.ToolMessage("ok", "call-1", schema.WithToolName("lookup")))

	events, err := eventStore.GetEvents(runID)
	if err != nil {
		t.Fatalf("GetEvents() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	wantEvents := []agentevents.StepEvent{
		{Type: "tool_call", Content: "lookup", ToolName: "lookup", ToolArgs: `{"id":1}`, ToolCallID: "call-1"},
		{Type: string(schema.Assistant), Content: "checking"},
		{Type: string(schema.Tool), Content: "ok", ToolName: "lookup", ToolCallID: "call-1"},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
	assertStoredMessageOrder(t, eventStore, sess.ID, []storedMessage{
		{Seq: 0, Role: "tool_call", Content: "lookup", ToolName: "lookup", ToolCallID: "call-1", RunID: runID},
		{Seq: 1, Role: string(schema.Assistant), Content: "checking", RunID: runID},
		{Seq: 2, Role: string(schema.Tool), Content: "ok", ToolName: "lookup", ToolCallID: "call-1", RunID: runID},
	})
	if seq != 3 {
		t.Fatalf("seq = %d, want 3", seq)
	}
}

type storedMessage struct {
	Seq        int
	Role       string
	Content    string
	ToolName   string
	ToolCallID string
	RunID      string
}

func assertStoredMessageOrder(t *testing.T, store *agentevents.EventStore, sessionID string, want []storedMessage) {
	t.Helper()

	gotMessages, err := store.GetSessionMessages(sessionID)
	if err != nil {
		t.Fatalf("GetSessionMessages() error = %v", err)
	}
	if len(gotMessages) != len(want) {
		t.Fatalf("len(GetSessionMessages) = %d, want %d: %#v", len(gotMessages), len(want), gotMessages)
	}
	for i, got := range gotMessages {
		wantMsg := want[i]
		if got.Seq != wantMsg.Seq ||
			got.Role != wantMsg.Role ||
			got.Content != wantMsg.Content ||
			got.ToolName != wantMsg.ToolName ||
			got.ToolCallID != wantMsg.ToolCallID ||
			got.RunID != wantMsg.RunID {
			t.Fatalf("stored message[%d] = %#v, want %#v", i, got, wantMsg)
		}
	}
}
