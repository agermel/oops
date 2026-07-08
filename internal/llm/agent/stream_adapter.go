package agent

import (
	"context"
	"errors"
	"io"

	"oops/internal/llm/ai/protocol"
	protoeino "oops/internal/llm/ai/protocol/einoadapter"
	coreagent "oops/internal/llm/core/agent"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func newEinoModelStreamFn(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.InvokableTool) (coreagent.StreamFn, error) {
	if chatModel == nil {
		return nil, coreagent.ErrMissingStream
	}
	toolInfos, err := toolInfosForModel(ctx, tools)
	if err != nil {
		return nil, err
	}
	return func(runCtx context.Context, req coreagent.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		messages, err := einoMessagesFromContext(req.Context)
		if err != nil {
			return nil, err
		}
		modelForRun := chatModel
		if len(toolInfos) > 0 {
			bound, err := chatModel.WithTools(cloneToolInfos(toolInfos))
			if err != nil {
				return nil, err
			}
			modelForRun = bound
		}
		reader, err := modelForRun.Stream(runCtx, messages)
		if err != nil {
			return nil, err
		}
		stream := protocol.NewAssistantMessageEventStream(16)
		go drainEinoMessageStream(runCtx, reader, stream)
		return stream, nil
	}, nil
}

func einoMessagesFromContext(ctx protocol.Context) ([]*schema.Message, error) {
	messages := make([]*schema.Message, 0, len(ctx.Messages)+1)
	if ctx.SystemPrompt != "" {
		messages = append(messages, schema.SystemMessage(ctx.SystemPrompt))
	}
	converted, err := protoeino.ToEinoMessages(ctx.Messages)
	if err != nil {
		return nil, err
	}
	messages = append(messages, converted...)
	return messages, nil
}

func drainEinoMessageStream(ctx context.Context, reader *schema.StreamReader[*schema.Message], out *protocol.AssistantMessageEventStream) {
	defer reader.Close()
	started := false
	var chunks []*schema.Message
	var text, thinking string
	for {
		msg, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			pushAssistantError(out, err, protocol.StopReasonError)
			return
		}
		if msg == nil {
			continue
		}
		chunks = append(chunks, msg)
		if !started {
			partial := streamingPartial(thinking, text)
			if err := out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &partial}); err != nil {
				return
			}
			started = true
		}
		text, thinking = pushTextChunkUpdates(out, msg, text, thinking)
	}
	if err := ctx.Err(); err != nil {
		pushAssistantError(out, err, protocol.StopReasonAborted)
		return
	}
	if len(chunks) == 0 {
		pushAssistantError(out, protocol.ErrStreamEndedWithoutResult, protocol.StopReasonError)
		return
	}
	final, err := partialAssistantMessage(chunks)
	if err != nil {
		pushAssistantError(out, err, protocol.StopReasonError)
		return
	}
	if !started {
		if err := out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &final}); err != nil {
			return
		}
	}
	pushFinalToolCalls(out, final)
	_ = out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventDone, Reason: final.StopReason, Message: &final})
}

func partialAssistantMessage(chunks []*schema.Message) (protocol.AssistantMessage, error) {
	merged, err := schema.ConcatMessages(chunks)
	if err != nil {
		return protocol.AssistantMessage{}, err
	}
	if merged.Role == "" {
		merged.Role = schema.Assistant
	}
	converted, err := protoeino.FromEinoMessage(merged)
	if err != nil {
		return protocol.AssistantMessage{}, err
	}
	assistant, ok := asAssistantMessage(converted)
	if !ok {
		return protocol.AssistantMessage{}, errors.New("model stream produced non-assistant message")
	}
	return assistant, nil
}

func pushTextChunkUpdates(out *protocol.AssistantMessageEventStream, msg *schema.Message, text, thinking string) (string, string) {
	if msg.ReasoningContent != "" {
		thinking += msg.ReasoningContent
		partial := streamingPartial(thinking, text)
		index := thinkingContentIndex(partial)
		_ = out.Push(protocol.AssistantMessageEvent{
			Type:         protocol.AssistantEventThinkingDelta,
			ContentIndex: &index,
			Delta:        msg.ReasoningContent,
			Partial:      &partial,
		})
	}
	if msg.Content != "" {
		text += msg.Content
		partial := streamingPartial(thinking, text)
		index := textContentIndex(partial)
		_ = out.Push(protocol.AssistantMessageEvent{
			Type:         protocol.AssistantEventTextDelta,
			ContentIndex: &index,
			Delta:        msg.Content,
			Partial:      &partial,
		})
	}
	return text, thinking
}

func pushFinalToolCalls(out *protocol.AssistantMessageEventStream, final protocol.AssistantMessage) {
	seen := map[string]bool{}
	for _, call := range toolCallsFromAssistant(final) {
		if seen[call.ID] {
			continue
		}
		seen[call.ID] = true
		callCopy := protocol.CloneToolCallContent(call)
		contentIndex := toolCallIndex(final, call.ID)
		_ = out.Push(protocol.AssistantMessageEvent{
			Type:         protocol.AssistantEventToolCallEnd,
			ContentIndex: &contentIndex,
			ToolCall:     &callCopy,
			Partial:      &final,
		})
	}
}

func streamingPartial(thinking, text string) protocol.AssistantMessage {
	content := protocol.ContentList{}
	if thinking != "" {
		content = append(content, protocol.NewThinkingContent(thinking))
	}
	if text != "" {
		content = append(content, protocol.NewTextContent(text))
	}
	return protocol.AssistantMessage{
		Content:    content,
		StopReason: protocol.StopReasonStop,
	}
}

func thinkingContentIndex(message protocol.AssistantMessage) int {
	for index, item := range message.Content {
		switch item.(type) {
		case protocol.ThinkingContent, *protocol.ThinkingContent:
			return index
		}
	}
	return 0
}

func textContentIndex(message protocol.AssistantMessage) int {
	for index, item := range message.Content {
		switch item.(type) {
		case protocol.TextContent, *protocol.TextContent:
			return index
		}
	}
	return 0
}

func pushAssistantError(out *protocol.AssistantMessageEventStream, err error, reason protocol.StopReason) {
	if reason != protocol.StopReasonAborted {
		reason = protocol.StopReasonError
	}
	message := protocol.AssistantMessage{
		Content:      protocol.ContentList{protocol.NewTextContent(sanitizeError(err.Error()))},
		StopReason:   reason,
		ErrorMessage: sanitizeError(err.Error()),
	}
	_ = out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventError, Reason: reason, Error: &message})
}

func toolCallsFromAssistant(message protocol.AssistantMessage) []protocol.ToolCallContent {
	var calls []protocol.ToolCallContent
	for _, item := range message.Content {
		switch value := item.(type) {
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

func toolCallIndex(message protocol.AssistantMessage, id string) int {
	for index, item := range message.Content {
		switch value := item.(type) {
		case protocol.ToolCallContent:
			if value.ID == id {
				return index
			}
		case *protocol.ToolCallContent:
			if value != nil && value.ID == id {
				return index
			}
		}
	}
	return 0
}

func toolInfosForModel(ctx context.Context, tools []tool.InvokableTool) ([]*schema.ToolInfo, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]*schema.ToolInfo, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		info, err := item.Info(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, cloneToolInfo(info))
	}
	return out, nil
}

func cloneToolInfos(infos []*schema.ToolInfo) []*schema.ToolInfo {
	out := make([]*schema.ToolInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, cloneToolInfo(info))
	}
	return out
}

func cloneToolInfo(info *schema.ToolInfo) *schema.ToolInfo {
	if info == nil {
		return nil
	}
	copied := *info
	return &copied
}
