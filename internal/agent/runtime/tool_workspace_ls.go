package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
)

type lsTool struct {
	baseTool
}

type lsInput struct {
	Path string `json:"path"`
}

type lsEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	Mode string `json:"mode"`
}

type lsDetails struct {
	Path    string    `json:"path"`
	Entries []lsEntry `json:"entries"`
}

func newLSTool(cfg workspaceToolConfig) toolruntime.Tool {
	return &lsTool{baseTool: newBaseTool(
		cfg,
		"ls",
		"List directory entries inside the workspace.",
		lsSchema,
		toolruntime.ExecutionModeParallel,
	)}
}

func (t *lsTool) Execute(_ context.Context, call toolruntime.ToolCall, _ toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	var input lsInput
	if err := decodeCall(call.RawArguments, &input); err != nil {
		return protocol.ToolResult{}, err
	}
	if err := requireText(input.Path, "path"); err != nil {
		return protocol.ToolResult{}, err
	}
	path, err := t.cfg.resolveInside(input.Path)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	items, err := os.ReadDir(path.abs)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	entries := make([]lsEntry, 0, len(items))
	lines := make([]string, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			return protocol.ToolResult{}, err
		}
		kind := "file"
		name := item.Name()
		if item.IsDir() {
			kind = "dir"
			name += "/"
		} else if info.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
		}
		rel := name
		if path.rel != "" {
			rel = path.rel + "/" + name
		}
		entry := lsEntry{
			Name: item.Name(),
			Path: rel,
			Type: kind,
			Size: info.Size(),
			Mode: info.Mode().String(),
		}
		entries = append(entries, entry)
		lines = append(lines, fmt.Sprintf("%s\t%d\t%s", kind, info.Size(), rel))
	}
	displayPath := relativeOrDot(path)
	text := fmt.Sprintf("Directory: %s\n\n%s", displayPath, strings.Join(lines, "\n"))
	return textResult(text, lsDetails{Path: displayPath, Entries: entries}), nil
}
