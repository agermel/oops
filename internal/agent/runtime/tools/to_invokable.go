package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
)

type invokableTool struct {
	tool       toolruntime.Tool
	definition protocol.ToolDefinition
	info       *schema.ToolInfo
}

func ToInvokableTool(runtimeTool toolruntime.Tool) (einotool.InvokableTool, error) {
	if runtimeTool == nil {
		return nil, errors.New("tool is nil")
	}
	definition := runtimeTool.Definition()
	if definition.Name == "" {
		return nil, errors.New("tool definition requires name")
	}
	info := &schema.ToolInfo{
		Name: definition.Name,
		Desc: definition.Description,
	}
	if len(definition.Parameters) > 0 {
		params := &einojsonschema.Schema{}
		if err := json.Unmarshal(definition.Parameters, params); err != nil {
			return nil, fmt.Errorf("parse schema for %q: %w", definition.Name, err)
		}
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(params)
	}
	return &invokableTool{
		tool:       runtimeTool,
		definition: protocol.CloneTools([]protocol.ToolDefinition{definition})[0],
		info:       info,
	}, nil
}

func ToInvokableTools(runtimeTools []toolruntime.Tool) ([]einotool.InvokableTool, error) {
	if len(runtimeTools) == 0 {
		return nil, nil
	}
	out := make([]einotool.InvokableTool, 0, len(runtimeTools))
	for _, runtimeTool := range runtimeTools {
		invokable, err := ToInvokableTool(runtimeTool)
		if err != nil {
			return nil, err
		}
		out = append(out, invokable)
	}
	return out, nil
}

func (t *invokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return cloneToolInfo(t.info)
}

func (t *invokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...einotool.Option) (string, error) {
	raw := json.RawMessage(argumentsInJSON)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	runner, err := toolruntime.NewRunner(toolruntime.RunnerConfig{
		Tools: []toolruntime.Tool{t.tool},
	})
	if err != nil {
		return "", err
	}
	result, err := runner.ExecuteTools(ctx, toolruntime.ToolRunRequest{
		ToolCalls: []protocol.ToolCallContent{
			protocol.NewToolCallContent("adapter_call", t.definition.Name, raw),
		},
	})
	if err != nil {
		return "", err
	}
	if len(result.Messages) == 0 {
		return "", nil
	}
	text := toolResultContentText(result.Messages[0].Content)
	if result.Messages[0].IsError {
		return text, errors.New(text)
	}
	return text, nil
}

func cloneToolInfo(info *schema.ToolInfo) (*schema.ToolInfo, error) {
	if info == nil {
		return nil, nil
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	var out schema.ToolInfo
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func toolResultContentText(content protocol.ContentList) string {
	if len(content) == 0 {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, item := range content {
		switch value := item.(type) {
		case protocol.TextContent:
			parts = append(parts, value.Text)
		case *protocol.TextContent:
			if value != nil {
				parts = append(parts, value.Text)
			}
		default:
			raw, err := json.Marshal(item)
			if err == nil {
				parts = append(parts, string(raw))
			}
		}
	}
	return strings.Join(parts, "\n")
}
