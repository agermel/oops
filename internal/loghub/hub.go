package loghub

import "sync"

// Hub stores recent entries and fans out live entries to subscribers.
type Hub[T any] struct {
	mu          sync.RWMutex
	subscribers map[chan T]struct{}
	history     []T
	pos         int
}

func New[T any](historySize int) *Hub[T] {
	return &Hub[T]{
		subscribers: make(map[chan T]struct{}),
		history:     make([]T, historySize),
	}
}

func (h *Hub[T]) Push(entry T) {
	h.mu.Lock()
	if len(h.history) > 0 {
		h.history[h.pos%len(h.history)] = entry
		h.pos++
	}
	for ch := range h.subscribers {
		select {
		case ch <- entry:
		default:
		}
	}
	h.mu.Unlock()
}

func (h *Hub[T]) Snapshot(tail int) []T {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.history) == 0 {
		return nil
	}

	total := h.pos
	if total > len(h.history) {
		total = len(h.history)
	}
	if tail <= 0 || tail > total {
		tail = total
	}

	out := make([]T, 0, tail)
	start := h.pos - tail
	for i := start; i < h.pos; i++ {
		out = append(out, h.history[i%len(h.history)])
	}
	return out
}

func (h *Hub[T]) Subscribe(tail int) (<-chan T, func()) {
	ch := make(chan T, 256)

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
