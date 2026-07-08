package agent

import (
	"context"
	"strings"

	"oops/internal/llm/ai/protocol"
	protoeino "oops/internal/llm/ai/protocol/einoadapter"
	coreagent "oops/internal/llm/core/agent"
	"oops/internal/llm/core/toolruntime"
	tooladapter "oops/internal/llm/core/toolruntime/einoadapter"
	"oops/internal/logutil"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

type RunHooks struct {
	OnMessage             MessageCallback
	BeforeToolCall        BeforeToolCallHook
	SemanticAfterToolCall SemanticToolResultHook
	AfterToolResult       ToolResultHook
}

type RunRequest struct {
	Model    model.ToolCallingChatModel
	Tools    []tool.InvokableTool
	Messages []*schema.Message
	MaxStep  int
	Hooks    RunHooks
}

type Runner struct{}

func NewRunner() *Runner {
	return &Runner{}
}

func (r *Runner) Run(ctx context.Context, req RunRequest) (<-chan LifecycleEvent, error) {
	agentContext, prompts, continueRun, err := buildCoreRunInput(req.Messages)
	if err != nil {
		return nil, err
	}

	runtimeTools, definitions, err := tooladapter.FromInvokableTools(ctx, req.Tools)
	if err != nil {
		return nil, err
	}
	agentContext.Tools = definitions
	toolRunner, err := toolruntime.NewRunner(toolruntime.RunnerConfig{
		Tools: runtimeTools,
		Hooks: toolruntime.Hooks{
			BeforeToolCall: bridgeBeforeToolCall(req.Hooks.BeforeToolCall),
			AfterToolCall:  bridgeAfterToolCall(req.Hooks.SemanticAfterToolCall),
		},
	})
	if err != nil {
		return nil, err
	}

	streamFn, err := newEinoModelStreamFn(ctx, req.Model, req.Tools)
	if err != nil {
		return nil, err
	}
	config := coreagent.AgentLoopConfig{
		MaxTurns:   req.MaxStep,
		Stream:     streamFn,
		ToolRunner: toolRunner,
	}

	events := make(chan LifecycleEvent, 100)
	go func() {
		defer close(events)
		emit := func(eventCtx context.Context, event protocol.AgentEvent) error {
			return r.emitProtocolEvent(eventCtx, events, event, req.Hooks)
		}
		var runErr error
		if continueRun {
			_, runErr = coreagent.RunAgentLoopContinue(ctx, agentContext, config, emit)
		} else {
			_, runErr = coreagent.RunAgentLoop(ctx, prompts, agentContext, config, emit)
		}
		if runErr != nil && ctx.Err() == nil {
			logutil.Error("llm: core agent run", zap.Error(runErr))
			sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleError, Content: sanitizeError(runErr.Error()), Err: runErr})
			sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleAgentEnd})
		}
	}()

	return events, nil
}

func buildCoreRunInput(messages []*schema.Message) (coreagent.AgentContext, protocol.MessageList, bool, error) {
	var systemSections []string
	var converted protocol.MessageList
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if msg.Role == schema.System {
			if msg.Content != "" {
				systemSections = append(systemSections, msg.Content)
			}
			continue
		}
		item, err := protoeino.FromEinoMessage(msg)
		if err != nil {
			return coreagent.AgentContext{}, nil, false, err
		}
		converted = append(converted, item)
	}

	agentContext := coreagent.AgentContext{
		SystemPrompt: strings.Join(systemSections, "\n\n"),
	}
	if len(converted) == 0 {
		return agentContext, nil, false, nil
	}
	last := converted[len(converted)-1]
	if last.MessageRole() == protocol.RoleUser {
		agentContext.Messages = protocol.CloneMessageList(converted[:len(converted)-1])
		return agentContext, protocol.CloneMessageList(converted[len(converted)-1:]), false, nil
	}
	agentContext.Messages = protocol.CloneMessageList(converted)
	return agentContext, nil, true, nil
}

func sendLifecycle(ctx context.Context, ch chan<- LifecycleEvent, evt LifecycleEvent) bool {
	select {
	case ch <- evt:
		return true
	case <-ctx.Done():
		return false
	}
}
