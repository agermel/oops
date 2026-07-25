package ai

import aiutils "oops/internal/agent/ai/utils"

const estimatedImageChars = 4800

func EstimateContentTokens(content ContentList) int {
	chars := 0
	for _, item := range content {
		switch value := item.(type) {
		case TextContent:
			chars += len(value.Text)
		case *TextContent:
			if value != nil {
				chars += len(value.Text)
			}
		case ThinkingContent:
			chars += len(value.Thinking)
		case *ThinkingContent:
			if value != nil {
				chars += len(value.Thinking)
			}
		case ImageContent, *ImageContent:
			chars += estimatedImageChars
		case ToolCallContent:
			chars += len(value.Name) + len(value.Arguments)
		case *ToolCallContent:
			if value != nil {
				chars += len(value.Name) + len(value.Arguments)
			}
		}
	}
	return aiutils.EstimateTokensForChars(chars)
}

func EstimateMessageTokens(message AgentMessage) int {
	if message == nil {
		return 0
	}
	switch value := message.(type) {
	case UserMessage:
		return EstimateContentTokens(value.Content)
	case *UserMessage:
		if value != nil {
			return EstimateContentTokens(value.Content)
		}
	case AssistantMessage:
		return EstimateContentTokens(value.Content)
	case *AssistantMessage:
		if value != nil {
			return EstimateContentTokens(value.Content)
		}
	case ToolResultMessage:
		return EstimateContentTokens(value.Content)
	case *ToolResultMessage:
		if value != nil {
			return EstimateContentTokens(value.Content)
		}
	}
	return 0
}

// EstimateContextTokens reuses the most recent provider usage and estimates
// messages added after it.
func EstimateContextTokens(ctx Context) int {
	usageIndex, usageTokens := latestAssistantUsage(ctx.Messages)
	if usageIndex >= 0 {
		tokens := usageTokens
		for _, message := range ctx.Messages[usageIndex+1:] {
			tokens += EstimateMessageTokens(message)
		}
		return tokens
	}

	tokens := aiutils.EstimateTextTokens(ctx.SystemPrompt)
	for _, message := range ctx.Messages {
		tokens += EstimateMessageTokens(message)
	}
	for _, tool := range ctx.Tools {
		tokens += aiutils.EstimateTextTokens(tool.Name)
		tokens += aiutils.EstimateTextTokens(tool.Description)
		tokens += aiutils.EstimateTextTokens(string(tool.Parameters))
	}
	return tokens
}

func latestAssistantUsage(messages MessageList) (int, int) {
	for index := len(messages) - 1; index >= 0; index-- {
		assistant, ok := AsAssistantMessage(messages[index])
		if !ok || assistant.StopReason == StopReasonError || assistant.StopReason == StopReasonAborted {
			continue
		}
		if tokens := usageContextTokens(assistant.Usage); tokens > 0 {
			return index, tokens
		}
	}
	return -1, 0
}

func usageContextTokens(usage Usage) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}
