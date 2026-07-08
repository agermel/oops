package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

type MessageRole string

const (
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleToolResult MessageRole = "toolResult"
)

type StopReason string

const (
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "toolUse"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

type AgentMessage interface {
	MessageRole() MessageRole
	Validate() error
}

type MessageList []AgentMessage

type UserMessage struct {
	Role      MessageRole `json:"role"`
	Content   ContentList `json:"content"`
	Timestamp int64       `json:"timestamp"`
}

func (m UserMessage) MessageRole() MessageRole { return RoleUser }

func (m UserMessage) Validate() error {
	if m.Role != "" && m.Role != RoleUser {
		return fmt.Errorf("user message role = %q", m.Role)
	}
	return validateContentList(m.Content)
}

func (m UserMessage) MarshalJSON() ([]byte, error) {
	type alias UserMessage
	m.Role = RoleUser
	return json.Marshal(alias(m))
}

type AssistantMessage struct {
	Role          MessageRole `json:"role"`
	Content       ContentList `json:"content"`
	Provider      string      `json:"provider,omitempty"`
	Model         string      `json:"model,omitempty"`
	ResponseModel string      `json:"responseModel,omitempty"`
	ResponseID    string      `json:"responseId,omitempty"`
	Usage         Usage       `json:"usage"`
	StopReason    StopReason  `json:"stopReason"`
	ErrorMessage  string      `json:"errorMessage,omitempty"`
	Timestamp     int64       `json:"timestamp"`
}

func (m AssistantMessage) MessageRole() MessageRole { return RoleAssistant }

func (m AssistantMessage) Validate() error {
	if m.Role != "" && m.Role != RoleAssistant {
		return fmt.Errorf("assistant message role = %q", m.Role)
	}
	if !isStopReason(m.StopReason) {
		return fmt.Errorf("unknown stop reason %q", m.StopReason)
	}
	return validateContentList(m.Content)
}

func (m AssistantMessage) MarshalJSON() ([]byte, error) {
	type alias AssistantMessage
	m.Role = RoleAssistant
	return json.Marshal(alias(m))
}

type ToolResultMessage struct {
	Role       MessageRole `json:"role"`
	ToolCallID string      `json:"toolCallId"`
	ToolName   string      `json:"toolName"`
	Content    ContentList `json:"content"`
	Details    any         `json:"details,omitempty"`
	IsError    bool        `json:"isError"`
	Timestamp  int64       `json:"timestamp"`
}

func (m ToolResultMessage) MessageRole() MessageRole { return RoleToolResult }

func (m ToolResultMessage) Validate() error {
	if m.Role != "" && m.Role != RoleToolResult {
		return fmt.Errorf("tool result message role = %q", m.Role)
	}
	if m.ToolCallID == "" {
		return errors.New("tool result message requires toolCallId")
	}
	if m.ToolName == "" {
		return errors.New("tool result message requires toolName")
	}
	return validateContentList(m.Content)
}

func (m ToolResultMessage) MarshalJSON() ([]byte, error) {
	type alias ToolResultMessage
	m.Role = RoleToolResult
	return json.Marshal(alias(m))
}

func (ms MessageList) MarshalJSON() ([]byte, error) {
	items := make([]json.RawMessage, 0, len(ms))
	for _, msg := range ms {
		if msg == nil {
			return nil, errors.New("message list contains nil message")
		}
		if err := msg.Validate(); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		items = append(items, raw)
	}
	return json.Marshal(items)
}

func (ms *MessageList) UnmarshalJSON(data []byte) error {
	var rawItems []json.RawMessage
	if err := json.Unmarshal(data, &rawItems); err != nil {
		return err
	}
	out := make(MessageList, 0, len(rawItems))
	for _, raw := range rawItems {
		msg, err := UnmarshalMessage(raw)
		if err != nil {
			return err
		}
		out = append(out, msg)
	}
	*ms = out
	return nil
}

func UnmarshalMessage(data []byte) (AgentMessage, error) {
	var header struct {
		Role MessageRole `json:"role"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	switch header.Role {
	case RoleUser:
		var msg UserMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		return msg, msg.Validate()
	case RoleAssistant:
		var msg AssistantMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		return msg, msg.Validate()
	case RoleToolResult:
		var msg ToolResultMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		return msg, msg.Validate()
	default:
		return nil, fmt.Errorf("unknown message role %q", header.Role)
	}
}

func validateContentList(content ContentList) error {
	for _, item := range content {
		if item == nil {
			return errors.New("content list contains nil content")
		}
		if err := item.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func isStopReason(reason StopReason) bool {
	switch reason {
	case StopReasonStop, StopReasonLength, StopReasonToolUse, StopReasonError, StopReasonAborted:
		return true
	default:
		return false
	}
}
