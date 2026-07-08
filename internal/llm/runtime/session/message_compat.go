package session

import (
	"encoding/json"
	"errors"

	"oops/internal/llm/ai/protocol"
)

func unmarshalEntryMessage(raw json.RawMessage) (protocol.AgentMessage, error) {
	message, err := protocol.UnmarshalMessage(raw)
	if err == nil {
		return message, nil
	}
	return unmarshalOldMessage(raw)
}

type oldMessage struct {
	Role            string        `json:"role"`
	Content         string        `json:"content"`
	ToolCallID      string        `json:"toolCallId,omitempty"`
	ToolName        string        `json:"toolName,omitempty"`
	ToolCallIDSnake string        `json:"tool_call_id,omitempty"`
	ToolNameSnake   string        `json:"tool_name,omitempty"`
	ToolCalls       []oldToolCall `json:"tool_calls,omitempty"`
}

type oldToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function oldToolCallFunction `json:"function"`
}

type oldToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func unmarshalOldMessage(raw json.RawMessage) (protocol.AgentMessage, error) {
	var msg oldMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	content := protocol.ContentList{protocol.NewTextContent(msg.Content)}
	switch msg.Role {
	case "user":
		return protocol.UserMessage{Content: content}, nil
	case "assistant":
		for _, call := range msg.ToolCalls {
			args := json.RawMessage(call.Function.Arguments)
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			content = append(content, protocol.NewToolCallContent(call.ID, call.Function.Name, args))
		}
		stopReason := protocol.StopReasonStop
		if len(msg.ToolCalls) > 0 {
			stopReason = protocol.StopReasonToolUse
		}
		return protocol.AssistantMessage{
			Content:    content,
			StopReason: stopReason,
		}, nil
	case "tool":
		toolCallID := msg.ToolCallID
		if toolCallID == "" {
			toolCallID = msg.ToolCallIDSnake
		}
		toolName := msg.ToolName
		if toolName == "" {
			toolName = msg.ToolNameSnake
		}
		return protocol.ToolResultMessage{
			ToolCallID: toolCallID,
			ToolName:   toolName,
			Content:    content,
		}, nil
	default:
		return nil, errors.New("unsupported message role")
	}
}
