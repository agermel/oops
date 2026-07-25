package core

import (
	"context"

	protocol "oops/internal/agent/ai"
)

func executeToolCalls(
	ctx context.Context,
	agentContext AgentContext,
	config AgentLoopConfig,
	emit EventSink,
	turn int,
	message protocol.AssistantMessage,
	calls []protocol.ToolCallContent,
) (ToolRunResult, error) {
	if config.ToolRunner == nil {
		return MissingRunner{}.ExecuteTools(ctx, ToolRunRequest{
			Turn:      turn,
			ToolCalls: cloneToolCalls(calls),
			Emit:      EventSink(emit),
		})
	}
	return config.ToolRunner.ExecuteTools(ctx, ToolRunRequest{
		Turn:             turn,
		Context:          toolContextSnapshot(agentContext),
		AssistantMessage: protocol.CloneAssistantMessage(message),
		ToolCalls:        cloneToolCalls(calls),
		Emit:             EventSink(emit),
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

func toolContextSnapshot(value AgentContext) ContextSnapshot {
	return ContextSnapshot{
		SystemPrompt: value.SystemPrompt,
		Messages:     cloneMessages(value.Messages),
		Tools:        cloneTools(value.Tools),
	}
}
