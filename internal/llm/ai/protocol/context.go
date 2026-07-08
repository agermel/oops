package protocol

type Context struct {
	SystemPrompt string           `json:"systemPrompt,omitempty"`
	Messages     MessageList      `json:"messages"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
}
