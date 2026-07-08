package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"oops/internal/llm/ai/protocol"
)

func TestRunAgentLoopTextAnswerEventOrder(t *testing.T) {
	events := recordEvents(t)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: queueStreams(textStream("done"))},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	assertRoles(t, newMessages, protocol.RoleUser, protocol.RoleAssistant)
	assertEventTypes(t, events.values,
		protocol.AgentEventAgentStart,
		protocol.AgentEventTurnStart,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageUpdate,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventTurnEnd,
		protocol.AgentEventAgentEnd,
	)
	if events.values[1].Turn != 1 {
		t.Fatalf("turn_start turn = %d, want 1", events.values[1].Turn)
	}
}

func TestRunAgentLoopToolCallThenAnswerOrder(t *testing.T) {
	call := toolCall("call_1", "lookup", `{"query":"pods"}`)
	events := recordEvents(t)
	streams := queueStreams(toolCallStream(call), textStream("answer"))
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream:     streams,
			ToolRunner: toolRunnerFunc(successTool(false, "found")),
		},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	assertRoles(t, newMessages, protocol.RoleUser, protocol.RoleAssistant, protocol.RoleToolResult, protocol.RoleAssistant)
	toolResult := newMessages[2].(protocol.ToolResultMessage)
	if toolResult.ToolCallID != "call_1" || textContent(t, toolResult.Content[0]) != "found" {
		t.Fatalf("tool result = %#v", toolResult)
	}
	assertEventTypes(t, events.values,
		protocol.AgentEventAgentStart,
		protocol.AgentEventTurnStart,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageUpdate,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventToolExecutionStart,
		protocol.AgentEventToolExecutionEnd,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventTurnEnd,
		protocol.AgentEventTurnStart,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageUpdate,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventTurnEnd,
		protocol.AgentEventAgentEnd,
	)
}

func TestRunAgentLoopProviderErrorTerminal(t *testing.T) {
	events := recordEvents(t)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Provider: "provider", Model: "model", Stream: streamError(errors.New("provider failed"))},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	got := lastAssistant(t, newMessages)
	if got.StopReason != protocol.StopReasonError || got.ErrorMessage != "provider failed" {
		t.Fatalf("assistant error = %#v", got)
	}
	assertSuffixTypes(t, events.values,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventTurnEnd,
		protocol.AgentEventAgentEnd,
	)
}

func TestRunAgentLoopStreamFunctionErrorTerminal(t *testing.T) {
	events := recordEvents(t)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: streamError(errors.New("stream failed"))},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	if got := lastAssistant(t, newMessages).ErrorMessage; got != "stream failed" {
		t.Fatalf("ErrorMessage = %q, want stream failed", got)
	}
}

func TestRunAgentLoopAssistantEventErrorTerminal(t *testing.T) {
	message := protocol.AssistantMessage{
		Content:      protocol.ContentList{},
		StopReason:   protocol.StopReasonError,
		ErrorMessage: "event failed",
	}
	events := recordEvents(t)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: queueStreams(eventsStream(
			protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &protocol.AssistantMessage{Content: protocol.ContentList{}, StopReason: protocol.StopReasonStop}},
			protocol.AssistantMessageEvent{Type: protocol.AssistantEventError, Reason: protocol.StopReasonError, Error: &message},
		))},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	if got := lastAssistant(t, newMessages).ErrorMessage; got != "event failed" {
		t.Fatalf("ErrorMessage = %q, want event failed", got)
	}
}

func TestRunAgentLoopAssistantEventErrorNormalizesStopReason(t *testing.T) {
	message := protocol.AssistantMessage{
		Content:      protocol.ContentList{},
		StopReason:   protocol.StopReasonStop,
		ErrorMessage: "event failed",
	}
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: queueStreams(eventsStream(
			protocol.AssistantMessageEvent{Type: protocol.AssistantEventError, Reason: protocol.StopReasonError, Error: &message},
		))},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	got := lastAssistant(t, newMessages)
	if got.StopReason != protocol.StopReasonError {
		t.Fatalf("StopReason = %q, want error", got.StopReason)
	}
}

func TestRunAgentLoopTerminalOnlyDoneStartsMessage(t *testing.T) {
	message := assistantTextMessage("done")
	events := recordEvents(t)
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: queueStreams(eventsStream(
			protocol.AssistantMessageEvent{Type: protocol.AssistantEventDone, Reason: protocol.StopReasonStop, Message: &message},
		))},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	assertSuffixTypes(t, events.values[:len(events.values)-2],
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
	)
}

func TestRunAgentLoopTerminalOnlyErrorStartsMessage(t *testing.T) {
	message := protocol.AssistantMessage{
		Content:      protocol.ContentList{},
		StopReason:   protocol.StopReasonError,
		ErrorMessage: "event failed",
	}
	events := recordEvents(t)
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: queueStreams(eventsStream(
			protocol.AssistantMessageEvent{Type: protocol.AssistantEventError, Reason: protocol.StopReasonError, Error: &message},
		))},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	assertSuffixTypes(t, events.values[:len(events.values)-2],
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
	)
}

func TestRunAgentLoopContextCancelAbortedTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := recordEvents(t)
	newMessages, err := RunAgentLoop(
		ctx,
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: queueStreams(emptyOpenStream())},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	got := lastAssistant(t, newMessages)
	if got.StopReason != protocol.StopReasonAborted {
		t.Fatalf("StopReason = %q, want aborted", got.StopReason)
	}
}

func TestRunAgentLoopStartedContextCancelEndsSameMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := protocol.NewAssistantMessageEventStream(1)
	start := protocol.AssistantMessage{Content: protocol.ContentList{}, StopReason: protocol.StopReasonStop}
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &start}); err != nil {
		t.Fatalf("Push(start) error = %v", err)
	}

	var events []protocol.AgentEvent
	newMessages, err := RunAgentLoop(
		ctx,
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: queueStreams(stream)},
		func(_ context.Context, event protocol.AgentEvent) error {
			events = append(events, event)
			if event.Type == protocol.AgentEventMessageStart && event.Message.MessageRole() == protocol.RoleAssistant {
				cancel()
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	got := lastAssistant(t, newMessages)
	if got.StopReason != protocol.StopReasonAborted {
		t.Fatalf("StopReason = %q, want aborted", got.StopReason)
	}

	assistantStarts := 0
	assistantEnds := 0
	for _, event := range events {
		if event.Message == nil || event.Message.MessageRole() != protocol.RoleAssistant {
			continue
		}
		switch event.Type {
		case protocol.AgentEventMessageStart:
			assistantStarts++
		case protocol.AgentEventMessageEnd:
			assistantEnds++
		}
	}
	if assistantStarts != 1 || assistantEnds != 1 {
		t.Fatalf("assistant lifecycle starts=%d ends=%d, want 1/1; events=%v", assistantStarts, assistantEnds, eventTypes(events))
	}
	assertSuffixTypes(t, events,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventTurnEnd,
		protocol.AgentEventAgentEnd,
	)
}

func TestRunAgentLoopCancelAfterToolCallSkipsToolRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	call := toolCall("call_1", "lookup", `{}`)
	called := false
	var events []protocol.AgentEventType

	newMessages, err := RunAgentLoop(
		ctx,
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: queueStreams(toolCallStream(call), textStream("done")),
			ToolRunner: toolRunnerFunc(func(context.Context, protocol.ToolCallContent, ToolUpdateSink) (protocol.ToolResult, bool, error) {
				called = true
				return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent("found")}}, false, nil
			}),
		},
		func(_ context.Context, event protocol.AgentEvent) error {
			events = append(events, event.Type)
			if event.Type == protocol.AgentEventMessageEnd && event.Message != nil && event.Message.MessageRole() == protocol.RoleAssistant {
				cancel()
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	if called {
		t.Fatal("ToolRunner was called after context cancellation")
	}
	if got := lastAssistant(t, newMessages); got.StopReason != protocol.StopReasonAborted {
		t.Fatalf("StopReason = %q, want aborted", got.StopReason)
	}
	for _, eventType := range events {
		if eventType == protocol.AgentEventToolExecutionStart {
			t.Fatalf("tool execution started after cancellation: %v", events)
		}
	}
}

func TestRunAgentLoopToolRunnerContextCancelAborts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	call := toolCall("call_1", "lookup", `{}`)

	newMessages, err := RunAgentLoop(
		ctx,
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: queueStreams(toolCallStream(call), textStream("done")),
			ToolRunner: toolRunnerFunc(func(toolCtx context.Context, _ protocol.ToolCallContent, _ ToolUpdateSink) (protocol.ToolResult, bool, error) {
				cancel()
				return protocol.ToolResult{}, false, toolCtx.Err()
			}),
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	for _, message := range newMessages {
		if message.MessageRole() == protocol.RoleToolResult {
			t.Fatalf("unexpected tool result after context cancellation: %#v", message)
		}
	}
	if got := lastAssistant(t, newMessages); got.StopReason != protocol.StopReasonAborted {
		t.Fatalf("StopReason = %q, want aborted", got.StopReason)
	}
}

func TestRunAgentLoopMaxTurns(t *testing.T) {
	call := toolCall("call_1", "lookup", `{}`)
	events := recordEvents(t)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			MaxTurns:   1,
			Stream:     queueStreams(toolCallStream(call)),
			ToolRunner: toolRunnerFunc(successTool(false, "found")),
		},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	got := lastAssistant(t, newMessages)
	if got.ErrorMessage != "max_turns_exceeded" {
		t.Fatalf("ErrorMessage = %q, want max_turns_exceeded", got.ErrorMessage)
	}
	assertSuffixTypes(t, events.values,
		protocol.AgentEventTurnStart,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventTurnEnd,
		protocol.AgentEventAgentEnd,
	)
	if events.values[len(events.values)-5].Turn != 2 {
		t.Fatalf("synthetic turn = %d, want 2", events.values[len(events.values)-5].Turn)
	}
}

func TestRunAgentLoopContinueGuards(t *testing.T) {
	_, err := RunAgentLoopContinue(context.Background(), AgentContext{}, AgentLoopConfig{Stream: queueStreams(textStream("done"))}, nil)
	if !errors.Is(err, ErrContinueEmptyContext) {
		t.Fatalf("empty context error = %v, want ErrContinueEmptyContext", err)
	}

	_, err = RunAgentLoopContinue(
		context.Background(),
		AgentContext{Messages: protocol.MessageList{assistantTextMessage("done")}},
		AgentLoopConfig{Stream: queueStreams(textStream("done"))},
		nil,
	)
	if !errors.Is(err, ErrContinueFromAssistant) {
		t.Fatalf("assistant tail error = %v, want ErrContinueFromAssistant", err)
	}
}

func TestRunAgentLoopContinueReturnsOnlyNewMessages(t *testing.T) {
	newMessages, err := RunAgentLoopContinue(
		context.Background(),
		AgentContext{Messages: protocol.MessageList{userMessage("existing")}},
		AgentLoopConfig{Stream: queueStreams(textStream("continued"))},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoopContinue() error = %v", err)
	}
	assertRoles(t, newMessages, protocol.RoleAssistant)
	if got := textContent(t, newMessages[0].(protocol.AssistantMessage).Content[0]); got != "continued" {
		t.Fatalf("continue message = %q, want continued", got)
	}
}

func TestRunAgentLoopMissingStreamGuard(t *testing.T) {
	events := recordEvents(t)
	_, err := RunAgentLoop(context.Background(), protocol.MessageList{userMessage("hello")}, AgentContext{}, AgentLoopConfig{}, events.emit)
	if !errors.Is(err, ErrMissingStream) {
		t.Fatalf("RunAgentLoop() error = %v, want ErrMissingStream", err)
	}
	if len(events.values) != 0 {
		t.Fatalf("events = %d, want 0", len(events.values))
	}
}

func TestRunAgentLoopEmitErrorStops(t *testing.T) {
	wantErr := errors.New("emit failed")
	count := 0
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: queueStreams(textStream("done"))},
		func(context.Context, protocol.AgentEvent) error {
			count++
			if count == 3 {
				return wantErr
			}
			return nil
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunAgentLoop() error = %v, want %v", err, wantErr)
	}
	if count != 3 {
		t.Fatalf("emit count = %d, want 3", count)
	}
}

func TestRunAgentLoopSteeringDrainPoint(t *testing.T) {
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{textStream("first"), textStream("second")}}
	steered := false
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: streams.stream,
			GetSteeringMessages: func(context.Context) (protocol.MessageList, error) {
				if steered {
					return nil, nil
				}
				steered = true
				return protocol.MessageList{userMessage("steer")}, nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	assertRoles(t, newMessages, protocol.RoleUser, protocol.RoleAssistant, protocol.RoleUser, protocol.RoleAssistant)
	if got := textContent(t, streams.requests[1].Context.Messages[2].(protocol.UserMessage).Content[0]); got != "steer" {
		t.Fatalf("second request steering = %q, want steer", got)
	}
}

func TestRunAgentLoopFollowUpDrainPoint(t *testing.T) {
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{textStream("first"), textStream("second")}}
	followed := false
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: streams.stream,
			GetFollowUpMessages: func(context.Context) (protocol.MessageList, error) {
				if followed {
					return nil, nil
				}
				followed = true
				return protocol.MessageList{userMessage("follow")}, nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	assertRoles(t, newMessages, protocol.RoleUser, protocol.RoleAssistant, protocol.RoleUser, protocol.RoleAssistant)
	if got := textContent(t, streams.requests[1].Context.Messages[2].(protocol.UserMessage).Content[0]); got != "follow" {
		t.Fatalf("second request follow-up = %q, want follow", got)
	}
}

func TestRunAgentLoopTransformAndConvertBeforeStream(t *testing.T) {
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{textStream("done")}}
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: streams.stream,
			TransformContext: func(_ context.Context, messages protocol.MessageList) (protocol.MessageList, error) {
				return append(messages, userMessage("transformed")), nil
			},
			ConvertToLLM: func(_ context.Context, messages protocol.MessageList) (protocol.MessageList, error) {
				return protocol.MessageList{messages[len(messages)-1]}, nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	requestMessages := streams.requests[0].Context.Messages
	if len(requestMessages) != 1 {
		t.Fatalf("request message len = %d, want 1", len(requestMessages))
	}
	if got := textContent(t, requestMessages[0].(protocol.UserMessage).Content[0]); got != "transformed" {
		t.Fatalf("request message = %q, want transformed", got)
	}
}

func TestRunAgentLoopMixedTerminateContinues(t *testing.T) {
	calls := []protocol.ToolCallContent{
		toolCall("call_1", "one", `{}`),
		toolCall("call_2", "two", `{}`),
	}
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{toolCallsStream(calls...), textStream("done")}}
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: streams.stream,
			ToolRunner: toolRunnerFunc(func(_ context.Context, call protocol.ToolCallContent, _ ToolUpdateSink) (protocol.ToolResult, bool, error) {
				return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent(call.ID)}, Terminate: call.ID == "call_1"}, false, nil
			}),
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	if len(streams.requests) != 2 {
		t.Fatalf("stream requests = %d, want 2", len(streams.requests))
	}
}

func TestRunAgentLoopAllTerminateRunsPostTurnHooks(t *testing.T) {
	call := toolCall("call_1", "lookup", `{}`)
	var order []string
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream:     queueStreams(toolCallStream(call)),
			ToolRunner: toolRunnerFunc(successTool(true, "done")),
			PrepareNextTurn: func(context.Context, TurnContext) (TurnUpdate, error) {
				order = append(order, "prepare")
				return TurnUpdate{}, nil
			},
			ShouldStopAfterTurn: func(context.Context, TurnContext) (bool, error) {
				order = append(order, "stop")
				return false, nil
			},
			GetSteeringMessages: func(context.Context) (protocol.MessageList, error) {
				order = append(order, "steering")
				return nil, nil
			},
			GetFollowUpMessages: func(context.Context) (protocol.MessageList, error) {
				order = append(order, "follow")
				return nil, nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	want := []string{"prepare", "stop", "steering", "follow"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("hook order = %v, want %v", order, want)
	}
}

func TestRunAgentLoopMissingToolRunnerCreatesErrorToolResult(t *testing.T) {
	call := toolCall("call_1", "lookup", `{}`)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{Stream: queueStreams(toolCallStream(call), textStream("done"))},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	result := newMessages[2].(protocol.ToolResultMessage)
	if !result.IsError {
		t.Fatal("tool result IsError = false, want true")
	}
}

func TestRunAgentLoopToolUpdateSinkFillsMetadata(t *testing.T) {
	call := toolCall("call_1", "lookup", `{}`)
	var update protocol.AgentEvent
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: queueStreams(toolCallStream(call), textStream("done")),
			ToolRunner: toolRunnerFunc(func(ctx context.Context, _ protocol.ToolCallContent, emit ToolUpdateSink) (protocol.ToolResult, bool, error) {
				if err := emit(ctx, protocol.AgentEvent{Delta: "working"}); err != nil {
					return protocol.ToolResult{}, false, err
				}
				return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent("found")}}, false, nil
			}),
		},
		func(_ context.Context, event protocol.AgentEvent) error {
			if event.Type == protocol.AgentEventToolExecutionUpdate {
				update = event
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	if update.Type != protocol.AgentEventToolExecutionUpdate || update.Turn != 1 || update.ToolCallID != "call_1" || update.ToolName != "lookup" {
		t.Fatalf("update event = %#v", update)
	}
}

func TestRunAgentLoopToolUpdateEmitErrorStops(t *testing.T) {
	call := toolCall("call_1", "lookup", `{}`)
	wantErr := errors.New("update emit failed")
	var eventTypes []protocol.AgentEventType
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: queueStreams(toolCallStream(call), textStream("done")),
			ToolRunner: toolRunnerFunc(func(ctx context.Context, _ protocol.ToolCallContent, emit ToolUpdateSink) (protocol.ToolResult, bool, error) {
				if err := emit(ctx, protocol.AgentEvent{Delta: "working"}); err != nil {
					return protocol.ToolResult{}, false, err
				}
				return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent("found")}}, false, nil
			}),
		},
		func(_ context.Context, event protocol.AgentEvent) error {
			eventTypes = append(eventTypes, event.Type)
			if event.Type == protocol.AgentEventToolExecutionUpdate {
				return wantErr
			}
			return nil
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunAgentLoop() error = %v, want %v", err, wantErr)
	}
	for _, eventType := range eventTypes {
		if eventType == protocol.AgentEventToolExecutionEnd || eventType == protocol.AgentEventAgentEnd {
			t.Fatalf("event %s emitted after update failure: %v", eventType, eventTypes)
		}
	}
}

func TestRunAgentLoopToolRunnerErrorCreatesErrorToolResult(t *testing.T) {
	call := toolCall("call_1", "lookup", `{}`)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: queueStreams(toolCallStream(call), textStream("done")),
			ToolRunner: toolRunnerFunc(func(context.Context, protocol.ToolCallContent, ToolUpdateSink) (protocol.ToolResult, bool, error) {
				return protocol.ToolResult{}, false, errors.New("tool failed")
			}),
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	result := newMessages[2].(protocol.ToolResultMessage)
	if !result.IsError || textContent(t, result.Content[0]) != "tool failed" {
		t.Fatalf("tool result = %#v", result)
	}
}

func TestRunAgentLoopMultipleToolResultsSourceOrder(t *testing.T) {
	calls := []protocol.ToolCallContent{
		toolCall("call_1", "one", `{}`),
		toolCall("call_2", "two", `{}`),
	}
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream:     queueStreams(toolCallsStream(calls...), textStream("done")),
			ToolRunner: toolRunnerFunc(successTool(false, "found")),
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	if newMessages[2].(protocol.ToolResultMessage).ToolCallID != "call_1" {
		t.Fatalf("first result = %s, want call_1", newMessages[2].(protocol.ToolResultMessage).ToolCallID)
	}
	if newMessages[3].(protocol.ToolResultMessage).ToolCallID != "call_2" {
		t.Fatalf("second result = %s, want call_2", newMessages[3].(protocol.ToolResultMessage).ToolCallID)
	}
}

func TestRunAgentLoopToolStartArgsClone(t *testing.T) {
	call := toolCall("call_1", "lookup", `{"query":"pods"}`)
	var captured json.RawMessage
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream:     queueStreams(toolCallStream(call), textStream("done")),
			ToolRunner: toolRunnerFunc(successTool(false, "found")),
		},
		func(_ context.Context, event protocol.AgentEvent) error {
			if event.Type == protocol.AgentEventToolExecutionStart {
				captured = event.Args
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	call.Arguments[1] = 'x'
	if string(captured) != `{"query":"pods"}` {
		t.Fatalf("captured args = %s, want original JSON", string(captured))
	}
}

func TestRunAgentLoopToolRunnerArgsClone(t *testing.T) {
	call := toolCall("call_1", "lookup", `{"query":"pods"}`)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: queueStreams(toolCallStream(call), textStream("done")),
			ToolRunner: toolRunnerFunc(func(_ context.Context, call protocol.ToolCallContent, _ ToolUpdateSink) (protocol.ToolResult, bool, error) {
				call.Arguments[1] = 'x'
				return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent("found")}}, false, nil
			}),
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	assistant := newMessages[1].(protocol.AssistantMessage)
	toolCall := assistant.Content[0].(protocol.ToolCallContent)
	if string(toolCall.Arguments) != `{"query":"pods"}` {
		t.Fatalf("assistant tool args = %s, want original JSON", string(toolCall.Arguments))
	}
}

func TestRunAgentLoopTurnContextSnapshotDeepClone(t *testing.T) {
	call := toolCall("call_1", "lookup", `{"query":"pods"}`)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream:     queueStreams(toolCallStream(call)),
			ToolRunner: toolRunnerFunc(successTool(true, "found")),
			PrepareNextTurn: func(_ context.Context, turnCtx TurnContext) (TurnUpdate, error) {
				user := turnCtx.NewMessages[0].(protocol.UserMessage)
				user.Content[0] = protocol.NewTextContent("mutated user")

				assistant := turnCtx.Context.Messages[1].(protocol.AssistantMessage)
				toolCall := assistant.Content[0].(protocol.ToolCallContent)
				toolCall.Arguments[1] = 'x'

				turnCtx.ToolResults[0].Content[0] = protocol.NewTextContent("mutated result")
				return TurnUpdate{}, nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}

	user := newMessages[0].(protocol.UserMessage)
	if got := textContent(t, user.Content[0]); got != "hello" {
		t.Fatalf("user content = %q, want hello", got)
	}
	assistant := newMessages[1].(protocol.AssistantMessage)
	toolCall := assistant.Content[0].(protocol.ToolCallContent)
	if string(toolCall.Arguments) != `{"query":"pods"}` {
		t.Fatalf("assistant tool args = %s, want original JSON", string(toolCall.Arguments))
	}
	toolResult := newMessages[2].(protocol.ToolResultMessage)
	if got := textContent(t, toolResult.Content[0]); got != "found" {
		t.Fatalf("tool result content = %q, want found", got)
	}
}

func TestRunAgentLoopPostTurnHookErrorSyntheticTurn(t *testing.T) {
	events := recordEvents(t)
	newMessages, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: queueStreams(textStream("done")),
			PrepareNextTurn: func(context.Context, TurnContext) (TurnUpdate, error) {
				return TurnUpdate{}, errors.New("prepare failed")
			},
		},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	if got := lastAssistant(t, newMessages).ErrorMessage; got != "prepare failed" {
		t.Fatalf("ErrorMessage = %q, want prepare failed", got)
	}
	assertSuffixTypes(t, events.values,
		protocol.AgentEventTurnEnd,
		protocol.AgentEventTurnStart,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventTurnEnd,
		protocol.AgentEventAgentEnd,
	)
}

func TestRunAgentLoopTurnHookOrder(t *testing.T) {
	var order []string
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: queueStreams(textStream("done")),
			PrepareNextTurn: func(context.Context, TurnContext) (TurnUpdate, error) {
				order = append(order, "prepare")
				return TurnUpdate{}, nil
			},
			ShouldStopAfterTurn: func(context.Context, TurnContext) (bool, error) {
				order = append(order, "stop")
				return false, nil
			},
			GetSteeringMessages: func(context.Context) (protocol.MessageList, error) {
				order = append(order, "steering")
				return nil, nil
			},
			GetFollowUpMessages: func(context.Context) (protocol.MessageList, error) {
				order = append(order, "follow")
				return nil, nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	want := []string{"prepare", "stop", "steering", "follow"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("hook order = %v, want %v", order, want)
	}
}

func TestRunAgentLoopShouldStopPriority(t *testing.T) {
	var called []string
	_, err := RunAgentLoop(
		context.Background(),
		protocol.MessageList{userMessage("hello")},
		AgentContext{},
		AgentLoopConfig{
			Stream: queueStreams(textStream("done")),
			ShouldStopAfterTurn: func(context.Context, TurnContext) (bool, error) {
				called = append(called, "stop")
				return true, nil
			},
			GetSteeringMessages: func(context.Context) (protocol.MessageList, error) {
				called = append(called, "steering")
				return protocol.MessageList{userMessage("steer")}, nil
			},
			GetFollowUpMessages: func(context.Context) (protocol.MessageList, error) {
				called = append(called, "follow")
				return protocol.MessageList{userMessage("follow")}, nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop() error = %v", err)
	}
	if !reflect.DeepEqual(called, []string{"stop"}) {
		t.Fatalf("called hooks = %v, want [stop]", called)
	}
}

type recordedEvents struct {
	t      *testing.T
	values []protocol.AgentEvent
}

func recordEvents(t *testing.T) *recordedEvents {
	return &recordedEvents{t: t}
}

func (r *recordedEvents) emit(_ context.Context, event protocol.AgentEvent) error {
	r.t.Helper()
	r.values = append(r.values, event)
	return nil
}

type requestRecorder struct {
	streams  []*protocol.AssistantMessageEventStream
	requests []StreamRequest
}

func (r *requestRecorder) stream(_ context.Context, req StreamRequest) (*protocol.AssistantMessageEventStream, error) {
	r.requests = append(r.requests, req)
	if len(r.streams) == 0 {
		return nil, errors.New("unexpected stream request")
	}
	stream := r.streams[0]
	r.streams = r.streams[1:]
	return stream, nil
}

type toolRunnerFunc func(context.Context, protocol.ToolCallContent, ToolUpdateSink) (protocol.ToolResult, bool, error)

func (fn toolRunnerFunc) ExecuteTool(ctx context.Context, call protocol.ToolCallContent, emit ToolUpdateSink) (protocol.ToolResult, bool, error) {
	return fn(ctx, call, emit)
}

func successTool(terminate bool, text string) func(context.Context, protocol.ToolCallContent, ToolUpdateSink) (protocol.ToolResult, bool, error) {
	return func(context.Context, protocol.ToolCallContent, ToolUpdateSink) (protocol.ToolResult, bool, error) {
		return protocol.ToolResult{
			Content:   protocol.ContentList{protocol.NewTextContent(text)},
			Terminate: terminate,
		}, false, nil
	}
}

func queueStreams(streams ...*protocol.AssistantMessageEventStream) StreamFn {
	recorder := &requestRecorder{streams: streams}
	return recorder.stream
}

func streamError(err error) StreamFn {
	return func(context.Context, StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		return nil, err
	}
}

func textStream(text string) *protocol.AssistantMessageEventStream {
	message := assistantTextMessage(text)
	index := 0
	return eventsStream(
		protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &protocol.AssistantMessage{Content: protocol.ContentList{}, StopReason: protocol.StopReasonStop}},
		protocol.AssistantMessageEvent{Type: protocol.AssistantEventTextDelta, ContentIndex: &index, Delta: text, Partial: &message},
		protocol.AssistantMessageEvent{Type: protocol.AssistantEventDone, Reason: protocol.StopReasonStop, Message: &message},
	)
}

func toolCallStream(call protocol.ToolCallContent) *protocol.AssistantMessageEventStream {
	return toolCallsStream(call)
}

func toolCallsStream(calls ...protocol.ToolCallContent) *protocol.AssistantMessageEventStream {
	content := make(protocol.ContentList, 0, len(calls))
	for _, call := range calls {
		content = append(content, call)
	}
	message := protocol.AssistantMessage{Content: content, StopReason: protocol.StopReasonToolUse}
	events := []protocol.AssistantMessageEvent{
		{Type: protocol.AssistantEventStart, Partial: &protocol.AssistantMessage{Content: protocol.ContentList{}, StopReason: protocol.StopReasonStop}},
	}
	for i, call := range calls {
		index := i
		callCopy := call
		events = append(events, protocol.AssistantMessageEvent{Type: protocol.AssistantEventToolCallEnd, ContentIndex: &index, ToolCall: &callCopy, Partial: &message})
	}
	events = append(events, protocol.AssistantMessageEvent{Type: protocol.AssistantEventDone, Reason: protocol.StopReasonToolUse, Message: &message})
	return eventsStream(events...)
}

func eventsStream(events ...protocol.AssistantMessageEvent) *protocol.AssistantMessageEventStream {
	stream := protocol.NewAssistantMessageEventStream(len(events))
	for _, event := range events {
		if err := stream.Push(event); err != nil {
			panic(err)
		}
	}
	return stream
}

func emptyOpenStream() *protocol.AssistantMessageEventStream {
	return protocol.NewAssistantMessageEventStream(0)
}

func userMessage(text string) protocol.UserMessage {
	return protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent(text)}}
}

func assistantTextMessage(text string) protocol.AssistantMessage {
	return protocol.AssistantMessage{
		Content:    protocol.ContentList{protocol.NewTextContent(text)},
		StopReason: protocol.StopReasonStop,
	}
}

func toolCall(id, name, args string) protocol.ToolCallContent {
	return protocol.NewToolCallContent(id, name, json.RawMessage(args))
}

func assertRoles(t *testing.T, messages protocol.MessageList, roles ...protocol.MessageRole) {
	t.Helper()
	if len(messages) != len(roles) {
		t.Fatalf("len(messages) = %d, want %d", len(messages), len(roles))
	}
	for i, role := range roles {
		if messages[i].MessageRole() != role {
			t.Fatalf("messages[%d].role = %q, want %q", i, messages[i].MessageRole(), role)
		}
	}
}

func assertEventTypes(t *testing.T, events []protocol.AgentEvent, types ...protocol.AgentEventType) {
	t.Helper()
	if len(events) != len(types) {
		t.Fatalf("len(events) = %d, want %d\n got: %v\nwant: %v", len(events), len(types), eventTypes(events), types)
	}
	for i, typ := range types {
		if events[i].Type != typ {
			t.Fatalf("events[%d].Type = %q, want %q\n got: %v\nwant: %v", i, events[i].Type, typ, eventTypes(events), types)
		}
	}
}

func assertSuffixTypes(t *testing.T, events []protocol.AgentEvent, suffix ...protocol.AgentEventType) {
	t.Helper()
	if len(events) < len(suffix) {
		t.Fatalf("len(events) = %d, want at least %d", len(events), len(suffix))
	}
	start := len(events) - len(suffix)
	assertEventTypes(t, events[start:], suffix...)
}

func eventTypes(events []protocol.AgentEvent) []protocol.AgentEventType {
	out := make([]protocol.AgentEventType, len(events))
	for i, event := range events {
		out[i] = event.Type
	}
	return out
}

func lastAssistant(t *testing.T, messages protocol.MessageList) protocol.AssistantMessage {
	t.Helper()
	if len(messages) == 0 {
		t.Fatal("messages is empty")
	}
	message, ok := messages[len(messages)-1].(protocol.AssistantMessage)
	if !ok {
		t.Fatalf("last message = %T, want AssistantMessage", messages[len(messages)-1])
	}
	return message
}

func textContent(t *testing.T, content protocol.Content) string {
	t.Helper()
	text, ok := content.(protocol.TextContent)
	if !ok {
		t.Fatalf("content = %T, want TextContent", content)
	}
	return text.Text
}
