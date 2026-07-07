package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentevents "oops/internal/llm/events"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestLifecycleEventToStepEventsPreservesPublicProtocol(t *testing.T) {
	lifecycleEvents := []LifecycleEvent{
		{Type: LifecycleAgentStart},
		{
			Type: LifecycleMessageEnd,
			Message: schema.AssistantMessage("查询机器列表", []schema.ToolCall{{
				ID: "call-1",
				Function: schema.FunctionCall{
					Name:      "list_nodelets",
					Arguments: "{}",
				},
			}}),
		},
		{
			Type:       LifecycleToolCall,
			Content:    "list_nodelets",
			ToolName:   "list_nodelets",
			ToolArgs:   "{}",
			ToolCallID: "call-1",
		},
		{
			Type:       LifecycleToolResult,
			Content:    "[]",
			ToolName:   "list_nodelets",
			ToolCallID: "call-1",
		},
		{Type: LifecycleAnswer, Content: "最终回答"},
		{Type: LifecycleError, Content: "failed"},
		{Type: LifecycleAgentEnd},
	}

	var got []agentevents.StepEvent
	for _, evt := range lifecycleEvents {
		got = append(got, lifecycleEventToStepEvents(evt)...)
	}

	want := []agentevents.StepEvent{
		{Type: "thinking", Content: "查询机器列表"},
		{Type: "tool_call", Content: "list_nodelets", ToolName: "list_nodelets", ToolArgs: "{}", ToolCallID: "call-1"},
		{Type: "tool_result", Content: "[]", ToolName: "list_nodelets", ToolCallID: "call-1"},
		{Type: "answer", Content: "最终回答"},
		{Type: "error", Content: "failed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StepEvents = %#v, want %#v", got, want)
	}
}

func TestAfterToolCallBridgeModifiesAndDropsToolResults(t *testing.T) {
	originalHook, hadOriginal := lookupAfterToolCall("lookup")
	t.Cleanup(func() {
		if hadOriginal {
			RegisterAfterToolCall("lookup", originalHook)
		} else {
			RegisterAfterToolCall("lookup", nil)
		}
	})

	RegisterAfterToolCall("lookup", func(_ context.Context, msg *schema.Message) *schema.Message {
		msg.Content = "changed"
		return msg
	})
	modified := afterToolCallBridge(context.Background(), schema.ToolMessage("raw", "call-1", schema.WithToolName("lookup")))
	if modified == nil || modified.Content != "changed" {
		t.Fatalf("modified result = %#v, want changed content", modified)
	}

	RegisterAfterToolCall("lookup", func(context.Context, *schema.Message) *schema.Message {
		return nil
	})
	if dropped := afterToolCallBridge(context.Background(), schema.ToolMessage("raw", "call-1", schema.WithToolName("lookup"))); dropped != nil {
		t.Fatalf("dropped result = %#v, want nil", dropped)
	}
}

func TestRunnerRunEmitsLifecycleTranscriptAndAppliesToolHook(t *testing.T) {
	script := &scriptedToolModel{}
	var persisted []*schema.Message

	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model: script,
		Tools: []tool.InvokableTool{invokableToolForTest{
			name: "lookup",
		}},
		Messages: []*schema.Message{schema.UserMessage("use lookup")},
		MaxStep:  3,
		Hooks: RunHooks{
			OnMessage: func(_ context.Context, msg *schema.Message) error {
				persisted = append(persisted, msg)
				return nil
			},
			AfterToolResult: func(_ context.Context, msg *schema.Message) *schema.Message {
				msg.Content = "hooked result"
				return msg
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var lifecycleTypes []LifecycleEventType
	var publicEvents []agentevents.StepEvent
	for evt := range events {
		lifecycleTypes = append(lifecycleTypes, evt.Type)
		publicEvents = append(publicEvents, lifecycleEventToStepEvents(evt)...)
	}

	wantLifecycle := []LifecycleEventType{
		LifecycleAgentStart,
		LifecycleMessageEnd,
		LifecycleToolCall,
		LifecycleMessageEnd,
		LifecycleToolResult,
		LifecycleMessageEnd,
		LifecycleAnswer,
		LifecycleAgentEnd,
	}
	if !reflect.DeepEqual(lifecycleTypes, wantLifecycle) {
		t.Fatalf("lifecycleTypes = %#v, want %#v", lifecycleTypes, wantLifecycle)
	}

	wantPublic := []agentevents.StepEvent{
		{Type: "thinking", Content: "need lookup"},
		{Type: "tool_call", Content: "lookup", ToolName: "lookup", ToolArgs: `{"id":1}`, ToolCallID: "call-1"},
		{Type: "tool_result", Content: "hooked result", ToolName: "lookup", ToolCallID: "call-1"},
		{Type: "answer", Content: "lookup done"},
	}
	if !reflect.DeepEqual(publicEvents, wantPublic) {
		t.Fatalf("publicEvents = %#v, want %#v", publicEvents, wantPublic)
	}

	if len(persisted) != 3 {
		t.Fatalf("len(persisted) = %d, want 3", len(persisted))
	}
	if persisted[1].Role != schema.Tool || persisted[1].Content != "hooked result" {
		t.Fatalf("persisted tool result = %#v, want hooked tool message", persisted[1])
	}
}

func TestRunnerRunEmitsErrorAndAgentEnd(t *testing.T) {
	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model:    &scriptedToolModel{errOnFirstCall: errors.New("model failed")},
		Messages: []*schema.Message{schema.UserMessage("fail")},
		MaxStep:  3,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var lifecycleTypes []LifecycleEventType
	for evt := range events {
		lifecycleTypes = append(lifecycleTypes, evt.Type)
	}

	want := []LifecycleEventType{LifecycleAgentStart, LifecycleError, LifecycleAgentEnd}
	if !reflect.DeepEqual(lifecycleTypes, want) {
		t.Fatalf("lifecycleTypes = %#v, want %#v", lifecycleTypes, want)
	}
}

func TestRunnerRunDropToolResultClearsPendingCall(t *testing.T) {
	var persisted []*schema.Message
	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model: &scriptedToolModel{
			errOnSecondCall: errors.New("second model failed"),
		},
		Tools: []tool.InvokableTool{invokableToolForTest{
			name: "lookup",
		}},
		Messages: []*schema.Message{schema.UserMessage("use lookup")},
		MaxStep:  3,
		Hooks: RunHooks{
			OnMessage: func(_ context.Context, msg *schema.Message) error {
				persisted = append(persisted, msg)
				return nil
			},
			AfterToolResult: func(context.Context, *schema.Message) *schema.Message {
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var publicEvents []agentevents.StepEvent
	for evt := range events {
		publicEvents = append(publicEvents, lifecycleEventToStepEvents(evt)...)
	}

	if len(publicEvents) != 3 {
		t.Fatalf("len(publicEvents) = %d, want 3: %#v", len(publicEvents), publicEvents)
	}
	if publicEvents[0] != (agentevents.StepEvent{Type: "thinking", Content: "need lookup"}) {
		t.Fatalf("thinking event = %#v", publicEvents[0])
	}
	if publicEvents[1] != (agentevents.StepEvent{Type: "tool_call", Content: "lookup", ToolName: "lookup", ToolArgs: `{"id":1}`, ToolCallID: "call-1"}) {
		t.Fatalf("tool call event = %#v", publicEvents[1])
	}
	if publicEvents[2].Type != "error" || !strings.Contains(publicEvents[2].Content, "second model failed") {
		t.Fatalf("error event = %#v", publicEvents[2])
	}
	if len(persisted) != 2 {
		t.Fatalf("len(persisted) = %d, want assistant and hidden tool result", len(persisted))
	}
	if persisted[0].Role != schema.Assistant {
		t.Fatalf("persisted[0].Role = %q, want assistant", persisted[0].Role)
	}
	if persisted[1].Role != schema.Tool || persisted[1].ToolCallID != "call-1" {
		t.Fatalf("persisted hidden tool result = %#v, want closed tool call", persisted[1])
	}
}

func TestRunnerRunClosesChannelOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events, err := NewRunner().Run(ctx, RunRequest{
		Model:    &blockingToolModel{started: make(chan struct{})},
		Messages: []*schema.Message{schema.UserMessage("wait")},
		MaxStep:  3,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if evt, ok := <-events; !ok || evt.Type != LifecycleAgentStart {
		t.Fatalf("first event = %#v, ok=%v, want agent_start", evt, ok)
	}
	cancel()

	select {
	case _, ok := <-events:
		if ok {
			for range events {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events channel did not close after context cancel")
	}
}

func TestBeforeToolCallRewritesExecutedArgs(t *testing.T) {
	lookupTool := &captureInvokableTool{name: "lookup", result: "raw result"}
	model := newHookTestModel(t, func(call int, input []*schema.Message) (*schema.Message, error) {
		if call == 1 {
			return assistantToolCall("lookup", "call-1", `{"raw":true}`), nil
		}
		return schema.AssistantMessage("done", nil), nil
	})

	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model:    model,
		Tools:    []tool.InvokableTool{lookupTool},
		Messages: []*schema.Message{schema.UserMessage("use lookup")},
		MaxStep:  3,
		Hooks: RunHooks{
			BeforeToolCall: func(_ context.Context, input ToolCallInput) (string, error) {
				if input.Name != "lookup" || input.Arguments != `{"raw":true}` {
					t.Fatalf("before input = %#v", input)
				}
				return `{"rewritten":true}`, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	drainLifecycle(events)

	if got := lookupTool.arguments(); len(got) != 1 || got[0] != `{"rewritten":true}` {
		t.Fatalf("tool arguments = %#v, want rewritten args", got)
	}
}

func TestRawAndExecutedArgsSplit(t *testing.T) {
	lookupTool := &captureInvokableTool{name: "lookup", result: "raw result"}
	model := newHookTestModel(t, func(call int, input []*schema.Message) (*schema.Message, error) {
		if call == 1 {
			return assistantToolCall("lookup", "call-1", `{"raw":true}`), nil
		}
		return schema.AssistantMessage("done", nil), nil
	})

	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model:    model,
		Tools:    []tool.InvokableTool{lookupTool},
		Messages: []*schema.Message{schema.UserMessage("use lookup")},
		MaxStep:  3,
		Hooks: RunHooks{
			BeforeToolCall: func(context.Context, ToolCallInput) (string, error) {
				return `{"executed":true}`, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var publicEvents []agentevents.StepEvent
	for evt := range events {
		publicEvents = append(publicEvents, lifecycleEventToStepEvents(evt)...)
	}

	var toolArgs string
	for _, evt := range publicEvents {
		if evt.Type == "tool_call" {
			toolArgs = evt.ToolArgs
		}
	}
	if toolArgs != `{"raw":true}` {
		t.Fatalf("public tool args = %q, want raw model args", toolArgs)
	}
	if got := lookupTool.arguments(); len(got) != 1 || got[0] != `{"executed":true}` {
		t.Fatalf("tool arguments = %#v, want executed args", got)
	}
}

func TestSemanticAfterToolCallRewritesModelInput(t *testing.T) {
	lookupTool := &captureInvokableTool{name: "lookup", result: "raw result"}
	var persisted []*schema.Message
	model := newHookTestModel(t, func(call int, input []*schema.Message) (*schema.Message, error) {
		if call == 1 {
			return assistantToolCall("lookup", "call-1", `{"id":1}`), nil
		}
		if got := lastToolContent(input); got != "semantic result" {
			t.Fatalf("second model input tool content = %q, want semantic result", got)
		}
		return schema.AssistantMessage("done", nil), nil
	})

	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model:    model,
		Tools:    []tool.InvokableTool{lookupTool},
		Messages: []*schema.Message{schema.UserMessage("use lookup")},
		MaxStep:  3,
		Hooks: RunHooks{
			OnMessage: func(_ context.Context, msg *schema.Message) error {
				persisted = append(persisted, msg)
				return nil
			},
			SemanticAfterToolCall: func(_ context.Context, input ToolCallInput, output ToolCallOutput) (string, error) {
				if input.Name != "lookup" || input.Arguments != `{"id":1}` || input.CallID != "call-1" {
					t.Fatalf("semantic input = %#v", input)
				}
				if output.Result != "raw result" {
					t.Fatalf("semantic output = %#v", output)
				}
				return "semantic result", nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var publicEvents []agentevents.StepEvent
	for evt := range events {
		publicEvents = append(publicEvents, lifecycleEventToStepEvents(evt)...)
	}
	if got := toolResultContent(publicEvents); got != "semantic result" {
		t.Fatalf("public tool result = %q, want semantic result", got)
	}
	if len(persisted) < 2 || persisted[1].Role != schema.Tool || persisted[1].Content != "semantic result" {
		t.Fatalf("persisted messages = %#v, want semantic tool result", persisted)
	}
}

func TestBeforeToolCallErrorRejectsExecutionAndClosesPendingToolCall(t *testing.T) {
	lookupTool := &captureInvokableTool{name: "lookup", result: "raw result"}
	var persisted []*schema.Message
	model := newHookTestModel(t, func(call int, input []*schema.Message) (*schema.Message, error) {
		if call == 1 {
			return assistantToolCall("lookup", "call-1", `{"id":1}`), nil
		}
		return schema.AssistantMessage("done", nil), nil
	})

	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model:    model,
		Tools:    []tool.InvokableTool{lookupTool},
		Messages: []*schema.Message{schema.UserMessage("use lookup")},
		MaxStep:  3,
		Hooks: RunHooks{
			OnMessage: func(_ context.Context, msg *schema.Message) error {
				persisted = append(persisted, msg)
				return nil
			},
			BeforeToolCall: func(context.Context, ToolCallInput) (string, error) {
				return "", errors.New("before rejected")
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	lifecycleTypes, publicEvents := collectLifecycleAndPublicEvents(events)
	if got := lookupTool.arguments(); len(got) != 0 {
		t.Fatalf("tool executed with args %#v, want no execution", got)
	}
	if !hasLifecycleEvent(lifecycleTypes, LifecycleAgentEnd) {
		t.Fatalf("lifecycleTypes = %#v, want agent_end", lifecycleTypes)
	}
	if !hasStepEvent(publicEvents, "tool_call", "lookup") {
		t.Fatalf("events = %#v, want raw tool call", publicEvents)
	}
	if !hasStepEvent(publicEvents, "tool_result", "before rejected") {
		t.Fatalf("events = %#v, want pending tool error result", publicEvents)
	}
	if !hasStepEvent(publicEvents, "error", "before rejected") {
		t.Fatalf("events = %#v, want error event", publicEvents)
	}
	if len(persisted) != 2 || persisted[1].Role != schema.Tool || !strings.Contains(persisted[1].Content, "before rejected") {
		t.Fatalf("persisted messages = %#v, want assistant plus pending tool error", persisted)
	}
}

func TestSemanticAfterToolCallErrorEmitsError(t *testing.T) {
	lookupTool := &captureInvokableTool{name: "lookup", result: "raw result"}
	var persisted []*schema.Message
	model := newHookTestModel(t, func(call int, input []*schema.Message) (*schema.Message, error) {
		if call == 1 {
			return assistantToolCall("lookup", "call-1", `{"id":1}`), nil
		}
		return schema.AssistantMessage("done", nil), nil
	})

	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model:    model,
		Tools:    []tool.InvokableTool{lookupTool},
		Messages: []*schema.Message{schema.UserMessage("use lookup")},
		MaxStep:  3,
		Hooks: RunHooks{
			OnMessage: func(_ context.Context, msg *schema.Message) error {
				persisted = append(persisted, msg)
				return nil
			},
			SemanticAfterToolCall: func(context.Context, ToolCallInput, ToolCallOutput) (string, error) {
				return "", errors.New("semantic failed")
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	lifecycleTypes, publicEvents := collectLifecycleAndPublicEvents(events)
	if got := lookupTool.arguments(); len(got) != 1 || got[0] != `{"id":1}` {
		t.Fatalf("tool arguments = %#v, want one execution with raw args", got)
	}
	if !hasLifecycleEvent(lifecycleTypes, LifecycleAgentEnd) {
		t.Fatalf("lifecycleTypes = %#v, want agent_end", lifecycleTypes)
	}
	if hasStepEvent(publicEvents, "tool_result", "raw result") {
		t.Fatalf("events = %#v, raw result should not be public after semantic error", publicEvents)
	}
	if !hasStepEvent(publicEvents, "tool_result", "semantic failed") {
		t.Fatalf("events = %#v, want pending tool error result", publicEvents)
	}
	if !hasStepEvent(publicEvents, "error", "semantic failed") {
		t.Fatalf("events = %#v, want error event", publicEvents)
	}
	if len(persisted) != 2 || persisted[1].Role != schema.Tool || !strings.Contains(persisted[1].Content, "semantic failed") {
		t.Fatalf("persisted messages = %#v, want assistant plus pending tool error", persisted)
	}
}

func TestDisplayAfterToolResultModifyAndDropCompatibility(t *testing.T) {
	t.Run("modify is display and persistence only", func(t *testing.T) {
		lookupTool := &captureInvokableTool{name: "lookup", result: "raw result"}
		var persisted []*schema.Message
		var secondModelToolContent string
		model := newHookTestModel(t, func(call int, input []*schema.Message) (*schema.Message, error) {
			if call == 1 {
				return assistantToolCall("lookup", "call-1", `{"id":1}`), nil
			}
			secondModelToolContent = lastToolContent(input)
			return schema.AssistantMessage("done", nil), nil
		})

		events, err := NewRunner().Run(context.Background(), RunRequest{
			Model:    model,
			Tools:    []tool.InvokableTool{lookupTool},
			Messages: []*schema.Message{schema.UserMessage("use lookup")},
			MaxStep:  3,
			Hooks: RunHooks{
				OnMessage: func(_ context.Context, msg *schema.Message) error {
					persisted = append(persisted, msg)
					return nil
				},
				SemanticAfterToolCall: func(context.Context, ToolCallInput, ToolCallOutput) (string, error) {
					return "semantic result", nil
				},
				AfterToolResult: func(_ context.Context, msg *schema.Message) *schema.Message {
					msg.Content = "display result"
					return msg
				},
			},
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		publicEvents := collectPublicEvents(events)
		if secondModelToolContent != "semantic result" {
			t.Fatalf("second model input tool content = %q, want semantic result", secondModelToolContent)
		}
		if got := toolResultContent(publicEvents); got != "display result" {
			t.Fatalf("public tool result = %q, want display result", got)
		}
		if len(persisted) < 2 || persisted[1].Role != schema.Tool || persisted[1].Content != "display result" {
			t.Fatalf("persisted messages = %#v, want display result persisted", persisted)
		}
	})

	t.Run("drop hides display while preserving transcript", func(t *testing.T) {
		var persisted []*schema.Message
		events, err := NewRunner().Run(context.Background(), RunRequest{
			Model: &scriptedToolModel{
				errOnSecondCall: errors.New("second model failed"),
			},
			Tools: []tool.InvokableTool{&captureInvokableTool{
				name: "lookup",
			}},
			Messages: []*schema.Message{schema.UserMessage("use lookup")},
			MaxStep:  3,
			Hooks: RunHooks{
				OnMessage: func(_ context.Context, msg *schema.Message) error {
					persisted = append(persisted, msg)
					return nil
				},
				AfterToolResult: func(context.Context, *schema.Message) *schema.Message {
					return nil
				},
			},
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		lifecycleTypes, publicEvents := collectLifecycleAndPublicEvents(events)
		if !hasLifecycleEvent(lifecycleTypes, LifecycleAgentEnd) {
			t.Fatalf("lifecycleTypes = %#v, want agent_end", lifecycleTypes)
		}
		if hasStepEvent(publicEvents, "tool_result", "") {
			t.Fatalf("events = %#v, want no public tool result", publicEvents)
		}
		if !hasStepEvent(publicEvents, "error", "second model failed") {
			t.Fatalf("events = %#v, want model error", publicEvents)
		}
		if len(persisted) != 2 || persisted[0].Role != schema.Assistant || persisted[1].Role != schema.Tool {
			t.Fatalf("persisted messages = %#v, want assistant and tool transcript", persisted)
		}
		if persisted[1].ToolCallID != "call-1" || persisted[1].ToolName != "lookup" {
			t.Fatalf("persisted tool result = %#v, want closed tool call", persisted[1])
		}
	})
}

func TestParallelToolHooksAreRaceSafe(t *testing.T) {
	toolA := &captureInvokableTool{name: "lookup_a", result: "a"}
	toolB := &captureInvokableTool{name: "lookup_b", result: "b"}
	var beforeCalls atomic.Int64
	var afterCalls atomic.Int64
	var closeOverlap sync.Once
	overlap := make(chan struct{})
	model := newHookTestModel(t, func(call int, input []*schema.Message) (*schema.Message, error) {
		if call == 1 {
			return schema.AssistantMessage("parallel", []schema.ToolCall{
				{ID: "call-a", Function: schema.FunctionCall{Name: "lookup_a", Arguments: `{"n":1}`}},
				{ID: "call-b", Function: schema.FunctionCall{Name: "lookup_b", Arguments: `{"n":2}`}},
			}), nil
		}
		return schema.AssistantMessage("done", nil), nil
	})

	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model:    model,
		Tools:    []tool.InvokableTool{toolA, toolB},
		Messages: []*schema.Message{schema.UserMessage("use both")},
		MaxStep:  3,
		Hooks: RunHooks{
			BeforeToolCall: func(_ context.Context, input ToolCallInput) (string, error) {
				beforeCalls.Add(1)
				return input.Arguments, nil
			},
			SemanticAfterToolCall: func(_ context.Context, _ ToolCallInput, output ToolCallOutput) (string, error) {
				if afterCalls.Add(1) == 2 {
					closeOverlap.Do(func() { close(overlap) })
				}
				select {
				case <-overlap:
				case <-time.After(2 * time.Second):
					return "", errors.New("semantic hooks did not overlap")
				}
				return output.Result, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	drainLifecycle(events)

	if beforeCalls.Load() != 2 || afterCalls.Load() != 2 {
		t.Fatalf("hook calls before=%d after=%d, want 2/2", beforeCalls.Load(), afterCalls.Load())
	}
}

func TestMCPToolHooksUseModelFacingToolName(t *testing.T) {
	queryTool := &captureInvokableTool{name: "conn__query", result: "ok"}
	var beforeName string
	var afterName string
	model := newHookTestModel(t, func(call int, input []*schema.Message) (*schema.Message, error) {
		if call == 1 {
			return assistantToolCall("conn__query", "call-1", `{"sql":"select 1"}`), nil
		}
		return schema.AssistantMessage("done", nil), nil
	})

	events, err := NewRunner().Run(context.Background(), RunRequest{
		Model:    model,
		Tools:    []tool.InvokableTool{queryTool},
		Messages: []*schema.Message{schema.UserMessage("query")},
		MaxStep:  3,
		Hooks: RunHooks{
			BeforeToolCall: func(_ context.Context, input ToolCallInput) (string, error) {
				beforeName = input.Name
				return input.Arguments, nil
			},
			SemanticAfterToolCall: func(_ context.Context, input ToolCallInput, output ToolCallOutput) (string, error) {
				afterName = input.Name
				return output.Result, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	drainLifecycle(events)

	if beforeName != "conn__query" || afterName != "conn__query" {
		t.Fatalf("hook names before=%q after=%q, want model-facing name", beforeName, afterName)
	}
}

type scriptedToolModel struct {
	mu              sync.Mutex
	calls           int
	errOnFirstCall  error
	errOnSecondCall error
}

func (m *scriptedToolModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.calls++
	if m.calls == 1 && m.errOnFirstCall != nil {
		return nil, m.errOnFirstCall
	}
	if m.calls == 2 && m.errOnSecondCall != nil {
		return nil, m.errOnSecondCall
	}
	if m.calls == 1 {
		return schema.AssistantMessage("need lookup", []schema.ToolCall{{
			ID: "call-1",
			Function: schema.FunctionCall{
				Name:      "lookup",
				Arguments: `{"id":1}`,
			},
		}}), nil
	}
	return schema.AssistantMessage("lookup done", nil), nil
}

func (m *scriptedToolModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("lookup done", nil)}), nil
}

func (m *scriptedToolModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type blockingToolModel struct {
	started chan struct{}
	once    sync.Once
}

func (m *blockingToolModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.once.Do(func() {
		if m.started != nil {
			close(m.started)
		}
	})
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingToolModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{}), nil
}

func (m *blockingToolModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type hookTestModel struct {
	t        *testing.T
	mu       sync.Mutex
	calls    int
	generate func(call int, input []*schema.Message) (*schema.Message, error)
}

func newHookTestModel(t *testing.T, generate func(call int, input []*schema.Message) (*schema.Message, error)) *hookTestModel {
	return &hookTestModel{t: t, generate: generate}
}

func (m *hookTestModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.calls++
	return m.generate(m.calls, input)
}

func (m *hookTestModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *hookTestModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type captureInvokableTool struct {
	name   string
	result string
	mu     sync.Mutex
	args   []string
}

func (t *captureInvokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: t.name}, nil
}

func (t *captureInvokableTool) InvokableRun(_ context.Context, arguments string, _ ...tool.Option) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.args = append(t.args, arguments)
	return t.result, nil
}

func (t *captureInvokableTool) arguments() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.args))
	copy(out, t.args)
	return out
}

func assistantToolCall(name, id, arguments string) *schema.Message {
	return schema.AssistantMessage("need lookup", []schema.ToolCall{{
		ID: id,
		Function: schema.FunctionCall{
			Name:      name,
			Arguments: arguments,
		},
	}})
}

func drainLifecycle(events <-chan LifecycleEvent) {
	for range events {
	}
}

func collectPublicEvents(events <-chan LifecycleEvent) []agentevents.StepEvent {
	var publicEvents []agentevents.StepEvent
	for evt := range events {
		publicEvents = append(publicEvents, lifecycleEventToStepEvents(evt)...)
	}
	return publicEvents
}

func collectLifecycleAndPublicEvents(events <-chan LifecycleEvent) ([]LifecycleEventType, []agentevents.StepEvent) {
	var lifecycleTypes []LifecycleEventType
	var publicEvents []agentevents.StepEvent
	for evt := range events {
		lifecycleTypes = append(lifecycleTypes, evt.Type)
		publicEvents = append(publicEvents, lifecycleEventToStepEvents(evt)...)
	}
	return lifecycleTypes, publicEvents
}

func lastToolContent(messages []*schema.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == schema.Tool {
			return messages[i].Content
		}
	}
	return ""
}

func toolResultContent(events []agentevents.StepEvent) string {
	for _, evt := range events {
		if evt.Type == "tool_result" {
			return evt.Content
		}
	}
	return ""
}

func hasStepEvent(events []agentevents.StepEvent, eventType, contentSubstring string) bool {
	for _, evt := range events {
		if evt.Type == eventType && strings.Contains(evt.Content, contentSubstring) {
			return true
		}
	}
	return false
}

func hasLifecycleEvent(events []LifecycleEventType, eventType LifecycleEventType) bool {
	for _, evt := range events {
		if evt == eventType {
			return true
		}
	}
	return false
}
