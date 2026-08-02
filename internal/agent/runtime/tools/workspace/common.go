package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
)

type baseTool struct {
	cfg        workspaceToolConfig
	definition protocol.ToolDefinition
	mode       toolruntime.ExecutionMode
}

func newBaseTool(cfg workspaceToolConfig, name, description string, parameters json.RawMessage, mode toolruntime.ExecutionMode) baseTool {
	return baseTool{
		cfg: cfg,
		definition: protocol.ToolDefinition{
			Name:        name,
			Description: description,
			Parameters:  cloneToolRaw(parameters),
		},
		mode: mode,
	}
}

func (t baseTool) Definition() protocol.ToolDefinition {
	return protocol.CloneTools([]protocol.ToolDefinition{t.definition})[0]
}

func (t baseTool) ExecutionMode() toolruntime.ExecutionMode {
	return t.mode
}

func (t baseTool) PrepareArguments(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return cloneToolRaw(raw), nil
}

func decodeCall(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	return nil
}

func textResult(text string, details any) protocol.ToolResult {
	return protocol.ToolResult{
		Content: protocol.ContentList{protocol.NewTextContent(text)},
		Details: details,
	}
}

func cloneToolRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return bytes.Clone(raw)
}

func requireText(value, name string) error {
	if value == "" {
		return errors.New(name + " is required")
	}
	return nil
}
