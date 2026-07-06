package tools

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// RetryPolicy 定义工具调用的重试策略。
type RetryPolicy struct {
	MaxRetries int           // 最大重试次数（默认 3）
	Backoff    time.Duration // 初始退避时间（默认 1s，指数增长）
}

// DefaultRetryPolicy 是全局默认重试策略。
var DefaultRetryPolicy = RetryPolicy{
	MaxRetries: 3,
	Backoff:    time.Second,
}

// isToolError 检查工具结果字符串是否表示工具层错误。
// 工具在 ops 调用失败时返回 "Xxx失败: ..." 格式的内容。
func isToolError(result string) bool {
	prefixes := []string{
		"查询失败",
		"日志查询失败",
		"连接检查失败",
		"机器列表查询失败",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(result, p) {
			return true
		}
	}
	return false
}

// isRetryableError 判断 Go error 是否为瞬时性错误（可重试）。
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()

	// 网络层瞬时错误。
	if strings.Contains(s, "timeout") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "reset by peer") ||
		strings.Contains(s, "TLS handshake timeout") ||
		strings.Contains(s, "dial tcp") ||
		strings.Contains(s, "connection reset") {
		return true
	}

	// HTTP 5xx 服务端错误。
	if strings.Contains(s, "500") ||
		strings.Contains(s, "502") ||
		strings.Contains(s, "503") ||
		strings.Contains(s, "504") {
		return true
	}

	// context 超时 / 取消。
	if strings.Contains(s, "context deadline exceeded") ||
		strings.Contains(s, "context canceled") {
		return true
	}

	return false
}

// retryOpsCall 对 fn 执行重试逻辑。
// fn 返回 error 为 nil 时表示成功。
// 返回 nil 表示成功（可能在重试后）；返回 error 表示所有尝试均失败或错误不可重试。
func retryOpsCall(ctx context.Context, policy RetryPolicy, fn func() error) error {
	var lastErr error
	backoff := policy.Backoff

	for attempt := 0; attempt <= policy.MaxRetries; attempt++ {
		// 非首次尝试：等待退避时间。
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// 不可重试的错误直接返回。
		if !isRetryableError(lastErr) {
			return lastErr
		}

		// 检查 context 是否已取消。
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	return lastErr
}

// formatRetryError 生成带重试信息的工具错误消息。
func formatRetryError(prefix string, err error, maxRetries int) string {
	return prefix + "（已重试" + strconv.Itoa(maxRetries) + "次）：" + err.Error()
}
