// Package console provides a real-time log hub that captures backend
// stdout/stderr and streams it to web clients via Server-Sent Events.
package console

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"oops/internal/loghub"
)

// Entry is a single console log line shipped to the browser.
type Entry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"` // "info" | "warn" | "error"
	Message   string `json:"message"`
}

const historySize = 200

// Hub receives log lines from the backend and fans them out to
// all connected SSE subscribers. It keeps a small ring buffer so
// new subscribers see recent history.
type Hub struct {
	entries *loghub.Hub[Entry]
}

var defaultHub = &Hub{
	entries: loghub.New[Entry](historySize),
}

// Default returns the process-wide singleton hub.
func Default() *Hub { return defaultHub }

// Write implements io.Writer. Each call is treated as one log line
// (the standard log package already emits line-at-a-time). The line is
// forwarded to every subscriber.
func (h *Hub) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n\r")
	if msg == "" {
		return len(p), nil
	}
	entry := Entry{
		Timestamp: time.Now().Format(time.RFC3339),
		Level:     levelFrom(msg),
		Message:   msg,
	}
	h.entries.Push(entry)
	return len(p), nil
}

// Feed writes a raw message directly to the hub without going through log.
// Useful for MCP stderr and other subsystems that don't use log.Printf.
func Feed(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	defaultHub.Write([]byte(msg))
}

// RedirectLog sets the standard log package's output to write to both
// os.Stderr and the hub so all log.Printf calls appear in the console.
func RedirectLog() {
	multi := io.MultiWriter(os.Stderr, defaultHub)
	log.SetOutput(multi)
	log.Default().SetOutput(multi)
}

// Subscribe registers a new SSE subscriber. The returned channel
// receives recent history first, then live entries. Call cancel when done.
func (h *Hub) Subscribe() (<-chan Entry, func()) {
	return h.entries.Subscribe(historySize)
}

// SSEHandler is an http.HandlerFunc that streams console logs to the browser.
func (h *Hub) SSEHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := h.Subscribe()
	defer cancel()

	// Send initial comment to force the browser into streaming mode.
	fmt.Fprintf(w, ":ok\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// levelFrom heuristically determines a severity level from a log message.
func levelFrom(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "fatal") || strings.Contains(lower, "panic"):
		return "error"
	case strings.Contains(lower, "warn") || strings.Contains(lower, "fail"):
		return "warn"
	default:
		return "info"
	}
}

// NewLineWriter returns an io.Writer that feeds each line to the hub
// via Feed. Use this to capture stderr of subprocesses line-by-line.
func NewLineWriter(prefix string) io.Writer {
	r, w := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			Feed("%s: %s", prefix, line)
		}
	}()
	return w
}
