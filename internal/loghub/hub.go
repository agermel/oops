// Package loghub provides bounded replay and live fan-out for in-process logs.
package loghub

import (
	"errors"
	"sync"
)

var (
	// ErrSubscriberLimit reports that the hub has reached its bounded fan-out.
	ErrSubscriberLimit = errors.New("log subscriber limit reached")
	// ErrClosed reports that the hub has stopped accepting subscriptions.
	ErrClosed = errors.New("log hub closed")
)

// Options bounds retained history and live fan-out for one hub.
type Options struct {
	HistorySize     int
	MaxSubscribers  int
	SubscriberQueue int
}

type subscription[T any] struct {
	ch chan T
}

// Hub stores recent entries and fans out live entries to subscribers.
// It owns channels only; callers own the goroutines that consume them.
type Hub[T any] struct {
	mu              sync.Mutex
	subscribers     map[*subscription[T]]struct{}
	history         []T
	pos             int
	count           int
	maxSubscribers  int
	subscriberQueue int
	closed          bool
}

// NewWithOptions creates a hub with explicit resource bounds.
func NewWithOptions[T any](options Options) *Hub[T] {
	if options.HistorySize < 0 {
		options.HistorySize = 0
	}
	if options.MaxSubscribers <= 0 {
		options.MaxSubscribers = 1
	}
	if options.SubscriberQueue <= 0 {
		options.SubscriberQueue = 1
	}
	return &Hub[T]{
		subscribers:     make(map[*subscription[T]]struct{}),
		history:         make([]T, options.HistorySize),
		maxSubscribers:  options.MaxSubscribers,
		subscriberQueue: options.SubscriberQueue,
	}
}

// Push retains one entry and sends it to ready subscribers. A subscriber whose
// queue is full is evicted so producers retain bounded work.
func (h *Hub[T]) Push(entry T) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if len(h.history) > 0 {
		h.history[h.pos%len(h.history)] = entry
		h.pos++
		if h.count < len(h.history) {
			h.count++
		}
	}
	for sub := range h.subscribers {
		select {
		case sub.ch <- entry:
		default:
			delete(h.subscribers, sub)
			close(sub.ch)
		}
	}
}

// Snapshot returns at most tail entries in chronological order.
func (h *Hub[T]) Snapshot(tail int) []T {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshotLocked(tail)
}

func (h *Hub[T]) snapshotLocked(tail int) []T {
	if h.count == 0 {
		return nil
	}
	if tail <= 0 || tail > h.count {
		tail = h.count
	}
	out := make([]T, 0, tail)
	start := h.pos - tail
	for i := start; i < h.pos; i++ {
		out = append(out, h.history[i%len(h.history)])
	}
	return out
}

// Subscribe atomically captures replay history and registers the live queue.
// The returned channel closes after cancellation, slow-consumer eviction, or
// hub shutdown.
func (h *Hub[T]) Subscribe(tail int) (<-chan T, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, ErrClosed
	}
	if len(h.subscribers) >= h.maxSubscribers {
		return nil, nil, ErrSubscriberLimit
	}

	replay := h.snapshotLocked(tail)
	capacity := len(replay) + h.subscriberQueue
	sub := &subscription[T]{ch: make(chan T, capacity)}
	for _, entry := range replay {
		sub.ch <- entry
	}
	h.subscribers[sub] = struct{}{}

	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subscribers[sub]; ok {
			delete(h.subscribers, sub)
			close(sub.ch)
		}
		h.mu.Unlock()
	}
	return sub.ch, cancel, nil
}

// Close releases all subscriptions. It is safe to call repeatedly.
func (h *Hub[T]) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for sub := range h.subscribers {
		delete(h.subscribers, sub)
		close(sub.ch)
	}
	h.history = nil
	h.count = 0
}
