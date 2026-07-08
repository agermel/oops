package agent

import (
	"context"
	"errors"

	"oops/internal/llm/ai/protocol"
)

type toolUpdateEmitError struct {
	err error
}

func (e toolUpdateEmitError) Error() string {
	return e.err.Error()
}

func (e toolUpdateEmitError) Unwrap() error {
	return e.err
}

type executedToolBatch struct {
	messages  []protocol.ToolResultMessage
	terminate bool
}

func collectToolCalls(message protocol.AssistantMessage) []protocol.ToolCallContent {
	var calls []protocol.ToolCallContent
	for _, content := range message.Content {
		switch value := content.(type) {
		case protocol.ToolCallContent:
			calls = append(calls, value)
		case *protocol.ToolCallContent:
			if value != nil {
				calls = append(calls, *value)
			}
		}
	}
	return calls
}

func executeToolCalls(ctx context.Context, config AgentLoopConfig, emit EventSink, turn int, calls []protocol.ToolCallContent) (executedToolBatch, error) {
	messages := make([]protocol.ToolResultMessage, 0, len(calls))
	terminateCount := 0

	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return executedToolBatch{messages: messages}, err
		}
		call = cloneToolCall(call)
		args := cloneRawMessage(call.Arguments)
		if err := emitEvent(ctx, emit, protocol.AgentEvent{
			Type:       protocol.AgentEventToolExecutionStart,
			Turn:       turn,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Args:       args,
		}); err != nil {
			return executedToolBatch{}, err
		}

		if err := ctx.Err(); err != nil {
			return executedToolBatch{messages: messages}, err
		}
		result, isError, err := runTool(ctx, config, emit, turn, call)
		if err != nil {
			var updateErr toolUpdateEmitError
			if errors.As(err, &updateErr) {
				return executedToolBatch{}, updateErr.err
			}
			if ctx.Err() != nil {
				return executedToolBatch{messages: messages}, ctx.Err()
			}
			result = protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent(err.Error())}}
			isError = true
		}
		if ctx.Err() != nil {
			return executedToolBatch{messages: messages}, ctx.Err()
		}
		if result.Terminate {
			terminateCount++
		}

		if err := emitEvent(ctx, emit, protocol.AgentEvent{
			Type:       protocol.AgentEventToolExecutionEnd,
			Turn:       turn,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Result:     &result,
			IsError:    isError,
		}); err != nil {
			return executedToolBatch{}, err
		}

		message := protocol.ToolResultMessage{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result.Content,
			Details:    result.Details,
			IsError:    isError,
		}
		if err := emitMessage(ctx, emit, protocol.AgentEventMessageStart, turn, message); err != nil {
			return executedToolBatch{}, err
		}
		if err := emitMessage(ctx, emit, protocol.AgentEventMessageEnd, turn, message); err != nil {
			return executedToolBatch{}, err
		}
		messages = append(messages, message)
	}

	return executedToolBatch{
		messages:  messages,
		terminate: len(calls) > 0 && terminateCount == len(calls),
	}, nil
}

func runTool(ctx context.Context, config AgentLoopConfig, emit EventSink, turn int, call protocol.ToolCallContent) (protocol.ToolResult, bool, error) {
	if config.ToolRunner == nil {
		return protocol.ToolResult{
			Content: protocol.ContentList{protocol.NewTextContent("tool runner is not configured")},
		}, true, nil
	}

	updateSink := func(updateCtx context.Context, event protocol.AgentEvent) error {
		event.Type = protocol.AgentEventToolExecutionUpdate
		event.Turn = turn
		if event.ToolCallID == "" {
			event.ToolCallID = call.ID
		}
		if event.ToolName == "" {
			event.ToolName = call.Name
		}
		if err := emitEvent(updateCtx, emit, event); err != nil {
			return toolUpdateEmitError{err: err}
		}
		return nil
	}
	return config.ToolRunner.ExecuteTool(ctx, call, updateSink)
}

func cloneToolCall(call protocol.ToolCallContent) protocol.ToolCallContent {
	return protocol.CloneToolCallContent(call)
}

func cloneRawMessage(raw []byte) []byte {
	return protocol.CloneToolCallContent(protocol.ToolCallContent{Arguments: raw}).Arguments
}
