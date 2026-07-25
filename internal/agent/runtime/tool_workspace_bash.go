package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
)

type bashTool struct {
	baseTool
}

type bashInput struct {
	Command   string `json:"command"`
	CWD       string `json:"cwd,omitempty"`
	TimeoutMS int    `json:"timeoutMs,omitempty"`
}

type bashDetails struct {
	Command    string         `json:"command"`
	CWD        string         `json:"cwd"`
	ExitCode   int            `json:"exitCode"`
	DurationMS int64          `json:"durationMs"`
	Timeout    bool           `json:"timeout"`
	Stdout     string         `json:"stdout,omitempty"`
	Stderr     string         `json:"stderr,omitempty"`
	Truncation map[string]any `json:"truncation,omitempty"`
}

func newBashTool(cfg workspaceToolConfig) toolruntime.Tool {
	return &bashTool{baseTool: newBaseTool(
		cfg,
		"bash",
		"Run a shell command in the workspace with timeout and bounded output.",
		bashSchema,
		toolruntime.ExecutionModeSequential,
	)}
}

func (t *bashTool) Execute(ctx context.Context, call toolruntime.ToolCall, sink toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	var input bashInput
	if err := decodeCall(call.RawArguments, &input); err != nil {
		return protocol.ToolResult{}, err
	}
	if err := requireText(input.Command, "command"); err != nil {
		return protocol.ToolResult{}, err
	}
	cwdInput := input.CWD
	if cwdInput == "" {
		cwdInput = "."
	}
	cwd, err := t.cfg.resolveInside(cwdInput)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	info, err := os.Stat(cwd.abs)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	if !info.IsDir() {
		return protocol.ToolResult{}, fmt.Errorf("cwd is not a directory: %s", relativeOrDot(cwd))
	}
	timeout := t.cfg.commandTimeout
	if input.TimeoutMS > 0 {
		requested := time.Duration(input.TimeoutMS) * time.Millisecond
		if requested < timeout {
			timeout = requested
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if sink != nil {
		_ = sink(ctx, protocol.AgentEvent{Delta: fmt.Sprintf("running in %s", relativeOrDot(cwd))})
	}
	start := time.Now()
	cmd := shellCommand(runCtx, input.Command)
	cmd.Dir = cwd.abs
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	duration := time.Since(start).Milliseconds()
	exitCode := 0
	if runErr != nil {
		exitCode = -1
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	timeoutHit := runCtx.Err() == context.DeadlineExceeded
	stdoutTrunc := t.cfg.truncateTail(stdout.String())
	stderrTrunc := t.cfg.truncateTail(stderr.String())
	text := formatBashOutput(input.Command, relativeOrDot(cwd), exitCode, timeoutHit, stdoutTrunc.content, stderrTrunc.content)
	return textResult(text, bashDetails{
		Command:    input.Command,
		CWD:        relativeOrDot(cwd),
		ExitCode:   exitCode,
		DurationMS: duration,
		Timeout:    timeoutHit,
		Stdout:     stdoutTrunc.content,
		Stderr:     stderrTrunc.content,
		Truncation: map[string]any{
			"stdout": stdoutTrunc.details,
			"stderr": stderrTrunc.details,
		},
	}), nil
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}

func formatBashOutput(command, cwd string, exitCode int, timeout bool, stdout, stderr string) string {
	var b strings.Builder
	b.WriteString("Command: ")
	b.WriteString(command)
	b.WriteString("\nCWD: ")
	b.WriteString(cwd)
	b.WriteString(fmt.Sprintf("\nExit code: %d", exitCode))
	if timeout {
		b.WriteString("\nTimeout: true")
	}
	if stdout != "" {
		b.WriteString("\n\nstdout:\n")
		b.WriteString(stdout)
	}
	if stderr != "" {
		b.WriteString("\n\nstderr:\n")
		b.WriteString(stderr)
	}
	return b.String()
}
