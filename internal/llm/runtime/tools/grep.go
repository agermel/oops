package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/core/toolruntime"
)

const maxGrepLineBytes = 500

type grepTool struct {
	baseTool
}

type grepInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Include    string `json:"include,omitempty"`
	MaxResults int    `json:"maxResults,omitempty"`
}

type grepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type grepDetails struct {
	Path       string      `json:"path"`
	Pattern    string      `json:"pattern"`
	Include    string      `json:"include,omitempty"`
	Matches    []grepMatch `json:"matches"`
	Truncated  bool        `json:"truncated"`
	MaxResults int         `json:"maxResults"`
}

func newGrepTool(cfg config) toolruntime.Tool {
	return &grepTool{baseTool: newBaseTool(
		cfg,
		"grep",
		"Search workspace files with a regular expression and return matching lines.",
		grepSchema,
		toolruntime.ExecutionModeParallel,
	)}
}

func (t *grepTool) Execute(ctx context.Context, call toolruntime.ToolCall, _ toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	var input grepInput
	if err := decodeCall(call.RawArguments, &input); err != nil {
		return protocol.ToolResult{}, err
	}
	if err := requireText(input.Pattern, "pattern"); err != nil {
		return protocol.ToolResult{}, err
	}
	if input.Path == "" {
		input.Path = "."
	}
	re, err := regexp.Compile(input.Pattern)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	limit := firstPositive(input.MaxResults, t.cfg.maxResults)
	if limit > t.cfg.maxResults {
		limit = t.cfg.maxResults
	}
	root, err := t.cfg.resolveInside(input.Path)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	matches := make([]grepMatch, 0, limit)
	truncated := false
	info, err := os.Stat(root.abs)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	if !info.IsDir() {
		if err := t.searchFile(ctx, root.abs, re, input.Include, limit, &matches); err != nil {
			return protocol.ToolResult{}, err
		}
		truncated = len(matches) >= limit
	} else {
		err = filepath.WalkDir(root.abs, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				if path != root.abs && shouldSkipDir(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if err := t.searchFile(ctx, path, re, input.Include, limit, &matches); err != nil {
				return err
			}
			if len(matches) >= limit {
				truncated = true
				return errStopWalk
			}
			return nil
		})
		if err != nil && err != errStopWalk {
			return protocol.ToolResult{}, err
		}
	}
	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		lines = append(lines, fmt.Sprintf("%s:%d:%s", match.Path, match.Line, match.Text))
	}
	text := fmt.Sprintf("Grep results under %s:\n\n%s", relativeOrDot(root), strings.Join(lines, "\n"))
	return textResult(text, grepDetails{
		Path:       relativeOrDot(root),
		Pattern:    input.Pattern,
		Include:    input.Include,
		Matches:    matches,
		Truncated:  truncated,
		MaxResults: limit,
	}), nil
}

func (t *grepTool) searchFile(ctx context.Context, path string, re *regexp.Regexp, include string, limit int, matches *[]grepMatch) error {
	if len(*matches) >= limit {
		return nil
	}
	if err := t.cfg.ensureInsideExisting(path); err != nil {
		return err
	}
	rel, err := filepath.Rel(t.cfg.root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if include != "" && !globMatches(include, rel, filepath.Base(path)) {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		lineNo++
		line := strings.ToValidUTF8(scanner.Text(), "\uFFFD")
		if !re.MatchString(line) {
			continue
		}
		*matches = append(*matches, grepMatch{
			Path: rel,
			Line: lineNo,
			Text: truncateLine(line, maxGrepLineBytes),
		})
		if len(*matches) >= limit {
			return nil
		}
	}
	return scanner.Err()
}

func globMatches(pattern, rel, base string) bool {
	if ok, _ := filepath.Match(pattern, base); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, rel); ok {
		return true
	}
	return false
}

func truncateLine(line string, maxBytes int) string {
	if len([]byte(line)) <= maxBytes {
		return line
	}
	out := truncateHead(line, 1, maxBytes)
	if out.content == "" {
		return "[line exceeds byte limit]"
	}
	return out.content
}
