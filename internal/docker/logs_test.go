package docker

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
)

// TestParseRawLogs 验证 TTY 格式日志解析。
func TestParseRawLogs(t *testing.T) {
	logs := parseLogs("container-1", []byte("2026-06-27T08:00:00Z app started\n"))

	if len(logs) != 1 {
		t.Fatalf("len(logs) = %d, want %d", len(logs), 1)
	}
	if logs[0].Message != "app started" {
		t.Fatalf("Message = %q, want %q", logs[0].Message, "app started")
	}
	if logs[0].Stream != "stdout" {
		t.Fatalf("Stream = %q, want %q", logs[0].Stream, "stdout")
	}
}

// TestParseMultiplexedLogs 验证非 TTY 格式日志解析。
func TestParseMultiplexedLogs(t *testing.T) {
	var data bytes.Buffer
	writeFrame(&data, stdcopy.Stdout, "2026-06-27T08:00:00Z out\n")
	writeFrame(&data, stdcopy.Stderr, "2026-06-27T08:00:01Z err\n")

	logs := parseLogs("container-1", data.Bytes())

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

// writeFrame 写入 Docker multiplexed log 帧。
func writeFrame(buffer *bytes.Buffer, stream stdcopy.StdType, payload string) {
	header := make([]byte, 8)
	header[0] = byte(stream)
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	_, _ = buffer.Write(header)
	_, _ = buffer.WriteString(payload)
}
