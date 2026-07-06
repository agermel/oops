package events

import (
	"path/filepath"
	"testing"
)

func TestEventStoreGetSessionMessagesSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	store, err := OpenEventStore(path)
	if err != nil {
		t.Fatalf("OpenEventStore: %v", err)
	}
	defer store.Close()

	if err := store.AppendEvent("run-1", "session-1", "project-1", 1, StepEvent{Type: "user", Content: "hello"}); err != nil {
		t.Fatalf("AppendEvent user: %v", err)
	}
	if err := store.AppendEvent("run-1", "session-1", "project-1", 2, StepEvent{Type: "assistant", Content: "world"}); err != nil {
		t.Fatalf("AppendEvent assistant: %v", err)
	}

	msgs, err := store.GetSessionMessages("session-1")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Fatalf("first msg = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "world" {
		t.Fatalf("second msg = %+v", msgs[1])
	}
}

func TestEventStoreMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	store, err := OpenEventStore(path)
	if err != nil {
		t.Fatalf("OpenEventStore first: %v", err)
	}
	if err := store.AppendEvent("run-1", "session-1", "project-1", 1, StepEvent{Type: "user", Content: "hello"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close first: %v", err)
	}

	store, err = OpenEventStore(path)
	if err != nil {
		t.Fatalf("OpenEventStore second: %v", err)
	}
	defer store.Close()

	sessions, err := store.ListSessions("project-1")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if sessions[0].ID != "session-1" || sessions[0].MessageCount != 1 {
		t.Fatalf("session summary = %+v", sessions[0])
	}
}
