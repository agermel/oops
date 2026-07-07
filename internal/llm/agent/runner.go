package agent

import (
	"context"
	"reflect"

	"oops/internal/logutil"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/flow/agent/react"
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
	opt, future := react.WithMessageFuture()

	reactAgent, err := newAgentWithToolsConfig(ctx, req.Model, toolsConfigForRun(req.Tools, req.Hooks), req.MaxStep)
	if err != nil {
		logutil.Error("llm: create agent", zap.Error(err))
		return nil, err
	}

	events := make(chan LifecycleEvent, 100)

	go func() {
		defer close(events)

		agentCtx, agentCancel := context.WithCancel(ctx)
		defer agentCancel()

		sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleAgentStart})
		defer sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleAgentEnd})

		type generateResult struct {
			msg *schema.Message
			err error
		}
		genDone := make(chan generateResult, 1)

		go func() {
			resp, err := reactAgent.Generate(agentCtx, req.Messages, opt)
			genDone <- generateResult{msg: resp, err: err}
		}()

		iter := future.GetMessages()
		var pendingToolCalls []pendingToolCall
		var lastFutureMsg *schema.Message
		for {
			msg, ok, err := iter.Next()
			if err != nil {
				logutil.Error("llm: iter", zap.Error(err))
				r.flushPendingToolErrors(agentCtx, events, pendingToolCalls, req.Hooks.OnMessage, err)
				sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleError, Content: sanitizeError(err.Error()), Err: err})
				return
			}
			if !ok {
				break
			}

			lastFutureMsg = msg
			pendingToolCalls = trackPendingToolCalls(pendingToolCalls, msg)
			displayMsg := applyAfterToolResult(agentCtx, msg, req.Hooks.AfterToolResult)
			persistMsg := displayMsg
			if persistMsg == nil && msg != nil && msg.Role == schema.Tool && msg.ToolCallID != "" {
				persistMsg = msg
			}

			if req.Hooks.OnMessage != nil && persistMsg != nil {
				if err := req.Hooks.OnMessage(agentCtx, persistMsg); err != nil {
					logutil.Error("llm: onMessage", zap.Error(err))
					sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleError, Content: sanitizeError(err.Error()), Err: err})
					return
				}
			}

			if displayMsg == nil {
				continue
			}
			if !r.emitMessageLifecycle(ctx, events, displayMsg) {
				return
			}
		}

		result := <-genDone
		if result.err != nil {
			logutil.Error("llm: generate", zap.Error(result.err))
			r.flushPendingToolErrors(agentCtx, events, pendingToolCalls, req.Hooks.OnMessage, result.err)
			sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleError, Content: sanitizeError(result.err.Error()), Err: result.err})
			return
		}

		if result.msg == nil {
			return
		}

		if !sameSchemaMessage(lastFutureMsg, result.msg) {
			if req.Hooks.OnMessage != nil {
				if err := req.Hooks.OnMessage(agentCtx, result.msg); err != nil {
					logutil.Error("llm: onMessage final", zap.Error(err))
				}
			}

			if !r.emitMessageLifecycle(ctx, events, result.msg) {
				return
			}
		}
		sendLifecycle(ctx, events, LifecycleEvent{
			Type:    LifecycleAnswer,
			Message: result.msg,
			Content: result.msg.Content,
		})
	}()

	return events, nil
}

func sameSchemaMessage(a, b *schema.Message) bool {
	if a == nil || b == nil {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}

func applyAfterToolResult(ctx context.Context, msg *schema.Message, hook ToolResultHook) *schema.Message {
	if msg == nil || hook == nil || msg.Role != schema.Tool || msg.ToolName == "" {
		return msg
	}
	copied := *msg
	return hook(ctx, &copied)
}

func (r *Runner) emitMessageLifecycle(ctx context.Context, events chan<- LifecycleEvent, msg *schema.Message) bool {
	if !sendLifecycle(ctx, events, LifecycleEvent{Type: LifecycleMessageEnd, Message: msg}) {
		return false
	}
	if msg == nil {
		return true
	}
	if msg.ToolCallID != "" {
		return sendLifecycle(ctx, events, LifecycleEvent{
			Type:       LifecycleToolResult,
			Message:    msg,
			Content:    msg.Content,
			ToolName:   msg.ToolName,
			ToolCallID: msg.ToolCallID,
		})
	}
	for _, tc := range msg.ToolCalls {
		if !sendLifecycle(ctx, events, LifecycleEvent{
			Type:       LifecycleToolCall,
			Message:    msg,
			Content:    tc.Function.Name,
			ToolName:   tc.Function.Name,
			ToolArgs:   tc.Function.Arguments,
			ToolCallID: tc.ID,
		}) {
			return false
		}
	}
	return true
}

func (r *Runner) flushPendingToolErrors(ctx context.Context, events chan<- LifecycleEvent, pending []pendingToolCall, onMessage MessageCallback, cause error) {
	if len(pending) == 0 || cause == nil {
		return
	}
	for _, msg := range pendingToolErrorMessages(pending, cause) {
		if onMessage != nil {
			if err := onMessage(ctx, msg); err != nil {
				logutil.Error("llm: onMessage pending tool error", zap.Error(err))
				continue
			}
		}
		if !r.emitMessageLifecycle(ctx, events, msg) {
			return
		}
	}
}

func sendLifecycle(ctx context.Context, ch chan<- LifecycleEvent, evt LifecycleEvent) bool {
	select {
	case ch <- evt:
		return true
	case <-ctx.Done():
		return false
	}
}
