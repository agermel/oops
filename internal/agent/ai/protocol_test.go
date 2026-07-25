package ai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMessageJSONSnapshot(t *testing.T) {
	reasoning := 1
	messages := MessageList{
		UserMessage{
			Content: ContentList{
				NewTextContent("hello"),
				ImageContent{Type: ContentTypeImage, Data: "aW1hZ2U=", MIMEType: "image/png"},
			},
			Timestamp: 1700000000000,
		},
		AssistantMessage{
			Content: ContentList{
				NewThinkingContent("checking"),
				NewTextContent("I will call a tool"),
				NewToolCallContent("call_1", "lookup", json.RawMessage(`{"query":"pods"}`)),
			},
			Provider:   "test-provider",
			Model:      "test-model",
			Usage:      usageWithReasoning(reasoning),
			StopReason: StopReasonToolUse,
			Timestamp:  1700000001000,
		},
		ToolResultMessage{
			ToolCallID: "call_1",
			ToolName:   "lookup",
			Content:    ContentList{NewTextContent("found")},
			Timestamp:  1700000002000,
		},
	}

	got, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	want, err := os.ReadFile("testdata/messages.golden.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Fatalf("message snapshot mismatch\nwant:\n%s\n\ngot:\n%s", string(want), string(got))
	}
}

func TestMessageListUnmarshalAndArgumentsMap(t *testing.T) {
	raw, err := os.ReadFile("testdata/messages.golden.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var messages MessageList
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}

	assistant, ok := messages[1].(AssistantMessage)
	if !ok {
		t.Fatalf("messages[1] = %T, want AssistantMessage", messages[1])
	}
	toolCall, ok := assistant.Content[2].(ToolCallContent)
	if !ok {
		t.Fatalf("assistant.Content[2] = %T, want ToolCallContent", assistant.Content[2])
	}
	args, err := toolCall.ArgumentsMap()
	if err != nil {
		t.Fatalf("ArgumentsMap() error = %v", err)
	}
	if args["query"] != "pods" {
		t.Fatalf("args[query] = %v, want pods", args["query"])
	}
}

func TestEventJSONSnapshotAndUnmarshal(t *testing.T) {
	index0 := 0
	index1 := 1
	message := AssistantMessage{
		Content:    ContentList{NewTextContent("hello")},
		Usage:      Usage{Input: 1, Output: 2, TotalTokens: 3},
		StopReason: StopReasonToolUse,
		Timestamp:  1700000001000,
	}
	toolResult := ToolResultMessage{
		ToolCallID: "call_1",
		ToolName:   "lookup",
		Content:    ContentList{NewTextContent("found")},
		Timestamp:  1700000002000,
	}
	payload := struct {
		AssistantEvents []AssistantMessageEvent `json:"assistantEvents"`
		AgentEvents     []AgentEvent            `json:"agentEvents"`
	}{
		AssistantEvents: []AssistantMessageEvent{
			{Type: AssistantEventStart, Partial: &AssistantMessage{Content: ContentList{}, Usage: Usage{}, StopReason: StopReasonStop, Timestamp: 1700000000000}},
			{Type: AssistantEventTextDelta, ContentIndex: &index0, Delta: "hello", Partial: &message},
			{Type: AssistantEventToolCallEnd, ContentIndex: &index1, ToolCall: ptrToolCall(NewToolCallContent("call_1", "lookup", json.RawMessage(`{"query":"pods"}`))), Partial: &message},
			{Type: AssistantEventDone, Reason: StopReasonToolUse, Message: &message},
		},
		AgentEvents: []AgentEvent{
			{Type: AgentEventAgentStart},
			{Type: AgentEventMessageUpdate, Message: message, AssistantMessageEvent: &AssistantMessageEvent{Type: AssistantEventTextDelta, ContentIndex: &index0, Delta: "hello", Partial: &message}, Delta: "hello"},
			{Type: AgentEventToolExecutionStart, ToolCallID: "call_1", ToolName: "lookup", Args: json.RawMessage(`{"query":"pods"}`)},
			{Type: AgentEventTurnEnd, Turn: 1, Message: message, ToolResults: []ToolResultMessage{toolResult}},
		},
	}

	got, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	want, err := os.ReadFile("testdata/events.golden.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Fatalf("event snapshot mismatch\nwant:\n%s\n\ngot:\n%s", string(want), string(got))
	}

	var decoded struct {
		AssistantEvents []AssistantMessageEvent `json:"assistantEvents"`
		AgentEvents     []AgentEvent            `json:"agentEvents"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, ok := decoded.AgentEvents[1].Message.(AssistantMessage); !ok {
		t.Fatalf("decoded message = %T, want AssistantMessage", decoded.AgentEvents[1].Message)
	}
}

func TestContextToolJSONSnapshot(t *testing.T) {
	ctx := Context{
		SystemPrompt: "You are helpful.",
		Messages: MessageList{
			UserMessage{Content: ContentList{NewTextContent("hello")}, Timestamp: 1700000000000},
		},
		Tools: []ToolDefinition{
			{
				Name:        "lookup",
				Description: "Lookup data",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
			},
		},
	}
	got, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	want, err := os.ReadFile("testdata/context_tool.golden.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Fatalf("context snapshot mismatch\nwant:\n%s\n\ngot:\n%s", string(want), string(got))
	}
}

func TestAssistantMessageEventValidationRequiresFields(t *testing.T) {
	tests := []AssistantMessageEvent{
		{Type: AssistantEventStart},
		{Type: AssistantEventTextStart, ContentIndex: ptrInt(0)},
		{Type: AssistantEventTextDelta, Delta: "missing index"},
		{Type: AssistantEventTextDelta, ContentIndex: ptrInt(0)},
		{Type: AssistantEventThinkingDelta, ContentIndex: ptrInt(0), Delta: "missing partial"},
		{Type: AssistantEventToolCallDelta, ContentIndex: ptrInt(0), Delta: "missing partial"},
		{Type: AssistantEventToolCallEnd, ContentIndex: ptrInt(0)},
		{Type: AssistantEventTextEnd, ContentIndex: ptrInt(0), Partial: &AssistantMessage{Content: ContentList{}, StopReason: StopReasonStop}},
		{Type: AssistantEventThinkingEnd, ContentIndex: ptrInt(0), Partial: &AssistantMessage{Content: ContentList{}, StopReason: StopReasonStop}},
		{Type: AssistantEventToolCallEnd, ContentIndex: ptrInt(0), ToolCall: ptrToolCall(NewToolCallContent("call_1", "lookup", json.RawMessage(`{"query":"pods"}`)))},
	}
	for _, event := range tests {
		if err := event.Validate(); err == nil {
			t.Fatalf("Validate(%s) error = nil, want error", event.Type)
		}
	}
}

func TestAgentEventMessageUpdateRequiresAssistantMessageEvent(t *testing.T) {
	message := AssistantMessage{Content: ContentList{NewTextContent("hello")}, Usage: Usage{}, StopReason: StopReasonStop}
	event := AgentEvent{Type: AgentEventMessageUpdate, Message: message, Delta: "hello"}
	if err := event.Validate(); err == nil {
		t.Fatal("Validate(message_update without assistantMessageEvent) error = nil, want error")
	}
}

func TestToolCallArgumentsRequireObject(t *testing.T) {
	content := NewToolCallContent("call_1", "lookup", json.RawMessage(`["not-object"]`))
	if err := content.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
	if _, err := content.ArgumentsMap(); err == nil {
		t.Fatal("ArgumentsMap() error = nil, want error")
	}
}

func TestMessageUnmarshalRejectsUnknownStructuredFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "message",
			raw:  `{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1,"extra":true}`,
		},
		{
			name: "content",
			raw:  `{"role":"user","content":[{"type":"text","text":"hello","extra":true}],"timestamp":1}`,
		},
		{
			name: "usage",
			raw:  `{"role":"assistant","content":[{"type":"text","text":"hello"}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"extra":true},"stopReason":"stop","timestamp":1}`,
		},
		{
			name: "usage cost",
			raw:  `{"role":"assistant","content":[{"type":"text","text":"hello"}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"total":2,"extra":true}},"stopReason":"stop","timestamp":1}`,
		},
		{
			name: "tool call",
			raw:  `{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"lookup","arguments":{"nested":true},"extra":true}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2},"stopReason":"toolUse","timestamp":1}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := UnmarshalMessage([]byte(tt.raw)); err == nil {
				t.Fatal("UnmarshalMessage() error = nil, want unknown field rejection")
			}
		})
	}

	msg, err := UnmarshalMessage([]byte(`{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"lookup","arguments":{"opaque":true}}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2},"stopReason":"toolUse","timestamp":1}`))
	if err != nil {
		t.Fatalf("UnmarshalMessage() opaque tool arguments error = %v", err)
	}
	if _, ok := msg.(AssistantMessage); !ok {
		t.Fatalf("message = %T, want AssistantMessage", msg)
	}
}

func TestAgentEventToolExecutionStartArgsRequireObject(t *testing.T) {
	event := AgentEvent{
		Type:       AgentEventToolExecutionStart,
		ToolCallID: "call_1",
		ToolName:   "lookup",
		Args:       json.RawMessage(`["not-object"]`),
	}
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestAssistantMessageEventStreamDoneResult(t *testing.T) {
	stream := NewAssistantMessageEventStream(2)
	message := AssistantMessage{
		Content:    ContentList{NewTextContent("done")},
		Usage:      Usage{Input: 1, Output: 1, TotalTokens: 2},
		StopReason: StopReasonStop,
		Timestamp:  1700000003000,
	}

	if err := stream.Push(AssistantMessageEvent{Type: AssistantEventStart, Partial: &message}); err != nil {
		t.Fatalf("Push(start) error = %v", err)
	}
	if err := stream.Push(AssistantMessageEvent{Type: AssistantEventDone, Reason: StopReasonStop, Message: &message}); err != nil {
		t.Fatalf("Push(done) error = %v", err)
	}
	result, err := stream.Result(context.Background())
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if result.StopReason != StopReasonStop {
		t.Fatalf("result.StopReason = %q, want stop", result.StopReason)
	}
	if err := stream.Push(AssistantMessageEvent{Type: AssistantEventTextDelta, Delta: "late"}); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Push(after terminal) error = %v, want ErrStreamClosed", err)
	}
}

func TestAssistantMessageEventStreamErrorResult(t *testing.T) {
	stream := NewAssistantMessageEventStream(1)
	message := AssistantMessage{
		Content:      ContentList{NewTextContent("partial")},
		Usage:        Usage{Input: 1, Output: 0, TotalTokens: 1},
		StopReason:   StopReasonError,
		ErrorMessage: "provider failed",
		Timestamp:    1700000004000,
	}

	if err := stream.Push(AssistantMessageEvent{Type: AssistantEventError, Reason: StopReasonError, Error: &message}); err != nil {
		t.Fatalf("Push(error) error = %v", err)
	}
	result, err := stream.Result(context.Background())
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if result.ErrorMessage != "provider failed" {
		t.Fatalf("result.ErrorMessage = %q, want provider failed", result.ErrorMessage)
	}
}

func TestAssistantMessageEventStreamResultSnapshotsTerminalMessage(t *testing.T) {
	stream := NewAssistantMessageEventStream(1)
	message := AssistantMessage{
		Content:    ContentList{NewTextContent("original")},
		StopReason: StopReasonStop,
	}
	if err := stream.Push(AssistantMessageEvent{Type: AssistantEventDone, Reason: StopReasonStop, Message: &message}); err != nil {
		t.Fatalf("Push(done) error = %v", err)
	}
	message.Content[0] = NewTextContent("mutated")
	result, err := stream.Result(context.Background())
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if got := TextFromContent(result.Content); got != "original" {
		t.Fatalf("result text = %q, want original", got)
	}
}

func TestAssistantMessageEventStreamZeroBufferTerminalResult(t *testing.T) {
	stream := NewAssistantMessageEventStream(0)
	message := AssistantMessage{
		Content:    ContentList{NewTextContent("done")},
		Usage:      Usage{},
		StopReason: StopReasonStop,
	}

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- stream.Push(AssistantMessageEvent{Type: AssistantEventDone, Reason: StopReasonStop, Message: &message})
	}()

	assertPushReturns(t, pushDone)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := stream.Result(ctx)
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if result.StopReason != StopReasonStop {
		t.Fatalf("result.StopReason = %q, want stop", result.StopReason)
	}
	event := assertNextStreamEvent(t, stream)
	if event.Type != AssistantEventDone {
		t.Fatalf("event.Type = %q, want done", event.Type)
	}
	assertStreamClosed(t, stream)
}

func TestAssistantMessageEventStreamDeliversTerminalEventBeforeClose(t *testing.T) {
	stream := NewAssistantMessageEventStream(0)
	message := AssistantMessage{
		Content:    ContentList{NewTextContent("complete")},
		StopReason: StopReasonStop,
	}
	if err := stream.Push(AssistantMessageEvent{Type: AssistantEventStart, Partial: &message}); err != nil {
		t.Fatalf("Push(start) error = %v", err)
	}
	if err := stream.Push(AssistantMessageEvent{Type: AssistantEventDone, Reason: StopReasonStop, Message: &message}); err != nil {
		t.Fatalf("Push(done) error = %v", err)
	}
	if event := assertNextStreamEvent(t, stream); event.Type != AssistantEventStart {
		t.Fatalf("first event type = %q, want start", event.Type)
	}
	if event := assertNextStreamEvent(t, stream); event.Type != AssistantEventDone {
		t.Fatalf("terminal event type = %q, want done", event.Type)
	}
	assertStreamClosed(t, stream)
	result, err := stream.Result(context.Background())
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if got := TextFromContent(result.Content); got != "complete" {
		t.Fatalf("result text = %q, want complete", got)
	}
}

func TestAssistantMessageEventStreamResultHonorsContextCancellation(t *testing.T) {
	stream := NewAssistantMessageEventStream(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stream.Result(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Result() error = %v, want context.Canceled", err)
	}
}

func TestCloneMessageRetainsProtocolFieldsAndCopiesMutableData(t *testing.T) {
	cacheWrite1h := 3
	reasoning := 7
	assistant := AssistantMessage{
		Content: ContentList{
			NewToolCallContent("call_1", "lookup", json.RawMessage(`{"query":"pods"}`)),
		},
		Usage: Usage{
			Input:        1,
			Output:       2,
			CacheRead:    3,
			CacheWrite:   4,
			CacheWrite1h: &cacheWrite1h,
			Reasoning:    &reasoning,
			TotalTokens:  10,
			Cost:         &UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1.0},
		},
		StopReason: StopReasonToolUse,
	}
	clonedAssistant := CloneAssistantMessage(assistant)
	clonedToolCall, ok := clonedAssistant.Content[0].(ToolCallContent)
	if !ok {
		t.Fatalf("cloned assistant content = %T, want ToolCallContent", clonedAssistant.Content[0])
	}
	clonedToolCall.Arguments[1] = 'X'
	*clonedAssistant.Usage.CacheWrite1h = 30
	*clonedAssistant.Usage.Reasoning = 70
	clonedAssistant.Usage.Cost.Total = 10

	originalToolCall := assistant.Content[0].(ToolCallContent)
	if string(originalToolCall.Arguments) != `{"query":"pods"}` {
		t.Fatalf("original tool arguments = %s, want unchanged", originalToolCall.Arguments)
	}
	if *assistant.Usage.CacheWrite1h != 3 {
		t.Fatalf("original CacheWrite1h = %d, want 3", *assistant.Usage.CacheWrite1h)
	}
	if *assistant.Usage.Reasoning != 7 {
		t.Fatalf("original Reasoning = %d, want 7", *assistant.Usage.Reasoning)
	}
	if assistant.Usage.Cost.Total != 1.0 {
		t.Fatalf("original cost total = %v, want 1.0", assistant.Usage.Cost.Total)
	}

	originalDetails := map[string]any{"nested": map[string]any{"value": "source"}}
	toolResult := ToolResultMessage{
		ToolCallID: "call_1",
		ToolName:   "lookup",
		Content:    ContentList{NewTextContent("found")},
		Details:    originalDetails,
	}
	clonedToolResult := CloneToolResultMessage(toolResult)
	clonedDetails := clonedToolResult.Details.(map[string]any)
	clonedNested := clonedDetails["nested"].(map[string]any)
	clonedNested["value"] = "clone"
	originalNested := originalDetails["nested"].(map[string]any)
	if originalNested["value"] != "source" {
		t.Fatalf("original details nested value = %v, want source", originalNested["value"])
	}

	tools := []ToolDefinition{
		{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	clonedTools := CloneTools(tools)
	clonedTools[0].Parameters[1] = 'X'
	if string(tools[0].Parameters) != `{"type":"object"}` {
		t.Fatalf("original tool parameters = %s, want unchanged", tools[0].Parameters)
	}
}

func usageWithReasoning(reasoning int) Usage {
	return Usage{
		Input:       10,
		Output:      5,
		CacheRead:   2,
		CacheWrite:  0,
		Reasoning:   &reasoning,
		TotalTokens: 15,
		Cost: &UsageCost{
			Input:      0.01,
			Output:     0.02,
			CacheRead:  0.001,
			CacheWrite: 0,
			Total:      0.031,
		},
	}
}

func ptrInt(value int) *int {
	return &value
}

func ptrToolCall(value ToolCallContent) *ToolCallContent {
	return &value
}

func assertPushReturns(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Push() blocked")
	}
}

func assertNextStreamEvent(t *testing.T, stream *AssistantMessageEventStream) AssistantMessageEvent {
	t.Helper()
	select {
	case event, ok := <-stream.Events():
		if !ok {
			t.Fatal("Events() closed before next event")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream event")
		return AssistantMessageEvent{}
	}
}

func assertStreamClosed(t *testing.T, stream *AssistantMessageEventStream) {
	t.Helper()
	select {
	case _, ok := <-stream.Events():
		if ok {
			t.Fatal("Events() open after terminal event")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream close")
	}
}
