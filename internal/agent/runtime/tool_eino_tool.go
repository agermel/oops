package runtime

import (
	"context"
	"encoding/json"

	einotool "github.com/cloudwego/eino/components/tool"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
)

type Tool struct {
	invokable  einotool.InvokableTool
	definition protocol.ToolDefinition
}

func FromInvokableTool(ctx context.Context, invokable einotool.InvokableTool) (*Tool, protocol.ToolDefinition, error) {
	info, err := invokable.Info(ctx)
	if err != nil {
		return nil, protocol.ToolDefinition{}, err
	}
	definition := protocol.ToolDefinition{
		Name:        info.Name,
		Description: info.Desc,
	}
	if info.ParamsOneOf != nil {
		schema, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil {
			return nil, protocol.ToolDefinition{}, err
		}
		if schema != nil {
			raw, err := json.Marshal(schema)
			if err != nil {
				return nil, protocol.ToolDefinition{}, err
			}
			definition.Parameters = raw
		}
	}
	tool := &Tool{
		invokable:  invokable,
		definition: protocol.CloneTools([]protocol.ToolDefinition{definition})[0],
	}
	return tool, definition, nil
}

func FromInvokableTools(ctx context.Context, invokables []einotool.InvokableTool) ([]toolruntime.Tool, []protocol.ToolDefinition, error) {
	tools := make([]toolruntime.Tool, 0, len(invokables))
	definitions := make([]protocol.ToolDefinition, 0, len(invokables))
	for _, invokable := range invokables {
		tool, definition, err := FromInvokableTool(ctx, invokable)
		if err != nil {
			return nil, nil, err
		}
		tools = append(tools, tool)
		definitions = append(definitions, definition)
	}
	return tools, protocol.CloneTools(definitions), nil
}

func (t *Tool) Definition() protocol.ToolDefinition {
	return protocol.CloneTools([]protocol.ToolDefinition{t.definition})[0]
}

func (t *Tool) ExecutionMode() toolruntime.ExecutionMode {
	return toolruntime.ExecutionModeParallel
}

func (t *Tool) PrepareArguments(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out, nil
}

func (t *Tool) Execute(ctx context.Context, call toolruntime.ToolCall, _ toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	out, err := t.invokable.InvokableRun(ctx, string(call.RawArguments))
	if err != nil {
		return protocol.ToolResult{}, err
	}
	return protocol.ToolResult{
		Content: protocol.ContentList{protocol.NewTextContent(out)},
	}, nil
}
