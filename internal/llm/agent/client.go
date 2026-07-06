package agent

import (
	"context"
	"sync"

	"oops/internal/config"
	agentevents "oops/internal/llm/events"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Client 封装 LLM 模型和工具，提供统一的提问接口。
type Client struct {
	model    model.ToolCallingChatModel
	toolsMu  sync.RWMutex
	allTools []tool.InvokableTool // 原始全量工具列表（未过滤）
	tools    []tool.InvokableTool // 当前生效的工具列表（已过滤禁用项）
	disabled map[string]bool      // toolName → true 表示已禁用
}

// NewClient 创建 LLM 客户端。tools 由调用方组装（原生工具 + MCP 工具等）。
func NewClient(ctx context.Context, cfg config.LLMConfig, tools []tool.InvokableTool) (*Client, error) {
	chatModel, err := newModel(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return &Client{
		model:    chatModel,
		allTools: tools,
		tools:    tools,
		disabled: make(map[string]bool),
	}, nil
}

// UpdateTools 运行时替换全量工具列表（MCP 连接变更时调用）。
// 会重新应用当前的禁用列表。
func (c *Client) UpdateTools(tools []tool.InvokableTool) {
	c.toolsMu.Lock()
	defer c.toolsMu.Unlock()
	c.allTools = tools
	c.rebuildLocked()
}

// SetToolEnabled 设置单个工具的启用状态。enabled=false 表示禁用。
// 线程安全，设置后立即重建生效工具列表。
func (c *Client) SetToolEnabled(name string, enabled bool) {
	c.toolsMu.Lock()
	defer c.toolsMu.Unlock()
	if enabled {
		delete(c.disabled, name)
	} else {
		c.disabled[name] = true
	}
	c.rebuildLocked()
}

// DisabledTools 返回当前被禁用的工具名集合（浅拷贝）。
func (c *Client) DisabledTools() map[string]bool {
	c.toolsMu.RLock()
	defer c.toolsMu.RUnlock()
	out := make(map[string]bool, len(c.disabled))
	for k, v := range c.disabled {
		out[k] = v
	}
	return out
}

// rebuildLocked 根据 disabled 过滤 allTools 并赋值给 c.tools。
// 调用方必须持有 c.toolsMu。
func (c *Client) rebuildLocked() {
	if len(c.disabled) == 0 {
		c.tools = c.allTools
		return
	}
	filtered := make([]tool.InvokableTool, 0, len(c.allTools))
	for _, t := range c.allTools {
		info, err := t.Info(context.Background())
		if err != nil || c.disabled[info.Name] {
			continue
		}
		filtered = append(filtered, t)
	}
	c.tools = filtered
}

// Ask 向 LLM Agent 提问，通过 channel 流式返回每一步执行过程。
// messages 是完整的消息列表（system prompt + 历史消息 + 当前问题）。
// onMessage 在 agent 产生每条新消息时被调用，用于持久化到 session。
// maxStep 控制 Agent 最大步数；<=0 时使用默认值 15。
func (c *Client) Ask(ctx context.Context, messages []*schema.Message, onMessage MessageCallback, maxStep int) (<-chan agentevents.StepEvent, error) {
	c.toolsMu.RLock()
	tools := c.tools
	c.toolsMu.RUnlock()
	return Ask(ctx, c.model, tools, messages, onMessage, maxStep)
}

// Complete 发送无工具调用的简单补全请求（用于 compaction 摘要等场景）。
// 返回模型的纯文本回复。
func (c *Client) Complete(ctx context.Context, messages []*schema.Message) (string, error) {
	resp, err := c.model.Generate(ctx, messages)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
