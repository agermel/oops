package ai

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
	assistant, ok := AsAssistantMessage(message)
	if !ok {
		return true
	}
	return assistant.StopReason != StopReasonError && assistant.StopReason != StopReasonAborted
}

func ValidateProviderMessageSequence(messages MessageList) error {
	analysis := AnalyzeProviderMessageSequence(messages)
	if analysis.Status == ProviderSequenceInvalid || analysis.Status == ProviderSequenceRecoverable {
		return analysis.Err
	}
	return nil
}

func AnalyzeProviderMessageSequence(messages MessageList) ProviderSequenceAnalysis {
	replay := providerReplayItems(messages)
	for i := 0; i < len(replay); {
		message := replay[i].message
		if toolResult, ok := AsToolResultMessage(message); ok {
			return invalidSequence(fmt.Errorf("tool result %q has no preceding assistant tool call", toolResult.ToolCallID))
		}
		assistant, ok := AsAssistantMessage(message)
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
			result, ok := AsToolResultMessage(replay[j].message)
			if !ok {
				break
			}
			if !expected[result.ToolCallID] {
				return invalidSequence(fmt.Errorf("tool result %q does not match preceding assistant tool calls", result.ToolCallID))
			}
			if seen[result.ToolCallID] {
				return invalidSequence(fmt.Errorf("duplicate tool result %q after assistant tool call", result.ToolCallID))
			}
			seen[result.ToolCallID] = true
		}
		if len(seen) != len(expected) {
			err := fmt.Errorf("assistant tool calls need %d tool results, got %d", len(expected), len(seen))
			if j == len(replay) {
				return ProviderSequenceAnalysis{Status: ProviderSequenceRecoverable, PendingAssistantIndex: replay[i].index, Err: err}
			}
			return invalidSequence(err)
		}
		i = j
	}
	return ProviderSequenceAnalysis{Status: ProviderSequenceComplete, PendingAssistantIndex: -1}
}

type providerReplayItem struct {
	index   int
	message AgentMessage
}

func providerReplayItems(messages MessageList) []providerReplayItem {
	out := make([]providerReplayItem, 0, len(messages))
	for i, message := range messages {
		if !ProviderShouldReplayMessage(message) {
			continue
		}
		if providerEmptyAssistant(message) {
			continue
		}
		out = append(out, providerReplayItem{index: i, message: CloneMessage(message)})
	}
	return out
}

func invalidSequence(err error) ProviderSequenceAnalysis {
	return ProviderSequenceAnalysis{Status: ProviderSequenceInvalid, PendingAssistantIndex: -1, Err: err}
}

func providerEmptyAssistant(message AgentMessage) bool {
	assistant, ok := AsAssistantMessage(message)
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
