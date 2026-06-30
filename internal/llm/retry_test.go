package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"nil", nil, false},
		{"timeout", errors.New("dial tcp: i/o timeout"), true},
		{"connection refused", errors.New("connection refused"), true},
		{"no such host", errors.New("no such host"), true},
		{"EOF", errors.New("EOF"), true},
		{"broken pipe", errors.New("broken pipe"), true},
		{"reset by peer", errors.New("connection reset by peer"), true},
		{"TLS timeout", errors.New("TLS handshake timeout"), true},
		{"context deadline", errors.New("context deadline exceeded"), true},
		{"500 error", errors.New("HTTP 500 Internal Server Error"), true},
		{"502 error", errors.New("HTTP 502 Bad Gateway"), true},
		{"503 error", errors.New("HTTP 503 Service Unavailable"), true},
		{"504 error", errors.New("HTTP 504 Gateway Timeout"), true},
		{"permission denied", errors.New("permission denied"), false},
		{"not found", errors.New("file not found"), false},
		{"invalid argument", errors.New("invalid argument"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableError(tt.err); got != tt.retryable {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, got, tt.retryable)
			}
		})
	}
}

func TestIsToolError(t *testing.T) {
	tests := []struct {
		result   string
		isError  bool
	}{
		{"查询失败：timeout", true},
		{"日志查询失败：connection refused", true},
		{"连接检查失败：something", true},
		{"机器列表查询失败：oops", true},
		{"该 Nodelet 上没有容器。", false},
		{"[{\"id\": \"local\"}]", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isToolError(tt.result); got != tt.isError {
			t.Errorf("isToolError(%q) = %v, want %v", tt.result, got, tt.isError)
		}
	}
}

func TestRetryOpsCall_Success(t *testing.T) {
	ctx := context.Background()
	calls := 0
	err := retryOpsCall(ctx, RetryPolicy{MaxRetries: 3, Backoff: time.Millisecond}, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestRetryOpsCall_RetryableThenSuccess(t *testing.T) {
	ctx := context.Background()
	calls := 0
	err := retryOpsCall(ctx, RetryPolicy{MaxRetries: 3, Backoff: time.Millisecond}, func() error {
		calls++
		if calls < 3 {
			return errors.New("dial tcp: i/o timeout")
		}
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestRetryOpsCall_AllFail(t *testing.T) {
	ctx := context.Background()
	calls := 0
	err := retryOpsCall(ctx, RetryPolicy{MaxRetries: 2, Backoff: time.Millisecond}, func() error {
		calls++
		return errors.New("connection refused")
	})
	if err == nil {
		t.Error("expected error, got nil")
	}
	if calls != 3 { // initial + 2 retries
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestRetryOpsCall_NonRetryable(t *testing.T) {
	ctx := context.Background()
	calls := 0
	err := retryOpsCall(ctx, RetryPolicy{MaxRetries: 3, Backoff: time.Millisecond}, func() error {
		calls++
		return errors.New("permission denied")
	})
	if err == nil {
		t.Error("expected error, got nil")
	}
	if calls != 1 { // no retry for non-retryable
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestRetryOpsCall_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	err := retryOpsCall(ctx, RetryPolicy{MaxRetries: 5, Backoff: 100 * time.Millisecond}, func() error {
		calls++
		return errors.New("dial tcp: timeout")
	})
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestFormatRetryError(t *testing.T) {
	err := errors.New("connection refused")
	got := formatRetryError("查询失败", err, 3)
	want := "查询失败（已重试3次）：connection refused"
	if got != want {
		t.Errorf("formatRetryError() = %q, want %q", got, want)
	}
}
