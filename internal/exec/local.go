// Package exec 在容器内执行命令，提供超时、输出截断等安全控制。
// oops/nodelet 本身跑在容器中，容器即是沙盒——因此直接使用 os/exec，不嵌套 Docker。
package exec

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"oops/internal/logutil"

	"go.uber.org/zap"
)

const (
	// DefaultMaxLines 是输出截断的默认最大行数。
	DefaultMaxLines = 2000

	// DefaultMaxBytes 是输出截断的默认最大字节数。
	DefaultMaxBytes = 50 * 1024
)

// Input 是一次命令执行的输入参数。
type Input struct {
	Command []string      // 命令及参数，如 ["go", "test", "./..."]
	WorkDir string        // 工作目录，空串表示当前进程工作目录
	Timeout time.Duration // 超时，调用方应设置 ctx deadline
	Env     []string      // 额外环境变量（追加到当前进程环境）
}

// Output 是一次命令执行的结果。
type Output struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exitCode"`
	Duration  int64  `json:"durationMs"`
	Truncated bool   `json:"truncated"`
}

// Run 执行命令，捕获 stdout/stderr，自动截断输出。
// ctx 应包含超时 deadline（由调用方设置）。
func Run(ctx context.Context, input Input) Output {
	start := time.Now()

	if len(input.Command) == 0 {
		return Output{
			ExitCode: -1,
			Stderr:   "empty command",
			Duration: time.Since(start).Milliseconds(),
		}
	}

	cmd := exec.CommandContext(ctx, input.Command[0], input.Command[1:]...)
	cmd.Dir = input.WorkDir
	if len(input.Env) > 0 {
		cmd.Env = append(cmd.Environ(), input.Env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logutil.Info("exec: running",
		zap.Strings("command", input.Command),
		zap.String("workDir", input.WorkDir),
	)

	err := cmd.Run()
	duration := time.Since(start).Milliseconds()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// context deadline / cancel 等非退出码错误
			exitCode = -1
		}
	}

	stdoutStr := stdout.String()
	stderrStr := stderr.String()
	truncated := false

	// 截断：保留尾部
	stdoutStr, stdoutTrunc := truncateTail(stdoutStr)
	stderrStr, stderrTrunc := truncateTail(stderrStr)
	truncated = stdoutTrunc || stderrTrunc

	out := Output{
		Stdout:    stdoutStr,
		Stderr:    stderrStr,
		ExitCode:  exitCode,
		Duration:  duration,
		Truncated: truncated,
	}

	logutil.Info("exec: done",
		zap.Strings("command", input.Command),
		zap.Int("exitCode", exitCode),
		zap.Int64("durationMs", duration),
		zap.Bool("truncated", truncated),
	)

	return out
}

// truncateTail 将文本截断到 DefaultMaxLines 行和 DefaultMaxBytes 字节，保留尾部。
func truncateTail(s string) (string, bool) {
	if s == "" {
		return s, false
	}

	lines := strings.Split(s, "\n")
	lineTruncated := len(lines) > DefaultMaxLines
	if lineTruncated {
		lines = lines[len(lines)-DefaultMaxLines:]
	}

	result := strings.Join(lines, "\n")
	byteTruncated := len(result) > DefaultMaxBytes
	if byteTruncated {
		result = result[len(result)-DefaultMaxBytes:]
	}

	truncated := lineTruncated || byteTruncated
	return result, truncated
}
