package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"oops/internal/llm/ai/protocol"
)

func TestBuildContextUsesCurrentLeafPath(t *testing.T) {
	s := New("s1")
	root := mustAppendMessage(t, s, "root")
	left := mustAppendMessage(t, s, "left")
	if err := s.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}
	right := mustAppendMessage(t, s, "right")

	ctx := s.BuildContext()
	assertTexts(t, ctx.Messages, []string{"root", "right"})

	children := s.Children(root.ID)
	if len(children) != 2 {
		t.Fatalf("children count = %d, want 2", len(children))
	}
	if left.ID == right.ID {
		t.Fatal("branch entries should be distinct")
	}
}

func TestBuildContextCompactionOrder(t *testing.T) {
	s := New("s1")
	if _, err := s.AppendModelChange("provider", "model-before-compaction"); err != nil {
		t.Fatal(err)
	}
	m1 := mustAppendMessage(t, s, "one")
	m2 := mustAppendMessage(t, s, "two")
	m3 := mustAppendMessage(t, s, "three")
	if _, err := s.AppendCompaction("older facts", m2.ID, 123); err != nil {
		t.Fatal(err)
	}
	m4 := mustAppendMessage(t, s, "four")

	ctx := s.BuildContext()
	assertTexts(t, ctx.Messages, []string{
		"Context summary:\n\nolder facts",
		"two",
		"three",
		"four",
	})
	if len(ctx.Messages) != 4 {
		t.Fatalf("context len = %d, want 4", len(ctx.Messages))
	}
	if ctx.Provider != "provider" || ctx.Model != "model-before-compaction" {
		t.Fatalf("state after compaction = provider:%q model:%q", ctx.Provider, ctx.Model)
	}
	for _, entry := range []Entry{m1, m2, m3, m4} {
		if _, ok := s.Entry(entry.ID); !ok {
			t.Fatalf("entry %s missing after compaction", entry.ID)
		}
	}
}

func TestBuildContextAppliesStateAndBranchSummary(t *testing.T) {
	s := New("s1")
	if _, err := s.AppendModelChange("provider", "model"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendThinkingLevelChange("medium"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendActiveToolsChange([]string{"read", "write"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendBranchSummary("previous branch chose read path"); err != nil {
		t.Fatal(err)
	}
	mustAppendMessage(t, s, "continue")

	ctx := s.BuildContext()
	if ctx.Provider != "provider" || ctx.Model != "model" || ctx.Reasoning != "medium" {
		t.Fatalf("state = provider:%q model:%q reasoning:%q", ctx.Provider, ctx.Model, ctx.Reasoning)
	}
	if got := ctx.ToolNames; len(got) != 2 || got[0] != "read" || got[1] != "write" {
		t.Fatalf("tool names = %#v", got)
	}
	assertTexts(t, ctx.Messages, []string{
		"Branch summary:\n\nprevious branch chose read path",
		"continue",
	})
}

func TestFileStorageLoadsOldJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	lines := []byte(
		`{"type":"session","timestamp":"` + now + `","cwd":"/tmp/project"}` + "\n" +
			`{"type":"message","id":"m1","timestamp":"` + now + `","message":{"role":"user","content":"hello"}}` + "\n" +
			`{"type":"message","id":"m2","parentId":"m1","timestamp":"` + now + `","message":{"role":"assistant","content":"world"}}` + "\n",
	)
	if err := os.WriteFile(path, lines, 0644); err != nil {
		t.Fatal(err)
	}

	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	s, err := repo.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := s.BuildContext()
	assertTexts(t, ctx.Messages, []string{"hello", "world"})
	if info := s.Info(); info.CWD != "/tmp/project" {
		t.Fatalf("cwd = %q", info.CWD)
	}
}

func mustAppendMessage(t *testing.T, s *Session, text string) Entry {
	t.Helper()
	entry, err := s.AppendMessage(protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent(text)},
		Timestamp: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func assertTexts(t *testing.T, messages protocol.MessageList, want []string) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("message count = %d, want %d", len(messages), len(want))
	}
	for i, message := range messages {
		got := messageText(t, message)
		if got != want[i] {
			t.Fatalf("message %d text = %q, want %q", i, got, want[i])
		}
	}
}

func messageText(t *testing.T, message protocol.AgentMessage) string {
	t.Helper()
	var content protocol.ContentList
	switch value := message.(type) {
	case protocol.UserMessage:
		content = value.Content
	case protocol.AssistantMessage:
		content = value.Content
	case protocol.ToolResultMessage:
		content = value.Content
	default:
		t.Fatalf("unexpected message type %T", message)
	}
	if len(content) == 0 {
		return ""
	}
	text, ok := content[0].(protocol.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", content[0])
	}
	return text.Text
}
