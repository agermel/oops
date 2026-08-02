package core

import (
	"context"
	"errors"

	protocol "oops/internal/agent/ai"
)

const defaultMaxTurns = 15

var (
	ErrMissingStream         = errors.New("agent loop requires stream function")
	ErrContinueEmptyContext  = errors.New("continue requires existing context")
	ErrContinueFromAssistant = errors.New("continue requires pending tool result or user context")
)

type QueueMode string

const (
	QueueModeOneAtATime QueueMode = "one-at-a-time"
	QueueModeAll        QueueMode = "all"
)

type EventSink func(context.Context, protocol.AgentEvent) error

type AgentContext struct {
	SystemPrompt string
	Messages     protocol.MessageList
	Tools        []protocol.ToolDefinition
}

type StreamRequest = protocol.StreamRequest

type StreamFn = protocol.StreamFunc

type AgentLoopConfig struct {
	Model               string
	Provider            string
	Reasoning           string
	SessionID           string
	MaxTurns            int
	Stream              StreamFn
	ToolRunner          ToolRunner
	BeforeToolCall      BeforeToolCallHook
	AfterToolCall       AfterToolCallHook
	TransformContext    func(context.Context, protocol.MessageList) (protocol.MessageList, error)
	ConvertToLLM        func(context.Context, protocol.MessageList) (protocol.MessageList, error)
	PrepareNextTurn     func(context.Context, TurnContext) (TurnUpdate, error)
	ShouldStopAfterTurn func(context.Context, TurnContext) (bool, error)
	GetSteeringMessages func(context.Context) (protocol.MessageList, error)
	GetFollowUpMessages func(context.Context) (protocol.MessageList, error)
}

type TurnContext struct {
	Message     protocol.AssistantMessage
	ToolResults []protocol.ToolResultMessage
	Context     AgentContext
	NewMessages protocol.MessageList
	Turn        int
}

type TurnUpdate struct {
	Context    *AgentContext
	Model      string
	Provider   string
	Reasoning  string
	ToolRunner ToolRunner
}
