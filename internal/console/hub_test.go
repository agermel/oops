package console

import (
	"errors"
	"strings"
	"testing"
	"time"

	"oops/internal/loghub"
)

func TestHubBoundsEntriesSubscriptionsAndClose(t *testing.T) {
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	hub := NewHubWithOptions(Options{
		HistorySize:     1,
		MaxSubscribers:  1,
		SubscriberQueue: 1,
		MaxEntryBytes:   24,
		Now:             func() time.Time { return now },
	})
	ch, cancel, err := hub.Subscribe()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()
	if _, _, err := hub.Subscribe(); !errors.Is(err, loghub.ErrSubscriberLimit) {
		t.Fatalf("second subscribe error = %v, want loghub.ErrSubscriberLimit", err)
	}

	_, _ = hub.Write([]byte(strings.Repeat("x", 80)))
	entry := <-ch
	if len(entry.Message) > 24 {
		t.Fatalf("message length = %d, want <= 24", len(entry.Message))
	}
	if !strings.HasSuffix(entry.Message, "…[truncated]") {
		t.Fatalf("message = %q, want truncation marker", entry.Message)
	}
	hub.Close()
	if _, ok := <-ch; ok {
		t.Fatal("subscriber stays open after hub close")
	}
	if _, _, err := hub.Subscribe(); !errors.Is(err, loghub.ErrClosed) {
		t.Fatalf("subscribe after close = %v, want loghub.ErrClosed", err)
	}
}
