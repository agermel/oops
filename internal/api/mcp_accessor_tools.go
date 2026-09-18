package api

import (
	"context"
	"encoding/json"
	"fmt"

	"oops/internal/mcp"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	mcpproto "github.com/mark3labs/mcp-go/mcp"
)

// mcpContentAccessor 是合成访问器工具依赖的窄接口，*mcp.Manager 天然满足。
// 抽出来是为了让 InvokableRun 的参数解析与结果序列化可以被单测（用 fake）。
type mcpContentAccessor interface {
	ListResources(ctx context.Context, connID string) (*mcpproto.ListResourcesResult, error)
	ReadResource(ctx context.Context, connID, uri string) (*mcpproto.ReadResourceResult, error)
	ListPrompts(ctx context.Context, connID string) (*mcpproto.ListPromptsResult, error)
	GetPrompt(ctx context.Context, connID, name string, args map[string]string) (*mcpproto.GetPromptResult, error)
}

type accessorKind int

const (
	accessorListResources accessorKind = iota
	accessorReadResource
	accessorListPrompts
	accessorGetPrompt
)

// mcpAccessorTool 把一个连接暴露的 prompts/resources 包装成 LLM 可调用的工具。
// 它通过 mcpContentAccessor 间接调用，不直接持有 session/进程，保持层边界清晰。
type mcpAccessorTool struct {
	accessor mcpContentAccessor
	connID   string
	info     *schema.ToolInfo
	kind     accessorKind
}

func (t mcpAccessorTool) Info(context.Context) (*schema.ToolInfo, error) {
	copied := *t.info
	return &copied, nil
}

func (t mcpAccessorTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	switch t.kind {
	case accessorListResources:
		res, err := t.accessor.ListResources(ctx, t.connID)
		if err != nil {
			return "", err
		}
		return marshalAccessorResult(res.Resources), nil
	case accessorReadResource:
		var args struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", fmt.Errorf("parse read_resource arguments: %w", err)
		}
		if args.URI == "" {
			return "", fmt.Errorf("read_resource: uri is required")
		}
		res, err := t.accessor.ReadResource(ctx, t.connID, args.URI)
		if err != nil {
			return "", err
		}
		return marshalAccessorResult(res.Contents), nil
	case accessorListPrompts:
		res, err := t.accessor.ListPrompts(ctx, t.connID)
		if err != nil {
			return "", err
		}
		return marshalAccessorResult(res.Prompts), nil
	case accessorGetPrompt:
		var args struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", fmt.Errorf("parse get_prompt arguments: %w", err)
		}
		if args.Name == "" {
			return "", fmt.Errorf("get_prompt: name is required")
		}
		res, err := t.accessor.GetPrompt(ctx, t.connID, args.Name, args.Arguments)
		if err != nil {
			return "", err
		}
		return marshalAccessorResult(res.Messages), nil
	default:
		return "", fmt.Errorf("unknown accessor kind %d", t.kind)
	}
}

// buildSyntheticAccessorEntries 为每个暴露了 prompts/resources 的运行中连接生成
// list/read（或 list/get）合成工具条目。这些条目仅用于命名空间 + 注入 LLM 的
// Tools 数组，不进入 visibleTools / 前端工具列表。
func buildSyntheticAccessorEntries(conns []mcp.ConnectionWithStatus, accessor mcpContentAccessor) []mcp.ConnectionTool {
	var entries []mcp.ConnectionTool
	for _, c := range conns {
		if c.Status != "running" {
			continue
		}
		if len(c.Resources) > 0 {
			entries = append(entries,
				syntheticAccessorEntry(c, "list_resources", accessorListResources, accessor),
				syntheticAccessorEntry(c, "read_resource", accessorReadResource, accessor),
			)
		}
		if len(c.Prompts) > 0 {
			entries = append(entries,
				syntheticAccessorEntry(c, "list_prompts", accessorListPrompts, accessor),
				syntheticAccessorEntry(c, "get_prompt", accessorGetPrompt, accessor),
			)
		}
	}
	return entries
}

func syntheticAccessorEntry(c mcp.ConnectionWithStatus, name string, kind accessorKind, accessor mcpContentAccessor) mcp.ConnectionTool {
	return mcp.ConnectionTool{
		ConnectionID:   c.ID,
		ConnectionName: c.Name,
		ConnectionType: c.Type,
		NodeletID:      c.NodeletID,
		OriginalName:   name,
		Description:    accessorDescription(name),
		Tool: mcpAccessorTool{
			accessor: accessor,
			connID:   c.ID,
			info:     accessorToolInfo(name),
			kind:     kind,
		},
	}
}

func accessorToolInfo(name string) *schema.ToolInfo {
	info := &schema.ToolInfo{
		Name: name,
		Desc: accessorDescription(name),
	}
	switch name {
	case "read_resource":
		info.ParamsOneOf = schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"uri": {Type: schema.String, Required: true, Desc: "要读取的 resource 的 URI"},
		})
	case "get_prompt":
		info.ParamsOneOf = schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":      {Type: schema.String, Required: true, Desc: "prompt 名称"},
			"arguments": {Type: schema.Object, Desc: "模板参数（可选）"},
		})
	}
	return info
}

func accessorDescription(name string) string {
	switch name {
	case "list_resources":
		return "List the read-only resources (documents/data) exposed by this MCP connection."
	case "read_resource":
		return "Read one resource's content by URI (discover URIs via list_resources first)."
	case "list_prompts":
		return "List the prompt templates exposed by this MCP connection."
	case "get_prompt":
		return "Get a prompt's rendered messages by name (optionally with template arguments)."
	default:
		return name
	}
}

func marshalAccessorResult(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("{\"error\":%q}", err.Error())
	}
	return string(b)
}
