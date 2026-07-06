package events

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
