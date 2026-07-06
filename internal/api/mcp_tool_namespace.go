package api

import (
	"context"
	"fmt"
	"strings"

	"oops/internal/mcp"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type namespacedMCPTool struct {
	modelName string
	inner     tool.InvokableTool
}

func (t namespacedMCPTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := t.inner.Info(ctx)
	if err != nil {
		return nil, err
	}
	copied := *info
	copied.Name = t.modelName
	return &copied, nil
}

func (t namespacedMCPTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	return t.inner.InvokableRun(ctx, argumentsInJSON, opts...)
}

func nativeToolNames(ctx context.Context, tools []tool.InvokableTool) map[string]struct{} {
	names := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			continue
		}
		names[info.Name] = struct{}{}
	}
	return names
}

func namespaceMCPTools(entries []mcp.ConnectionTool, reserved map[string]struct{}) []mcp.ConnectionTool {
	if len(entries) == 0 {
		return nil
	}
	baseCounts := make(map[string]int, len(entries))
	for _, entry := range entries {
		baseCounts[baseMCPToolName(entry.ConnectionType, entry.OriginalName)]++
	}

	used := make(map[string]struct{}, len(reserved)+len(entries))
	for name := range reserved {
		used[name] = struct{}{}
	}

	candidates := make([]string, len(entries))
	candidateCounts := make(map[string]int, len(entries))
	for i, entry := range entries {
		base := baseMCPToolName(entry.ConnectionType, entry.OriginalName)
		candidate := base
		if _, collides := used[candidate]; collides || baseCounts[base] > 1 {
			candidate = scopedMCPToolName(entry)
		}
		candidates[i] = candidate
		candidateCounts[candidate]++
	}

	out := make([]mcp.ConnectionTool, 0, len(entries))
	for i, entry := range entries {
		modelName := candidates[i]
		if candidateCounts[modelName] > 1 {
			modelName = fullyScopedMCPToolName(entry)
		}
		modelName = reserveUniqueName(modelName, entry.ConnectionID, used)
		entry.ModelName = modelName
		out = append(out, entry)
	}
	return out
}

func baseMCPToolName(connType, originalName string) string {
	prefix := toolNamePart(connType)
	if prefix == "" {
		prefix = "mcp"
	}
	original := toolNamePart(originalName)
	if original == "" {
		original = "tool"
	}
	if strings.HasPrefix(original, prefix+"_") {
		return original
	}
	return prefix + "_" + original
}

func scopedMCPToolName(entry mcp.ConnectionTool) string {
	if name := serverScopedMCPToolName(entry); name != "" {
		return name
	}
	return connectionScopedMCPToolName(entry)
}

func fullyScopedMCPToolName(entry mcp.ConnectionTool) string {
	if name := serverAndConnectionScopedMCPToolName(entry); name != "" {
		return name
	}
	return connectionScopedMCPToolName(entry)
}

func serverScopedMCPToolName(entry mcp.ConnectionTool) string {
	server := serverSlug(entry)
	if server == "" {
		return ""
	}
	prefix, original := normalizedToolNameParts(entry)
	return prefix + "_server_" + server + "_" + original
}

func serverAndConnectionScopedMCPToolName(entry mcp.ConnectionTool) string {
	server := serverSlug(entry)
	if server == "" {
		return ""
	}
	prefix, original := normalizedToolNameParts(entry)
	conn := connectionSlug(entry, prefix)
	if conn == "" {
		return serverScopedMCPToolName(entry)
	}
	return prefix + "_server_" + server + "_" + conn + "_" + original
}

func connectionScopedMCPToolName(entry mcp.ConnectionTool) string {
	prefix, original := normalizedToolNameParts(entry)
	return prefix + "_" + connectionSlug(entry, prefix) + "_" + original
}

func normalizedToolNameParts(entry mcp.ConnectionTool) (string, string) {
	prefix := toolNamePart(entry.ConnectionType)
	if prefix == "" {
		prefix = "mcp"
	}
	original := toolNamePart(entry.OriginalName)
	if original == "" {
		original = "tool"
	}
	original = strings.TrimPrefix(original, prefix+"_")
	if original == "" {
		original = "tool"
	}
	return prefix, original
}

func serverSlug(entry mcp.ConnectionTool) string {
	slug := toolNamePart(entry.ServerName)
	if slug == "" {
		slug = toolNamePart(entry.NodeletID)
	}
	return slug
}

func connectionSlug(entry mcp.ConnectionTool, prefix string) string {
	slug := toolNamePart(entry.ConnectionName)
	if slug == "" {
		slug = toolNamePart(entry.ConnectionID)
	}
	slug = strings.TrimPrefix(slug, prefix+"_")
	if slug == "" || slug == prefix {
		slug = strings.TrimPrefix(toolNamePart(entry.ConnectionID), prefix+"_")
	}
	if slug == "" || slug == prefix {
		slug = "connection"
	}
	return slug
}

func reserveUniqueName(candidate, connectionID string, used map[string]struct{}) string {
	if reserveName(candidate, used) {
		return candidate
	}

	connPart := toolNamePart(connectionID)
	if connPart == "" {
		connPart = "connection"
	}
	base := candidate + "_" + connPart
	if reserveName(base, used) {
		return base
	}
	for i := 2; ; i++ {
		name := fmt.Sprintf("%s_%d", base, i)
		if reserveName(name, used) {
			return name
		}
	}
}

func reserveName(name string, used map[string]struct{}) bool {
	if _, ok := used[name]; ok {
		return false
	}
	used[name] = struct{}{}
	return true
}

func toolNamePart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
