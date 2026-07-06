package mcp

import (
	"strings"
	"sync"
	"time"
	"unicode"
)

const connectionLogHistorySize = 1000

// LogEntry is a single MCP connection log line.
type LogEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	ConnectionID string    `json:"connectionId"`
	Stream       string    `json:"stream"`
	Message      string    `json:"message"`
	RawMessage   string    `json:"rawMessage,omitempty"`
	Level        string    `json:"level,omitempty"`
}

// ConnectionLogHub keeps recent logs for one MCP connection and fans out live entries.
type ConnectionLogHub struct {
	connectionID string
	mu           sync.RWMutex
	subscribers  map[chan LogEntry]struct{}
	history      []LogEntry
	pos          int
}

func NewConnectionLogHub(connectionID string) *ConnectionLogHub {
	return &ConnectionLogHub{
		connectionID: connectionID,
		subscribers:  make(map[chan LogEntry]struct{}),
		history:      make([]LogEntry, connectionLogHistorySize),
	}
}

func (h *ConnectionLogHub) Append(stream, level, message string) {
	message = strings.TrimRight(message, "\r\n")
	if message == "" {
		return
	}
	if level == "" {
		level = levelFromLogMessage(message)
	}
	entry := LogEntry{
		Timestamp:    time.Now(),
		ConnectionID: h.connectionID,
		Stream:       stream,
		Message:      message,
		RawMessage:   message,
		Level:        level,
	}
	h.push(entry)
	h.broadcast(entry)
}

func (h *ConnectionLogHub) Snapshot(tail int) []LogEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()

	total := h.pos
	if total > connectionLogHistorySize {
		total = connectionLogHistorySize
	}
	if tail <= 0 || tail > total {
		tail = total
	}

	out := make([]LogEntry, 0, tail)
	start := h.pos - tail
	for i := start; i < h.pos; i++ {
		out = append(out, h.history[i%connectionLogHistorySize])
	}
	return out
}

func (h *ConnectionLogHub) Subscribe(tail int) (<-chan LogEntry, func()) {
	ch := make(chan LogEntry, 256)

	for _, entry := range h.Snapshot(tail) {
		ch <- entry
	}

	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		delete(h.subscribers, ch)
		h.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

func (h *ConnectionLogHub) LineWriter(stream string) *ConnectionLogLineWriter {
	return &ConnectionLogLineWriter{hub: h, stream: stream}
}

func (h *ConnectionLogHub) push(entry LogEntry) {
	h.mu.Lock()
	h.history[h.pos%connectionLogHistorySize] = entry
	h.pos++
	h.mu.Unlock()
}

func (h *ConnectionLogHub) broadcast(entry LogEntry) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers {
		select {
		case ch <- entry:
		default:
		}
	}
}

// ConnectionLogLineWriter turns chunked process output into connection log lines.
type ConnectionLogLineWriter struct {
	mu     sync.Mutex
	hub    *ConnectionLogHub
	stream string
	buf    strings.Builder
}

func (w *ConnectionLogLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	written, err := w.buf.Write(p)
	if err != nil {
		return written, err
	}

	data := w.buf.String()
	lines := strings.Split(data, "\n")
	w.buf.Reset()

	limit := len(lines)
	if !strings.HasSuffix(data, "\n") {
		limit--
		_, _ = w.buf.WriteString(lines[len(lines)-1])
	}

	for _, line := range lines[:limit] {
		w.emit(line)
	}
	return written, nil
}

func (w *ConnectionLogLineWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.emit(w.buf.String())
	w.buf.Reset()
	return nil
}

func (w *ConnectionLogLineWriter) emit(line string) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" {
		return
	}
	w.hub.Append(w.stream, "", line)
}

func levelFromLogMessage(message string) string {
	normalized := strings.ToLower(message)
	words := strings.FieldsFunc(normalized, func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	for _, word := range words {
		switch word {
		case "fatal", "critical", "crit", "severe", "panic":
			return "fatal"
		case "error", "err", "failed", "failure":
			return "error"
		case "warn", "warning", "wrn":
			return "warn"
		case "debug", "dbg":
			return "debug"
		case "trace", "verbose":
			return "trace"
		case "info", "inf":
			return "info"
		}
	}
	return "info"
}
