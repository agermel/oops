package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/core/toolruntime"
)

type editTool struct {
	baseTool
}

type editInput struct {
	Path       string `json:"path"`
	OldText    string `json:"oldText"`
	NewText    string `json:"newText"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
}

type editDetails struct {
	Path             string `json:"path"`
	ReplacementCount int    `json:"replacementCount"`
	BytesWritten     int    `json:"bytesWritten"`
}

func newEditTool(cfg config) toolruntime.Tool {
	return &editTool{baseTool: newBaseTool(
		cfg,
		"edit",
		"Replace exact text in a workspace file. By default the old text must appear exactly once.",
		editSchema,
		toolruntime.ExecutionModeSequential,
	)}
}

func (t *editTool) Execute(_ context.Context, call toolruntime.ToolCall, _ toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	var input editInput
	if err := decodeCall(call.RawArguments, &input); err != nil {
		return protocol.ToolResult{}, err
	}
	if err := requireText(input.Path, "path"); err != nil {
		return protocol.ToolResult{}, err
	}
	if input.OldText == "" {
		return protocol.ToolResult{}, fmt.Errorf("oldText is required")
	}
	path, err := t.cfg.resolveInside(input.Path)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	var count int
	var bytesWritten int
	err = withFileMutationQueue(path.abs, func() error {
		data, err := os.ReadFile(path.abs)
		if err != nil {
			return err
		}
		content := string(data)
		count = strings.Count(content, input.OldText)
		if count == 0 {
			return fmt.Errorf("oldText was not found in %s", relativeOrDot(path))
		}
		if !input.ReplaceAll && count != 1 {
			return fmt.Errorf("oldText appears %d times in %s; set replaceAll to true or provide a unique snippet", count, relativeOrDot(path))
		}
		replaceCount := 1
		if input.ReplaceAll {
			replaceCount = -1
		}
		next := strings.Replace(content, input.OldText, input.NewText, replaceCount)
		bytesWritten = len([]byte(next))
		return os.WriteFile(path.abs, []byte(next), 0o644)
	})
	if err != nil {
		return protocol.ToolResult{}, err
	}
	replacementCount := 1
	if input.ReplaceAll {
		replacementCount = count
	}
	text := fmt.Sprintf("Edited %s (%d replacement)", relativeOrDot(path), replacementCount)
	if replacementCount != 1 {
		text = fmt.Sprintf("Edited %s (%d replacements)", relativeOrDot(path), replacementCount)
	}
	return textResult(text, editDetails{
		Path:             relativeOrDot(path),
		ReplacementCount: replacementCount,
		BytesWritten:     bytesWritten,
	}), nil
}
