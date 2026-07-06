package mcp

import "testing"

func TestConnectionLogHubSnapshotAndSubscribe(t *testing.T) {
	hub := NewConnectionLogHub("conn-1")
	hub.Append("system", "info", "started")
	hub.Append("stderr", "", "boom")

	snapshot := hub.Snapshot(1)
	if len(snapshot) != 1 {
		t.Fatalf("len(snapshot) = %d, want 1", len(snapshot))
	}
	if snapshot[0].Message != "boom" || snapshot[0].Level != "info" {
		t.Fatalf("snapshot[0] = %+v", snapshot[0])
	}

	ch, cancel := hub.Subscribe(2)
	defer cancel()

	first := <-ch
	second := <-ch
	if first.Message != "started" || second.Message != "boom" {
		t.Fatalf("replay = %q, %q", first.Message, second.Message)
	}

	hub.Append("system", "warn", "retry")
	live := <-ch
	if live.Message != "retry" || live.Level != "warn" {
		t.Fatalf("live = %+v", live)
	}
}
