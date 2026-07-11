package mcp

import (
	"errors"
	"io"
	"strings"
	"testing"

	"oops/internal/loghub"
)

func TestConnectionLogHubSnapshotAndSubscribe(t *testing.T) {
	hub := NewConnectionLogHub("conn-1")
	t.Cleanup(hub.Close)
	hub.Append("system", "info", "started")
	hub.Append("stderr", "", "boom")

	snapshot := hub.Snapshot(1)
	if len(snapshot) != 1 {
		t.Fatalf("len(snapshot) = %d, want 1", len(snapshot))
	}
	if snapshot[0].Message != "boom" || snapshot[0].Level != "info" {
		t.Fatalf("snapshot[0] = %+v", snapshot[0])
	}

	ch, cancel, err := hub.Subscribe(2)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
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

func TestConnectionLogLineWriterBoundsUnterminatedOutputAndCloses(t *testing.T) {
	hub := NewConnectionLogHub("conn-1")
	t.Cleanup(hub.Close)
	writer := hub.LineWriter("stderr")
	if _, err := writer.Write([]byte(strings.Repeat("x", connectionLogMaxMessageBytes*2))); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := writer.Write([]byte("after close")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after close = %v, want io.ErrClosedPipe", err)
	}

	logs := hub.Snapshot(1)
	if len(logs) != 1 {
		t.Fatalf("len(logs) = %d, want 1", len(logs))
	}
	if len(logs[0].Message) > connectionLogMaxMessageBytes {
		t.Fatalf("message length = %d, want <= %d", len(logs[0].Message), connectionLogMaxMessageBytes)
	}
	if !strings.HasSuffix(logs[0].Message, connectionLogTruncatedMarker) {
		t.Fatalf("message = %q, want truncation marker", logs[0].Message)
	}
}

func TestConnectionLogHubBoundsSubscriptionsAndClosesThem(t *testing.T) {
	hub := newConnectionLogHubWithOptions("conn-1", loghub.Options{
		HistorySize:     1,
		MaxSubscribers:  1,
		SubscriberQueue: 1,
	})
	ch, cancel, err := hub.Subscribe(0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()
	if _, _, err := hub.Subscribe(0); !errors.Is(err, ErrConnectionLogSubscriberLimit) {
		t.Fatalf("second subscribe error = %v, want ErrConnectionLogSubscriberLimit", err)
	}
	hub.Close()
	if _, ok := <-ch; ok {
		t.Fatal("subscriber stays open after hub close")
	}
	if _, _, err := hub.Subscribe(0); !errors.Is(err, ErrConnectionLogClosed) {
		t.Fatalf("subscribe after close = %v, want ErrConnectionLogClosed", err)
	}
}
