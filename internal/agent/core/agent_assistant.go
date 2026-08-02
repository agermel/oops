package core

import (
	"context"

	protocol "oops/internal/agent/ai"
)

// 拿到当前会话 agentContext 和本轮配置 config
// 调用 config.Stream 请求模型
func streamAssistantResponse(ctx context.Context, agentContext AgentContext, config AgentLoopConfig, emit EventSink, turn int) (protocol.AssistantMessage, error) {
	if err := ctx.Err(); err != nil {
		return emitAssistantError(ctx, emit, turn, config, err.Error(), protocol.StopReasonAborted)
	}
	messages := cloneMessages(agentContext.Messages)
	if config.TransformContext != nil {
		transformed, err := config.TransformContext(ctx, messages)
		if err != nil {
			return emitAssistantError(ctx, emit, turn, config, err.Error(), protocol.StopReasonError)
		}
		messages = cloneMessages(transformed)
	}
	if config.ConvertToLLM != nil {
		converted, err := config.ConvertToLLM(ctx, messages)
		if err != nil {
			return emitAssistantError(ctx, emit, turn, config, err.Error(), protocol.StopReasonError)
		}
		messages = cloneMessages(converted)
	}

	stream, err := config.Stream(ctx, StreamRequest{
		Context: protocol.Context{
			SystemPrompt: agentContext.SystemPrompt,
			Messages:     messages,
			Tools:        cloneTools(agentContext.Tools),
		},
		Model:     config.Model,
		Provider:  config.Provider,
		Reasoning: config.Reasoning,
		SessionID: config.SessionID,
	})
	if err != nil {
		return emitAssistantError(ctx, emit, turn, config, err.Error(), protocol.StopReasonError)
	}
	if stream == nil {
		return emitAssistantError(ctx, emit, turn, config, "stream returned nil", protocol.StopReasonError)
	}

	started := false
	for {
		select {
		case <-ctx.Done():
			return emitAssistantErrorTerminal(ctx, emit, turn, config, ctx.Err().Error(), protocol.StopReasonAborted, started)
		case event, ok := <-stream.Events():
			if !ok {
				message, err := stream.Result(ctx)
				if err != nil {
					reason := protocol.StopReasonError
					if ctx.Err() != nil {
						reason = protocol.StopReasonAborted
					}
					return emitAssistantErrorTerminal(ctx, emit, turn, config, err.Error(), reason, started)
				}
				if message == nil {
					return emitAssistantErrorTerminal(ctx, emit, turn, config, "stream ended without message", protocol.StopReasonError, started)
				}
				return *message, nil
			}

			if err := event.Validate(); err != nil {
				return emitAssistantErrorTerminal(ctx, emit, turn, config, err.Error(), protocol.StopReasonError, started)
			}
			switch event.Type {
			case protocol.AssistantEventStart:
				if err := emitMessage(ctx, emit, protocol.AgentEventMessageStart, turn, *event.Partial); err != nil {
					return protocol.AssistantMessage{}, err
				}
				started = true
			case protocol.AssistantEventDone:
				if !started {
					if err := emitMessage(ctx, emit, protocol.AgentEventMessageStart, turn, *event.Message); err != nil {
						return protocol.AssistantMessage{}, err
					}
					started = true
				}
				if err := emitMessage(ctx, emit, protocol.AgentEventMessageEnd, turn, *event.Message); err != nil {
					return protocol.AssistantMessage{}, err
				}
				return *event.Message, nil
			case protocol.AssistantEventError:
				message := normalizeAssistantErrorMessage(*event.Error, event.Reason)
				if !started {
					if err := emitMessage(ctx, emit, protocol.AgentEventMessageStart, turn, message); err != nil {
						return protocol.AssistantMessage{}, err
					}
					started = true
				}
				if err := emitMessage(ctx, emit, protocol.AgentEventMessageEnd, turn, message); err != nil {
					return protocol.AssistantMessage{}, err
				}
				return message, nil
			default:
				if err := emitAssistantUpdate(ctx, emit, turn, event); err != nil {
					return protocol.AssistantMessage{}, err
				}
			}
		}
	}
}

func emitAssistantUpdate(ctx context.Context, emit EventSink, turn int, event protocol.AssistantMessageEvent) error {
	if event.Partial == nil {
		return nil
	}
	return emitEvent(ctx, emit, protocol.AgentEvent{
		Type:                  protocol.AgentEventMessageUpdate,
		Turn:                  turn,
		Message:               *event.Partial,
		AssistantMessageEvent: &event,
		Delta:                 event.Delta,
	})
}
