package toolruntime

import (
	"context"

	"oops/internal/llm/ai/protocol"
)

type MissingRunner struct{}

var _ ToolRunner = MissingRunner{}

func (MissingRunner) ExecuteTools(ctx context.Context, req ToolRunRequest) (ToolRunResult, error) {
	messages := make([]protocol.ToolResultMessage, 0, len(req.ToolCalls))
	for _, call := range req.ToolCalls {
		call = protocol.CloneToolCallContent(call)
		result := protocol.ToolResult{
			Content: protocol.ContentList{protocol.NewTextContent("tool runner is not configured")},
		}
		if err := emitLifecycleEvent(ctx, req.Emit, protocol.AgentEvent{
			Type:       protocol.AgentEventToolExecutionStart,
			Turn:       req.Turn,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Args:       protocol.CloneToolCallContent(call).Arguments,
		}); err != nil {
			return ToolRunResult{}, err
		}
		if err := emitLifecycleEvent(ctx, req.Emit, protocol.AgentEvent{
			Type:       protocol.AgentEventToolExecutionEnd,
			Turn:       req.Turn,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Result:     &result,
			IsError:    true,
		}); err != nil {
			return ToolRunResult{}, err
		}
		message := protocol.ToolResultMessage{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    protocol.CloneContentList(result.Content),
			IsError:    true,
		}
		if err := emitLifecycleEvent(ctx, req.Emit, protocol.AgentEvent{Type: protocol.AgentEventMessageStart, Turn: req.Turn, Message: message}); err != nil {
			return ToolRunResult{}, err
		}
		if err := emitLifecycleEvent(ctx, req.Emit, protocol.AgentEvent{Type: protocol.AgentEventMessageEnd, Turn: req.Turn, Message: message}); err != nil {
			return ToolRunResult{}, err
		}
		messages = append(messages, message)
	}
	return ToolRunResult{Messages: messages}, nil
}

func emitLifecycleEvent(ctx context.Context, emit EventSink, event protocol.AgentEvent) error {
	if emit == nil {
		return nil
	}
	return emit(ctx, event)
}
