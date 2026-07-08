package agent

import (
	"context"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/core/toolruntime"
)

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

func executeToolCalls(
	ctx context.Context,
	agentContext AgentContext,
	config AgentLoopConfig,
	emit EventSink,
	turn int,
	message protocol.AssistantMessage,
	calls []protocol.ToolCallContent,
) (toolruntime.ToolRunResult, error) {
	if config.ToolRunner == nil {
		return toolruntime.MissingRunner{}.ExecuteTools(ctx, toolruntime.ToolRunRequest{
			Turn:      turn,
			ToolCalls: cloneToolCalls(calls),
			Emit:      toolruntime.EventSink(emit),
		})
	}
	return config.ToolRunner.ExecuteTools(ctx, toolruntime.ToolRunRequest{
		Turn:             turn,
		Context:          toolruntimeContext(agentContext),
		AssistantMessage: protocol.CloneAssistantMessage(message),
		ToolCalls:        cloneToolCalls(calls),
		Emit:             toolruntime.EventSink(emit),
	})
}

func cloneToolCalls(calls []protocol.ToolCallContent) []protocol.ToolCallContent {
	if len(calls) == 0 {
		return nil
	}
	out := make([]protocol.ToolCallContent, len(calls))
	for i, call := range calls {
		out[i] = protocol.CloneToolCallContent(call)
	}
	return out
}

func toolruntimeContext(value AgentContext) toolruntime.ContextSnapshot {
	return toolruntime.ContextSnapshot{
		SystemPrompt: value.SystemPrompt,
		Messages:     cloneMessages(value.Messages),
		Tools:        cloneTools(value.Tools),
	}
}
