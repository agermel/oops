package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

type ContentType string

const (
	ContentTypeText     ContentType = "text"
	ContentTypeThinking ContentType = "thinking"
	ContentTypeImage    ContentType = "image"
	ContentTypeToolCall ContentType = "toolCall"
)

type Content interface {
	ContentType() ContentType
	Validate() error
}

type ContentList []Content

type TextContent struct {
	Type          ContentType `json:"type"`
	Text          string      `json:"text"`
	TextSignature string      `json:"textSignature,omitempty"`
}

func NewTextContent(text string) TextContent {
	return TextContent{Type: ContentTypeText, Text: text}
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

type ThinkingContent struct {
	Type              ContentType `json:"type"`
	Thinking          string      `json:"thinking"`
	ThinkingSignature string      `json:"thinkingSignature,omitempty"`
	Redacted          bool        `json:"redacted,omitempty"`
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

type ImageContent struct {
	Type     ContentType `json:"type"`
	Data     string      `json:"data,omitempty"`
	MIMEType string      `json:"mimeType,omitempty"`
	URL      string      `json:"url,omitempty"`
	Detail   string      `json:"detail,omitempty"`
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

type ToolCallContent struct {
	Type             ContentType     `json:"type"`
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
}

func NewToolCallContent(id, name string, arguments json.RawMessage) ToolCallContent {
	return ToolCallContent{Type: ContentTypeToolCall, ID: id, Name: name, Arguments: cloneRaw(arguments)}
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
	if !isJSONObject(c.Arguments) {
		return errors.New("tool call arguments must be a json object")
	}
	return nil
}

func (c ToolCallContent) ArgumentsMap() (map[string]any, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(c.Arguments, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("tool call arguments must be a json object")
	}
	return out, nil
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
	if err := json.Unmarshal(data, &rawItems); err != nil {
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
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		return c, c.Validate()
	case ContentTypeThinking:
		var c ThinkingContent
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		return c, c.Validate()
	case ContentTypeImage:
		var c ImageContent
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		return c, c.Validate()
	case ContentTypeToolCall:
		var c ToolCallContent
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		return c, c.Validate()
	default:
		return nil, fmt.Errorf("unknown content type %q", header.Type)
	}
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func isJSONObject(raw json.RawMessage) bool {
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	return out != nil
}
