package docker

import (
	"bytes"
	"strings"
	"time"

	"oops/internal/agent"

	"github.com/moby/moby/api/pkg/stdcopy"
)

// parseLogs 把 Docker 原始日志转换成 Agent 日志结构。
func parseLogs(containerID string, data []byte) []agent.LogEntry {
	if len(data) == 0 {
		return nil
	}

	entries := make([]agent.LogEntry, 0)
	stdout := newLogCollector(containerID, "stdout", &entries)
	stderr := newLogCollector(containerID, "stderr", &entries)
	if _, err := stdcopy.StdCopy(stdout, stderr, bytes.NewReader(data)); err == nil {
		stdout.flush()
		stderr.flush()
		return entries
	}

	rawEntries := make([]agent.LogEntry, 0)
	collector := newLogCollector(containerID, "stdout", &rawEntries)
	_, _ = collector.Write(data)
	collector.flush()
	return rawEntries
}

// logCollector 收集某个输出流上的日志行。
type logCollector struct {
	containerID string
	stream      string
	buffer      bytes.Buffer
	entries     *[]agent.LogEntry
}

// newLogCollector 创建日志收集器。
func newLogCollector(containerID string, stream string, entries *[]agent.LogEntry) *logCollector {
	return &logCollector{containerID: containerID, stream: stream, entries: entries}
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
		*c.entries = append(*c.entries, parseLogLine(c.containerID, c.stream, line))
	}
	return written, nil
}

// flush 解析最后一段没有换行符的日志。
func (c *logCollector) flush() {
	line := strings.TrimSuffix(c.buffer.String(), "\n")
	if line == "" {
		return
	}
	*c.entries = append(*c.entries, parseLogLine(c.containerID, c.stream, line))
	c.buffer.Reset()
}

// parseLogLine 解析 Docker 带时间戳的单行日志。
func parseLogLine(containerID string, stream string, line string) agent.LogEntry {
	timestamp, message := splitTimestamp(line)
	return agent.LogEntry{
		Timestamp:   timestamp,
		ContainerID: containerID,
		Stream:      stream,
		Message:     message,
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
