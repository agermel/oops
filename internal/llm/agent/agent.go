package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"oops/internal/config"
	agentevents "oops/internal/llm/events"
	"oops/internal/logutil"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

var pydanticTraceRe = regexp.MustCompile(`\n For further information visit https?://[^\s]+`)

// sanitizeError 清洗工具/MCP 层的错误信息。
func sanitizeError(raw string) string {
	s := pydanticTraceRe.ReplaceAllString(raw, "")
	s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return raw
	}
	return s
}

// MessageCallback 是 Ask 在产生每条新消息时调用的回调。
// ctx 是 agent 的内部 context；若回调返回非 nil 错误，Ask 会终止执行。
type MessageCallback func(ctx context.Context, msg *schema.Message) error

type ToolCallInput struct {
	Name      string
	Arguments string
	CallID    string // Eino v0.9.10 的 before hook 不暴露 call id；semantic hook 会填充。
}

type ToolCallOutput struct {
	Result string
}

// BeforeToolCallHook 在工具执行前处理参数。
// 返回值是实际传给工具的 JSON 参数；返回 error 表示拒绝本次工具执行。
type BeforeToolCallHook func(ctx context.Context, input ToolCallInput) (string, error)

// SemanticToolResultHook 在工具执行后处理语义结果。
// 返回值会进入 ReAct 后续上下文、MessageFuture 和下游回调。
type SemanticToolResultHook func(ctx context.Context, input ToolCallInput, output ToolCallOutput) (string, error)

// ToolResultHook 是工具结果后处理回调。
// 在工具结果进入 OnMessage 和生命周期事件前调用，可修改 msg.Content。
// 返回 nil 表示丢弃该工具结果事件。
type ToolResultHook func(ctx context.Context, msg *schema.Message) *schema.Message

var afterToolCallMu sync.RWMutex

// AfterToolCall 是按工具名注册的旧工具结果后处理 hook。
// key 为工具名（如 "get_logs"、"list_containers"），MCP 工具也使用其工具名。
// 仅在 msg.Role == Tool 时触发。
//
// Deprecated: 新运行时代码使用 RunHooks.BeforeToolCall 和
// RunHooks.SemanticAfterToolCall。此 map 仅作为 display/persistence 兼容桥保留。
// 直接修改应发生在启动期；运行期注册或移除使用 RegisterAfterToolCall。
var AfterToolCall = map[string]ToolResultHook{}

// RegisterAfterToolCall 注册或移除旧 display/persistence 工具结果 hook。
// hook 为 nil 时删除对应工具名。
func RegisterAfterToolCall(name string, hook ToolResultHook) {
	afterToolCallMu.Lock()
	defer afterToolCallMu.Unlock()
	if hook == nil {
		delete(AfterToolCall, name)
		return
	}
	AfterToolCall[name] = hook
}

func lookupAfterToolCall(name string) (ToolResultHook, bool) {
	afterToolCallMu.RLock()
	defer afterToolCallMu.RUnlock()
	hook, ok := AfterToolCall[name]
	return hook, ok
}

// newModel 创建 OpenAI 兼容的 ChatModel。
func newModel(ctx context.Context, cfg config.LLMConfig) (model.ToolCallingChatModel, error) {
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   cfg.Model,
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model: %w", err)
	}
	return chatModel, nil
}

func toolsConfigForRun(tools []tool.InvokableTool, hooks RunHooks) compose.ToolsNodeConfig {
	baseTools := make([]tool.BaseTool, len(tools))
	for i, t := range tools {
		baseTools[i] = t
	}

	config := compose.ToolsNodeConfig{Tools: baseTools}
	if hooks.BeforeToolCall != nil {
		config.ToolArgumentsHandler = func(ctx context.Context, name, arguments string) (string, error) {
			return hooks.BeforeToolCall(ctx, ToolCallInput{
				Name:      name,
				Arguments: arguments,
			})
		}
	}
	if hooks.SemanticAfterToolCall != nil {
		config.ToolCallMiddlewares = []compose.ToolMiddleware{{
			Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
				return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
					output, err := next(ctx, input)
					if err != nil {
						return nil, err
					}
					if output == nil {
						output = &compose.ToolOutput{}
					}
					result, err := hooks.SemanticAfterToolCall(ctx, ToolCallInput{
						Name:      input.Name,
						Arguments: input.Arguments,
						CallID:    input.CallID,
					}, ToolCallOutput{Result: output.Result})
					if err != nil {
						return nil, err
					}
					output.Result = result
					return output, nil
				}
			},
		}}
	}
	return config
}

// newAgent 创建 ReAct Agent（不含 MessageFuture option）。
func newAgent(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.InvokableTool, maxStep int) (*react.Agent, error) {
	return newAgentWithToolsConfig(ctx, chatModel, toolsConfigForRun(tools, RunHooks{}), maxStep)
}

func newAgentWithToolsConfig(ctx context.Context, chatModel model.ToolCallingChatModel, toolsConfig compose.ToolsNodeConfig, maxStep int) (*react.Agent, error) {
	if maxStep <= 0 {
		maxStep = 15
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig:      toolsConfig,
		MaxStep:          maxStep,
	})
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}
	return agent, nil
}

// Ask 向 LLM Agent 提问，通过 channel 流式返回每一步执行过程。
// messages 是完整的消息列表，由调用方构建（通常包含 system prompt + 历史消息 + 当前问题）。
// onMessage 在 agent 产生每条新消息（assistant 输出、工具结果）时被调用，用于持久化到 session。
// maxStep 控制 Agent 最大步数；<=0 时使用默认值 15。
// 调用方需要从 channel 读取 StepEvent 直到 channel 关闭。
// 若 agent 创建失败，返回 error（此时 channel 为 nil）。
func Ask(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.InvokableTool, messages []*schema.Message, onMessage MessageCallback, maxStep int) (<-chan agentevents.StepEvent, error) {
	lifecycleEvents, err := NewRunner().Run(ctx, RunRequest{
		Model:    chatModel,
		Tools:    tools,
		Messages: messages,
		MaxStep:  maxStep,
		Hooks: RunHooks{
			OnMessage:       onMessage,
			AfterToolResult: afterToolCallBridge,
		},
	})
	if err != nil {
		return nil, err
	}

	events := make(chan agentevents.StepEvent, 100)
	go func() {
		defer close(events)

		for lifecycleEvt := range lifecycleEvents {
			for _, evt := range lifecycleEventToStepEvents(lifecycleEvt) {
				switch evt.Type {
				case "tool_call":
					logutil.Infof("llm: tool call → %s(%s)", evt.ToolName, evt.ToolArgs)
				case "tool_result":
					logutil.Infof("llm: tool %q result (%d bytes)", evt.ToolName, len(evt.Content))
				}
				if !sendEvent(ctx, events, evt) {
					return
				}
			}
		}
	}()

	return events, nil
}

// sendEvent 发送一个步骤事件到 channel，若 ctx 已取消则返回 false。
func sendEvent(ctx context.Context, ch chan<- agentevents.StepEvent, evt agentevents.StepEvent) bool {
	select {
	case ch <- evt:
		return true
	case <-ctx.Done():
		return false
	}
}

type pendingToolCall struct {
	ID   string
	Name string
}

func trackPendingToolCalls(pending []pendingToolCall, msg *schema.Message) []pendingToolCall {
	if msg == nil {
		return pending
	}
	if msg.Role == schema.Assistant {
		for _, tc := range msg.ToolCalls {
			if tc.ID == "" {
				continue
			}
			pending = append(pending, pendingToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
			})
		}
		return pending
	}
	if msg.Role == schema.Tool && msg.ToolCallID != "" {
		return removePendingToolCall(pending, msg.ToolCallID)
	}
	return pending
}

func removePendingToolCall(pending []pendingToolCall, id string) []pendingToolCall {
	out := pending[:0]
	for _, call := range pending {
		if call.ID == id {
			continue
		}
		out = append(out, call)
	}
	return out
}

func pendingToolErrorMessages(pending []pendingToolCall, cause error) []*schema.Message {
	if len(pending) == 0 || cause == nil {
		return nil
	}
	content := sanitizeError(cause.Error())
	msgs := make([]*schema.Message, 0, len(pending))
	for _, call := range pending {
		if call.ID == "" {
			continue
		}
		msgs = append(msgs, &schema.Message{
			Role:       schema.Tool,
			Content:    content,
			ToolCallID: call.ID,
			ToolName:   call.Name,
		})
	}
	return msgs
}

// messageToStepEvents 将一条 schema.Message 转换为一个或多个 StepEvent。
func messageToStepEvents(msg *schema.Message) []agentevents.StepEvent {
	if msg == nil {
		return nil
	}
	var events []agentevents.StepEvent
	events = append(events, lifecycleEventToStepEvents(LifecycleEvent{
		Type:    LifecycleMessageEnd,
		Message: msg,
	})...)
	if msg.ToolCallID != "" {
		return append(events, lifecycleEventToStepEvents(LifecycleEvent{
			Type:       LifecycleToolResult,
			Message:    msg,
			Content:    msg.Content,
			ToolName:   msg.ToolName,
			ToolCallID: msg.ToolCallID,
		})...)
	}
	for _, tc := range msg.ToolCalls {
		events = append(events, lifecycleEventToStepEvents(LifecycleEvent{
			Type:       LifecycleToolCall,
			Message:    msg,
			Content:    tc.Function.Name,
			ToolName:   tc.Function.Name,
			ToolArgs:   tc.Function.Arguments,
			ToolCallID: tc.ID,
		})...)
	}
	return events
}
