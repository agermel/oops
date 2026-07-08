package tools

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/core/toolruntime"
)

type findTool struct {
	baseTool
}

type findInput struct {
	Path       string `json:"path,omitempty"`
	Pattern    string `json:"pattern,omitempty"`
	Type       string `json:"type,omitempty"`
	MaxResults int    `json:"maxResults,omitempty"`
}

type findDetails struct {
	Path       string   `json:"path"`
	Pattern    string   `json:"pattern,omitempty"`
	Type       string   `json:"type"`
	Results    []string `json:"results"`
	Truncated  bool     `json:"truncated"`
	MaxResults int      `json:"maxResults"`
}

func newFindTool(cfg config) toolruntime.Tool {
	return &findTool{baseTool: newBaseTool(
		cfg,
		"find",
		"Find files or directories in the workspace by name substring or glob pattern.",
		findSchema,
		toolruntime.ExecutionModeParallel,
	)}
}

func (t *findTool) Execute(_ context.Context, call toolruntime.ToolCall, _ toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	var input findInput
	if err := decodeCall(call.RawArguments, &input); err != nil {
		return protocol.ToolResult{}, err
	}
	if input.Path == "" {
		input.Path = "."
	}
	if input.Type == "" {
		input.Type = "any"
	}
	if input.Type != "any" && input.Type != "file" && input.Type != "dir" {
		return protocol.ToolResult{}, fmt.Errorf("type must be any, file, or dir")
	}
	limit := firstPositive(input.MaxResults, t.cfg.maxResults)
	if limit > t.cfg.maxResults {
		limit = t.cfg.maxResults
	}
	root, err := t.cfg.resolveInside(input.Path)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	results := make([]string, 0, limit)
	truncated := false
	err = filepath.WalkDir(root.abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root.abs {
			return nil
		}
		if entry.IsDir() && shouldSkipDir(entry.Name()) {
			return filepath.SkipDir
		}
		if !findTypeMatches(entry, input.Type) || !namePatternMatches(root.abs, path, input.Pattern) {
			return nil
		}
		rel, err := filepath.Rel(t.cfg.root, path)
		if err != nil {
			return err
		}
		results = append(results, filepath.ToSlash(rel))
		if len(results) >= limit {
			truncated = true
			return errStopWalk
		}
		return nil
	})
	if err != nil && err != errStopWalk {
		return protocol.ToolResult{}, err
	}
	text := fmt.Sprintf("Find results under %s:\n\n%s", relativeOrDot(root), strings.Join(results, "\n"))
	return textResult(text, findDetails{
		Path:       relativeOrDot(root),
		Pattern:    input.Pattern,
		Type:       input.Type,
		Results:    results,
		Truncated:  truncated,
		MaxResults: limit,
	}), nil
}

func findTypeMatches(entry fs.DirEntry, typ string) bool {
	switch typ {
	case "file":
		return !entry.IsDir()
	case "dir":
		return entry.IsDir()
	default:
		return true
	}
}

func namePatternMatches(root, path, pattern string) bool {
	if pattern == "" {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	base := filepath.Base(path)
	if strings.ContainsAny(pattern, "*?[") {
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, rel); ok {
			return true
		}
		return false
	}
	needle := strings.ToLower(pattern)
	return strings.Contains(strings.ToLower(base), needle) || strings.Contains(strings.ToLower(rel), needle)
}
