package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
)

type readTool struct {
	baseTool
}

type readInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type readDetails struct {
	Path       string            `json:"path"`
	StartLine  int               `json:"startLine"`
	EndLine    int               `json:"endLine"`
	TotalLines int               `json:"totalLines"`
	Truncation TruncationDetails `json:"truncation"`
	SizeBytes  int               `json:"sizeBytes"`
}

func newReadTool(cfg workspaceToolConfig) toolruntime.Tool {
	return &readTool{baseTool: newBaseTool(
		cfg,
		"read",
		"Read a UTF-8 text file from the workspace. Supports offset and limit for large files. Output is truncated to bounded size.",
		readSchema,
		toolruntime.ExecutionModeParallel,
	)}
}

func (t *readTool) Execute(_ context.Context, call toolruntime.ToolCall, _ toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	var input readInput
	if err := decodeCall(call.RawArguments, &input); err != nil {
		return protocol.ToolResult{}, err
	}
	if err := requireText(input.Path, "path"); err != nil {
		return protocol.ToolResult{}, err
	}
	if input.Offset < 0 || input.Limit < 0 {
		return protocol.ToolResult{}, fmt.Errorf("offset and limit must be positive")
	}
	path, err := t.cfg.resolveInside(input.Path)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	info, err := os.Stat(path.abs)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	if info.IsDir() {
		return protocol.ToolResult{}, fmt.Errorf("%s is a directory", relativeOrDot(path))
	}
	data, err := os.ReadFile(path.abs)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	content := strings.ToValidUTF8(string(data), "\uFFFD")
	lines := splitLinesForCounting(content)
	totalLines := len(lines)
	start := 0
	if input.Offset > 0 {
		start = input.Offset - 1
	}
	if start > totalLines {
		start = totalLines
	}
	end := totalLines
	if input.Limit > 0 && start+input.Limit < end {
		end = start + input.Limit
	}
	window := strings.Join(lines[start:end], "\n")
	truncated := t.cfg.truncateHead(window)
	displayPath := relativeOrDot(path)
	text := fmt.Sprintf("File: %s\nLines: %d-%d of %d\n\n%s", displayPath, start+1, end, totalLines, truncated.content)
	if truncated.details.FirstLineExceedsLimit {
		text = fmt.Sprintf("File: %s\nLines: %d-%d of %d\n\n[First line exceeds output byte limit]", displayPath, start+1, end, totalLines)
	}
	return textResult(text, readDetails{
		Path:       displayPath,
		StartLine:  start + 1,
		EndLine:    end,
		TotalLines: totalLines,
		Truncation: truncated.details,
		SizeBytes:  len(data),
	}), nil
}
