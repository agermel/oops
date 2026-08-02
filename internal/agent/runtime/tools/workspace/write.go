package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
)

type writeTool struct {
	baseTool
}

type writeInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Append  bool   `json:"append,omitempty"`
}

type writeDetails struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytesWritten"`
	Appended     bool   `json:"appended"`
}

func newWriteTool(cfg workspaceToolConfig) toolruntime.Tool {
	return &writeTool{baseTool: newBaseTool(
		cfg,
		"write",
		"Create or write a UTF-8 text file inside the workspace. Parent directories are created automatically.",
		writeSchema,
		toolruntime.ExecutionModeSequential,
	)}
}

func (t *writeTool) Execute(ctx context.Context, call toolruntime.ToolCall, _ toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return protocol.ToolResult{}, err
	}
	var input writeInput
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
	err = withFileMutationQueue(path.abs, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path.abs), 0o755); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if input.Append {
			file, err := os.OpenFile(path.abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = file.WriteString(input.Content)
			return err
		}
		return os.WriteFile(path.abs, []byte(input.Content), 0o644)
	})
	if err != nil {
		return protocol.ToolResult{}, err
	}
	text := fmt.Sprintf("Wrote %d bytes to %s", len([]byte(input.Content)), relativeOrDot(path))
	if input.Append {
		text = fmt.Sprintf("Appended %d bytes to %s", len([]byte(input.Content)), relativeOrDot(path))
	}
	return textResult(text, writeDetails{
		Path:         relativeOrDot(path),
		BytesWritten: len([]byte(input.Content)),
		Appended:     input.Append,
	}), nil
}
