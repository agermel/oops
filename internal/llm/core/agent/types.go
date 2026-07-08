package agent

import (
	"context"
	"errors"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/core/toolruntime"
)

const defaultMaxTurns = 15

var (
	ErrContinueEmptyContext  = errors.New("continue requires existing context")
	ErrContinueFromAssistant = errors.New("continue requires pending tool result or user context")
	ErrMissingStream         = errors.New("agent loop requires stream function")
)

type EventSink func(context.Context, protocol.AgentEvent) error

type AgentContext struct {
	SystemPrompt string
	Messages     protocol.MessageList
	Tools        []protocol.ToolDefinition
}

type StreamRequest struct {
	Context   protocol.Context
	Model     string
	Provider  string
	Reasoning string
	SessionID string
}

type StreamFn func(context.Context, StreamRequest) (*protocol.AssistantMessageEventStream, error)

type AgentLoopConfig struct {
	Model               string
	Provider            string
	Reasoning           string
	SessionID           string
	MaxTurns            int
	Stream              StreamFn
	ToolRunner          toolruntime.ToolRunner
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
	ToolRunner toolruntime.ToolRunner
}
