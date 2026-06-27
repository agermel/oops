package llm

import (
	"context"
	"fmt"

	"oops/internal/config"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

// Client 封装 LLM 模型和工具，提供统一的提问接口。
type Client struct {
	model model.ToolCallingChatModel
	tools []tool.InvokableTool
}

// NewClient 创建 LLM 客户端。
func NewClient(ctx context.Context, cfg config.LLMConfig, ops OpsData) (*Client, error) {
	chatModel, err := newModel(ctx, cfg)
	if err != nil {
		return nil, err
	}

	tools, err := NewTools(ops)
	if err != nil {
		return nil, fmt.Errorf("create tools: %w", err)
	}

	return &Client{
		model: chatModel,
		tools: tools,
	}, nil
}

// Ask 向 LLM Agent 提问，通过 channel 流式返回每一步执行过程。
func (c *Client) Ask(ctx context.Context, question string) (<-chan StepEvent, error) {
	return Ask(ctx, c.model, c.tools, question)
}
