package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	aiutils "oops/internal/agent/ai/utils"
)

func NewTextContent(text string) TextContent {
	return TextContent{Type: ContentTypeText, Text: text}
}

func TextFromContent(content ContentList) string {
	var text strings.Builder
	for _, item := range content {
		switch value := item.(type) {
		case TextContent:
			text.WriteString(value.Text)
		case *TextContent:
			if value != nil {
				text.WriteString(value.Text)
			}
		}
	}
	return text.String()
}

func ToolCallsFromAssistant(message AssistantMessage) []ToolCallContent {
	var calls []ToolCallContent
	for _, content := range message.Content {
		switch value := content.(type) {
		case ToolCallContent:
			calls = append(calls, value)
		case *ToolCallContent:
			if value != nil {
				calls = append(calls, *value)
			}
		}
	}
	return calls
}

func (c TextContent) ContentType() ContentType { return ContentTypeText }

func (c TextContent) Validate() error {
	if c.Type != "" && c.Type != ContentTypeText {
		return fmt.Errorf("text content type = %q", c.Type)
	}
	return nil
}

func (c TextContent) MarshalJSON() ([]byte, error) {
	type alias TextContent
	c.Type = ContentTypeText
	return json.Marshal(alias(c))
}

func NewThinkingContent(thinking string) ThinkingContent {
	return ThinkingContent{Type: ContentTypeThinking, Thinking: thinking}
}

func (c ThinkingContent) ContentType() ContentType { return ContentTypeThinking }

func (c ThinkingContent) Validate() error {
	if c.Type != "" && c.Type != ContentTypeThinking {
		return fmt.Errorf("thinking content type = %q", c.Type)
	}
	return nil
}

func (c ThinkingContent) MarshalJSON() ([]byte, error) {
	type alias ThinkingContent
	c.Type = ContentTypeThinking
	return json.Marshal(alias(c))
}

func (c ImageContent) ContentType() ContentType { return ContentTypeImage }

func (c ImageContent) Validate() error {
	if c.Type != "" && c.Type != ContentTypeImage {
		return fmt.Errorf("image content type = %q", c.Type)
	}
	if c.Data == "" && c.URL == "" {
		return errors.New("image content requires data or url")
	}
	return nil
}

func (c ImageContent) MarshalJSON() ([]byte, error) {
	type alias ImageContent
	c.Type = ContentTypeImage
	return json.Marshal(alias(c))
}

func NewToolCallContent(id, name string, arguments json.RawMessage) ToolCallContent {
	return ToolCallContent{Type: ContentTypeToolCall, ID: id, Name: name, Arguments: bytes.Clone(arguments)}
}

func (c ToolCallContent) ContentType() ContentType { return ContentTypeToolCall }

func (c ToolCallContent) Validate() error {
	if c.Type != "" && c.Type != ContentTypeToolCall {
		return fmt.Errorf("tool call content type = %q", c.Type)
	}
	if c.ID == "" {
		return errors.New("tool call content requires id")
	}
	if c.Name == "" {
		return errors.New("tool call content requires name")
	}
	if len(c.Arguments) == 0 {
		return errors.New("tool call content requires arguments")
	}
	if !json.Valid(c.Arguments) {
		return errors.New("tool call arguments must be valid json")
	}
	if !aiutils.IsJSONObject(c.Arguments) {
		return errors.New("tool call arguments must be a json object")
	}
	return nil
}

func (c ToolCallContent) ArgumentsMap() (map[string]any, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return aiutils.ParseJSONObject(c.Arguments)
}

func (c ToolCallContent) MarshalJSON() ([]byte, error) {
	type alias ToolCallContent
	c.Type = ContentTypeToolCall
	return json.Marshal(alias(c))
}

func (cs ContentList) MarshalJSON() ([]byte, error) {
	items := make([]json.RawMessage, 0, len(cs))
	for _, content := range cs {
		if content == nil {
			return nil, errors.New("content list contains nil content")
		}
		if err := content.Validate(); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(content)
		if err != nil {
			return nil, err
		}
		items = append(items, raw)
	}
	return json.Marshal(items)
}

func (cs *ContentList) UnmarshalJSON(data []byte) error {
	var rawItems []json.RawMessage
	if err := aiutils.DecodeStrictJSON(data, &rawItems); err != nil {
		return err
	}
	out := make(ContentList, 0, len(rawItems))
	for _, raw := range rawItems {
		content, err := unmarshalContent(raw)
		if err != nil {
			return err
		}
		out = append(out, content)
	}
	*cs = out
	return nil
}

func unmarshalContent(raw json.RawMessage) (Content, error) {
	var header struct {
		Type ContentType `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, err
	}
	switch header.Type {
	case ContentTypeText:
		var c TextContent
		if err := aiutils.DecodeStrictJSON(raw, &c); err != nil {
			return nil, err
		}
		return c, c.Validate()
	case ContentTypeThinking:
		var c ThinkingContent
		if err := aiutils.DecodeStrictJSON(raw, &c); err != nil {
			return nil, err
		}
		return c, c.Validate()
	case ContentTypeImage:
		var c ImageContent
		if err := aiutils.DecodeStrictJSON(raw, &c); err != nil {
			return nil, err
		}
		return c, c.Validate()
	case ContentTypeToolCall:
		var c ToolCallContent
		if err := aiutils.DecodeStrictJSON(raw, &c); err != nil {
			return nil, err
		}
		return c, c.Validate()
	default:
		return nil, fmt.Errorf("unknown content type %q", header.Type)
	}
}
