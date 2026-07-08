package agent

import (
	"context"
	"encoding/json"
	"strings"

	"oops/internal/llm/ai/protocol"
	protoeino "oops/internal/llm/ai/protocol/einoadapter"
	"oops/internal/llm/core/toolruntime"

	"github.com/cloudwego/eino/schema"
)

func (r *Runner) emitProtocolEvent(ctx context.Context, events chan<- LifecycleEvent, event protocol.AgentEvent, hooks RunHooks) error {
	switch event.Type {
	case protocol.AgentEventAgentStart:
		sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleAgentStart})
	case protocol.AgentEventAgentEnd:
		sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleAgentEnd})
	case protocol.AgentEventMessageEnd:
		return r.emitMessageEnd(ctx, events, event.Message, hooks)
	}
	return nil
}

func (r *Runner) emitMessageEnd(ctx context.Context, events chan<- LifecycleEvent, message protocol.AgentMessage, hooks RunHooks) error {
	if message == nil || message.MessageRole() == protocol.RoleUser {
		return nil
	}
	if assistant, ok := asAssistantMessage(message); ok {
		if assistant.StopReason == protocol.StopReasonError || assistant.StopReason == protocol.StopReasonAborted {
			sendLifecycle(ctx, events, LifecycleEvent{
				Type:    LifecycleError,
				Content: sanitizeError(assistantErrorText(assistant)),
			})
			return nil
		}
		msg, err := protoeino.ToEinoMessage(assistant)
		if err != nil {
			return err
		}
		if hooks.OnMessage != nil {
			if err := hooks.OnMessage(ctx, msg); err != nil {
				return err
			}
		}
		if !r.emitMessageLifecycle(ctx, events, msg) {
			return nil
		}
		if len(msg.ToolCalls) == 0 {
			sendLifecycle(ctx, events, LifecycleEvent{
				Type:    LifecycleAnswer,
				Message: msg,
				Content: msg.Content,
			})
		}
		return nil
	}
	if toolResult, ok := asToolResultMessage(message); ok {
		msg, err := protoeino.ToEinoMessage(toolResult)
		if err != nil {
			return err
		}
		displayMsg := applyAfterToolResult(ctx, msg, hooks.AfterToolResult)
		persistMsg := displayMsg
		if persistMsg == nil {
			persistMsg = msg
		}
		if hooks.OnMessage != nil {
			if err := hooks.OnMessage(ctx, persistMsg); err != nil {
				return err
			}
		}
		if displayMsg != nil {
			if !r.emitMessageLifecycle(ctx, events, displayMsg) {
				return nil
			}
		}
		if toolResult.IsError {
			sendLifecycle(ctx, events, LifecycleEvent{
				Type:    LifecycleError,
				Content: sanitizeError(textFromContent(toolResult.Content)),
			})
		}
		return nil
	}
	return nil
}

func (r *Runner) emitMessageLifecycle(ctx context.Context, events chan<- LifecycleEvent, msg *schema.Message) bool {
	if !sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleMessageEnd, Message: msg}) {
		return false
	}
	if msg == nil {
		return true
	}
	if msg.ToolCallID != "" {
		return sendLifecycle(ctx, events, LifecycleEvent{
			Type:       LifecycleToolResult,
			Message:    msg,
			Content:    msg.Content,
			ToolName:   msg.ToolName,
			ToolCallID: msg.ToolCallID,
		})
	}
	for _, tc := range msg.ToolCalls {
		if !sendLifecycle(ctx, events, LifecycleEvent{
			Type:       LifecycleToolCall,
			Message:    msg,
			Content:    tc.Function.Name,
			ToolName:   tc.Function.Name,
			ToolArgs:   tc.Function.Arguments,
			ToolCallID: tc.ID,
		}) {
			return false
		}
	}
	return true
}

func applyAfterToolResult(ctx context.Context, msg *schema.Message, hook ToolResultHook) *schema.Message {
	if msg == nil || hook == nil || msg.Role != schema.Tool || msg.ToolName == "" {
		return msg
	}
	copied := *msg
	return hook(ctx, &copied)
}

func bridgeBeforeToolCall(hook BeforeToolCallHook) toolruntime.BeforeToolCallHook {
	if hook == nil {
		return nil
	}
	return func(ctx context.Context, input toolruntime.BeforeToolCallContext) (toolruntime.BeforeToolCallResult, error) {
		rewritten, err := hook(ctx, ToolCallInput{
			Name:      input.ToolCall.Name,
			Arguments: string(input.RawArguments),
			CallID:    input.ToolCall.ID,
		})
		if err != nil {
			return toolruntime.BeforeToolCallResult{}, err
		}
		if rewritten == "" {
			rewritten = "{}"
		}
		return toolruntime.BeforeToolCallResult{Arguments: json.RawMessage(rewritten)}, nil
	}
}

func bridgeAfterToolCall(hook SemanticToolResultHook) toolruntime.AfterToolCallHook {
	if hook == nil {
		return nil
	}
	return func(ctx context.Context, input toolruntime.AfterToolCallContext) (toolruntime.AfterToolCallResult, error) {
		result, err := hook(ctx, ToolCallInput{
			Name:      input.ToolCall.Name,
			Arguments: string(input.RawArguments),
			CallID:    input.ToolCall.ID,
		}, ToolCallOutput{Result: textFromContent(input.Result.Content)})
		if err != nil {
			return toolruntime.AfterToolCallResult{}, err
		}
		return toolruntime.AfterToolCallResult{
			Content:    protocol.ContentList{protocol.NewTextContent(result)},
			HasContent: true,
		}, nil
	}
}

func assistantErrorText(message protocol.AssistantMessage) string {
	if message.ErrorMessage != "" {
		return message.ErrorMessage
	}
	return textFromContent(message.Content)
}

func textFromContent(content protocol.ContentList) string {
	var out strings.Builder
	for _, item := range content {
		switch value := item.(type) {
		case protocol.TextContent:
			out.WriteString(value.Text)
		case *protocol.TextContent:
			if value != nil {
				out.WriteString(value.Text)
			}
		case protocol.ThinkingContent:
			out.WriteString(value.Thinking)
		case *protocol.ThinkingContent:
			if value != nil {
				out.WriteString(value.Thinking)
			}
		}
	}
	return out.String()
}

func asAssistantMessage(message protocol.AgentMessage) (protocol.AssistantMessage, bool) {
	switch value := message.(type) {
	case protocol.AssistantMessage:
		return value, true
	case *protocol.AssistantMessage:
		if value != nil {
			return *value, true
		}
	}
	return protocol.AssistantMessage{}, false
}

func asToolResultMessage(message protocol.AgentMessage) (protocol.ToolResultMessage, bool) {
	switch value := message.(type) {
	case protocol.ToolResultMessage:
		return value, true
	case *protocol.ToolResultMessage:
		if value != nil {
			return *value, true
		}
	}
	return protocol.ToolResultMessage{}, false
}
