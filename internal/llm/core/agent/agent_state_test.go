package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"oops/internal/llm/ai/protocol"
)

func TestAgentStateSnapshotAndPrompt(t *testing.T) {
	agent := NewAgent(AgentOptions{
		Context: AgentContext{
			SystemPrompt: "system",
			Tools:        []protocol.ToolDefinition{{Name: "lookup"}},
		},
		Config: AgentLoopConfig{Model: "model", Provider: "provider", Stream: queueStreams(textStream("done"))},
	})

	state := agent.State()
	state.Tools[0].Name = "mutated"
	if got := agent.State().Tools[0].Name; got != "lookup" {
		t.Fatalf("tool name = %q, want lookup", got)
	}

	result, err := agent.Prompt(context.Background(), protocol.MessageList{userMessage("hello")})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	assertRoles(t, result, protocol.RoleUser, protocol.RoleAssistant)
	state = agent.State()
	if state.Phase != AgentPhaseIdle || state.IsStreaming {
		t.Fatalf("state phase/isStreaming = %s/%v", state.Phase, state.IsStreaming)
	}
	assertRoles(t, state.Messages, protocol.RoleUser, protocol.RoleAssistant)
}

func TestAgentBusyAndAgentEndListenerSettlement(t *testing.T) {
	started := make(chan struct{})
	releaseStream := make(chan struct{})
	listenerEntered := make(chan struct{})
	releaseListener := make(chan struct{})
	stream := func(context.Context, StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		close(started)
		<-releaseStream
		return textStream("done"), nil
	}
	agent := NewAgent(AgentOptions{Config: AgentLoopConfig{Stream: stream}})
	agent.Listen(func(_ context.Context, event protocol.AgentEvent, state AgentState) error {
		if event.Type == protocol.AgentEventAgentEnd {
			if state.Phase == AgentPhaseIdle {
				t.Fatal("agent_end listener saw idle phase")
			}
			close(listenerEntered)
			<-releaseListener
		}
		return nil
	})

	done := make(chan error, 1)
	go func() {
		_, err := agent.Prompt(context.Background(), protocol.MessageList{userMessage("hello")})
		done <- err
	}()
	<-started
	if _, err := agent.Prompt(context.Background(), protocol.MessageList{userMessage("second")}); !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("busy prompt error = %v", err)
	}
	close(releaseStream)
	<-listenerEntered
	if phase := agent.State().Phase; phase == AgentPhaseIdle {
		t.Fatalf("phase during agent_end listener = %s, want non-idle", phase)
	}
	close(releaseListener)
	if err := <-done; err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if phase := agent.State().Phase; phase != AgentPhaseIdle {
		t.Fatalf("phase after settlement = %s, want idle", phase)
	}
}

func TestAgentIdleSteeringContinueDrainsAssistantTail(t *testing.T) {
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{textStream("steered")}}
	agent := NewAgent(AgentOptions{
		Context: AgentContext{Messages: protocol.MessageList{assistantTextMessage("done")}},
		Config:  AgentLoopConfig{Stream: streams.stream},
	})
	if err := agent.Steer(protocol.MessageList{userMessage("steer")}); err != nil {
		t.Fatalf("Steer() error = %v", err)
	}
	if !agent.HasQueuedMessages() {
		t.Fatal("HasQueuedMessages() = false, want true")
	}
	result, err := agent.Continue(context.Background())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	assertRoles(t, result, protocol.RoleUser, protocol.RoleAssistant)
	if len(streams.requests) != 1 {
		t.Fatalf("stream requests = %d, want 1", len(streams.requests))
	}
	assertRoles(t, streams.requests[0].Context.Messages, protocol.RoleAssistant, protocol.RoleUser)
}

func TestAgentIdleFollowUpContinueDrainsAssistantTail(t *testing.T) {
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{textStream("followed")}}
	agent := NewAgent(AgentOptions{
		Context: AgentContext{Messages: protocol.MessageList{assistantTextMessage("done")}},
		Config:  AgentLoopConfig{Stream: streams.stream},
	})
	if err := agent.FollowUp(protocol.MessageList{userMessage("follow")}); err != nil {
		t.Fatalf("FollowUp() error = %v", err)
	}
	_, err := agent.Continue(context.Background())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	assertRoles(t, streams.requests[0].Context.Messages, protocol.RoleAssistant, protocol.RoleUser)
}

func TestAgentContinueGuards(t *testing.T) {
	agent := NewAgent(AgentOptions{Config: AgentLoopConfig{Stream: queueStreams(textStream("done"))}})
	if _, err := agent.Continue(context.Background()); !errors.Is(err, ErrContinueEmptyContext) {
		t.Fatalf("empty continue error = %v", err)
	}

	agent = NewAgent(AgentOptions{
		Context: AgentContext{Messages: protocol.MessageList{assistantTextMessage("done")}},
		Config:  AgentLoopConfig{Stream: queueStreams(textStream("done"))},
	})
	if _, err := agent.Continue(context.Background()); !errors.Is(err, ErrContinueFromAssistant) {
		t.Fatalf("assistant continue error = %v", err)
	}
}

func TestAgentQueueModesAndClears(t *testing.T) {
	streams := &requestRecorder{streams: []*protocol.AssistantMessageEventStream{textStream("done")}}
	agent := NewAgent(AgentOptions{
		Context:      AgentContext{Messages: protocol.MessageList{assistantTextMessage("done")}},
		Config:       AgentLoopConfig{Stream: streams.stream},
		SteeringMode: QueueModeAll,
	})
	if err := agent.Steer(protocol.MessageList{userMessage("one"), userMessage("two")}); err != nil {
		t.Fatalf("Steer() error = %v", err)
	}
	if _, err := agent.Continue(context.Background()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	assertRoles(t, streams.requests[0].Context.Messages, protocol.RoleAssistant, protocol.RoleUser, protocol.RoleUser)

	if err := agent.FollowUp(protocol.MessageList{userMessage("queued")}); err != nil {
		t.Fatalf("FollowUp() error = %v", err)
	}
	agent.ClearAllQueues()
	if agent.HasQueuedMessages() {
		t.Fatal("queue still has messages")
	}
}

func TestAgentAbortDuringStream(t *testing.T) {
	started := make(chan struct{})
	stream := func(context.Context, StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		close(started)
		return emptyOpenStream(), nil
	}
	agent := NewAgent(AgentOptions{Config: AgentLoopConfig{Stream: stream}})
	done := make(chan struct {
		result protocol.MessageList
		err    error
	}, 1)
	go func() {
		result, err := agent.Prompt(context.Background(), protocol.MessageList{userMessage("hello")})
		done <- struct {
			result protocol.MessageList
			err    error
		}{result: result, err: err}
	}()
	<-started
	if ok := agent.Abort(); !ok {
		t.Fatal("Abort() = false, want true")
	}
	if phase := agent.State().Phase; phase != AgentPhaseAborted {
		t.Fatalf("phase = %s, want aborted", phase)
	}
	select {
	case output := <-done:
		if output.err != nil {
			t.Fatalf("Prompt() error = %v", output.err)
		}
		if got := lastAssistant(t, output.result).StopReason; got != protocol.StopReasonAborted {
			t.Fatalf("stop reason = %q, want aborted", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt() did not settle")
	}
	if err := agent.WaitForIdle(context.Background()); err != nil {
		t.Fatalf("WaitForIdle() error = %v", err)
	}
	if phase := agent.State().Phase; phase != AgentPhaseIdle {
		t.Fatalf("phase after abort = %s, want idle", phase)
	}
}

func TestAgentPendingToolPhaseAndListenerErrorSettlement(t *testing.T) {
	call := toolCall("call_1", "lookup", `{}`)
	var phases []AgentPhase
	listenerErr := errors.New("listener failed")
	agent := NewAgent(AgentOptions{Config: AgentLoopConfig{
		Stream:     queueStreams(toolCallStream(call), textStream("done")),
		ToolRunner: toolRunnerFunc(successTool(true, "found")),
	}})
	agent.Listen(func(_ context.Context, event protocol.AgentEvent, state AgentState) error {
		if event.Type == protocol.AgentEventToolExecutionStart || event.Type == protocol.AgentEventToolExecutionEnd {
			phases = append(phases, state.Phase)
		}
		if event.Type == protocol.AgentEventAgentEnd {
			return listenerErr
		}
		return nil
	})
	_, err := agent.Prompt(context.Background(), protocol.MessageList{userMessage("hello")})
	if !errors.Is(err, listenerErr) {
		t.Fatalf("Prompt() error = %v, want listener error", err)
	}
	if !reflect.DeepEqual(phases, []AgentPhase{AgentPhaseExecutingTools, AgentPhaseStreaming}) {
		t.Fatalf("phases = %v", phases)
	}
	if phase := agent.State().Phase; phase != AgentPhaseIdle {
		t.Fatalf("phase after listener error = %s, want idle", phase)
	}
}
