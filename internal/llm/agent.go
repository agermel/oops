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

// newAgent 创建 ReAct Agent。
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

// Ask 向 LLM Agent 提问并返回回答。
func Ask(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.InvokableTool, question string) (string, error) {
	agent, err := newAgent(ctx, chatModel, tools)
	if err != nil {
		return "", err
	}

	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(question),
	}

	resp, err := agent.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("agent generate: %w", err)
	}
	return resp.Content, nil
}
