package api

import (
	"strings"
	"time"

	protocol "oops/internal/agent/ai"
)

const (
	nonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
	nonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"
	missingToolResultText         = "No result provided"
)

// TransformMessages prepares stored history for one target model.
//
// It removes content the target cannot consume, keeps model-specific replay
// fields only for messages from the same model, and repairs incomplete tool
// call turns before the adapter validates their sequence.
type TransformMessagesOptions struct {
	Provider string
	Model    string

	SupportsImages bool

	// NormalizeToolCallID adapts a previous model's tool call ID to the target
	// protocol. It runs only while switching models.
	NormalizeToolCallID func(id string, source protocol.AssistantMessage) string
}

func TransformMessages(messages protocol.MessageList, options TransformMessagesOptions) protocol.MessageList {
	idMap := make(map[string]string)
	transformed := make(protocol.MessageList, 0, len(messages))
	for _, message := range messages {
		transformed = append(transformed, transformMessage(message, options, idMap))
	}
	return completeToolResultHistory(transformed)
}

func transformMessage(message protocol.AgentMessage, options TransformMessagesOptions, idMap map[string]string) protocol.AgentMessage {
	switch value := message.(type) {
	case protocol.UserMessage:
		value.Content = transformInputImages(value.Content, nonVisionUserImagePlaceholder, options.SupportsImages)
		return value
	case *protocol.UserMessage:
		if value == nil {
			return nil
		}
		cloned := protocol.CloneUserMessage(*value)
		cloned.Content = transformInputImages(cloned.Content, nonVisionUserImagePlaceholder, options.SupportsImages)
		return &cloned
	case protocol.ToolResultMessage:
		value.Content = transformInputImages(value.Content, nonVisionToolImagePlaceholder, options.SupportsImages)
		if normalized, ok := idMap[value.ToolCallID]; ok {
			value.ToolCallID = normalized
		}
		return value
	case *protocol.ToolResultMessage:
		if value == nil {
			return nil
		}
		cloned := protocol.CloneToolResultMessage(*value)
		cloned.Content = transformInputImages(cloned.Content, nonVisionToolImagePlaceholder, options.SupportsImages)
		if normalized, ok := idMap[cloned.ToolCallID]; ok {
			cloned.ToolCallID = normalized
		}
		return &cloned
	case protocol.AssistantMessage:
		return transformAssistantHistory(value, options, idMap)
	case *protocol.AssistantMessage:
		if value == nil {
			return nil
		}
		cloned := transformAssistantHistory(*value, options, idMap)
		return &cloned
	default:
		return protocol.CloneMessage(message)
	}
}

func transformInputImages(content protocol.ContentList, placeholder string, supportsImages bool) protocol.ContentList {
	if supportsImages {
		return protocol.CloneContentList(content)
	}

	transformed := make(protocol.ContentList, 0, len(content))
	previousWasPlaceholder := false
	for _, item := range content {
		if isImage(item) {
			if !previousWasPlaceholder {
				transformed = append(transformed, protocol.NewTextContent(placeholder))
			}
			previousWasPlaceholder = true
			continue
		}

		cloned := protocol.CloneContent(item)
		transformed = append(transformed, cloned)
		previousWasPlaceholder = textEquals(cloned, placeholder)
	}
	return transformed
}

func transformAssistantHistory(message protocol.AssistantMessage, options TransformMessagesOptions, idMap map[string]string) protocol.AssistantMessage {
	message = protocol.CloneAssistantMessage(message)
	sameModel := message.Provider == options.Provider && message.Model == options.Model
	content := make(protocol.ContentList, 0, len(message.Content))
	for _, item := range message.Content {
		switch value := item.(type) {
		case protocol.ThinkingContent:
			if transformed, keep := transformThinking(value, sameModel); keep {
				content = append(content, transformed)
			}
		case *protocol.ThinkingContent:
			if value != nil {
				if transformed, keep := transformThinking(*value, sameModel); keep {
					content = append(content, transformed)
				}
			}
		case protocol.TextContent:
			if sameModel {
				content = append(content, value)
			} else {
				content = append(content, protocol.NewTextContent(value.Text))
			}
		case *protocol.TextContent:
			if value != nil {
				if sameModel {
					content = append(content, *value)
				} else {
					content = append(content, protocol.NewTextContent(value.Text))
				}
			}
		case protocol.ToolCallContent:
			content = append(content, transformToolCall(value, message, sameModel, options, idMap))
		case *protocol.ToolCallContent:
			if value != nil {
				content = append(content, transformToolCall(*value, message, sameModel, options, idMap))
			}
		default:
			content = append(content, protocol.CloneContent(item))
		}
	}
	message.Content = content
	return message
}

func transformThinking(thinking protocol.ThinkingContent, sameModel bool) (protocol.Content, bool) {
	if thinking.Redacted {
		return thinking, sameModel
	}
	if sameModel && thinking.ThinkingSignature != "" {
		return thinking, true
	}
	if strings.TrimSpace(thinking.Thinking) == "" {
		return nil, false
	}
	if sameModel {
		return thinking, true
	}
	return protocol.NewTextContent(thinking.Thinking), true
}

func transformToolCall(call protocol.ToolCallContent, source protocol.AssistantMessage, sameModel bool, options TransformMessagesOptions, idMap map[string]string) protocol.ToolCallContent {
	call = protocol.CloneToolCallContent(call)
	if sameModel {
		return call
	}
	call.ThoughtSignature = ""
	if options.NormalizeToolCallID == nil {
		return call
	}
	normalized := options.NormalizeToolCallID(call.ID, source)
	if normalized != call.ID {
		idMap[call.ID] = normalized
		call.ID = normalized
	}
	return call
}

func completeToolResultHistory(messages protocol.MessageList) protocol.MessageList {
	result := make(protocol.MessageList, 0, len(messages))
	var pending []protocol.ToolCallContent
	existing := make(map[string]bool)

	insertMissing := func() {
		for _, call := range pending {
			if existing[call.ID] {
				continue
			}
			result = append(result, protocol.ToolResultMessage{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    protocol.ContentList{protocol.NewTextContent(missingToolResultText)},
				IsError:    true,
				Timestamp:  time.Now().UnixMilli(),
			})
		}
		pending = nil
		existing = make(map[string]bool)
	}

	for _, message := range messages {
		if assistant, ok := protocol.AsAssistantMessage(message); ok {
			insertMissing()
			if assistant.StopReason == protocol.StopReasonError || assistant.StopReason == protocol.StopReasonAborted {
				continue
			}
			if calls := protocol.ToolCallsFromAssistant(assistant); len(calls) > 0 {
				pending = calls
				existing = make(map[string]bool)
			}
			result = append(result, message)
			continue
		}
		if toolResult, ok := protocol.AsToolResultMessage(message); ok {
			existing[toolResult.ToolCallID] = true
			result = append(result, message)
			continue
		}
		if isUserMessage(message) {
			insertMissing()
		}
		result = append(result, message)
	}

	insertMissing()
	return result
}

func isUserMessage(message protocol.AgentMessage) bool {
	switch message.(type) {
	case protocol.UserMessage, *protocol.UserMessage:
		return true
	default:
		return false
	}
}

func isImage(content protocol.Content) bool {
	switch value := content.(type) {
	case protocol.ImageContent:
		return true
	case *protocol.ImageContent:
		return value != nil
	default:
		return false
	}
}

func textEquals(content protocol.Content, value string) bool {
	switch text := content.(type) {
	case protocol.TextContent:
		return text.Text == value
	case *protocol.TextContent:
		return text != nil && text.Text == value
	default:
		return false
	}
}
