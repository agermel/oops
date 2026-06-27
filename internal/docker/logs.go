package docker

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"

	"oops/internal/nodelet"

	"github.com/moby/moby/api/pkg/stdcopy"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// parseLogs 把 Docker 原始日志转换成 Nodelet 日志结构。
func parseLogs(containerID string, data []byte) []nodelet.LogEntry {
	if len(data) == 0 {
		return nil
	}

	entries := make([]nodelet.LogEntry, 0)
	stdout := newLogCollector(containerID, "stdout", func(entry nodelet.LogEntry) {
		entries = append(entries, entry)
	})
	stderr := newLogCollector(containerID, "stderr", func(entry nodelet.LogEntry) {
		entries = append(entries, entry)
	})
	if _, err := stdcopy.StdCopy(stdout, stderr, bytes.NewReader(data)); err == nil {
		stdout.flush()
		stderr.flush()
		return entries
	}

	rawEntries := make([]nodelet.LogEntry, 0)
	collector := newLogCollector(containerID, "stdout", func(entry nodelet.LogEntry) {
		rawEntries = append(rawEntries, entry)
	})
	_, _ = collector.Write(data)
	collector.flush()
	return rawEntries
}

// streamLogs 把 Docker 日志流转换成 Nodelet 日志结构流。
func streamLogs(ctx context.Context, containerID string, reader io.ReadCloser) <-chan nodelet.LogEntry {
	entries := make(chan nodelet.LogEntry)
	go func() {
		defer close(entries)
		defer reader.Close()

		emit := func(entry nodelet.LogEntry) {
			select {
			case entries <- entry:
			case <-ctx.Done():
			}
		}

		buffered := bufio.NewReader(reader)
		stdout := newLogCollector(containerID, "stdout", emit)
		stderr := newLogCollector(containerID, "stderr", emit)
		if looksLikeDockerFrame(buffered) {
			_, _ = stdcopy.StdCopy(stdout, stderr, buffered)
			stdout.flush()
			stderr.flush()
			return
		}

		_, _ = io.Copy(stdout, buffered)
		stdout.flush()
	}()
	return entries
}

// looksLikeDockerFrame 判断日志流是否是 Docker multiplexed frame。
func looksLikeDockerFrame(reader *bufio.Reader) bool {
	header, err := reader.Peek(8)
	if err != nil {
		return false
	}
	return (header[0] == 1 || header[0] == 2) && header[1] == 0 && header[2] == 0 && header[3] == 0
}

// logCollector 收集某个输出流上的日志行。
type logCollector struct {
	containerID string
	stream      string
	buffer      bytes.Buffer
	emit        func(nodelet.LogEntry)
}

// newLogCollector 创建日志收集器。
func newLogCollector(containerID string, stream string, emit func(nodelet.LogEntry)) *logCollector {
	return &logCollector{containerID: containerID, stream: stream, emit: emit}
}

// Write 写入 Docker 日志片段并按行解析。
func (c *logCollector) Write(p []byte) (int, error) {
	written, err := c.buffer.Write(p)
	if err != nil {
		return written, err
	}

	data := c.buffer.String()
	lines := strings.Split(data, "\n")
	c.buffer.Reset()

	limit := len(lines)
	if !strings.HasSuffix(data, "\n") {
		limit--
		_, _ = c.buffer.WriteString(lines[len(lines)-1])
	}

	for _, line := range lines[:limit] {
		if line == "" {
			continue
		}
		c.emit(parseLogLine(c.containerID, c.stream, line))
	}
	return written, nil
}

// flush 解析最后一段没有换行符的日志。
func (c *logCollector) flush() {
	line := strings.TrimSuffix(c.buffer.String(), "\n")
	if line == "" {
		return
	}
	c.emit(parseLogLine(c.containerID, c.stream, line))
	c.buffer.Reset()
}

// parseLogLine 解析 Docker 带时间戳的单行日志。
func parseLogLine(containerID string, stream string, line string) nodelet.LogEntry {
	timestamp, message := splitTimestamp(line)
	return nodelet.LogEntry{
		Timestamp:   timestamp,
		ContainerID: containerID,
		Stream:      stream,
		Message:     message,
		RawMessage:  message,
		Level:       guessLogLevel(message),
	}
}

// splitTimestamp 拆分 Docker logs --timestamps 的时间和内容。
func splitTimestamp(line string) (time.Time, string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) != 2 {
		return time.Time{}, line
	}
	timestamp, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, line
	}
	return timestamp, parts[1]
}

// guessLogLevel 根据常见日志词判断级别。
func guessLogLevel(message string) string {
	normalized := strings.ToLower(ansiPattern.ReplaceAllString(message, ""))
	words := strings.FieldsFunc(normalized, func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	for _, word := range words {
		switch word {
		case "fatal", "critical", "crit", "severe":
			return "fatal"
		case "error", "err":
			return "error"
		case "warn", "warning", "wrn":
			return "warn"
		case "info", "inf":
			return "info"
		case "debug", "dbg":
			return "debug"
		case "trace", "verbose":
			return "trace"
		}
	}
	return "unknown"
}
