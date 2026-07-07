package api

import (
	"net/http/httptest"
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
	if events[0].Type != "tool_call" || events[0].ToolArgs != `{"raw":true}` || events[0].ToolCallID != "call-1" {
		t.Fatalf("tool call event = %#v", events[0])
	}
	if events[2].Type != string(schema.Tool) || events[2].Content != "semantic result" || events[2].ToolArgs != "" {
		t.Fatalf("tool event = %#v", events[2])
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
	if events[0].Type != "tool_call" || events[0].ToolArgs != `{"id":1}` || events[0].ToolCallID != "call-1" {
		t.Fatalf("tool call event = %#v", events[0])
	}
	if events[1].Type != string(schema.Assistant) || events[1].Content != "checking" {
		t.Fatalf("assistant event = %#v", events[1])
	}
	if events[2].Type != string(schema.Tool) || events[2].Content != "ok" || events[2].ToolName != "lookup" {
		t.Fatalf("tool event = %#v", events[2])
	}
	if seq != 3 {
		t.Fatalf("seq = %d, want 3", seq)
	}
}
