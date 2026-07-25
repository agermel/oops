package core

import (
	"context"
	"encoding/json"

	protocol "oops/internal/agent/ai"
)

type ExecutionMode string

const (
	ExecutionModeParallel   ExecutionMode = "parallel"
	ExecutionModeSequential ExecutionMode = "sequential"
)

type Tool interface {
	Definition() protocol.ToolDefinition
	ExecutionMode() ExecutionMode
	PrepareArguments(context.Context, json.RawMessage) (json.RawMessage, error)
	Execute(context.Context, ToolCall, ToolUpdateSink) (protocol.ToolResult, error)
}

type ToolUpdateSink func(context.Context, protocol.AgentEvent) error

type ToolCall struct {
	ID           string
	Name         string
	RawArguments json.RawMessage
	Arguments    map[string]any
}

type ContextSnapshot struct {
	SystemPrompt string
	Messages     protocol.MessageList
	Tools        []protocol.ToolDefinition
}

type ToolRunner interface {
	ExecuteTools(context.Context, ToolRunRequest) (ToolRunResult, error)
}

type ToolRunRequest struct {
	Turn             int
	Context          ContextSnapshot
	AssistantMessage protocol.AssistantMessage
	ToolCalls        []protocol.ToolCallContent
	Emit             EventSink
}

type ToolRunResult struct {
	Messages  []protocol.ToolResultMessage
	Terminate bool
}

type Hooks struct {
	BeforeToolCall BeforeToolCallHook
	AfterToolCall  AfterToolCallHook
}

type BeforeToolCallHook func(context.Context, BeforeToolCallContext) (BeforeToolCallResult, error)

type BeforeToolCallContext struct {
	AssistantMessage protocol.AssistantMessage
	ToolCall         protocol.ToolCallContent
	Arguments        map[string]any
	RawArguments     json.RawMessage
	Context          ContextSnapshot
}

type BeforeToolCallResult struct {
	Arguments json.RawMessage
	Block     bool
	Reason    string
}

type AfterToolCallHook func(context.Context, AfterToolCallContext) (AfterToolCallResult, error)

type AfterToolCallContext struct {
	AssistantMessage protocol.AssistantMessage
	ToolCall         protocol.ToolCallContent
	Arguments        map[string]any
	RawArguments     json.RawMessage
	Result           protocol.ToolResult
	IsError          bool
	Context          ContextSnapshot
}

type AfterToolCallResult struct {
	Content    protocol.ContentList
	HasContent bool
	Details    any
	HasDetails bool
	IsError    *bool
	Terminate  *bool
}

type RunnerConfig struct {
	Registry      *Registry
	Tools         []Tool
	ExecutionMode ExecutionMode
	Hooks         Hooks
}

type Runner struct {
	registry *Registry
	mode     ExecutionMode
	hooks    Hooks
}

var _ ToolRunner = (*Runner)(nil)
