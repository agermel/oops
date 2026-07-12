package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

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
