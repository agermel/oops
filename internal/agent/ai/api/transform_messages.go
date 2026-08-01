package api

import (
	"strings"
	"time"

	protocol "oops/internal/agent/ai"
)

// 一些文字占位符，用于描述处理掉的内容
const (
	nonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
	nonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"
	missingToolResultText         = "No result provided"
)

// TransformMessages 将存储好的历史对话转换成模型可用的消息对话，就是将历史消息整理好
// 用于模型请求前加载历史消息的时候
// 处理两种场景
// 模型切换：处理思考签名、工具调用 ID、图片能力等跨模型兼容问题
// 补齐“有 ToolCall、缺 ToolResult”的调用轮次，并跳过 error、aborted 的助手消息
// 保证历史消息在模型请求后能够重放
type TransformMessagesOptions struct {
	Provider string
	Model    string

	SupportsImages bool

	// NormalizeToolCallID 会把先前模型生成的工具调用 ID 调整为目标协议所要求的格式。
	// 该函数只在切换模型时执行。
	// 每个供应商有他自己的 NormalizeToolCallID
	NormalizeToolCallID func(id string, source protocol.AssistantMessage) string
}

// 将历史消息（存储好的）通过选项选择转化为模型可用的消息对话（将历史对话 1.突然结束的 2.切换模型后目标模型无法理解的对话格式 等目标模型无法理解的格式进行加工）
// TransformMessages -> transformMessage (处理逐条历史信息)
//
//	-> completeToolResultHistory （补全历史中缺失的工具执行结果）
func TransformMessages(messages protocol.MessageList, options TransformMessagesOptions) protocol.MessageList {
	// idMap 的目的是保证工具调用与工具结果仍然使用同一个 ID
	idMap := make(map[string]string)
	transformed := make(protocol.MessageList, 0, len(messages))
	for _, message := range messages {
		transformed = append(transformed, transformMessage(message, options, idMap))
	}
	return completeToolResultHistory(transformed)
}

// idMap 是工具调用 ID 的映射表
// agent 框架提供工具清单，而工具调用 ID 是模型调用一次工具所产生的记录 ID
// toolcall 的 id 是由模型生成的所以我们需要 idMap 去做一次 id 识别与转换
// 以适应切换模型的时候，历史的id能够兼容新供应商的标准

// 根据消息的不同类型进行不同的处理
// UserMessage: transformInputImages 处理输入图片
// ToolResultMessage: transformInputImages 处理输入图片 通过idMap决定是否需要修改toolid
// AssistantMessage: transformAssistantHistory 整理助手信息
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

// transformInputImages 用于按目标模型的图片能力处理消息内容
// 若支持图片，复制并保留原始图片内容
// 目标模型只接收文本时，它将图片替换为占位文本。
// 连续多张图片会合并为一条占位文本，避免历史消息出现重复提示
// 多张图片只做一条占位文本的原因是因为既然目标模型无法获取图片内容，那么图片数量不影响语义；占位文本只需告知模型存在已省略的图片即可。
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

// 整理助手信息
// 处理 AssistantHistory 的 ContentList 里面的逐个 content
// ThinkingContent -> transformThinking
// TextContent -> 直接append
// ToolCallContent -> transformToolCall
// newContent 的目的就是弄掉独属于samemodel的一些配置 如session_id或secret这些，保留可读文本，移除 TextSignature 等模型专用重放字段。
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

// 补全历史中缺失的工具执行结果
// 维护 pending：上一条助手消息发起、等待结果的工具调用。
// 维护 existing：已经收到结果的工具调用 ID。
// 遇到助手消息时，先补齐上一轮遗留调用；跳过 error、aborted 助手消息（不留在上下文中）；收集当前消息中的工具调用到 pending
// 遇到工具结果时，记录它的 ToolCallID，并保留该消息。
// 遇到用户消息、下一条助手消息或历史结束时，为 pending 中缺少结果的调用生成一条错误工具结果："No result provided"、IsError: true。
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
