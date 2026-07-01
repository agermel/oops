package llm

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

// MessageRole 是 oops 自己的消息角色，与 eino 的 schema.RoleType 解耦。
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// ToolCall 表示模型请求的一次工具调用。
type ToolCall struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ArgsJSON string `json:"arguments"`
}

// AgentMessage 是 oops 应用层的统一消息类型。
// 
// 会话历史、会话树、压缩逻辑均基于此类型运转。
// 仅在调用 LLM 的边界上通过 convertToLlm() 转换为 eino 的 *schema.Message。
type AgentMessage struct {
	Role       MessageRole `json:"role"`
	Content    string      `json:"content"`
	ToolCallID string      `json:"toolCallId,omitempty"`
	ToolName   string      `json:"toolName,omitempty"`
	ToolCalls  []ToolCall  `json:"toolCalls,omitempty"`
	Timestamp  int64       `json:"timestamp"`
}

// NewUserMessage 创建用户消息。
func NewUserMessage(content string) AgentMessage {
	return AgentMessage{
		Role:      RoleUser,
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	}
}

// NewAssistantMessage 创建助手消息（无工具调用）。
func NewAssistantMessage(content string) AgentMessage {
	return AgentMessage{
		Role:      RoleAssistant,
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	}
}

// NewAssistantMessageWithTools 创建带工具调用的助手消息。
func NewAssistantMessageWithTools(content string, toolCalls []ToolCall) AgentMessage {
	return AgentMessage{
		Role:      RoleAssistant,
		Content:   content,
		ToolCalls: toolCalls,
		Timestamp: time.Now().UnixMilli(),
	}
}

// NewSystemMessage 创建系统消息。
func NewSystemMessage(content string) AgentMessage {
	return AgentMessage{
		Role:      RoleSystem,
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	}
}

// NewToolResultMessage 创建工具结果消息。
func NewToolResultMessage(toolCallID, toolName, content string) AgentMessage {
	return AgentMessage{
		Role:       RoleTool,
		Content:    content,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// ExtractText 提取消息的纯文本内容（用于 token 估算和摘要）。
func ExtractText(msg AgentMessage) string {
	return msg.Content
}

// convertToLlm 将 AgentMessage[] 转换为 eino 的 *schema.Message[]。
// convertToLlm 采用最晚转换策略 —— 仅在调用 LLM 边界上才转为 eino 消息格式。
func convertToLlm(msgs []AgentMessage) []*schema.Message {
	result := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, agentMessageToSchema(m))
	}
	return result
}

// agentMessageToSchema 将单个 AgentMessage 转换为 *schema.Message。
func agentMessageToSchema(m AgentMessage) *schema.Message {
	msg := &schema.Message{
		Role:       schema.RoleType(m.Role),
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		ToolName:   m.ToolName,
	}
	for _, tc := range m.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID: tc.ID,
			Function: schema.FunctionCall{
				Name:      tc.Name,
				Arguments: tc.ArgsJSON,
			},
		})
	}
	return msg
}

// schemaMessageToAgent 将 eino 的 *schema.Message 转换为 AgentMessage。
func schemaMessageToAgent(msg *schema.Message) AgentMessage {
	am := AgentMessage{
		Role:       MessageRole(msg.Role),
		Content:    msg.Content,
		ToolCallID: msg.ToolCallID,
		ToolName:   msg.ToolName,
		Timestamp:  time.Now().UnixMilli(),
	}
	for _, tc := range msg.ToolCalls {
		am.ToolCalls = append(am.ToolCalls, ToolCall{
			ID:       tc.ID,
			Name:     tc.Function.Name,
			ArgsJSON: tc.Function.Arguments,
		})
	}
	return am
}
