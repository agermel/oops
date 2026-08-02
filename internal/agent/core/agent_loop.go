package core

import (
	"context"

	protocol "oops/internal/agent/ai"
)

// Agent 核心主循环
// MessageList（prompts 本次新加入的用户信息）,agentContext(调用前已有的完整历史记录)，eventSink 把 Core 循环产生的事件传给 Agent Runtime、Session 和 SSE 层
func RunAgentLoop(ctx context.Context, prompts protocol.MessageList, agentContext AgentContext, config AgentLoopConfig, emit EventSink) (protocol.MessageList, error) {
	if config.Stream == nil {
		return nil, ErrMissingStream
	}

	current := cloneContext(agentContext)
	promptMessages := cloneMessages(prompts)
	current.Messages = append(current.Messages, promptMessages...)
	newMessages := cloneMessages(promptMessages)

	// 把本轮输入正式写入 Agent 事件流
	// AgentEventAgentStart 事件开始，发送 agent_start 生命周期事件RunAgentLoop
	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventAgentStart}); err != nil {
		return nil, err
	}
	// TurnStart 标记本轮推理开始
	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventTurnStart, Turn: 1}); err != nil {
		return nil, err
	}
	// 每条新输入依次发送 MessageStart 与 MessageEnd，表示该消息已经完整进入会话
	// 这些事件经由 EventSink 更新 Core 的消息与流状态，并被 Runtime 监听，用于 Session 持久化、SSE 推送和界面展示。模型调用发生在这段之后
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
	last := agentContext.Messages[len(agentContext.Messages)-1]
	if last == nil {
		return nil, ErrContinueEmptyContext
	}
	if last.MessageRole() == protocol.RoleAssistant {
		return nil, ErrContinueFromAssistant
	}
	if config.Stream == nil {
		return nil, ErrMissingStream
	}

	current := cloneContext(agentContext)
	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventAgentStart}); err != nil {
		return nil, err
	}
	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventTurnStart, Turn: 1}); err != nil {
		return nil, err
	}
	return runLoop(ctx, current, config, emit, nil, 1)
}

// Core 的单次 Agent 执行的引擎
// getQueuedMessages 读取当前排队的 steering 消息
func runLoop(ctx context.Context, current AgentContext, config AgentLoopConfig, emit EventSink, newMessages protocol.MessageList, turn int) (protocol.MessageList, error) {
	maxTurns := config.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	firstTurn := true
	pendingMessages, err := getQueuedMessages(ctx, config.GetSteeringMessages)
	if err != nil {
		return nil, err
	}

	// 外层循环
	// 每次内层执行完毕后，它会检查是否有 follow-up 消息，有就再开一轮执行，没有就结束整个 Agent
	for {
		// 保证至少执行一次
		hasMoreToolCalls := true
		// 内层循环
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if firstTurn {
				firstTurn = false
			} else {
				turn++
				// 非第一次 TurnStart 状态
				if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventTurnStart, Turn: turn}); err != nil {
					return nil, err
				}
			}

			// 超过最大次数了 error
			if turn > maxTurns {
				return emitActiveTerminalError(ctx, emit, newMessages, turn, config, "max_turns_exceeded", protocol.StopReasonError)
			}

			for _, pending := range pendingMessages {
				// 开始创建消息
				if err := emitMessage(ctx, emit, protocol.AgentEventMessageStart, turn, pending); err != nil {
					return nil, err
				}
				// 消息创建结束
				if err := emitMessage(ctx, emit, protocol.AgentEventMessageEnd, turn, pending); err != nil {
					return nil, err
				}
				current.Messages = append(current.Messages, pending)
				newMessages = append(newMessages, pending)
			}
			pendingMessages = nil
			// 请求模型
			message, err := streamAssistantResponse(ctx, current, config, emit, turn)
			if err != nil {
				return nil, err
			}
			current.Messages = append(current.Messages, message)
			newMessages = append(newMessages, message)

			toolResults := []protocol.ToolResultMessage{}
			hasMoreToolCalls = false
			if message.StopReason != protocol.StopReasonError && message.StopReason != protocol.StopReasonAborted {
				toolCalls := protocol.ToolCallsFromAssistant(message)
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
			pendingMessages, err = getQueuedMessages(ctx, config.GetSteeringMessages)
			if err != nil {
				return emitPostTurnHookError(ctx, emit, newMessages, turn, config, err.Error())
			}
		}

		pendingMessages, err = getQueuedMessages(ctx, config.GetFollowUpMessages)
		if err != nil {
			return emitPostTurnHookError(ctx, emit, newMessages, turn, config, err.Error())
		}
		if len(pendingMessages) == 0 {
			break
		}
	}

	if err := emitEvent(ctx, emit, protocol.AgentEvent{Type: protocol.AgentEventAgentEnd, Messages: cloneMessages(newMessages)}); err != nil {
		return nil, err
	}
	return cloneMessages(newMessages), nil
}

func getQueuedMessages(ctx context.Context, get func(context.Context) (protocol.MessageList, error)) (protocol.MessageList, error) {
	if get == nil {
		return nil, nil
	}
	messages, err := get(ctx)
	if err != nil {
		return nil, err
	}
	return cloneMessages(messages), nil
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
	if update.ToolRunner != nil {
		config.ToolRunner = update.ToolRunner
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
