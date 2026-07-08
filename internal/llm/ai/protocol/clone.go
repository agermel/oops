package protocol

import "encoding/json"

func CloneMessageList(messages MessageList) MessageList {
	if len(messages) == 0 {
		return nil
	}
	out := make(MessageList, 0, len(messages))
	for _, message := range messages {
		out = append(out, CloneMessage(message))
	}
	return out
}

func CloneMessage(message AgentMessage) AgentMessage {
	switch value := message.(type) {
	case UserMessage:
		return CloneUserMessage(value)
	case *UserMessage:
		if value == nil {
			return nil
		}
		cloned := CloneUserMessage(*value)
		return &cloned
	case AssistantMessage:
		return CloneAssistantMessage(value)
	case *AssistantMessage:
		if value == nil {
			return nil
		}
		cloned := CloneAssistantMessage(*value)
		return &cloned
	case ToolResultMessage:
		return CloneToolResultMessage(value)
	case *ToolResultMessage:
		if value == nil {
			return nil
		}
		cloned := CloneToolResultMessage(*value)
		return &cloned
	default:
		return message
	}
}

func CloneUserMessage(message UserMessage) UserMessage {
	message.Content = CloneContentList(message.Content)
	return message
}

func CloneAssistantMessage(message AssistantMessage) AssistantMessage {
	message.Content = CloneContentList(message.Content)
	message.Usage = CloneUsage(message.Usage)
	return message
}

func CloneAssistantMessagePtr(message *AssistantMessage) *AssistantMessage {
	if message == nil {
		return nil
	}
	cloned := CloneAssistantMessage(*message)
	return &cloned
}

func CloneToolResultMessages(messages []ToolResultMessage) []ToolResultMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ToolResultMessage, len(messages))
	for i, message := range messages {
		out[i] = CloneToolResultMessage(message)
	}
	return out
}

func CloneToolResultMessage(message ToolResultMessage) ToolResultMessage {
	message.Content = CloneContentList(message.Content)
	message.Details = cloneJSONLike(message.Details)
	return message
}

func CloneContentList(content ContentList) ContentList {
	if len(content) == 0 {
		return nil
	}
	out := make(ContentList, 0, len(content))
	for _, item := range content {
		out = append(out, CloneContent(item))
	}
	return out
}

func CloneContent(content Content) Content {
	switch value := content.(type) {
	case TextContent:
		return value
	case *TextContent:
		if value == nil {
			return nil
		}
		cloned := *value
		return &cloned
	case ThinkingContent:
		return value
	case *ThinkingContent:
		if value == nil {
			return nil
		}
		cloned := *value
		return &cloned
	case ImageContent:
		return value
	case *ImageContent:
		if value == nil {
			return nil
		}
		cloned := *value
		return &cloned
	case ToolCallContent:
		return CloneToolCallContent(value)
	case *ToolCallContent:
		if value == nil {
			return nil
		}
		cloned := CloneToolCallContent(*value)
		return &cloned
	default:
		return content
	}
}

func CloneToolCallContent(content ToolCallContent) ToolCallContent {
	content.Arguments = cloneRaw(content.Arguments)
	return content
}

func CloneTools(tools []ToolDefinition) []ToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	out := make([]ToolDefinition, len(tools))
	for i, tool := range tools {
		tool.Parameters = cloneRaw(tool.Parameters)
		out[i] = tool
	}
	return out
}

func CloneToolResult(result ToolResult) ToolResult {
	result.Content = CloneContentList(result.Content)
	result.Details = cloneJSONLike(result.Details)
	return result
}

func CloneUsage(usage Usage) Usage {
	if usage.CacheWrite1h != nil {
		value := *usage.CacheWrite1h
		usage.CacheWrite1h = &value
	}
	if usage.Reasoning != nil {
		value := *usage.Reasoning
		usage.Reasoning = &value
	}
	if usage.Cost != nil {
		cost := *usage.Cost
		usage.Cost = &cost
	}
	return usage
}

func cloneJSONLike(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return value
	}
	return out
}
