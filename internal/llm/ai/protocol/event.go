package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

type AssistantMessageEventType string

const (
	AssistantEventStart         AssistantMessageEventType = "start"
	AssistantEventTextStart     AssistantMessageEventType = "text_start"
	AssistantEventTextDelta     AssistantMessageEventType = "text_delta"
	AssistantEventTextEnd       AssistantMessageEventType = "text_end"
	AssistantEventThinkingStart AssistantMessageEventType = "thinking_start"
	AssistantEventThinkingDelta AssistantMessageEventType = "thinking_delta"
	AssistantEventThinkingEnd   AssistantMessageEventType = "thinking_end"
	AssistantEventToolCallStart AssistantMessageEventType = "toolcall_start"
	AssistantEventToolCallDelta AssistantMessageEventType = "toolcall_delta"
	AssistantEventToolCallEnd   AssistantMessageEventType = "toolcall_end"
	AssistantEventDone          AssistantMessageEventType = "done"
	AssistantEventError         AssistantMessageEventType = "error"
)

type AssistantMessageEvent struct {
	Type         AssistantMessageEventType `json:"type"`
	ContentIndex *int                      `json:"contentIndex,omitempty"`
	Delta        string                    `json:"delta,omitempty"`
	Content      string                    `json:"content,omitempty"`
	ToolCall     *ToolCallContent          `json:"toolCall,omitempty"`
	Partial      *AssistantMessage         `json:"partial,omitempty"`
	Reason       StopReason                `json:"reason,omitempty"`
	Message      *AssistantMessage         `json:"message,omitempty"`
	Error        *AssistantMessage         `json:"error,omitempty"`
}

func (e AssistantMessageEvent) Validate() error {
	switch e.Type {
	case AssistantEventStart:
		if e.Partial == nil {
			return errors.New("start event requires partial")
		}
		return e.Partial.Validate()
	case AssistantEventTextStart, AssistantEventThinkingStart, AssistantEventToolCallStart:
		if e.ContentIndex == nil {
			return fmt.Errorf("%s event requires contentIndex", e.Type)
		}
		if e.Partial == nil {
			return fmt.Errorf("%s event requires partial", e.Type)
		}
		return e.Partial.Validate()
	case AssistantEventTextDelta, AssistantEventThinkingDelta, AssistantEventToolCallDelta:
		if e.ContentIndex == nil {
			return fmt.Errorf("%s event requires contentIndex", e.Type)
		}
		if e.Delta == "" {
			return fmt.Errorf("%s event requires delta", e.Type)
		}
		if e.Partial == nil {
			return fmt.Errorf("%s event requires partial", e.Type)
		}
		return e.Partial.Validate()
	case AssistantEventTextEnd, AssistantEventThinkingEnd:
		if e.ContentIndex == nil {
			return fmt.Errorf("%s event requires contentIndex", e.Type)
		}
		if e.Content == "" {
			return fmt.Errorf("%s event requires content", e.Type)
		}
		if e.Partial == nil {
			return fmt.Errorf("%s event requires partial", e.Type)
		}
		return e.Partial.Validate()
	case AssistantEventToolCallEnd:
		if e.ContentIndex == nil {
			return fmt.Errorf("%s event requires contentIndex", e.Type)
		}
		if e.ToolCall == nil {
			return errors.New("toolcall_end event requires toolCall")
		}
		if err := e.ToolCall.Validate(); err != nil {
			return err
		}
		if e.Partial == nil {
			return fmt.Errorf("%s event requires partial", e.Type)
		}
		return e.Partial.Validate()
	case AssistantEventDone:
		if e.Message == nil {
			return errors.New("done event requires message")
		}
		if !isSuccessfulTerminalReason(e.Reason) {
			return fmt.Errorf("done reason = %q", e.Reason)
		}
		return e.Message.Validate()
	case AssistantEventError:
		if e.Error == nil {
			return errors.New("error event requires error message")
		}
		if e.Reason != StopReasonError && e.Reason != StopReasonAborted {
			return fmt.Errorf("error reason = %q", e.Reason)
		}
		return e.Error.Validate()
	default:
		return fmt.Errorf("unknown assistant event type %q", e.Type)
	}
}

func (e AssistantMessageEvent) FinalMessage() (*AssistantMessage, bool) {
	switch e.Type {
	case AssistantEventDone:
		return e.Message, e.Message != nil
	case AssistantEventError:
		return e.Error, e.Error != nil
	default:
		return nil, false
	}
}

type AgentEventType string

const (
	AgentEventAgentStart          AgentEventType = "agent_start"
	AgentEventAgentEnd            AgentEventType = "agent_end"
	AgentEventTurnStart           AgentEventType = "turn_start"
	AgentEventTurnEnd             AgentEventType = "turn_end"
	AgentEventMessageStart        AgentEventType = "message_start"
	AgentEventMessageUpdate       AgentEventType = "message_update"
	AgentEventMessageEnd          AgentEventType = "message_end"
	AgentEventToolExecutionStart  AgentEventType = "tool_execution_start"
	AgentEventToolExecutionUpdate AgentEventType = "tool_execution_update"
	AgentEventToolExecutionEnd    AgentEventType = "tool_execution_end"
)

type AgentEvent struct {
	Type                  AgentEventType         `json:"type"`
	Turn                  int                    `json:"turn,omitempty"`
	Message               AgentMessage           `json:"message,omitempty"`
	Messages              MessageList            `json:"messages,omitempty"`
	ToolResults           []ToolResultMessage    `json:"toolResults,omitempty"`
	AssistantMessageEvent *AssistantMessageEvent `json:"assistantMessageEvent,omitempty"`
	Delta                 string                 `json:"delta,omitempty"`
	ToolCallID            string                 `json:"toolCallId,omitempty"`
	ToolName              string                 `json:"toolName,omitempty"`
	Args                  json.RawMessage        `json:"args,omitempty"`
	Result                *ToolResult            `json:"result,omitempty"`
	IsError               bool                   `json:"isError,omitempty"`
}

func (e AgentEvent) MarshalJSON() ([]byte, error) {
	type wire AgentEvent
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(wire(e))
}

func (e AgentEvent) Validate() error {
	if e.Message != nil {
		if err := e.Message.Validate(); err != nil {
			return err
		}
	}
	if e.AssistantMessageEvent != nil {
		if err := e.AssistantMessageEvent.Validate(); err != nil {
			return err
		}
	}
	switch e.Type {
	case AgentEventMessageUpdate:
		if e.Message == nil {
			return errors.New("message_update event requires message")
		}
		if e.AssistantMessageEvent == nil {
			return errors.New("message_update event requires assistantMessageEvent")
		}
	case AgentEventToolExecutionStart:
		if e.ToolCallID == "" {
			return errors.New("tool_execution_start event requires toolCallId")
		}
		if e.ToolName == "" {
			return errors.New("tool_execution_start event requires toolName")
		}
		if len(e.Args) == 0 || !json.Valid(e.Args) {
			return errors.New("tool_execution_start event requires valid args json")
		}
		if !isJSONObject(e.Args) {
			return errors.New("tool_execution_start event args must be a json object")
		}
	}
	return nil
}

func (e *AgentEvent) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type                  AgentEventType         `json:"type"`
		Turn                  int                    `json:"turn,omitempty"`
		Message               json.RawMessage        `json:"message,omitempty"`
		Messages              MessageList            `json:"messages,omitempty"`
		ToolResults           []ToolResultMessage    `json:"toolResults,omitempty"`
		AssistantMessageEvent *AssistantMessageEvent `json:"assistantMessageEvent,omitempty"`
		Delta                 string                 `json:"delta,omitempty"`
		ToolCallID            string                 `json:"toolCallId,omitempty"`
		ToolName              string                 `json:"toolName,omitempty"`
		Args                  json.RawMessage        `json:"args,omitempty"`
		Result                *ToolResult            `json:"result,omitempty"`
		IsError               bool                   `json:"isError,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var msg AgentMessage
	if len(raw.Message) > 0 && string(raw.Message) != "null" {
		converted, err := UnmarshalMessage(raw.Message)
		if err != nil {
			return err
		}
		msg = converted
	}
	*e = AgentEvent{
		Type:                  raw.Type,
		Turn:                  raw.Turn,
		Message:               msg,
		Messages:              raw.Messages,
		ToolResults:           raw.ToolResults,
		AssistantMessageEvent: raw.AssistantMessageEvent,
		Delta:                 raw.Delta,
		ToolCallID:            raw.ToolCallID,
		ToolName:              raw.ToolName,
		Args:                  raw.Args,
		Result:                raw.Result,
		IsError:               raw.IsError,
	}
	return e.Validate()
}

func isSuccessfulTerminalReason(reason StopReason) bool {
	switch reason {
	case StopReasonStop, StopReasonLength, StopReasonToolUse:
		return true
	default:
		return false
	}
}
