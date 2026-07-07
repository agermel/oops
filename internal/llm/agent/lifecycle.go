package agent

import (
	"github.com/cloudwego/eino/schema"
)

type LifecycleEventType string

const (
	LifecycleAgentStart LifecycleEventType = "agent_start"
	LifecycleMessageEnd LifecycleEventType = "message_end"
	LifecycleToolCall   LifecycleEventType = "tool_call"
	LifecycleToolResult LifecycleEventType = "tool_result"
	LifecycleAnswer     LifecycleEventType = "answer"
	LifecycleError      LifecycleEventType = "error"
	LifecycleAgentEnd   LifecycleEventType = "agent_end"
)

type LifecycleEvent struct {
	Type       LifecycleEventType
	Message    *schema.Message
	Content    string
	ToolName   string
	ToolArgs   string
	ToolCallID string
	Err        error
}
