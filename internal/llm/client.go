package llm

import (
	"context"
	"sync"

	"oops/internal/config"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

// Client 封装 LLM 模型和工具，提供统一的提问接口。
type Client struct {
	model   model.ToolCallingChatModel
	toolsMu sync.RWMutex
	tools   []tool.InvokableTool
}

// NewClient 创建 LLM 客户端。tools 由调用方组装（原生工具 + MCP 工具等）。
func NewClient(ctx context.Context, cfg config.LLMConfig, tools []tool.InvokableTool) (*Client, error) {
	chatModel, err := newModel(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return &Client{
		model: chatModel,
		tools: tools,
	}, nil
}

// UpdateTools 运行时替换工具列表（MCP 连接变更时调用）。
// 线程安全，不影响正在执行的 Ask。
func (c *Client) UpdateTools(tools []tool.InvokableTool) {
	c.toolsMu.Lock()
	defer c.toolsMu.Unlock()
	c.tools = tools
}

// Ask 向 LLM Agent 提问，通过 channel 流式返回每一步执行过程。
func (c *Client) Ask(ctx context.Context, question string) (<-chan StepEvent, error) {
	c.toolsMu.RLock()
	tools := c.tools
	c.toolsMu.RUnlock()
	return Ask(ctx, c.model, tools, question)
}
