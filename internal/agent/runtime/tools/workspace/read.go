package workspace

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
	Truncation truncationDetails `json:"truncation"`
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

func (t *readTool) Execute(ctx context.Context, call toolruntime.ToolCall, _ toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return protocol.ToolResult{}, err
	}
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
	window, err := readTextWindow(ctx, path.abs, input.Offset, input.Limit, t.cfg.maxLines, t.cfg.maxBytes)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	start := 0
	if input.Offset > 0 {
		start = input.Offset - 1
	}
	if start > window.totalLines {
		start = window.totalLines
	}
	end := window.totalLines
	if input.Limit > 0 && start+input.Limit < end {
		end = start + input.Limit
	}
	displayPath := relativeOrDot(path)
	text := fmt.Sprintf("File: %s\nLines: %d-%d of %d\n\n%s", displayPath, start+1, end, window.totalLines, window.truncation.content)
	if window.truncation.details.FirstLineExceedsLimit {
		text = fmt.Sprintf("File: %s\nLines: %d-%d of %d\n\n[First line exceeds output byte limit]", displayPath, start+1, end, window.totalLines)
	}
	return textResult(text, readDetails{
		Path:       displayPath,
		StartLine:  start + 1,
		EndLine:    end,
		TotalLines: window.totalLines,
		Truncation: window.truncation.details,
		SizeBytes:  int(info.Size()),
	}), nil
}

type textWindow struct {
	totalLines int
	truncation truncationResult
}

type headWindowCollector struct {
	maxLines int
	maxBytes int
	lines    []string

	totalLines       int
	totalBytes       int
	outputBytes      int
	byteLimitReached bool
}

func readTextWindow(ctx context.Context, path string, offset, limit, maxLines, maxBytes int) (textWindow, error) {
	file, err := os.Open(path)
	if err != nil {
		return textWindow{}, err
	}
	defer file.Close()

	start := 0
	if offset > 0 {
		start = offset - 1
	}
	collector := headWindowCollector{maxLines: maxLines, maxBytes: maxBytes}
	reader := bufio.NewReader(file)
	totalLines := 0
	for {
		if err := ctx.Err(); err != nil {
			return textWindow{}, err
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return textWindow{}, readErr
		}
		if len(line) == 0 && readErr == io.EOF {
			if totalLines == 0 {
				totalLines = 1
				if start == 0 {
					collector.add("")
				}
			}
			break
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		line = strings.ToValidUTF8(line, "\uFFFD")
		lineIndex := totalLines
		totalLines++
		if lineIndex >= start && (limit == 0 || lineIndex < start+limit) {
			collector.add(line)
		}
		if readErr == io.EOF {
			break
		}
	}
	return textWindow{totalLines: totalLines, truncation: collector.result()}, nil
}

func (c *headWindowCollector) add(line string) {
	lineBytes := len([]byte(line))
	if c.totalLines > 0 {
		c.totalBytes++
	}
	c.totalLines++
	c.totalBytes += lineBytes

	if len(c.lines) >= c.maxLines || c.byteLimitReached {
		return
	}
	outputLineBytes := lineBytes
	if len(c.lines) > 0 {
		outputLineBytes++
	}
	if c.outputBytes+outputLineBytes > c.maxBytes {
		c.byteLimitReached = true
		return
	}
	c.lines = append(c.lines, line)
	c.outputBytes += outputLineBytes
}

func (c *headWindowCollector) result() truncationResult {
	if c.totalLines == 0 {
		c.add("")
	}
	content := strings.Join(c.lines, "\n")
	details := truncationDetails{
		TotalLines:  c.totalLines,
		TotalBytes:  c.totalBytes,
		OutputLines: len(c.lines),
		OutputBytes: len([]byte(content)),
		MaxLines:    c.maxLines,
		MaxBytes:    c.maxBytes,
	}
	if c.totalLines > c.maxLines || c.totalBytes > c.maxBytes {
		details.Truncated = true
		if c.byteLimitReached {
			details.TruncatedBy = "bytes"
			details.FirstLineExceedsLimit = len(c.lines) == 0
		} else {
			details.TruncatedBy = "lines"
		}
	}
	return truncationResult{content: content, details: details}
}
