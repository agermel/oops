package agent

import (
	"slices"

	"oops/internal/llm/ai/protocol"
)

type AgentPhase string

const (
	AgentPhaseIdle           AgentPhase = "Idle"
	AgentPhaseStreaming      AgentPhase = "Streaming"
	AgentPhaseExecutingTools AgentPhase = "ExecutingTools"
)

type AgentState struct {
	SystemPrompt     string
	Model            string
	Provider         string
	Reasoning        string
	Tools            []protocol.ToolDefinition
	Messages         protocol.MessageList
	IsStreaming      bool
	Phase            AgentPhase
	StreamingMessage protocol.AgentMessage
	PendingToolCalls []string
	ErrorMessage     string
}

func newAgentState(context AgentContext, config AgentLoopConfig) AgentState {
	return AgentState{
		SystemPrompt: context.SystemPrompt,
		Model:        config.Model,
		Provider:     config.Provider,
		Reasoning:    config.Reasoning,
		Tools:        cloneTools(context.Tools),
		Messages:     cloneMessages(context.Messages),
		Phase:        AgentPhaseIdle,
	}
}

func cloneAgentState(state AgentState) AgentState {
	state.Tools = cloneTools(state.Tools)
	state.Messages = cloneMessages(state.Messages)
	state.StreamingMessage = protocol.CloneMessage(state.StreamingMessage)
	if len(state.PendingToolCalls) > 0 {
		state.PendingToolCalls = slices.Clone(state.PendingToolCalls)
	}
	return state
}

func (a *Agent) reduceAgentEventLocked(event protocol.AgentEvent) {
	switch event.Type {
	case protocol.AgentEventAgentStart:
		a.state.IsStreaming = true
		a.state.Phase = AgentPhaseStreaming
		a.state.ErrorMessage = ""
	case protocol.AgentEventMessageStart, protocol.AgentEventMessageUpdate:
		a.state.StreamingMessage = protocol.CloneMessage(event.Message)
		a.state.IsStreaming = true
		a.state.Phase = AgentPhaseStreaming
	case protocol.AgentEventMessageEnd:
		if event.Message != nil {
			a.state.Messages = append(a.state.Messages, protocol.CloneMessage(event.Message))
		}
		a.state.StreamingMessage = nil
	case protocol.AgentEventToolExecutionStart:
		a.addPendingToolCallLocked(event.ToolCallID)
		a.state.IsStreaming = true
		a.state.Phase = AgentPhaseExecutingTools
	case protocol.AgentEventToolExecutionEnd:
		a.removePendingToolCallLocked(event.ToolCallID)
		if len(a.state.PendingToolCalls) > 0 {
			a.state.Phase = AgentPhaseExecutingTools
		} else {
			a.state.Phase = AgentPhaseStreaming
		}
	case protocol.AgentEventTurnEnd:
		if message, ok := event.Message.(protocol.AssistantMessage); ok && message.ErrorMessage != "" {
			a.state.ErrorMessage = message.ErrorMessage
		}
		if message, ok := event.Message.(*protocol.AssistantMessage); ok && message != nil && message.ErrorMessage != "" {
			a.state.ErrorMessage = message.ErrorMessage
		}
	case protocol.AgentEventAgentEnd:
		a.state.StreamingMessage = nil
		a.state.PendingToolCalls = nil
		if a.active != nil {
			a.active.observedAgentEnd = true
		}
	}
}

func (a *Agent) addPendingToolCallLocked(id string) {
	if id == "" {
		return
	}
	for _, existing := range a.state.PendingToolCalls {
		if existing == id {
			return
		}
	}
	a.state.PendingToolCalls = append(a.state.PendingToolCalls, id)
}

func (a *Agent) removePendingToolCallLocked(id string) {
	if id == "" || len(a.state.PendingToolCalls) == 0 {
		return
	}
	pending := a.state.PendingToolCalls[:0]
	for _, existing := range a.state.PendingToolCalls {
		if existing != id {
			pending = append(pending, existing)
		}
	}
	a.state.PendingToolCalls = pending
}
