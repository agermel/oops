package session

import (
	"fmt"

	protocol "oops/internal/agent/ai"
)

type messageSequenceStatus string

const (
	messageSequenceComplete   messageSequenceStatus = "complete"
	messageSequenceIncomplete messageSequenceStatus = "incomplete"
	messageSequenceInvalid    messageSequenceStatus = "invalid"
)

type messageSequenceAnalysis struct {
	status                messageSequenceStatus
	pendingAssistantIndex int
	err                   error
}

func validateMessageSequence(messages protocol.MessageList) error {
	analysis := analyzeMessageSequence(messages)
	if analysis.status == messageSequenceInvalid || analysis.status == messageSequenceIncomplete {
		return analysis.err
	}
	return nil
}

func analyzeMessageSequence(messages protocol.MessageList) messageSequenceAnalysis {
	items := messageSequenceItems(messages)
	for i := 0; i < len(items); {
		message := items[i].message
		if toolResult, ok := protocol.AsToolResultMessage(message); ok {
			return invalidMessageSequence(fmt.Errorf("tool result %q has no preceding assistant tool call", toolResult.ToolCallID))
		}
		assistant, ok := protocol.AsAssistantMessage(message)
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
		for ; j < len(items); j++ {
			result, ok := protocol.AsToolResultMessage(items[j].message)
			if !ok {
				break
			}
			if !expected[result.ToolCallID] {
				return invalidMessageSequence(fmt.Errorf("tool result %q does not match preceding assistant tool calls", result.ToolCallID))
			}
			if seen[result.ToolCallID] {
				return invalidMessageSequence(fmt.Errorf("duplicate tool result %q after assistant tool call", result.ToolCallID))
			}
			seen[result.ToolCallID] = true
		}
		if len(seen) != len(expected) {
			err := fmt.Errorf("assistant tool calls need %d tool results, got %d", len(expected), len(seen))
			if j == len(items) {
				return messageSequenceAnalysis{status: messageSequenceIncomplete, pendingAssistantIndex: items[i].index, err: err}
			}
			return invalidMessageSequence(err)
		}
		i = j
	}
	return messageSequenceAnalysis{status: messageSequenceComplete, pendingAssistantIndex: -1}
}

type messageSequenceItem struct {
	index   int
	message protocol.AgentMessage
}

func messageSequenceItems(messages protocol.MessageList) []messageSequenceItem {
	items := make([]messageSequenceItem, 0, len(messages))
	for index, message := range messages {
		if !includeMessageInSequence(message) || emptyAssistantMessage(message) {
			continue
		}
		items = append(items, messageSequenceItem{index: index, message: message})
	}
	return items
}

func includeMessageInSequence(message protocol.AgentMessage) bool {
	assistant, ok := protocol.AsAssistantMessage(message)
	return !ok || (assistant.StopReason != protocol.StopReasonError && assistant.StopReason != protocol.StopReasonAborted)
}

func emptyAssistantMessage(message protocol.AgentMessage) bool {
	assistant, ok := protocol.AsAssistantMessage(message)
	if !ok {
		return false
	}
	for _, content := range assistant.Content {
		switch content.(type) {
		case protocol.TextContent, *protocol.TextContent, protocol.ImageContent, *protocol.ImageContent, protocol.ToolCallContent, *protocol.ToolCallContent:
			return false
		}
	}
	return true
}

func toolCallIDSet(content protocol.ContentList) map[string]bool {
	ids := map[string]bool{}
	for _, item := range content {
		switch call := item.(type) {
		case protocol.ToolCallContent:
			ids[call.ID] = true
		case *protocol.ToolCallContent:
			if call != nil {
				ids[call.ID] = true
			}
		}
	}
	return ids
}

func invalidMessageSequence(err error) messageSequenceAnalysis {
	return messageSequenceAnalysis{status: messageSequenceInvalid, pendingAssistantIndex: -1, err: err}
}
