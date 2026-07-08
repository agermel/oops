package einoadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"oops/internal/llm/ai/protocol"

	"github.com/cloudwego/eino/schema"
)

var (
	ErrNilMessage     = errors.New("eino message is nil")
	ErrSystemBoundary = errors.New("system message belongs to context conversion")
)

func FromEinoMessage(msg *schema.Message) (protocol.AgentMessage, error) {
	if msg == nil {
		return nil, ErrNilMessage
	}
	switch msg.Role {
	case schema.System:
		return nil, ErrSystemBoundary
	case schema.User:
		content, err := fromEinoUserContent(msg)
		if err != nil {
			return nil, err
		}
		return protocol.UserMessage{Content: content}, nil
	case schema.Assistant:
		content, err := fromEinoAssistantContent(msg)
		if err != nil {
			return nil, err
		}
		return protocol.AssistantMessage{
			Content:      content,
			Usage:        fromEinoUsage(msg.ResponseMeta),
			StopReason:   fromEinoStopReason(msg.ResponseMeta, len(msg.ToolCalls) > 0),
			ErrorMessage: fromEinoErrorMessage(msg.ResponseMeta),
		}, nil
	case schema.Tool:
		return protocol.ToolResultMessage{
			ToolCallID: msg.ToolCallID,
			ToolName:   msg.ToolName,
			Content:    fromEinoToolContent(msg),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported eino role %q", msg.Role)
	}
}

func ToEinoMessage(msg protocol.AgentMessage) (*schema.Message, error) {
	if msg == nil {
		return nil, errors.New("protocol message is nil")
	}
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	switch m := msg.(type) {
	case protocol.UserMessage:
		return toEinoUserMessage(m)
	case *protocol.UserMessage:
		return toEinoUserMessage(*m)
	case protocol.AssistantMessage:
		return toEinoAssistantMessage(m)
	case *protocol.AssistantMessage:
		return toEinoAssistantMessage(*m)
	case protocol.ToolResultMessage:
		return toEinoToolMessage(m)
	case *protocol.ToolResultMessage:
		return toEinoToolMessage(*m)
	default:
		return nil, fmt.Errorf("unsupported protocol message %T", msg)
	}
}

func FromEinoMessages(messages []*schema.Message) (protocol.MessageList, error) {
	out := make(protocol.MessageList, 0, len(messages))
	for _, msg := range messages {
		converted, err := FromEinoMessage(msg)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func ToEinoMessages(messages protocol.MessageList) ([]*schema.Message, error) {
	out := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		converted, err := ToEinoMessage(msg)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func fromEinoUserContent(msg *schema.Message) (protocol.ContentList, error) {
	if len(msg.UserInputMultiContent) > 0 {
		return fromEinoInputParts(msg.UserInputMultiContent)
	}
	if len(msg.MultiContent) > 0 {
		return fromEinoMultiContentParts(msg.MultiContent)
	}
	return protocol.ContentList{protocol.NewTextContent(msg.Content)}, nil
}

func fromEinoAssistantContent(msg *schema.Message) (protocol.ContentList, error) {
	content := make(protocol.ContentList, 0, len(msg.AssistantGenMultiContent)+len(msg.ToolCalls)+2)
	hasReasoningPart := false
	if len(msg.AssistantGenMultiContent) > 0 {
		parts, err := fromEinoOutputParts(msg.AssistantGenMultiContent)
		if err != nil {
			return nil, err
		}
		hasReasoningPart = contentListHasThinking(parts)
		content = append(content, parts...)
	} else if msg.Content != "" {
		content = append(content, protocol.NewTextContent(msg.Content))
	}
	if msg.ReasoningContent != "" && !hasReasoningPart {
		content = append(protocol.ContentList{protocol.NewThinkingContent(msg.ReasoningContent)}, content...)
	}
	for _, call := range msg.ToolCalls {
		arguments, err := rawToolArguments(call.Function.Arguments)
		if err != nil {
			return nil, err
		}
		content = append(content, protocol.NewToolCallContent(call.ID, call.Function.Name, arguments))
	}
	return content, nil
}

func fromEinoToolContent(msg *schema.Message) protocol.ContentList {
	if len(msg.MultiContent) > 0 {
		content, err := fromEinoMultiContentParts(msg.MultiContent)
		if err == nil {
			return content
		}
	}
	return protocol.ContentList{protocol.NewTextContent(msg.Content)}
}

func toEinoUserMessage(msg protocol.UserMessage) (*schema.Message, error) {
	if len(msg.Content) == 1 {
		if text, ok := asText(msg.Content[0]); ok {
			return schema.UserMessage(text.Text), nil
		}
	}
	parts := make([]schema.MessageInputPart, 0, len(msg.Content))
	for _, content := range msg.Content {
		switch c := content.(type) {
		case protocol.TextContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: c.Text})
		case *protocol.TextContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: c.Text})
		case protocol.ImageContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toEinoInputImage(c)})
		case *protocol.ImageContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toEinoInputImage(*c)})
		default:
			return nil, fmt.Errorf("user message cannot contain %T", content)
		}
	}
	return &schema.Message{Role: schema.User, UserInputMultiContent: parts}, nil
}

func toEinoAssistantMessage(msg protocol.AssistantMessage) (*schema.Message, error) {
	out := &schema.Message{
		Role:         schema.Assistant,
		ResponseMeta: toEinoResponseMeta(msg),
	}
	var textParts []string
	var outputParts []schema.MessageOutputPart
	var hasOutputParts bool
	for _, content := range msg.Content {
		switch c := content.(type) {
		case protocol.TextContent:
			textParts = append(textParts, c.Text)
			outputParts = append(outputParts, schema.MessageOutputPart{Type: schema.ChatMessagePartTypeText, Text: c.Text})
		case *protocol.TextContent:
			textParts = append(textParts, c.Text)
			outputParts = append(outputParts, schema.MessageOutputPart{Type: schema.ChatMessagePartTypeText, Text: c.Text})
		case protocol.ThinkingContent:
			if out.ReasoningContent == "" {
				out.ReasoningContent = c.Thinking
			}
			outputParts = append(outputParts, schema.MessageOutputPart{
				Type:      schema.ChatMessagePartTypeReasoning,
				Reasoning: &schema.MessageOutputReasoning{Text: c.Thinking, Signature: c.ThinkingSignature},
			})
			hasOutputParts = true
		case *protocol.ThinkingContent:
			if out.ReasoningContent == "" {
				out.ReasoningContent = c.Thinking
			}
			outputParts = append(outputParts, schema.MessageOutputPart{
				Type:      schema.ChatMessagePartTypeReasoning,
				Reasoning: &schema.MessageOutputReasoning{Text: c.Thinking, Signature: c.ThinkingSignature},
			})
			hasOutputParts = true
		case protocol.ImageContent:
			outputParts = append(outputParts, schema.MessageOutputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toEinoOutputImage(c)})
			hasOutputParts = true
		case *protocol.ImageContent:
			outputParts = append(outputParts, schema.MessageOutputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toEinoOutputImage(*c)})
			hasOutputParts = true
		case protocol.ToolCallContent:
			out.ToolCalls = append(out.ToolCalls, toEinoToolCall(c))
		case *protocol.ToolCallContent:
			out.ToolCalls = append(out.ToolCalls, toEinoToolCall(*c))
		default:
			return nil, fmt.Errorf("assistant message cannot contain %T", content)
		}
	}
	out.Content = strings.Join(textParts, "")
	if hasOutputParts {
		out.AssistantGenMultiContent = outputParts
	}
	return out, nil
}

func toEinoToolMessage(msg protocol.ToolResultMessage) (*schema.Message, error) {
	out := schema.ToolMessage(textFromContent(msg.Content), msg.ToolCallID, schema.WithToolName(msg.ToolName))
	parts := make([]schema.ChatMessagePart, 0, len(msg.Content))
	for _, content := range msg.Content {
		switch c := content.(type) {
		case protocol.TextContent:
			parts = append(parts, schema.ChatMessagePart{Type: schema.ChatMessagePartTypeText, Text: c.Text})
		case *protocol.TextContent:
			parts = append(parts, schema.ChatMessagePart{Type: schema.ChatMessagePartTypeText, Text: c.Text})
		case protocol.ImageContent:
			parts = append(parts, toEinoMultiContentImagePart(c))
		case *protocol.ImageContent:
			parts = append(parts, toEinoMultiContentImagePart(*c))
		default:
			return nil, fmt.Errorf("tool result message cannot contain %T", content)
		}
	}
	if len(parts) > 1 || (len(parts) == 1 && parts[0].Type != schema.ChatMessagePartTypeText) {
		out.MultiContent = parts
	}
	return out, nil
}

func fromEinoInputParts(parts []schema.MessageInputPart) (protocol.ContentList, error) {
	out := make(protocol.ContentList, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			out = append(out, protocol.NewTextContent(part.Text))
		case schema.ChatMessagePartTypeImageURL:
			if part.Image == nil {
				return nil, errors.New("image input part missing image")
			}
			out = append(out, fromEinoInputImage(part.Image))
		default:
			return nil, fmt.Errorf("unsupported user input part %q", part.Type)
		}
	}
	return out, nil
}

func fromEinoOutputParts(parts []schema.MessageOutputPart) (protocol.ContentList, error) {
	out := make(protocol.ContentList, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			out = append(out, protocol.NewTextContent(part.Text))
		case schema.ChatMessagePartTypeImageURL:
			if part.Image == nil {
				return nil, errors.New("image output part missing image")
			}
			out = append(out, fromEinoOutputImage(part.Image))
		case schema.ChatMessagePartTypeReasoning:
			if part.Reasoning == nil {
				return nil, errors.New("reasoning output part missing reasoning")
			}
			out = append(out, protocol.ThinkingContent{
				Type:              protocol.ContentTypeThinking,
				Thinking:          part.Reasoning.Text,
				ThinkingSignature: part.Reasoning.Signature,
			})
		default:
			return nil, fmt.Errorf("unsupported assistant output part %q", part.Type)
		}
	}
	return out, nil
}

func fromEinoMultiContentParts(parts []schema.ChatMessagePart) (protocol.ContentList, error) {
	out := make(protocol.ContentList, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			out = append(out, protocol.NewTextContent(part.Text))
		case schema.ChatMessagePartTypeImageURL:
			if part.ImageURL == nil {
				return nil, errors.New("image part missing image_url")
			}
			out = append(out, protocol.ImageContent{
				Type:     protocol.ContentTypeImage,
				Data:     part.ImageURL.URI,
				URL:      part.ImageURL.URL,
				MIMEType: part.ImageURL.MIMEType,
				Detail:   string(part.ImageURL.Detail),
			})
		default:
			return nil, fmt.Errorf("unsupported multi content part %q", part.Type)
		}
	}
	return out, nil
}

func fromEinoInputImage(img *schema.MessageInputImage) protocol.ImageContent {
	content := protocol.ImageContent{Type: protocol.ContentTypeImage, MIMEType: img.MIMEType, Detail: string(img.Detail)}
	if img.URL != nil {
		content.URL = *img.URL
	}
	if img.Base64Data != nil {
		content.Data = *img.Base64Data
	}
	return content
}

func fromEinoOutputImage(img *schema.MessageOutputImage) protocol.ImageContent {
	content := protocol.ImageContent{Type: protocol.ContentTypeImage, MIMEType: img.MIMEType}
	if img.URL != nil {
		content.URL = *img.URL
	}
	if img.Base64Data != nil {
		content.Data = *img.Base64Data
	}
	return content
}

func toEinoInputImage(content protocol.ImageContent) *schema.MessageInputImage {
	return &schema.MessageInputImage{
		MessagePartCommon: schema.MessagePartCommon{
			URL:        ptrIfNotEmpty(content.URL),
			Base64Data: ptrIfNotEmpty(content.Data),
			MIMEType:   content.MIMEType,
		},
		Detail: schema.ImageURLDetail(content.Detail),
	}
}

func toEinoOutputImage(content protocol.ImageContent) *schema.MessageOutputImage {
	return &schema.MessageOutputImage{
		MessagePartCommon: schema.MessagePartCommon{
			URL:        ptrIfNotEmpty(content.URL),
			Base64Data: ptrIfNotEmpty(content.Data),
			MIMEType:   content.MIMEType,
		},
	}
}

func toEinoMultiContentImagePart(content protocol.ImageContent) schema.ChatMessagePart {
	return schema.ChatMessagePart{
		Type: schema.ChatMessagePartTypeImageURL,
		ImageURL: &schema.ChatMessageImageURL{
			URL:      content.URL,
			URI:      content.Data,
			MIMEType: content.MIMEType,
			Detail:   schema.ImageURLDetail(content.Detail),
		},
	}
}

func toEinoToolCall(content protocol.ToolCallContent) schema.ToolCall {
	return schema.ToolCall{
		ID:   content.ID,
		Type: "function",
		Function: schema.FunctionCall{
			Name:      content.Name,
			Arguments: string(content.Arguments),
		},
	}
}

func fromEinoUsage(meta *schema.ResponseMeta) protocol.Usage {
	if meta == nil || meta.Usage == nil {
		return protocol.Usage{}
	}
	usage := meta.Usage
	reasoning := usage.CompletionTokensDetails.ReasoningTokens
	out := protocol.Usage{
		Input:       usage.PromptTokens,
		Output:      usage.CompletionTokens,
		CacheRead:   usage.PromptTokenDetails.CachedTokens,
		TotalTokens: usage.TotalTokens,
	}
	if reasoning != 0 {
		out.Reasoning = &reasoning
	}
	return out
}

func toEinoResponseMeta(msg protocol.AssistantMessage) *schema.ResponseMeta {
	return &schema.ResponseMeta{
		FinishReason: toEinoFinishReason(msg.StopReason),
		Usage: &schema.TokenUsage{
			PromptTokens:     msg.Usage.Input,
			CompletionTokens: msg.Usage.Output,
			TotalTokens:      msg.Usage.TotalTokens,
			PromptTokenDetails: schema.PromptTokenDetails{
				CachedTokens: msg.Usage.CacheRead,
			},
			CompletionTokensDetails: schema.CompletionTokensDetails{
				ReasoningTokens: intValue(msg.Usage.Reasoning),
			},
		},
	}
}

func fromEinoStopReason(meta *schema.ResponseMeta, hasToolCalls bool) protocol.StopReason {
	if meta == nil {
		if hasToolCalls {
			return protocol.StopReasonToolUse
		}
		return protocol.StopReasonStop
	}
	switch meta.FinishReason {
	case "", "stop":
		if hasToolCalls {
			return protocol.StopReasonToolUse
		}
		return protocol.StopReasonStop
	case "length":
		return protocol.StopReasonLength
	case "tool_calls", "tool_use", "toolUse":
		return protocol.StopReasonToolUse
	case "error":
		return protocol.StopReasonError
	case "aborted":
		return protocol.StopReasonAborted
	default:
		return protocol.StopReasonError
	}
}

func toEinoFinishReason(reason protocol.StopReason) string {
	switch reason {
	case protocol.StopReasonStop:
		return "stop"
	case protocol.StopReasonLength:
		return "length"
	case protocol.StopReasonToolUse:
		return "tool_calls"
	case protocol.StopReasonError:
		return "error"
	case protocol.StopReasonAborted:
		return "aborted"
	default:
		return ""
	}
}

func fromEinoErrorMessage(meta *schema.ResponseMeta) string {
	if meta == nil {
		return ""
	}
	switch meta.FinishReason {
	case "", "stop", "length", "tool_calls", "tool_use", "toolUse", "error", "aborted":
		return ""
	default:
		return "finish_reason: " + meta.FinishReason
	}
}

func rawToolArguments(arguments string) (json.RawMessage, error) {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	raw := json.RawMessage(arguments)
	if !json.Valid(raw) {
		return nil, fmt.Errorf("tool call arguments must be valid json: %q", arguments)
	}
	return raw, nil
}

func textFromContent(content protocol.ContentList) string {
	var parts []string
	for _, item := range content {
		if text, ok := asText(item); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "")
}

func asText(content protocol.Content) (protocol.TextContent, bool) {
	switch c := content.(type) {
	case protocol.TextContent:
		return c, true
	case *protocol.TextContent:
		return *c, true
	default:
		return protocol.TextContent{}, false
	}
}

func contentListHasThinking(content protocol.ContentList) bool {
	for _, item := range content {
		switch item.(type) {
		case protocol.ThinkingContent, *protocol.ThinkingContent:
			return true
		}
	}
	return false
}

func ptrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
