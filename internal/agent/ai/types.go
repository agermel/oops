// Package ai defines the agent model protocol.
package ai

import (
	"context"
	"encoding/json"
)

// Content

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

type ThinkingContent struct {
	Type              ContentType `json:"type"`
	Thinking          string      `json:"thinking"`
	ThinkingSignature string      `json:"thinkingSignature,omitempty"`
	Redacted          bool        `json:"redacted,omitempty"`
}

type ImageContent struct {
	Type     ContentType `json:"type"`
	Data     string      `json:"data,omitempty"`
	MIMEType string      `json:"mimeType,omitempty"`
	URL      string      `json:"url,omitempty"`
	Detail   string      `json:"detail,omitempty"`
}

type ToolCallContent struct {
	Type             ContentType     `json:"type"`
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
}

// Messages

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

type ToolResultMessage struct {
	Role       MessageRole `json:"role"`
	ToolCallID string      `json:"toolCallId"`
	ToolName   string      `json:"toolName"`
	Content    ContentList `json:"content"`
	Details    any         `json:"details,omitempty"`
	IsError    bool        `json:"isError"`
	Timestamp  int64       `json:"timestamp"`
}

// Tools and usage

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ToolResult struct {
	Content   ContentList `json:"content"`
	Details   any         `json:"details,omitempty"`
	Terminate bool        `json:"terminate,omitempty"`
}

type Usage struct {
	Input        int        `json:"input"`
	Output       int        `json:"output"`
	CacheRead    int        `json:"cacheRead"`
	CacheWrite   int        `json:"cacheWrite"`
	CacheWrite1h *int       `json:"cacheWrite1h,omitempty"`
	Reasoning    *int       `json:"reasoning,omitempty"`
	TotalTokens  int        `json:"totalTokens"`
	Cost         *UsageCost `json:"cost,omitempty"`
}

type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// Context and calls

type Context struct {
	SystemPrompt string           `json:"systemPrompt,omitempty"`
	Messages     MessageList      `json:"messages"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
}

type StreamRequest struct {
	Context   Context
	Model     string
	Provider  string
	Reasoning string
	SessionID string
}

type StreamFunc func(context.Context, StreamRequest) (*AssistantMessageEventStream, error)

// Events

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

// Provider sequence analysis

type ProviderSequenceStatus string

const (
	ProviderSequenceComplete    ProviderSequenceStatus = "complete"
	ProviderSequenceRecoverable ProviderSequenceStatus = "recoverable"
	ProviderSequenceInvalid     ProviderSequenceStatus = "invalid"
)

type ProviderSequenceAnalysis struct {
	Status                ProviderSequenceStatus
	PendingAssistantIndex int
	Err                   error
}
