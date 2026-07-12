package loghub

import (
	"errors"
	"testing"
)

func TestHubSnapshotAndSubscribe(t *testing.T) {
	hub := NewWithOptions[string](Options{HistorySize: 2, MaxSubscribers: 1, SubscriberQueue: 2})
	t.Cleanup(hub.Close)
	hub.Push("one")
	hub.Push("two")
	hub.Push("three")

	snapshot := hub.Snapshot(0)
	if len(snapshot) != 2 {
		t.Fatalf("len(snapshot) = %d, want 2", len(snapshot))
	}
	if snapshot[0] != "two" || snapshot[1] != "three" {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	ch, cancel, err := hub.Subscribe(1)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	if got := <-ch; got != "three" {
		t.Fatalf("replayed entry = %q, want three", got)
	}

	hub.Push("four")
	if got := <-ch; got != "four" {
		t.Fatalf("live entry = %q, want four", got)
	}
}

func TestHubEvictsSlowSubscriberAndCloseIsIdempotent(t *testing.T) {
	hub := NewWithOptions[string](Options{HistorySize: 1, MaxSubscribers: 1, SubscriberQueue: 1})
	ch, cancel, err := hub.Subscribe(0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	hub.Push("one")
	hub.Push("two")

	if got := <-ch; got != "one" {
		t.Fatalf("queued entry = %q, want one", got)
	}
	if _, ok := <-ch; ok {
		t.Fatal("slow subscriber channel remains open")
	}
	cancel()
	hub.Close()
	hub.Close()
}

func TestHubSubscriptionLimitsAndClose(t *testing.T) {
	hub := NewWithOptions[string](Options{HistorySize: 1, MaxSubscribers: 1, SubscriberQueue: 1})
	_, cancel, err := hub.Subscribe(0)
	if err != nil {
		t.Fatalf("first subscribe: %v", err)
	}
	defer cancel()
	if _, _, err := hub.Subscribe(0); !errors.Is(err, ErrSubscriberLimit) {
		t.Fatalf("second subscribe error = %v, want ErrSubscriberLimit", err)
	}
	hub.Close()
	if _, _, err := hub.Subscribe(0); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscribe after close error = %v, want ErrClosed", err)
	}
}
