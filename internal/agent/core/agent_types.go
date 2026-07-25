package core

import (
	"context"
	"errors"

	protocol "oops/internal/agent/ai"
)

const defaultMaxTurns = 15

var (
	ErrMissingStream = errors.New("agent loop requires stream function")
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
	TransformContext    func(context.Context, protocol.MessageList) (protocol.MessageList, error)
	ConvertToLLM        func(context.Context, protocol.MessageList) (protocol.MessageList, error)
	PrepareNextTurn     func(context.Context, TurnContext) (TurnUpdate, error)
	ShouldStopAfterTurn func(context.Context, TurnContext) (bool, error)
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
