package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/core/toolruntime"
)

type baseTool struct {
	cfg        config
	definition protocol.ToolDefinition
	mode       toolruntime.ExecutionMode
}

func newBaseTool(cfg config, name, description string, parameters json.RawMessage, mode toolruntime.ExecutionMode) baseTool {
	return baseTool{
		cfg: cfg,
		definition: protocol.ToolDefinition{
			Name:        name,
			Description: description,
			Parameters:  cloneRaw(parameters),
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
	return cloneRaw(raw), nil
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

func cloneRaw(raw json.RawMessage) json.RawMessage {
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
