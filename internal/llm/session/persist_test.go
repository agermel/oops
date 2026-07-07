package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestOpenSessionStore_LoadsToolFields(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"message","id":"entry_1","parentId":"","timestamp":"2026-07-06T10:00:00Z","message":{"role":"assistant","content":"checking","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"list_nodelets","arguments":"{}"}}]}}
{"type":"message","id":"entry_2","parentId":"entry_1","timestamp":"2026-07-06T10:00:01Z","message":{"role":"tool","content":"[]","tool_call_id":"call_1","tool_name":"list_nodelets"}}
`
	if err := os.WriteFile(filepath.Join(dir, "tool_session.jsonl"), []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	store, err := OpenSessionStore(dir)
	if err != nil {
		t.Fatalf("OpenSessionStore: %v", err)
	}

	sess, ok := store.Get("tool_session")
	if !ok {
		t.Fatal("tool session was not loaded")
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(sess.Messages))
	}
	if len(sess.Messages[0].ToolCalls) != 1 {
		t.Fatalf("assistant tool calls = %d, want 1", len(sess.Messages[0].ToolCalls))
	}
	if sess.Messages[0].ToolCalls[0].ID != "call_1" || sess.Messages[0].ToolCalls[0].Function.Name != "list_nodelets" {
		t.Fatalf("assistant tool call = %+v", sess.Messages[0].ToolCalls[0])
	}
	if sess.Messages[1].Role != schema.Tool {
		t.Fatalf("role = %q, want tool", sess.Messages[1].Role)
	}
	if sess.Messages[1].ToolCallID != "call_1" || sess.Messages[1].ToolName != "list_nodelets" {
		t.Fatalf("tool fields = id %q name %q", sess.Messages[1].ToolCallID, sess.Messages[1].ToolName)
	}
}
