package ai

import (
	"encoding/json"
	"errors"
	"fmt"

	aiutils "oops/internal/agent/ai/utils"
)

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
	if err := aiutils.DecodeStrictJSON(data, &rawItems); err != nil {
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
		if err := aiutils.DecodeStrictJSON(data, &msg); err != nil {
			return nil, err
		}
		return msg, msg.Validate()
	case RoleAssistant:
		var msg AssistantMessage
		if err := aiutils.DecodeStrictJSON(data, &msg); err != nil {
			return nil, err
		}
		return msg, msg.Validate()
	case RoleToolResult:
		var msg ToolResultMessage
		if err := aiutils.DecodeStrictJSON(data, &msg); err != nil {
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

func AsUserMessage(message AgentMessage) (UserMessage, bool) {
	switch value := message.(type) {
	case UserMessage:
		return value, true
	case *UserMessage:
		if value != nil {
			return *value, true
		}
	}
	return UserMessage{}, false
}

func AsAssistantMessage(message AgentMessage) (AssistantMessage, bool) {
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

func AsToolResultMessage(message AgentMessage) (ToolResultMessage, bool) {
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
