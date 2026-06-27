package llm

import (
	"context"
	"fmt"

	"oops/internal/config"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// systemPrompt 是 Agent 的系统提示词。
const systemPrompt = `你是一个基础设施运维助手，负责回答当前监控环境中的问题。

你可以使用工具查询实时运维数据。回答时：
- 先调用工具获取最新数据，不要猜测
- 如果工具返回错误，解释原因而不是忽略
- 容器列表按状态分类展示，先列出异常容器
- 日志输出标明时间戳和输出流（stdout/stderr）
- 如果用户提到的机器或容器不存在，明确告知
- 回答简洁，聚焦运维数据
- 不要建议执行 shell 命令或修改系统配置`

// StepEvent 表示 Agent 执行过程中的一个步骤，通过 SSE 推送给前端。
type StepEvent struct {
	Type       string `json:"type"`                 // thinking | tool_call | tool_result | answer | error
	Content    string `json:"content"`              // 文本内容
	ToolName   string `json:"toolName,omitempty"`   // 工具名称（tool_call / tool_result）
	ToolArgs   string `json:"toolArgs,omitempty"`   // 工具参数 JSON（tool_call）
	ToolCallID string `json:"toolCallId,omitempty"` // 工具调用 ID（tool_result）
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
func newAgent(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.InvokableTool) (*react.Agent, error) {
	baseTools := make([]tool.BaseTool, len(tools))
	for i, t := range tools {
		baseTools[i] = t
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: baseTools,
		},
		MaxStep: 30,
	})
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}
	return agent, nil
}

// Ask 向 LLM Agent 提问，通过 channel 流式返回每一步执行过程。
// 调用方需要从 channel 读取 StepEvent 直到 channel 关闭。
// 若 agent 创建失败，返回 error（此时 channel 为 nil）。
func Ask(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.InvokableTool, question string) (<-chan StepEvent, error) {
	opt, future := react.WithMessageFuture()

	agent, err := newAgent(ctx, chatModel, tools)
	if err != nil {
		return nil, err
	}

	events := make(chan StepEvent, 100)

	go func() {
		defer close(events)

		// 在后台执行 agent.Generate，主循环读取迭代器。
		type generateResult struct {
			msg *schema.Message
			err error
		}
		genDone := make(chan generateResult, 1)

		go func() {
			messages := []*schema.Message{
				schema.SystemMessage(systemPrompt),
				schema.UserMessage(question),
			}
			resp, err := agent.Generate(ctx, messages, opt)
			genDone <- generateResult{msg: resp, err: err}
		}()

		// 读取中间步骤。
		iter := future.GetMessages()
		for {
			msg, ok, err := iter.Next()
			if err != nil {
				sendEvent(ctx, events, StepEvent{Type: "error", Content: err.Error()})
				return
			}
			if !ok {
				break
			}

			for _, evt := range messageToStepEvents(msg) {
				if !sendEvent(ctx, events, evt) {
					return
				}
			}
		}

		// 等待 agent.Generate 完成，获取最终答案。
		result := <-genDone
		if result.err != nil {
			sendEvent(ctx, events, StepEvent{Type: "error", Content: result.err.Error()})
			return
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
