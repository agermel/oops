package agent

import (
	"context"

	agentevents "oops/internal/llm/events"

	"github.com/cloudwego/eino/schema"
)

func lifecycleEventToStepEvents(evt LifecycleEvent) []agentevents.StepEvent {
	switch evt.Type {
	case LifecycleMessageEnd:
		if evt.Message == nil || evt.Message.ToolCallID != "" || evt.Message.Content == "" || len(evt.Message.ToolCalls) == 0 {
			return nil
		}
		return []agentevents.StepEvent{{Type: "thinking", Content: evt.Message.Content}}
	case LifecycleToolCall:
		return []agentevents.StepEvent{{
			Type:       "tool_call",
			Content:    evt.Content,
			ToolName:   evt.ToolName,
			ToolArgs:   evt.ToolArgs,
			ToolCallID: evt.ToolCallID,
		}}
	case LifecycleToolResult:
		return []agentevents.StepEvent{{
			Type:       "tool_result",
			Content:    evt.Content,
			ToolName:   evt.ToolName,
			ToolCallID: evt.ToolCallID,
		}}
	case LifecycleAnswer:
		return []agentevents.StepEvent{{Type: "answer", Content: evt.Content}}
	case LifecycleError:
		return []agentevents.StepEvent{{Type: "error", Content: evt.Content}}
	default:
		return nil
	}
}

func afterToolCallBridge(ctx context.Context, msg *schema.Message) *schema.Message {
	if msg == nil || msg.ToolName == "" {
		return msg
	}
	hook, ok := lookupAfterToolCall(msg.ToolName)
	if !ok {
		return msg
	}
	return hook(ctx, msg)
}
