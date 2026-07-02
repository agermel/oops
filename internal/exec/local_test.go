package exec

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunSimpleCommand(t *testing.T) {
	ctx := context.Background()
	out := Run(ctx, Input{
		Command: []string{"echo", "hello"},
		Timeout: 5 * time.Second,
	})

	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", out.ExitCode, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "hello") {
		t.Fatalf("expected stdout to contain 'hello', got: %s", out.Stdout)
	}
	if out.Duration <= 0 {
		t.Fatalf("expected positive duration, got %d", out.Duration)
	}
}

func TestRunNonZeroExit(t *testing.T) {
	ctx := context.Background()
	out := Run(ctx, Input{
		Command: []string{"sh", "-c", "echo stderr msg >&2; exit 42"},
		Timeout: 5 * time.Second,
	})

	if out.ExitCode != 42 {
		t.Fatalf("expected exit 42, got %d", out.ExitCode)
	}
	if !strings.Contains(out.Stderr, "stderr msg") {
		t.Fatalf("expected stderr to contain 'stderr msg', got: %s", out.Stderr)
	}
}

func TestRunTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	out := Run(ctx, Input{
		Command: []string{"sleep", "10"},
	})

	if out.ExitCode != -1 {
		t.Fatalf("expected exit -1 (timeout), got %d", out.ExitCode)
	}
}

func TestRunEmptyCommand(t *testing.T) {
	ctx := context.Background()
	out := Run(ctx, Input{
		Command: []string{},
	})

	if out.ExitCode != -1 {
		t.Fatalf("expected exit -1 for empty command, got %d", out.ExitCode)
	}
	if out.Stderr != "empty command" {
		t.Fatalf("expected 'empty command' stderr, got: %s", out.Stderr)
	}
}

func TestRunWorkingDir(t *testing.T) {
	ctx := context.Background()
	out := Run(ctx, Input{
		Command: []string{"pwd"},
		WorkDir: "/tmp",
		Timeout: 5 * time.Second,
	})

	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
	if !strings.Contains(out.Stdout, "/tmp") {
		t.Fatalf("expected stdout to contain '/tmp', got: %s", out.Stdout)
	}
}

func TestRunEnv(t *testing.T) {
	ctx := context.Background()
	out := Run(ctx, Input{
		Command: []string{"sh", "-c", "echo $OOPS_TEST_VAR"},
		Env:     []string{"OOPS_TEST_VAR=hello_from_env"},
		Timeout: 5 * time.Second,
	})

	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", out.ExitCode, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "hello_from_env") {
		t.Fatalf("expected stdout to contain 'hello_from_env', got: %s", out.Stdout)
	}
}

func TestTruncateTail(t *testing.T) {
	t.Run("no truncation", func(t *testing.T) {
		s := "hello\nworld"
		result, truncated := truncateTail(s)
		if truncated {
			t.Fatal("expected no truncation")
		}
		if result != s {
			t.Fatalf("expected %q, got %q", s, result)
		}
	})

	t.Run("truncate lines", func(t *testing.T) {
		var lines []string
		for range DefaultMaxLines + 10 {
			lines = append(lines, "x")
		}
		s := strings.Join(lines, "\n")
		result, truncated := truncateTail(s)
		if !truncated {
			t.Fatal("expected truncation")
		}
		got := len(strings.Split(result, "\n"))
		if got > DefaultMaxLines {
			t.Fatalf("expected at most %d lines, got %d", DefaultMaxLines, got)
		}
	})

	t.Run("truncate bytes", func(t *testing.T) {
		s := strings.Repeat("x", DefaultMaxBytes+100)
		result, truncated := truncateTail(s)
		if !truncated {
			t.Fatal("expected truncation")
		}
		if len(result) > DefaultMaxBytes {
			t.Fatalf("expected at most %d bytes, got %d", DefaultMaxBytes, len(result))
		}
	})

	t.Run("empty string", func(t *testing.T) {
		result, truncated := truncateTail("")
		if truncated {
			t.Fatal("expected no truncation for empty string")
		}
		if result != "" {
			t.Fatalf("expected empty, got %q", result)
		}
	})
}
