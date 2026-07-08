package session

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestSessionStore_DAG_AppendMessage_CreatesEntry(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	store.AppendMessage(sess.ID, schema.UserMessage("hello"))
	store.AppendMessage(sess.ID, schema.AssistantMessage("hi there", nil))

	path := store.pathToLeaf(sess.ID)
	if len(path) != 2 {
		t.Fatalf("pathToLeaf len = %d, want 2", len(path))
	}
	if path[0].Message.Content != "hello" {
		t.Errorf("first entry content = %q, want hello", path[0].Message.Content)
	}
	if path[1].Message.Content != "hi there" {
		t.Errorf("second entry content = %q, want hi there", path[1].Message.Content)
	}
	if path[1].ParentID != path[0].ID {
		t.Errorf("ParentID chain broken: %q -> %q", path[1].ParentID, path[0].ID)
	}
}

func TestSessionStore_BuildContext_NoCompaction(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	store.AppendMessage(sess.ID, schema.UserMessage("hello"))
	store.AppendMessage(sess.ID, schema.AssistantMessage("world", nil))

	msgs := store.BuildContext(sess.ID)
	if len(msgs) != 2 {
		t.Fatalf("BuildContext len = %d, want 2", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Errorf("first message = %q, want hello", msgs[0].Content)
	}
	if msgs[1].Content != "world" {
		t.Errorf("second message = %q, want world", msgs[1].Content)
	}
}

func TestSessionStore_BuildContext_UsesCurrentLeafPathOnly(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	store.AppendMessage(sess.ID, schema.UserMessage("root"))
	rootID := lastEntryID(t, store, sess.ID)

	store.AppendMessage(sess.ID, schema.AssistantMessage("branch-a", nil))
	store.leafIDs[sess.ID] = rootID
	store.AppendMessage(sess.ID, schema.AssistantMessage("branch-b", nil))

	assertMessageContents(t, store.BuildContext(sess.ID), []string{
		"root",
		"branch-b",
	})
}

func TestSessionStore_CompactIfNeeded(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	longMsg := strings.Repeat("x", 200)
	for range 20 {
		store.AppendMessage(sess.ID, schema.UserMessage(longMsg))
		store.AppendMessage(sess.ID, schema.AssistantMessage(longMsg, nil))
	}

	entry := store.CompactIfNeeded(sess.ID, 500, 4)
	if entry == nil {
		t.Fatal("CompactIfNeeded returned nil, want compaction entry")
	}
	if entry.Summary == "" {
		t.Error("compaction summary is empty")
	}
	if entry.FirstKeptEntryID == "" {
		t.Error("compaction FirstKeptEntryID is empty")
	}
	if entry.TokensBefore <= 0 {
		t.Errorf("TokensBefore = %d, want > 0", entry.TokensBefore)
	}

	store.AppendCompaction(sess.ID, entry)

	msgs := store.BuildContext(sess.ID)
	if len(msgs) == 0 {
		t.Fatal("BuildContext returned empty after compaction")
	}
	if !strings.Contains(msgs[0].Content, "旧上下文摘要") {
		t.Errorf("first message should be summary, got: %s", msgs[0].Content[:100])
	}
	if len(msgs) < 3 {
		t.Errorf("expected at least 3 messages, got %d", len(msgs))
	}
}

func TestSessionStore_BuildContext_CompactionPreservesSummaryKeptAndNewMessageOrder(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	store.AppendMessage(sess.ID, schema.UserMessage("old message"))
	store.AppendMessage(sess.ID, schema.AssistantMessage("first kept", nil))
	firstKeptID := lastEntryID(t, store, sess.ID)
	store.AppendMessage(sess.ID, schema.UserMessage("second kept"))
	store.AppendCompaction(sess.ID, &SessionEntry{
		Summary:          "summary-one",
		FirstKeptEntryID: firstKeptID,
	})
	store.AppendMessage(sess.ID, schema.UserMessage("new message"))

	assertMessageContents(t, store.BuildContext(sess.ID), []string{
		summaryContentForTest("summary-one"),
		"first kept",
		"second kept",
		"new message",
	})
}

func TestSessionStore_BuildContext_MultipleCompactions(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	store.AppendMessage(sess.ID, schema.UserMessage("old before first"))
	store.AppendMessage(sess.ID, schema.AssistantMessage("first compaction kept", nil))
	firstCompactionKeptID := lastEntryID(t, store, sess.ID)
	store.AppendMessage(sess.ID, schema.UserMessage("before first compaction"))
	store.AppendCompaction(sess.ID, &SessionEntry{
		Summary:          "summary-one",
		FirstKeptEntryID: firstCompactionKeptID,
	})

	store.AppendMessage(sess.ID, schema.UserMessage("after first compaction old"))
	store.AppendMessage(sess.ID, schema.AssistantMessage("last compaction kept", nil))
	lastCompactionKeptID := lastEntryID(t, store, sess.ID)
	store.AppendMessage(sess.ID, schema.UserMessage("before second compaction"))
	store.AppendCompaction(sess.ID, &SessionEntry{
		Summary:          "summary-two",
		FirstKeptEntryID: lastCompactionKeptID,
	})
	store.AppendMessage(sess.ID, schema.UserMessage("after second compaction"))

	assertMessageContents(t, store.BuildContext(sess.ID), []string{
		summaryContentForTest("summary-two"),
		"last compaction kept",
		"before second compaction",
		"after second compaction",
	})
}

func TestSessionStore_MessagesBefore(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	store.AppendMessage(sess.ID, schema.UserMessage("older message"))
	store.AppendMessage(sess.ID, schema.AssistantMessage("recent message", nil))
	path := store.pathToLeaf(sess.ID)

	lines := store.MessagesBefore(sess.ID, path[1].ID, 100)
	if len(lines) != 1 {
		t.Fatalf("MessagesBefore len = %d, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "older message") {
		t.Fatalf("MessagesBefore line = %q, want older message", lines[0])
	}
}

func TestSummarizeEntries(t *testing.T) {
	entries := []*SessionEntry{
		{
			Type: EntryMessage,
			Message: &schema.Message{
				Role:    schema.User,
				Content: "Redis 是否正常？",
			},
		},
		{
			Type: EntryMessage,
			Message: &schema.Message{
				Role:    schema.Assistant,
				Content: "正在检查 Redis 连接状态...检查完成，Redis 正常运行。",
			},
		},
	}

	summary := summarizeEntries(entries)
	if !strings.Contains(summary, "user:") {
		t.Error("summary should contain user role")
	}
	if !strings.Contains(summary, "assistant:") {
		t.Error("summary should contain assistant role")
	}
	if !strings.Contains(summary, "Redis") {
		t.Error("summary should contain the message content")
	}
}

func lastEntryID(t *testing.T, store *SessionStore, sessionID string) string {
	t.Helper()

	path := store.pathToLeaf(sessionID)
	if len(path) == 0 {
		t.Fatal("pathToLeaf returned empty")
	}
	return path[len(path)-1].ID
}

func assertMessageContents(t *testing.T, messages []*schema.Message, want []string) {
	t.Helper()

	if len(messages) != len(want) {
		t.Fatalf("len(messages) = %d, want %d: %#v", len(messages), len(want), messageContents(messages))
	}
	for i, msg := range messages {
		if msg.Content != want[i] {
			t.Fatalf("message[%d] = %q, want %q; all messages: %#v", i, msg.Content, want[i], messageContents(messages))
		}
	}
}

func messageContents(messages []*schema.Message) []string {
	contents := make([]string, 0, len(messages))
	for _, msg := range messages {
		contents = append(contents, msg.Content)
	}
	return contents
}

func summaryContentForTest(summary string) string {
	return "以下是旧上下文摘要。后续回答必须参考它，但最近消息优先级更高。\n\n" + summary
}
