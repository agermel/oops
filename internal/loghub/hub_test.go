package loghub

import "testing"

func TestHubSnapshotAndSubscribe(t *testing.T) {
	hub := New[string](2)
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

	ch, cancel := hub.Subscribe(1)
	defer cancel()

	if got := <-ch; got != "three" {
		t.Fatalf("replayed entry = %q, want three", got)
	}

	hub.Push("four")
	if got := <-ch; got != "four" {
		t.Fatalf("live entry = %q, want four", got)
	}
}
