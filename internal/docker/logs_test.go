package docker

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
)

// TestParseRawLogs 验证 TTY 格式日志解析。
func TestParseRawLogs(t *testing.T) {
	logs := parseLogs("container-1", bytes.NewReader([]byte("2026-06-27T08:00:00Z info app started\n")))

	if len(logs) != 1 {
		t.Fatalf("len(logs) = %d, want %d", len(logs), 1)
	}
	if logs[0].Message != "info app started" {
		t.Fatalf("Message = %q, want %q", logs[0].Message, "info app started")
	}
	if logs[0].Stream != "stdout" {
		t.Fatalf("Stream = %q, want %q", logs[0].Stream, "stdout")
	}
	if logs[0].RawMessage != "info app started" {
		t.Fatalf("RawMessage = %q, want %q", logs[0].RawMessage, "info app started")
	}
	if logs[0].Level != "info" {
		t.Fatalf("Level = %q, want %q", logs[0].Level, "info")
	}
}

// TestParseMultiplexedLogs 验证非 TTY 格式日志解析。
func TestParseMultiplexedLogs(t *testing.T) {
	var data bytes.Buffer
	writeFrame(&data, stdcopy.Stdout, "2026-06-27T08:00:00Z out\n")
	writeFrame(&data, stdcopy.Stderr, "2026-06-27T08:00:01Z err\n")

	logs := parseLogs("container-1", bytes.NewReader(data.Bytes()))

	if len(logs) != 2 {
		t.Fatalf("len(logs) = %d, want %d", len(logs), 2)
	}
	if logs[0].Stream != "stdout" || logs[0].Message != "out" {
		t.Fatalf("logs[0] = %#v", logs[0])
	}
	if logs[1].Stream != "stderr" || logs[1].Message != "err" {
		t.Fatalf("logs[1] = %#v", logs[1])
	}
}

// TestGuessLogLevel 验证常见日志级别识别。
func TestGuessLogLevel(t *testing.T) {
	cases := map[string]string{
		"ERROR request failed":       "error",
		`{"level":"warn","msg":"x"}`: "warn",
		"\x1b[31mFATAL panic\x1b[0m": "fatal",
		"plain line":                 "unknown",
	}
	for input, want := range cases {
		if got := guessLogLevel(input); got != want {
			t.Fatalf("guessLogLevel(%q) = %q, want %q", input, got, want)
		}
	}
}

// writeFrame 写入 Docker multiplexed log 帧。
func writeFrame(buffer *bytes.Buffer, stream stdcopy.StdType, payload string) {
	header := make([]byte, 8)
	header[0] = byte(stream)
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	_, _ = buffer.Write(header)
	_, _ = buffer.WriteString(payload)
}
