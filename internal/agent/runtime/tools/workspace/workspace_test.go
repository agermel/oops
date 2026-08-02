package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
)

func TestReadRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Dir(root)
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := mustTool(t, root, "read", Options{})
	_, err := executeTool(t, tool, map[string]any{"path": "../outside.txt"})
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("error = %v, want path escape", err)
	}
}

func TestReadRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tool := mustTool(t, root, "read", Options{})
	_, err := executeTool(t, tool, map[string]any{"path": "link.txt"})
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("error = %v, want symlink escape", err)
	}
}

func TestReadLineWindowAndTruncation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := mustTool(t, root, "read", Options{MaxLines: 2, MaxBytes: 1024})
	result, err := executeTool(t, tool, map[string]any{"path": "notes.txt", "offset": 2, "limit": 3})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "two\nthree") || strings.Contains(text, "four") {
		t.Fatalf("text = %q", text)
	}
	details := result.Details.(readDetails)
	if !details.Truncation.Truncated || details.Truncation.TruncatedBy != "lines" {
		t.Fatalf("truncation = %#v", details.Truncation)
	}
}

func TestReadStreamsUTF8LinesAndReportsByteLimit(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("界", 100_000) + "\r\nsecond\r\n"
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := mustTool(t, root, "read", Options{MaxLines: 10, MaxBytes: 1024})
	result, err := executeTool(t, tool, map[string]any{"path": "large.txt"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	details := result.Details.(readDetails)
	if details.TotalLines != 2 || details.SizeBytes != len([]byte(content)) {
		t.Fatalf("details = %#v", details)
	}
	if !details.Truncation.Truncated || details.Truncation.TruncatedBy != "bytes" || !details.Truncation.FirstLineExceedsLimit {
		t.Fatalf("truncation = %#v", details.Truncation)
	}
	if text := resultText(t, result); !strings.Contains(text, "[First line exceeds output byte limit]") {
		t.Fatalf("text = %q", text)
	}

	result, err = executeTool(t, tool, map[string]any{"path": "large.txt", "offset": 2, "limit": 1})
	if err != nil {
		t.Fatalf("Execute(offset) error = %v", err)
	}
	if text := resultText(t, result); !strings.HasSuffix(text, "second") || strings.Contains(text, "\r") {
		t.Fatalf("text = %q", text)
	}
}

func TestReadEmptyFileCountsOneLine(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := executeTool(t, mustTool(t, root, "read", Options{}), map[string]any{"path": "empty.txt"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	details := result.Details.(readDetails)
	if details.TotalLines != 1 || details.Truncation.TotalLines != 1 || details.Truncation.OutputLines != 1 {
		t.Fatalf("details = %#v", details)
	}
}

func TestListFindAndGrep(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.go"), []byte("package main\nvar needle = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lsResult, err := executeTool(t, mustTool(t, root, "ls", Options{}), map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("ls error = %v", err)
	}
	if text := resultText(t, lsResult); !strings.Contains(text, "a.txt") || !strings.Contains(text, "sub/") {
		t.Fatalf("ls text = %q", text)
	}

	findResult, err := executeTool(t, mustTool(t, root, "find", Options{}), map[string]any{"path": ".", "pattern": "*.go", "type": "file"})
	if err != nil {
		t.Fatalf("find error = %v", err)
	}
	findDetails := findResult.Details.(findDetails)
	if len(findDetails.Results) != 1 || findDetails.Results[0] != "sub/b.go" {
		t.Fatalf("find results = %#v", findDetails.Results)
	}

	grepResult, err := executeTool(t, mustTool(t, root, "grep", Options{}), map[string]any{"pattern": "needle", "path": ".", "include": "*.go"})
	if err != nil {
		t.Fatalf("grep error = %v", err)
	}
	grepDetails := grepResult.Details.(grepDetails)
	if len(grepDetails.Matches) != 1 || grepDetails.Matches[0].Path != "sub/b.go" || grepDetails.Matches[0].Line != 2 {
		t.Fatalf("grep matches = %#v", grepDetails.Matches)
	}
}

func TestWriteEditAndBash(t *testing.T) {
	root := t.TempDir()
	write := mustTool(t, root, "write", Options{})
	if _, err := executeTool(t, write, map[string]any{"path": "nested/out.txt", "content": "hello world"}); err != nil {
		t.Fatalf("write error = %v", err)
	}
	edit := mustTool(t, root, "edit", Options{})
	if _, err := executeTool(t, edit, map[string]any{"path": "nested/out.txt", "oldText": "world", "newText": "agent"}); err != nil {
		t.Fatalf("edit error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "nested", "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello agent" {
		t.Fatalf("content = %q", data)
	}

	bash := mustTool(t, root, "bash", Options{})
	result, err := executeTool(t, bash, map[string]any{"command": "printf ok", "cwd": "."})
	if err != nil {
		t.Fatalf("bash error = %v", err)
	}
	if text := resultText(t, result); !strings.Contains(text, "Exit code: 0") || !strings.Contains(text, "ok") {
		t.Fatalf("bash text = %q", text)
	}
}

func TestBashKeepsSeparateBoundedStreams(t *testing.T) {
	root := t.TempDir()
	bash := mustTool(t, root, "bash", Options{MaxLines: 2, MaxBytes: 1024})
	result, err := executeTool(t, bash, map[string]any{
		"command": "printf 'one\\ntwo\\nthree\\n'; printf 'warn\\n' >&2",
		"cwd":     ".",
	})
	if err != nil {
		t.Fatalf("bash error = %v", err)
	}
	details := result.Details.(bashDetails)
	if details.Stdout != "two\nthree" || details.Stderr != "warn\n" {
		t.Fatalf("details = %#v", details)
	}
	if !details.Truncation["stdout"].Truncated || details.Truncation["stderr"].Truncated {
		t.Fatalf("truncation = %#v", details.Truncation)
	}
	assertFullOutput(t, details.StdoutOutputPath, "one\ntwo\nthree\n")
	if details.StderrOutputPath != "" {
		t.Fatalf("StderrOutputPath = %q", details.StderrOutputPath)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "stdout:\ntwo\nthree") || !strings.Contains(text, "Full output: "+details.StdoutOutputPath) {
		t.Fatalf("text = %q", text)
	}
}

func TestBashDistinguishesTimeoutAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command uses POSIX exec")
	}
	root := t.TempDir()
	bash := mustTool(t, root, "bash", Options{CommandTimeout: time.Second})

	timedOut, err := executeTool(t, bash, map[string]any{"command": "exec sleep 5", "timeoutMs": 20})
	if err != nil {
		t.Fatalf("timeout execution error = %v", err)
	}
	timeoutDetails := timedOut.Details.(bashDetails)
	if !timeoutDetails.Timeout || timeoutDetails.Cancelled {
		t.Fatalf("timeout details = %#v", timeoutDetails)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, err := json.Marshal(map[string]any{"command": "printf late"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := bash.Execute(ctx, toolruntime.ToolCall{
		ID:           "call_cancelled",
		Name:         "bash",
		RawArguments: raw,
	}, nil)
	if err != nil {
		t.Fatalf("cancelled execution error = %v", err)
	}
	cancelledDetails := cancelled.Details.(bashDetails)
	if cancelledDetails.Timeout || !cancelledDetails.Cancelled {
		t.Fatalf("cancelled details = %#v", cancelledDetails)
	}
}

func TestBashTimeoutStopsChildProcessTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command uses POSIX syntax")
	}
	root := t.TempDir()
	bash := mustTool(t, root, "bash", Options{CommandTimeout: time.Second})
	_, err := executeTool(t, bash, map[string]any{
		"command":   "(sleep 0.1; printf late > child.txt) & wait",
		"timeoutMs": 10,
	})
	if err != nil {
		t.Fatalf("bash error = %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "child.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child.txt stat error = %v, want not exist", err)
	}
}

func TestWorkspaceFileToolsRespectCancelledContext(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args map[string]any
	}{
		{name: "read", args: map[string]any{"path": "file.txt"}},
		{name: "ls", args: map[string]any{"path": "."}},
		{name: "find", args: map[string]any{"path": "."}},
		{name: "grep", args: map[string]any{"path": ".", "pattern": "needle"}},
		{name: "write", args: map[string]any{"path": "new.txt", "content": "late"}},
		{name: "edit", args: map[string]any{"path": "file.txt", "oldText": "needle", "newText": "late"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := mustTool(t, root, test.name, Options{})
			raw, err := json.Marshal(test.args)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = tool.Execute(ctx, toolruntime.ToolCall{
				ID:           "call_cancelled",
				Name:         test.name,
				RawArguments: raw,
			}, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Execute() error = %v, want context.Canceled", err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new.txt stat error = %v, want not exist", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "needle\n" {
		t.Fatalf("file.txt = %q", data)
	}
}

func mustTool(t *testing.T, root, name string, options Options) toolruntime.Tool {
	t.Helper()
	options.Root = root
	toolList, err := NewTools(options)
	if err != nil {
		t.Fatalf("NewTools() error = %v", err)
	}
	for _, item := range toolList {
		if item.Definition().Name == name {
			return item
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func executeTool(t *testing.T, tool toolruntime.Tool, args map[string]any) (protocol.ToolResult, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(context.Background(), toolruntime.ToolCall{
		ID:           "call_1",
		Name:         tool.Definition().Name,
		RawArguments: raw,
	}, nil)
}

func resultText(t *testing.T, result protocol.ToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("missing result content")
	}
	text, ok := result.Content[0].(protocol.TextContent)
	if !ok {
		t.Fatalf("content = %#v", result.Content[0])
	}
	return text.Text
}
