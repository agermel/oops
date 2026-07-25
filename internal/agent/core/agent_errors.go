package core

import (
	"context"

	protocol "oops/internal/agent/ai"
)

func newAssistantError(provider, model string, reason protocol.StopReason, message string) protocol.AssistantMessage {
	return protocol.AssistantMessage{
		Content:      protocol.ContentList{},
		Provider:     provider,
		Model:        model,
		StopReason:   reason,
		ErrorMessage: message,
	}
}

func normalizeAssistantErrorMessage(message protocol.AssistantMessage, reason protocol.StopReason) protocol.AssistantMessage {
	message.StopReason = reason
	return message
}

func emitAssistantError(ctx context.Context, emit EventSink, turn int, config AgentLoopConfig, message string, reason protocol.StopReason) (protocol.AssistantMessage, error) {
	return emitAssistantErrorTerminal(ctx, emit, turn, config, message, reason, false)
}

func emitAssistantErrorTerminal(ctx context.Context, emit EventSink, turn int, config AgentLoopConfig, message string, reason protocol.StopReason, started bool) (protocol.AssistantMessage, error) {
	assistantMessage := newAssistantError(config.Provider, config.Model, reason, message)
	if !started {
		if err := emitMessage(ctx, emit, protocol.AgentEventMessageStart, turn, assistantMessage); err != nil {
			return protocol.AssistantMessage{}, err
		}
	}
	if err := emitMessage(ctx, emit, protocol.AgentEventMessageEnd, turn, assistantMessage); err != nil {
		return protocol.AssistantMessage{}, err
	}
	return assistantMessage, nil
}

func emitActiveTerminalError(ctx context.Context, emit EventSink, newMessages protocol.MessageList, turn int, config AgentLoopConfig, message string, reason protocol.StopReason) (protocol.MessageList, error) {
	assistantMessage, err := emitAssistantError(ctx, emit, turn, config, message, reason)
	if err != nil {
		return nil, err
	}
	newMessages = append(newMessages, assistantMessage)
	if err := emitEvent(ctx, emit, protocol.AgentEvent{
		Type:    protocol.AgentEventTurnEnd,
		Turn:    turn,
		Message: assistantMessage,
	}); err != nil {
		return nil, err
	}
	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventAgentEnd, Messages: cloneMessages(newMessages)}); err != nil {
		return nil, err
	}
	return cloneMessages(newMessages), nil
}

func emitPostTurnHookError(ctx context.Context, emit EventSink, newMessages protocol.MessageList, previousTurn int, config AgentLoopConfig, message string) (protocol.MessageList, error) {
	turn := previousTurn + 1
	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventTurnStart, Turn: turn}); err != nil {
		return nil, err
	}
	return emitActiveTerminalError(ctx, emit, newMessages, turn, config, message, protocol.StopReasonError)
}
