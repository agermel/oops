package protocol

import "encoding/json"

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ToolResult struct {
	Content   ContentList `json:"content"`
	Details   any         `json:"details,omitempty"`
	Terminate bool        `json:"terminate,omitempty"`
}
