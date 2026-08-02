package core

import (
	"context"
	"errors"
	"reflect"
	"testing"

	protocol "oops/internal/agent/ai"
)

func TestRunAgentLoopContinueUsesExistingUserTail(t *testing.T) {
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{textStream("retried")}}
	events := recordEvents(t)

	newMessages, err := RunAgentLoopContinue(
		context.Background(),
		AgentContext{Messages: protocol.MessageList{userMessage("retry this")}},
		AgentLoopConfig{Stream: streams.stream},
		events.emit,
	)
	if err != nil {
		t.Fatalf("RunAgentLoopContinue() error = %v", err)
	}

	assertRoles(t, newMessages, protocol.RoleAssistant)
	assertEventTypes(t, events.values,
		protocol.AgentEventAgentStart,
		protocol.AgentEventTurnStart,
		protocol.AgentEventMessageStart,
		protocol.AgentEventMessageUpdate,
		protocol.AgentEventMessageEnd,
		protocol.AgentEventTurnEnd,
		protocol.AgentEventAgentEnd,
	)
	if len(streams.requests) != 1 {
		t.Fatalf("stream requests = %d, want 1", len(streams.requests))
	}
	assertRoles(t, streams.requests[0].Context.Messages, protocol.RoleUser)
	if got := agentMessageText(t, streams.requests[0].Context.Messages[0]); got != "retry this" {
		t.Fatalf("request user message = %q, want retry this", got)
	}
}

func TestRunAgentLoopContinueRejectsInvalidContext(t *testing.T) {
	tests := []struct {
		name    string
		context AgentContext
		wantErr error
	}{
		{name: "empty", wantErr: ErrContinueEmptyContext},
		{
			name:    "assistant tail",
			context: AgentContext{Messages: protocol.MessageList{assistantTextMessage("done")}},
			wantErr: ErrContinueFromAssistant,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := RunAgentLoopContinue(
				context.Background(),
				test.context,
				AgentLoopConfig{Stream: queueStreams(textStream("unused"))},
				nil,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("RunAgentLoopContinue() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestAgentContinueFromAssistantPrioritizesSteering(t *testing.T) {
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{
		textStream("steered response"),
		textStream("follow-up response"),
	}}
	agent := NewAgent(AgentOptions{
		Context: AgentContext{Messages: protocol.MessageList{
			userMessage("initial"),
			assistantTextMessage("initial response"),
		}},
		Config: AgentLoopConfig{Stream: streams.stream},
	})
	if err := agent.Steer(protocol.MessageList{userMessage("steer first")}); err != nil {
		t.Fatalf("Steer() error = %v", err)
	}
	if err := agent.FollowUp(protocol.MessageList{userMessage("follow up second")}); err != nil {
		t.Fatalf("FollowUp() error = %v", err)
	}

	newMessages, err := agent.Continue(context.Background())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	assertRoles(t, newMessages,
		protocol.RoleUser,
		protocol.RoleAssistant,
		protocol.RoleUser,
		protocol.RoleAssistant,
	)
	wantTexts := []string{"steer first", "steered response", "follow up second", "follow-up response"}
	if got := messageTexts(t, newMessages); !reflect.DeepEqual(got, wantTexts) {
		t.Fatalf("new message texts = %v, want %v", got, wantTexts)
	}
	if len(streams.requests) != 2 {
		t.Fatalf("stream requests = %d, want 2", len(streams.requests))
	}
	if got := agentMessageText(t, streams.requests[0].Context.Messages[2]); got != "steer first" {
		t.Fatalf("first request tail = %q, want steer first", got)
	}
	if got := agentMessageText(t, streams.requests[1].Context.Messages[4]); got != "follow up second" {
		t.Fatalf("second request tail = %q, want follow up second", got)
	}
	if agent.HasQueuedMessages() {
		t.Fatal("queues still contain messages after continuation")
	}
}

func TestAgentSteeringQueueModes(t *testing.T) {
	tests := []struct {
		name         string
		mode         QueueMode
		streams      []*protocol.AssistantMessageEventStream
		wantTexts    []string
		wantRequests int
	}{
		{
			name:         "one at a time",
			mode:         QueueModeOneAtATime,
			streams:      []*protocol.AssistantMessageEventStream{textStream("first response"), textStream("second response")},
			wantTexts:    []string{"first steering", "first response", "second steering", "second response"},
			wantRequests: 2,
		},
		{
			name:         "all",
			mode:         QueueModeAll,
			streams:      []*protocol.AssistantMessageEventStream{textStream("combined response")},
			wantTexts:    []string{"first steering", "second steering", "combined response"},
			wantRequests: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			streams := &requestRecorder{streams: test.streams}
			agent := NewAgent(AgentOptions{
				Context: AgentContext{Messages: protocol.MessageList{
					userMessage("initial"),
					assistantTextMessage("initial response"),
				}},
				Config:       AgentLoopConfig{Stream: streams.stream},
				SteeringMode: test.mode,
			})
			if err := agent.Steer(protocol.MessageList{
				userMessage("first steering"),
				userMessage("second steering"),
			}); err != nil {
				t.Fatalf("Steer() error = %v", err)
			}

			newMessages, err := agent.Continue(context.Background())
			if err != nil {
				t.Fatalf("Continue() error = %v", err)
			}
			if got := messageTexts(t, newMessages); !reflect.DeepEqual(got, test.wantTexts) {
				t.Fatalf("new message texts = %v, want %v", got, test.wantTexts)
			}
			if len(streams.requests) != test.wantRequests {
				t.Fatalf("stream requests = %d, want %d", len(streams.requests), test.wantRequests)
			}
			if agent.HasQueuedMessages() {
				t.Fatal("steering queue still contains messages")
			}
		})
	}
}

func TestAgentQueueModeDefaultsAndValidation(t *testing.T) {
	agent := NewAgent(AgentOptions{})
	if got := agent.SteeringMode(); got != QueueModeOneAtATime {
		t.Fatalf("SteeringMode() = %q, want %q", got, QueueModeOneAtATime)
	}
	if got := agent.FollowUpMode(); got != QueueModeOneAtATime {
		t.Fatalf("FollowUpMode() = %q, want %q", got, QueueModeOneAtATime)
	}
	if err := agent.SetSteeringMode(QueueModeAll); err != nil {
		t.Fatalf("SetSteeringMode() error = %v", err)
	}
	if err := agent.SetFollowUpMode(QueueModeAll); err != nil {
		t.Fatalf("SetFollowUpMode() error = %v", err)
	}
	if err := agent.SetSteeringMode(QueueMode("invalid")); err == nil {
		t.Fatal("SetSteeringMode() error = nil, want validation error")
	}
	if err := agent.SetFollowUpMode(QueueMode("invalid")); err == nil {
		t.Fatal("SetFollowUpMode() error = nil, want validation error")
	}
	if agent.SteeringMode() != QueueModeAll || agent.FollowUpMode() != QueueModeAll {
		t.Fatalf("queue modes changed after invalid update: steering=%q follow-up=%q", agent.SteeringMode(), agent.FollowUpMode())
	}
}

func TestAgentContinueFromUserTail(t *testing.T) {
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{textStream("continued")}}
	agent := NewAgent(AgentOptions{
		Context: AgentContext{Messages: protocol.MessageList{userMessage("pending user")}},
		Config:  AgentLoopConfig{Stream: streams.stream},
	})

	newMessages, err := agent.Continue(context.Background())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	assertRoles(t, newMessages, protocol.RoleAssistant)
	assertRoles(t, agent.State().Messages, protocol.RoleUser, protocol.RoleAssistant)
	if len(streams.requests) != 1 {
		t.Fatalf("stream requests = %d, want 1", len(streams.requests))
	}
	if got := agentMessageText(t, streams.requests[0].Context.Messages[0]); got != "pending user" {
		t.Fatalf("request user message = %q, want pending user", got)
	}
}

func TestAgentAbortWaitForIdleAndBusyReset(t *testing.T) {
	streamStarted := make(chan struct{})
	agentEndEntered := make(chan AgentPhase, 1)
	releaseAgentEnd := make(chan struct{})
	agent := NewAgent(AgentOptions{Config: AgentLoopConfig{
		Stream: func(context.Context, StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			close(streamStarted)
			return emptyOpenStream(), nil
		},
	}})
	agent.Listen(func(_ context.Context, event protocol.AgentEvent, state AgentState) error {
		if event.Type == protocol.AgentEventAgentEnd {
			agentEndEntered <- state.Phase
			<-releaseAgentEnd
		}
		return nil
	})

	type runResult struct {
		messages protocol.MessageList
		err      error
	}
	runDone := make(chan runResult, 1)
	go func() {
		messages, err := agent.Prompt(context.Background(), protocol.MessageList{userMessage("long run")})
		runDone <- runResult{messages: messages, err: err}
	}()
	<-streamStarted

	if err := agent.Reset(); !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("Reset() while active error = %v, want %v", err, ErrAgentBusy)
	}
	waiterStarted := make(chan struct{})
	idleDone := make(chan error, 1)
	go func() {
		close(waiterStarted)
		idleDone <- agent.WaitForIdle(context.Background())
	}()
	<-waiterStarted

	if !agent.Abort() {
		t.Fatal("Abort() = false, want active run cancellation")
	}
	if phase := <-agentEndEntered; phase != AgentPhaseAborted {
		t.Fatalf("agent_end phase = %q, want %q", phase, AgentPhaseAborted)
	}
	if phase := agent.State().Phase; phase != AgentPhaseAborted {
		t.Fatalf("phase while agent_end listener is pending = %q, want %q", phase, AgentPhaseAborted)
	}
	select {
	case err := <-idleDone:
		t.Fatalf("WaitForIdle() completed before listener settlement with error %v", err)
	default:
	}

	close(releaseAgentEnd)
	result := <-runDone
	if result.err != nil {
		t.Fatalf("Prompt() error = %v", result.err)
	}
	if got := lastAssistant(t, result.messages).StopReason; got != protocol.StopReasonAborted {
		t.Fatalf("aborted assistant stop reason = %q, want %q", got, protocol.StopReasonAborted)
	}
	if err := <-idleDone; err != nil {
		t.Fatalf("WaitForIdle() error = %v", err)
	}
	state := agent.State()
	if state.Phase != AgentPhaseIdle || state.IsStreaming {
		t.Fatalf("settled state phase/isStreaming = %q/%v, want %q/false", state.Phase, state.IsStreaming, AgentPhaseIdle)
	}
	if agent.Abort() {
		t.Fatal("Abort() = true after run settled")
	}
}

func TestAgentPromptWithCanceledContextSkipsStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	streamCalls := 0
	agent := NewAgent(AgentOptions{Config: AgentLoopConfig{
		Stream: func(context.Context, StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			streamCalls++
			return textStream("unexpected"), nil
		},
	}})

	messages, err := agent.Prompt(ctx, protocol.MessageList{userMessage("cancelled")})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", streamCalls)
	}
	if got := lastAssistant(t, messages).StopReason; got != protocol.StopReasonAborted {
		t.Fatalf("stop reason = %q, want %q", got, protocol.StopReasonAborted)
	}
}

func TestAgentResetClearsIdleRuntimeStateAndQueues(t *testing.T) {
	agent := NewAgent(AgentOptions{
		Context: AgentContext{
			SystemPrompt: "system",
			Tools:        []protocol.ToolDefinition{{Name: "lookup"}},
		},
		Config: AgentLoopConfig{
			Model:     "model",
			Provider:  "provider",
			Reasoning: "high",
			Stream:    streamError(errors.New("provider failed")),
		},
	})
	if _, err := agent.Prompt(context.Background(), protocol.MessageList{userMessage("hello")}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if err := agent.Steer(protocol.MessageList{userMessage("queued steering")}); err != nil {
		t.Fatalf("Steer() error = %v", err)
	}
	if err := agent.FollowUp(protocol.MessageList{userMessage("queued follow-up")}); err != nil {
		t.Fatalf("FollowUp() error = %v", err)
	}
	if state := agent.State(); state.ErrorMessage != "provider failed" || len(state.Messages) == 0 {
		t.Fatalf("pre-reset state error/messages = %q/%d", state.ErrorMessage, len(state.Messages))
	}

	if err := agent.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	state := agent.State()
	if len(state.Messages) != 0 || state.StreamingMessage != nil || len(state.PendingToolCalls) != 0 || state.ErrorMessage != "" {
		t.Fatalf("reset runtime state = %#v", state)
	}
	if state.Phase != AgentPhaseIdle || state.IsStreaming {
		t.Fatalf("reset phase/isStreaming = %q/%v, want %q/false", state.Phase, state.IsStreaming, AgentPhaseIdle)
	}
	if agent.HasQueuedMessages() {
		t.Fatal("Reset() retained queued messages")
	}
	if state.SystemPrompt != "system" || state.Model != "model" || state.Provider != "provider" || state.Reasoning != "high" {
		t.Fatalf("Reset() changed configuration state: %#v", state)
	}
	if len(state.Tools) != 1 || state.Tools[0].Name != "lookup" {
		t.Fatalf("Reset() changed tools: %#v", state.Tools)
	}
}

func messageTexts(t *testing.T, messages protocol.MessageList) []string {
	t.Helper()
	texts := make([]string, len(messages))
	for i, message := range messages {
		texts[i] = agentMessageText(t, message)
	}
	return texts
}

func agentMessageText(t *testing.T, message protocol.AgentMessage) string {
	t.Helper()
	switch value := message.(type) {
	case protocol.UserMessage:
		if len(value.Content) == 0 {
			t.Fatal("user message content is empty")
		}
		return textContent(t, value.Content[0])
	case protocol.AssistantMessage:
		if len(value.Content) == 0 {
			t.Fatal("assistant message content is empty")
		}
		return textContent(t, value.Content[0])
	default:
		t.Fatalf("message = %T, want user or assistant", message)
		return ""
	}
}
