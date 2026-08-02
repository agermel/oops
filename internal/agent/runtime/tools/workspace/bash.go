package workspace

import (
	"context"
	"errors"
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
	Command          string                       `json:"command"`
	CWD              string                       `json:"cwd"`
	ExitCode         int                          `json:"exitCode"`
	DurationMS       int64                        `json:"durationMs"`
	Timeout          bool                         `json:"timeout"`
	Cancelled        bool                         `json:"cancelled"`
	Stdout           string                       `json:"stdout,omitempty"`
	Stderr           string                       `json:"stderr,omitempty"`
	StdoutOutputPath string                       `json:"stdoutOutputPath,omitempty"`
	StderrOutputPath string                       `json:"stderrOutputPath,omitempty"`
	Truncation       map[string]truncationDetails `json:"truncation,omitempty"`
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
	cmd, err := shellCommand(runCtx, input.Command)
	if err != nil {
		return protocol.ToolResult{}, err
	}
	cmd.Dir = cwd.abs
	cmd.WaitDelay = 100 * time.Millisecond
	stdout := newShellOutputCapture(t.cfg.maxLines, t.cfg.maxBytes)
	stderr := newShellOutputCapture(t.cfg.maxLines, t.cfg.maxBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	duration := time.Since(start).Milliseconds()
	stdoutResult, stdoutErr := stdout.finish()
	stderrResult, stderrErr := stderr.finish()
	if captureErr := errors.Join(stdoutErr, stderrErr); captureErr != nil {
		return protocol.ToolResult{}, fmt.Errorf("capture shell output: %w", captureErr)
	}
	exitCode := 0
	if runErr != nil {
		exitCode = -1
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	timeoutHit := runCtx.Err() == context.DeadlineExceeded
	cancelled := runCtx.Err() == context.Canceled
	text := formatBashOutput(input.Command, relativeOrDot(cwd), exitCode, timeoutHit, cancelled, stdoutResult, stderrResult)
	return textResult(text, bashDetails{
		Command:          input.Command,
		CWD:              relativeOrDot(cwd),
		ExitCode:         exitCode,
		DurationMS:       duration,
		Timeout:          timeoutHit,
		Cancelled:        cancelled,
		Stdout:           stdoutResult.content,
		Stderr:           stderrResult.content,
		StdoutOutputPath: stdoutResult.fullOutputPath,
		StderrOutputPath: stderrResult.fullOutputPath,
		Truncation: map[string]truncationDetails{
			"stdout": stdoutResult.details,
			"stderr": stderrResult.details,
		},
	}), nil
}

func shellCommand(ctx context.Context, command string) (*exec.Cmd, error) {
	if runtime.GOOS != "windows" {
		if _, err := os.Stat("/bin/bash"); err == nil {
			return commandForShell(ctx, "/bin/bash", command), nil
		}
	}
	for _, name := range []string{"bash", "bash.exe"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return commandForShell(ctx, path, command), nil
		}
	}
	if runtime.GOOS != "windows" {
		path, err := exec.LookPath("sh")
		if err == nil {
			return commandForShell(ctx, path, command), nil
		}
	}
	return nil, errors.New("bash shell is unavailable")
}

func commandForShell(ctx context.Context, shell, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	configureShellProcess(cmd)
	return cmd
}

func formatBashOutput(command, cwd string, exitCode int, timeout, cancelled bool, stdout, stderr shellCaptureResult) string {
	var b strings.Builder
	b.WriteString("Command: ")
	b.WriteString(command)
	b.WriteString("\nCWD: ")
	b.WriteString(cwd)
	b.WriteString(fmt.Sprintf("\nExit code: %d", exitCode))
	if timeout {
		b.WriteString("\nTimeout: true")
	}
	if cancelled {
		b.WriteString("\nCancelled: true")
	}
	if stdout.content != "" || stdout.fullOutputPath != "" {
		b.WriteString("\n\nstdout:\n")
		b.WriteString(stdout.content)
		writeFullOutputNotice(&b, "stdout", stdout)
	}
	if stderr.content != "" || stderr.fullOutputPath != "" {
		b.WriteString("\n\nstderr:\n")
		b.WriteString(stderr.content)
		writeFullOutputNotice(&b, "stderr", stderr)
	}
	return b.String()
}

func writeFullOutputNotice(b *strings.Builder, label string, result shellCaptureResult) {
	if result.fullOutputPath == "" {
		return
	}
	if result.content != "" {
		b.WriteString("\n\n")
	}
	b.WriteString("[")
	b.WriteString(label)
	b.WriteString(" truncated. Full output: ")
	b.WriteString(result.fullOutputPath)
	b.WriteString("]")
}
