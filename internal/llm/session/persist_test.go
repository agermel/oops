package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSessionStore_LoadsLegacyMessageFirstJSONL(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("testdata", "legacy_session.jsonl"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "legacy_session.jsonl"), data, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	store, err := OpenSessionStore(dir)
	if err != nil {
		t.Fatalf("OpenSessionStore: %v", err)
	}

	sess, ok := store.Get("legacy_session")
	if !ok {
		t.Fatal("legacy session was not loaded")
	}
	if sess.ProjectID != "" {
		t.Fatalf("ProjectID = %q, want empty", sess.ProjectID)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(sess.Messages))
	}
	if sess.Messages[0].Content != "legacy hello" {
		t.Fatalf("first content = %q, want legacy hello", sess.Messages[0].Content)
	}
	if sess.Messages[1].Content != "legacy world" {
		t.Fatalf("second content = %q, want legacy world", sess.Messages[1].Content)
	}

	info := store.List("*")
	if len(info) != 1 {
		t.Fatalf("len(List) = %d, want 1", len(info))
	}
	if info[0].ID != "legacy_session" || info[0].MessageCount != 2 {
		t.Fatalf("SessionInfo = %+v", info[0])
	}
	if info[0].CreatedAt == 0 || info[0].UpdatedAt == 0 {
		t.Fatalf("timestamps were not restored: %+v", info[0])
	}
}
