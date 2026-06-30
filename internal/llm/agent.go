package llm

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"oops/internal/config"
	"oops/internal/logutil"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"go.uber.org/zap"
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

// SystemPrompt 是 Agent 的系统提示词。调用方应在构建消息列表时将其作为首条消息。
const SystemPrompt = `你是一个基础设施运维助手，负责回答当前监控环境中的问题。

你可以使用工具查询实时运维数据。回答时：
- 先调用工具获取最新数据，不要猜测
- 如果工具返回错误，解释原因而不是忽略
- 容器列表按状态分类展示，先列出异常容器
- 日志输出标明时间戳和输出流（stdout/stderr）
- 如果用户提到的机器或容器不存在，明确告知
- 回答简洁，聚焦运维数据
- 不要建议执行 shell 命令或修改系统配置`

// MessageCallback 是 Ask 在产生每条新消息时调用的回调。
// ctx 是 agent 的内部 context；若回调返回非 nil 错误，Ask 会终止执行。
type MessageCallback func(ctx context.Context, msg *schema.Message) error

// ToolResultHook 是工具结果后处理回调。
// 在工具结果进入 session 和 SSE 之前调用，可修改 msg.Content。
// 返回 nil 表示丢弃该事件（不推送给前端，也不存入 session）。
type ToolResultHook func(ctx context.Context, msg *schema.Message) *schema.Message

// AfterToolCall 是按工具名注册的工具结果后处理 hook。
// key 为工具名（如 "get_logs"、"list_containers"），MCP 工具也使用其工具名。
// 仅在 msg.Role == Tool 时触发。
var AfterToolCall = map[string]ToolResultHook{}

// StepEvent 表示 Agent 执行过程中的一个步骤，通过 SSE 推送给前端。
type StepEvent struct {
	Type       string `json:"type"`                 // thinking | tool_call | tool_result | answer | error | session | stats
	Content    string `json:"content"`              // 文本内容
	ToolName   string `json:"toolName,omitempty"`   // 工具名称（tool_call / tool_result）
	ToolArgs   string `json:"toolArgs,omitempty"`   // 工具参数 JSON（tool_call）
	ToolCallID string `json:"toolCallId,omitempty"` // 工具调用 ID（tool_result）
	AgentType  string `json:"agentType,omitempty"`  // Agent 类型（session 事件中携带）
	MaxStep    int    `json:"maxStep,omitempty"`    // 最大步数（session 事件中携带）
	Tokens     int    `json:"tokens,omitempty"`     // 估算 token 用量（stats 事件中携带）
	Trimmed    int    `json:"trimmed,omitempty"`    // 被裁剪的消息数（stats 事件中携带）
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

// newAgent 创建 ReAct Agent（不含 MessageFuture option）。
func newAgent(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.InvokableTool, maxStep int) (*react.Agent, error) {
	baseTools := make([]tool.BaseTool, len(tools))
	for i, t := range tools {
		baseTools[i] = t
	}

	if maxStep <= 0 {
		maxStep = 15
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: baseTools,
		},
		MaxStep: maxStep,
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
func Ask(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.InvokableTool, messages []*schema.Message, onMessage MessageCallback, maxStep int) (<-chan StepEvent, error) {
	opt, future := react.WithMessageFuture()

	agent, err := newAgent(ctx, chatModel, tools, maxStep)
	if err != nil {
		logutil.Error("llm: create agent", zap.Error(err))
		return nil, err
	}

	events := make(chan StepEvent, 100)

	go func() {
		defer close(events)

		// 派生可取消的 context：外层 goroutine 退出时（无论正常结束、迭代器报错、
		// 还是客户端断开），cancel 能及时终止内层 agent.Generate，避免浪费 LLM 调用。
		agentCtx, agentCancel := context.WithCancel(ctx)
		defer agentCancel()

		// 在后台执行 agent.Generate，主循环读取迭代器。
		type generateResult struct {
			msg *schema.Message
			err error
		}
		genDone := make(chan generateResult, 1)

		go func() {
			resp, err := agent.Generate(agentCtx, messages, opt)
			genDone <- generateResult{msg: resp, err: err}
		}()

		// 读取中间步骤。
		iter := future.GetMessages()
		for {
			msg, ok, err := iter.Next()
			if err != nil {
				logutil.Error("llm: iter", zap.Error(err))
				sendEvent(ctx, events, StepEvent{Type: "error", Content: sanitizeError(err.Error())})
				return
			}
			if !ok {
				break
			}

			// afterToolCall hook：工具结果后处理
			if msg.Role == schema.Tool && msg.ToolName != "" {
				if hook, ok := AfterToolCall[msg.ToolName]; ok {
					msg = hook(agentCtx, msg)
					if msg == nil {
						continue // hook 决定丢弃该消息
					}
				}
			}

			if onMessage != nil {
				if err := onMessage(agentCtx, msg); err != nil {
					logutil.Error("llm: onMessage", zap.Error(err))
					sendEvent(ctx, events, StepEvent{Type: "error", Content: sanitizeError(err.Error())})
					return
				}
			}

			for _, evt := range messageToStepEvents(msg) {
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

		// 等待 agent.Generate 完成，获取最终答案。
		result := <-genDone
		if result.err != nil {
			logutil.Error("llm: generate", zap.Error(result.err))
			sendEvent(ctx, events, StepEvent{Type: "error", Content: sanitizeError(result.err.Error())})
			return
		}

		if onMessage != nil {
			if err := onMessage(agentCtx, result.msg); err != nil {
				logutil.Error("llm: onMessage final", zap.Error(err))
			}
		}

		sendEvent(ctx, events, StepEvent{Type: "answer", Content: result.msg.Content})
	}()

	return events, nil
}

// sendEvent 发送一个步骤事件到 channel，若 ctx 已取消则返回 false。
func sendEvent(ctx context.Context, ch chan<- StepEvent, evt StepEvent) bool {
	select {
	case ch <- evt:
		return true
	case <-ctx.Done():
		return false
	}
}

// messageToStepEvents 将一条 schema.Message 转换为一个或多个 StepEvent。
func messageToStepEvents(msg *schema.Message) []StepEvent {
	// 工具返回消息。
	if msg.ToolCallID != "" {
		return []StepEvent{{
			Type:       "tool_result",
			Content:    msg.Content,
			ToolName:   msg.ToolName,
			ToolCallID: msg.ToolCallID,
		}}
	}

	// AI 消息。
	var events []StepEvent

	// 带工具调用的 AI 内容作为中间思考展示；纯文本 AI 消息由最终 answer 事件展示。
	if msg.Content != "" && len(msg.ToolCalls) > 0 {
		events = append(events, StepEvent{Type: "thinking", Content: msg.Content})
	}

	// 如果有工具调用，逐个发 tool_call。
	for _, tc := range msg.ToolCalls {
		events = append(events, StepEvent{
			Type:       "tool_call",
			Content:    tc.Function.Name,
			ToolName:   tc.Function.Name,
			ToolArgs:   tc.Function.Arguments,
			ToolCallID: tc.ID,
		})
	}

	return events
}
