package agent

import (
	"context"

	"oops/internal/llm/ai/protocol"
)

func RunAgentLoop(ctx context.Context, prompts protocol.MessageList, agentContext AgentContext, config AgentLoopConfig, emit EventSink) (protocol.MessageList, error) {
	if config.Stream == nil {
		return nil, ErrMissingStream
	}

	current := cloneContext(agentContext)
	promptMessages := cloneMessages(prompts)
	current.Messages = append(current.Messages, promptMessages...)
	newMessages := cloneMessages(promptMessages)

	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventAgentStart}); err != nil {
		return nil, err
	}
	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventTurnStart, Turn: 1}); err != nil {
		return nil, err
	}
	for _, prompt := range promptMessages {
		if err := emitMessage(ctx, emit, protocol.AgentEventMessageStart, 1, prompt); err != nil {
			return nil, err
		}
		if err := emitMessage(ctx, emit, protocol.AgentEventMessageEnd, 1, prompt); err != nil {
			return nil, err
		}
	}

	return runLoop(ctx, current, config, emit, newMessages, 1)
}

func RunAgentLoopContinue(ctx context.Context, agentContext AgentContext, config AgentLoopConfig, emit EventSink) (protocol.MessageList, error) {
	if len(agentContext.Messages) == 0 {
		return nil, ErrContinueEmptyContext
	}
	if agentContext.Messages[len(agentContext.Messages)-1].MessageRole() == protocol.RoleAssistant {
		return nil, ErrContinueFromAssistant
	}
	if config.Stream == nil {
		return nil, ErrMissingStream
	}

	current := cloneContext(agentContext)
	newMessages := protocol.MessageList{}

	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventAgentStart}); err != nil {
		return nil, err
	}
	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventTurnStart, Turn: 1}); err != nil {
		return nil, err
	}

	return runLoop(ctx, current, config, emit, newMessages, 1)
}

func runLoop(ctx context.Context, current AgentContext, config AgentLoopConfig, emit EventSink, newMessages protocol.MessageList, turn int) (protocol.MessageList, error) {
	maxTurns := config.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	firstTurn := true
	hasMoreToolCalls := true
	var pendingMessages protocol.MessageList

	for {
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if firstTurn {
				firstTurn = false
			} else {
				turn++
				if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventTurnStart, Turn: turn}); err != nil {
					return nil, err
				}
			}

			if len(pendingMessages) > 0 {
				for _, message := range pendingMessages {
					if err := emitMessage(ctx, emit, protocol.AgentEventMessageStart, turn, message); err != nil {
						return nil, err
					}
					if err := emitMessage(ctx, emit, protocol.AgentEventMessageEnd, turn, message); err != nil {
						return nil, err
					}
					current.Messages = append(current.Messages, message)
					newMessages = append(newMessages, message)
				}
				pendingMessages = nil
			}

			if turn > maxTurns {
				return emitActiveTerminalError(ctx, emit, newMessages, turn, config, "max_turns_exceeded", protocol.StopReasonError)
			}

			message, err := streamAssistantResponse(ctx, current, config, emit, turn)
			if err != nil {
				return nil, err
			}
			current.Messages = append(current.Messages, message)
			newMessages = append(newMessages, message)

			toolResults := []protocol.ToolResultMessage{}
			hasMoreToolCalls = false
			if message.StopReason != protocol.StopReasonError && message.StopReason != protocol.StopReasonAborted {
				toolCalls := collectToolCalls(message)
				if len(toolCalls) > 0 {
					if err := ctx.Err(); err != nil {
						return emitActiveTerminalError(ctx, emit, newMessages, turn, config, err.Error(), protocol.StopReasonAborted)
					}
					toolBatch, err := executeToolCalls(ctx, current, config, emit, turn, message, toolCalls)
					if len(toolBatch.Messages) > 0 {
						toolResults = toolBatch.Messages
						for _, result := range toolResults {
							current.Messages = append(current.Messages, result)
							newMessages = append(newMessages, result)
						}
					}
					if err != nil {
						if ctx.Err() != nil {
							return emitActiveTerminalError(ctx, emit, newMessages, turn, config, ctx.Err().Error(), protocol.StopReasonAborted)
						}
						return nil, err
					}
					hasMoreToolCalls = !toolBatch.Terminate
				}
			}

			if err := emitEvent(ctx, emit, protocol.AgentEvent{
				Type:        protocol.AgentEventTurnEnd,
				Turn:        turn,
				Message:     message,
				ToolResults: toolResults,
			}); err != nil {
				return nil, err
			}

			if message.StopReason == protocol.StopReasonError || message.StopReason == protocol.StopReasonAborted {
				if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventAgentEnd, Messages: cloneMessages(newMessages)}); err != nil {
					return nil, err
				}
				return cloneMessages(newMessages), nil
			}

			turnCtx := TurnContext{
				Message:     message,
				ToolResults: cloneToolResultMessages(toolResults),
				Context:     cloneContext(current),
				NewMessages: cloneMessages(newMessages),
				Turn:        turn,
			}
			if config.PrepareNextTurn != nil {
				update, err := config.PrepareNextTurn(ctx, turnCtx)
				if err != nil {
					return emitPostTurnHookError(ctx, emit, newMessages, turn, config, err.Error())
				}
				applyTurnUpdate(&current, &config, update)
				turnCtx.Context = cloneContext(current)
			}
			if config.ShouldStopAfterTurn != nil {
				stop, err := config.ShouldStopAfterTurn(ctx, turnCtx)
				if err != nil {
					return emitPostTurnHookError(ctx, emit, newMessages, turn, config, err.Error())
				}
				if stop {
					if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventAgentEnd, Messages: cloneMessages(newMessages)}); err != nil {
						return nil, err
					}
					return cloneMessages(newMessages), nil
				}
			}
			if config.GetSteeringMessages != nil {
				messages, err := config.GetSteeringMessages(ctx)
				if err != nil {
					return emitPostTurnHookError(ctx, emit, newMessages, turn, config, err.Error())
				}
				pendingMessages = cloneMessages(messages)
			}
		}

		if config.GetFollowUpMessages != nil {
			messages, err := config.GetFollowUpMessages(ctx)
			if err != nil {
				return emitPostTurnHookError(ctx, emit, newMessages, turn, config, err.Error())
			}
			if len(messages) > 0 {
				pendingMessages = cloneMessages(messages)
				continue
			}
		}
		break
	}

	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventAgentEnd, Messages: cloneMessages(newMessages)}); err != nil {
		return nil, err
	}
	return cloneMessages(newMessages), nil
}

func applyTurnUpdate(current *AgentContext, config *AgentLoopConfig, update TurnUpdate) {
	if update.Context != nil {
		*current = cloneContext(*update.Context)
	}
	if update.Model != "" {
		config.Model = update.Model
	}
	if update.Provider != "" {
		config.Provider = update.Provider
	}
	if update.Reasoning != "" {
		config.Reasoning = update.Reasoning
	}
}

func emitEvent(ctx context.Context, emit EventSink, event protocol.AgentEvent) error {
	if emit == nil {
		return nil
	}
	return emit(ctx, event)
}

func emitMessage(ctx context.Context, emit EventSink, eventType protocol.AgentEventType, turn int, message protocol.AgentMessage) error {
	return emitEvent(ctx, emit, protocol.AgentEvent{Type: eventType, Turn: turn, Message: message})
}

func cloneContext(value AgentContext) AgentContext {
	return AgentContext{
		SystemPrompt: value.SystemPrompt,
		Messages:     cloneMessages(value.Messages),
		Tools:        cloneTools(value.Tools),
	}
}

func cloneMessages(messages protocol.MessageList) protocol.MessageList {
	return protocol.CloneMessageList(messages)
}

func cloneTools(tools []protocol.ToolDefinition) []protocol.ToolDefinition {
	return protocol.CloneTools(tools)
}

func cloneToolResultMessages(messages []protocol.ToolResultMessage) []protocol.ToolResultMessage {
	return protocol.CloneToolResultMessages(messages)
}
