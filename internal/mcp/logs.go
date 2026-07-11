package mcp

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"

	"oops/internal/loghub"
)

const (
	connectionLogHistorySize     = 1000
	connectionLogMaxSubscribers  = 32
	connectionLogSubscriberQueue = 128
	connectionLogMaxMessageBytes = 16 << 10
	connectionLogTruncatedMarker = " …[truncated]"
)

var (
	// ErrConnectionLogSubscriberLimit reports an overloaded MCP log stream.
	ErrConnectionLogSubscriberLimit = errors.New("connection log subscriber limit reached")
	// ErrConnectionLogClosed reports a manager that has stopped serving logs.
	ErrConnectionLogClosed = errors.New("connection log hub closed")
)

// LogEntry is a single MCP connection log line.
type LogEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	ConnectionID string    `json:"connectionId"`
	Stream       string    `json:"stream"`
	Message      string    `json:"message"`
	RawMessage   string    `json:"rawMessage,omitempty"`
	Level        string    `json:"level,omitempty"`
}

// ConnectionLogHub keeps bounded logs for one MCP connection.
type ConnectionLogHub struct {
	connectionID string
	entries      *loghub.Hub[LogEntry]
}

func NewConnectionLogHub(connectionID string) *ConnectionLogHub {
	return newConnectionLogHubWithOptions(connectionID, loghub.Options{
		HistorySize:     connectionLogHistorySize,
		MaxSubscribers:  connectionLogMaxSubscribers,
		SubscriberQueue: connectionLogSubscriberQueue,
	})
}

func newConnectionLogHubWithOptions(connectionID string, options loghub.Options) *ConnectionLogHub {
	return &ConnectionLogHub{
		connectionID: connectionID,
		entries:      loghub.NewWithOptions[LogEntry](options),
	}
}

func (h *ConnectionLogHub) Append(stream, level, message string) {
	if h == nil {
		return
	}
	message = strings.TrimRight(message, "\r\n")
	if message == "" {
		return
	}
	message = truncateConnectionLogMessage(message)
	if level == "" {
		level = levelFromLogMessage(message)
	}
	h.entries.Push(LogEntry{
		Timestamp:    time.Now(),
		ConnectionID: h.connectionID,
		Stream:       stream,
		Message:      message,
		RawMessage:   message,
		Level:        level,
	})
}

func (h *ConnectionLogHub) Snapshot(tail int) []LogEntry {
	if h == nil {
		return nil
	}
	return h.entries.Snapshot(tail)
}

func (h *ConnectionLogHub) Subscribe(tail int) (<-chan LogEntry, func(), error) {
	if h == nil {
		return nil, nil, ErrConnectionLogClosed
	}
	ch, cancel, err := h.entries.Subscribe(tail)
	switch {
	case errors.Is(err, loghub.ErrSubscriberLimit):
		return nil, nil, ErrConnectionLogSubscriberLimit
	case errors.Is(err, loghub.ErrClosed):
		return nil, nil, ErrConnectionLogClosed
	default:
		return ch, cancel, err
	}
}

func (h *ConnectionLogHub) Close() {
	if h != nil {
		h.entries.Close()
	}
}

func (h *ConnectionLogHub) LineWriter(stream string) *ConnectionLogLineWriter {
	return &ConnectionLogLineWriter{hub: h, stream: stream}
}

// ConnectionLogLineWriter turns chunked process output into bounded log lines.
type ConnectionLogLineWriter struct {
	mu        sync.Mutex
	hub       *ConnectionLogHub
	stream    string
	buf       bytes.Buffer
	truncated bool
	closed    bool
}

func (w *ConnectionLogLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}

	written := len(p)
	for len(p) > 0 {
		lineEnd := bytes.IndexByte(p, '\n')
		if lineEnd < 0 {
			w.appendChunk(p)
			break
		}
		w.appendChunk(p[:lineEnd])
		w.emitBuffered()
		p = p[lineEnd+1:]
	}
	return written, nil
}

func (w *ConnectionLogLineWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.emitBuffered()
	w.closed = true
	return nil
}

func (w *ConnectionLogLineWriter) appendChunk(chunk []byte) {
	maxContentBytes := connectionLogMaxMessageBytes - len(connectionLogTruncatedMarker)
	remaining := maxContentBytes - w.buf.Len()
	if remaining <= 0 {
		if len(chunk) > 0 {
			w.truncated = true
		}
		return
	}
	if len(chunk) > remaining {
		_, _ = w.buf.Write(chunk[:remaining])
		w.truncated = true
		return
	}
	_, _ = w.buf.Write(chunk)
}

func (w *ConnectionLogLineWriter) emitBuffered() {
	line := strings.TrimRight(w.buf.String(), "\r")
	if w.truncated {
		line += connectionLogTruncatedMarker
	}
	w.buf.Reset()
	w.truncated = false
	if strings.TrimSpace(line) == "" {
		return
	}
	w.hub.Append(w.stream, "", line)
}

func truncateConnectionLogMessage(message string) string {
	if len(message) <= connectionLogMaxMessageBytes {
		return message
	}
	return message[:connectionLogMaxMessageBytes-len(connectionLogTruncatedMarker)] + connectionLogTruncatedMarker
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
