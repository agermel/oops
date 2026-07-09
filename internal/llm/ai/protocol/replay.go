package protocol

import (
	"fmt"
)

// ProviderReplayMessages returns the history entries that are sent back to a
// chat provider on the next model call.
func ProviderReplayMessages(messages MessageList) MessageList {
	out := make(MessageList, 0, len(messages))
	for _, message := range messages {
		if !ProviderShouldReplayMessage(message) {
			continue
		}
		if providerEmptyAssistant(message) {
			continue
		}
		out = append(out, CloneMessage(message))
	}
	return out
}

func ProviderShouldReplayMessage(message AgentMessage) bool {
	switch value := message.(type) {
	case AssistantMessage:
		return value.StopReason != StopReasonError && value.StopReason != StopReasonAborted
	case *AssistantMessage:
		if value == nil {
			return true
		}
		return value.StopReason != StopReasonError && value.StopReason != StopReasonAborted
	default:
		return true
	}
}

func ValidateProviderMessageSequence(messages MessageList) error {
	replay := ProviderReplayMessages(messages)
	for i := 0; i < len(replay); {
		message := replay[i]
		if toolResult, ok := asToolResultMessage(message); ok {
			return fmt.Errorf("tool result %q has no preceding assistant tool call", toolResult.ToolCallID)
		}
		assistant, ok := asAssistantMessage(message)
		if !ok {
			i++
			continue
		}
		expected := toolCallIDSet(assistant.Content)
		if len(expected) == 0 {
			i++
			continue
		}
		seen := map[string]bool{}
		j := i + 1
		for ; j < len(replay); j++ {
			result, ok := asToolResultMessage(replay[j])
			if !ok {
				break
			}
			if !expected[result.ToolCallID] {
				return fmt.Errorf("tool result %q does not match preceding assistant tool calls", result.ToolCallID)
			}
			if seen[result.ToolCallID] {
				return fmt.Errorf("duplicate tool result %q after assistant tool call", result.ToolCallID)
			}
			seen[result.ToolCallID] = true
		}
		if len(seen) != len(expected) {
			return fmt.Errorf("assistant tool calls need %d tool results, got %d", len(expected), len(seen))
		}
		i = j
	}
	return nil
}

func providerEmptyAssistant(message AgentMessage) bool {
	assistant, ok := asAssistantMessage(message)
	if !ok {
		return false
	}
	for _, item := range assistant.Content {
		switch item.(type) {
		case TextContent, *TextContent, ImageContent, *ImageContent, ToolCallContent, *ToolCallContent:
			return false
		}
	}
	return true
}

func asAssistantMessage(message AgentMessage) (AssistantMessage, bool) {
	switch value := message.(type) {
	case AssistantMessage:
		return value, true
	case *AssistantMessage:
		if value != nil {
			return *value, true
		}
	}
	return AssistantMessage{}, false
}

func asToolResultMessage(message AgentMessage) (ToolResultMessage, bool) {
	switch value := message.(type) {
	case ToolResultMessage:
		return value, true
	case *ToolResultMessage:
		if value != nil {
			return *value, true
		}
	}
	return ToolResultMessage{}, false
}

func toolCallIDSet(content ContentList) map[string]bool {
	out := map[string]bool{}
	for _, item := range content {
		switch value := item.(type) {
		case ToolCallContent:
			out[value.ID] = true
		case *ToolCallContent:
			if value != nil {
				out[value.ID] = true
			}
		}
	}
	return out
}
