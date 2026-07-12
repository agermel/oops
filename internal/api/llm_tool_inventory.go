package api

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"oops/internal/mcp"
)

func formatMCPToolInventory(entries []mcp.ConnectionTool) string {
	if len(entries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## 当前可用 MCP 工具\n")
	b.WriteString("这些工具已经注册到当前 Agent。用户询问 MCP 能力或可用工具时，直接基于此清单回答；调用工具时必须使用括号中的模型工具名。\n")

	current := ""
	for _, entry := range entries {
		label := entry.ConnectionName
		if label == "" {
			label = entry.ConnectionID
		}
		if entry.ConnectionType != "" {
			label = fmt.Sprintf("%s/%s", entry.ConnectionType, label)
		}
		if label != current {
			current = label
			b.WriteString("\n")
			b.WriteString("- ")
			b.WriteString(promptQuotedName(label))
			b.WriteString(":\n")
		}
		name := entry.ModelName
		if name == "" {
			name = entry.OriginalName
		}
		b.WriteString("  - ")
		b.WriteString(promptQuotedName(entry.OriginalName))
		if name != entry.OriginalName {
			b.WriteString(" (")
			b.WriteString(promptQuotedName(name))
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func promptQuotedName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if s == "" {
		s = "unnamed"
	}
	const maxRunes = 120
	runes := []rune(s)
	if len(runes) > maxRunes {
		s = string(runes[:maxRunes])
	}
	return strconv.Quote(s)
}
