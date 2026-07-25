package core

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	protocol "oops/internal/agent/ai"
)

type testTool struct {
	def     protocol.ToolDefinition
	mode    ExecutionMode
	prepare func(context.Context, json.RawMessage) (json.RawMessage, error)
	execute func(context.Context, ToolCall, ToolUpdateSink) (protocol.ToolResult, error)
}

func (t testTool) Definition() protocol.ToolDefinition { return t.def }
func (t testTool) ExecutionMode() ExecutionMode        { return t.mode }
func (t testTool) PrepareArguments(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.prepare != nil {
		return t.prepare(ctx, raw)
	}
	return cloneRaw(raw), nil
}
func (t testTool) Execute(ctx context.Context, call ToolCall, emit ToolUpdateSink) (protocol.ToolResult, error) {
	return t.execute(ctx, call, emit)
}

func TestRunnerUnknownToolCreatesErrorResult(t *testing.T) {
	runner, err := NewRunner(RunnerConfig{})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	var events []protocol.AgentEvent
	result, err := runner.ExecuteTools(context.Background(), ToolRunRequest{
		Turn:      1,
		ToolCalls: []protocol.ToolCallContent{toolCall("call_1", "missing", `{}`)},
		Emit: func(_ context.Context, event protocol.AgentEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTools() error = %v", err)
	}
	if len(result.Messages) != 1 || !result.Messages[0].IsError {
		t.Fatalf("messages = %#v, want one error message", result.Messages)
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, []protocol.AgentEventType{
		protocol.AgentEventToolExecutionStart,
		protocol.AgentEventToolExecutionEnd,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
	}) {
		t.Fatalf("events = %v", got)
	}
}

func TestRunnerHooksRewriteAndAfterOverride(t *testing.T) {
	var executedRaw string
	runner, err := NewRunner(RunnerConfig{
		Tools: []Tool{testTool{
			def: protocol.ToolDefinition{
				Name:       "lookup",
				Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
			},
			prepare: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{"query":"prepared"}`), nil
			},
			execute: func(_ context.Context, call ToolCall, _ ToolUpdateSink) (protocol.ToolResult, error) {
				executedRaw = string(call.RawArguments)
				return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent(call.Arguments["query"].(string))}}, nil
			},
		}},
		Hooks: Hooks{
			BeforeToolCall: func(_ context.Context, ctx BeforeToolCallContext) (BeforeToolCallResult, error) {
				if ctx.Arguments["query"] != "prepared" {
					t.Fatalf("before args = %#v", ctx.Arguments)
				}
				return BeforeToolCallResult{Arguments: json.RawMessage(`{"query":"rewritten"}`)}, nil
			},
			AfterToolCall: func(_ context.Context, ctx AfterToolCallContext) (AfterToolCallResult, error) {
				if text := ctx.Result.Content[0].(protocol.TextContent).Text; text != "rewritten" {
					t.Fatalf("after result text = %q", text)
				}
				isError := false
				terminate := true
				return AfterToolCallResult{
					Content:    protocol.ContentList{protocol.NewTextContent("visible")},
					HasContent: true,
					Details:    map[string]any{"ok": true},
					HasDetails: true,
					IsError:    &isError,
					Terminate:  &terminate,
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	var startArgs json.RawMessage
	result, err := runner.ExecuteTools(context.Background(), ToolRunRequest{
		Turn:      1,
		ToolCalls: []protocol.ToolCallContent{toolCall("call_1", "lookup", `{"raw":true}`)},
		Emit: func(_ context.Context, event protocol.AgentEvent) error {
			if event.Type == protocol.AgentEventToolExecutionStart {
				startArgs = event.Args
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTools() error = %v", err)
	}
	if string(startArgs) != `{"raw":true}` {
		t.Fatalf("start args = %s", startArgs)
	}
	if executedRaw != `{"query":"rewritten"}` {
		t.Fatalf("executed raw = %s", executedRaw)
	}
	if !result.Terminate {
		t.Fatal("Terminate = false, want true")
	}
	got := result.Messages[0]
	if text := got.Content[0].(protocol.TextContent).Text; text != "visible" {
		t.Fatalf("message text = %q", text)
	}
	if got.IsError {
		t.Fatal("IsError = true, want false")
	}
}

func TestRunnerParallelEndCompletionOrderAndMessageSourceOrder(t *testing.T) {
	secondEnd := make(chan struct{})
	runner, err := NewRunner(RunnerConfig{
		Tools: []Tool{
			testTool{
				def: protocol.ToolDefinition{Name: "first"},
				execute: func(context.Context, ToolCall, ToolUpdateSink) (protocol.ToolResult, error) {
					<-secondEnd
					return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent("first")}}, nil
				},
			},
			testTool{
				def: protocol.ToolDefinition{Name: "second"},
				execute: func(context.Context, ToolCall, ToolUpdateSink) (protocol.ToolResult, error) {
					return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent("second")}}, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	var endOrder []string
	result, err := runner.ExecuteTools(context.Background(), ToolRunRequest{
		Turn: 1,
		ToolCalls: []protocol.ToolCallContent{
			toolCall("call_1", "first", `{}`),
			toolCall("call_2", "second", `{}`),
		},
		Emit: func(_ context.Context, event protocol.AgentEvent) error {
			if event.Type == protocol.AgentEventToolExecutionEnd {
				endOrder = append(endOrder, event.ToolCallID)
				if event.ToolCallID == "call_2" {
					close(secondEnd)
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTools() error = %v", err)
	}
	if !reflect.DeepEqual(endOrder, []string{"call_2", "call_1"}) {
		t.Fatalf("end order = %v", endOrder)
	}
	if got := []string{result.Messages[0].ToolCallID, result.Messages[1].ToolCallID}; !reflect.DeepEqual(got, []string{"call_1", "call_2"}) {
		t.Fatalf("message order = %v", got)
	}
}

func TestRunnerLateToolUpdateIsIgnored(t *testing.T) {
	var late ToolUpdateSink
	runner, err := NewRunner(RunnerConfig{
		Tools: []Tool{testTool{
			def: protocol.ToolDefinition{Name: "lookup"},
			execute: func(ctx context.Context, _ ToolCall, emit ToolUpdateSink) (protocol.ToolResult, error) {
				late = emit
				if err := emit(ctx, protocol.AgentEvent{Delta: "working"}); err != nil {
					return protocol.ToolResult{}, err
				}
				return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent("done")}}, nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	updateCount := 0
	_, err = runner.ExecuteTools(context.Background(), ToolRunRequest{
		Turn:      1,
		ToolCalls: []protocol.ToolCallContent{toolCall("call_1", "lookup", `{}`)},
		Emit: func(_ context.Context, event protocol.AgentEvent) error {
			if event.Type == protocol.AgentEventToolExecutionUpdate {
				updateCount++
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTools() error = %v", err)
	}
	if err := late(context.Background(), protocol.AgentEvent{Delta: "late"}); err != nil {
		t.Fatalf("late update error = %v", err)
	}
	if updateCount != 1 {
		t.Fatalf("update count = %d, want 1", updateCount)
	}
}
