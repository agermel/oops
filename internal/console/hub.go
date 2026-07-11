// Package console provides a real-time process log hub for browser clients.
package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"oops/internal/loghub"
)

const (
	defaultHistorySize     = 200
	defaultMaxSubscribers  = 32
	defaultSubscriberQueue = 128
	defaultMaxEntryBytes   = 16 << 10
)

// Entry is a single console log line shipped to the browser.
type Entry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// Options bounds one process console hub and supports deterministic tests.
type Options struct {
	HistorySize     int
	MaxSubscribers  int
	SubscriberQueue int
	MaxEntryBytes   int
	Now             func() time.Time
}

// Hub receives log lines from the backend and fans them out to SSE clients.
type Hub struct {
	entries       *loghub.Hub[Entry]
	maxEntryBytes int
	now           func() time.Time
}

// NewHub creates the process-owned console hub used by the composition root.
func NewHub() *Hub {
	return NewHubWithOptions(Options{})
}

// NewHubWithOptions creates a console hub with explicit capacity limits.
func NewHubWithOptions(options Options) *Hub {
	if options.HistorySize <= 0 {
		options.HistorySize = defaultHistorySize
	}
	if options.MaxSubscribers <= 0 {
		options.MaxSubscribers = defaultMaxSubscribers
	}
	if options.SubscriberQueue <= 0 {
		options.SubscriberQueue = defaultSubscriberQueue
	}
	if options.MaxEntryBytes <= 0 {
		options.MaxEntryBytes = defaultMaxEntryBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Hub{
		entries: loghub.NewWithOptions[Entry](loghub.Options{
			HistorySize:     options.HistorySize,
			MaxSubscribers:  options.MaxSubscribers,
			SubscriberQueue: options.SubscriberQueue,
		}),
		maxEntryBytes: options.MaxEntryBytes,
		now:           options.Now,
	}
}

// Write implements io.Writer. Each call becomes one bounded console entry.
func (h *Hub) Write(p []byte) (int, error) {
	if h == nil {
		return len(p), nil
	}
	msg := strings.TrimRight(string(p), "\n\r")
	if msg == "" {
		return len(p), nil
	}
	h.entries.Push(Entry{
		Timestamp: h.now().Format(time.RFC3339),
		Level:     levelFrom(msg),
		Message:   truncateMessage(msg, h.maxEntryBytes),
	})
	return len(p), nil
}

// Feed records one formatted line in this hub.
func (h *Hub) Feed(format string, args ...any) {
	if h == nil {
		return
	}
	_, _ = h.Write([]byte(fmt.Sprintf(format, args...)))
}

// Subscribe registers a bounded SSE subscriber with replay history.
func (h *Hub) Subscribe() (<-chan Entry, func(), error) {
	if h == nil {
		return nil, nil, loghub.ErrClosed
	}
	return h.entries.Subscribe(defaultHistorySize)
}

// Close closes every subscriber and rejects future writes/subscriptions.
func (h *Hub) Close() {
	if h != nil {
		h.entries.Close()
	}
}

// SSEHandler streams console logs to one HTTP client.
func (h *Hub) SSEHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch, cancel, err := h.Subscribe()
	if err != nil {
		if errors.Is(err, loghub.ErrSubscriberLimit) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, err.Error(), http.StatusTooManyRequests)
			return
		}
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, ":ok\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
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

func truncateMessage(message string, maxBytes int) string {
	if len(message) <= maxBytes {
		return message
	}
	const marker = " …[truncated]"
	if maxBytes <= len(marker) {
		return marker[:maxBytes]
	}
	return message[:maxBytes-len(marker)] + marker
}

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
