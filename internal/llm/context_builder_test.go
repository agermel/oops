package llm

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

	// 检查 entries 数量。
	path := store.pathToLeaf(sess.ID)
	if len(path) != 2 {
		t.Fatalf("pathToLeaf len = %d, want 2", len(path))
	}

	// 检查 entry 内容。
	if path[0].Message.Content != "hello" {
		t.Errorf("first entry content = %q, want hello", path[0].Message.Content)
	}
	if path[1].Message.Content != "hi there" {
		t.Errorf("second entry content = %q, want hi there", path[1].Message.Content)
	}

	// 检查 ParentID 链。
	if path[1].ParentID != path[0].ID {
		t.Errorf("ParentID chain broken: %q → %q", path[1].ParentID, path[0].ID)
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

func TestSessionStore_CompactIfNeeded(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	// 添加大量消息以超出小预算。
	longMsg := strings.Repeat("x", 200) // ~80 tokens per message
	for i := 0; i < 20; i++ {
		store.AppendMessage(sess.ID, schema.UserMessage(longMsg))
		store.AppendMessage(sess.ID, schema.AssistantMessage(longMsg, nil))
	}

	// 用小预算触发压缩。
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

	// 持久化 compaction。
	store.AppendCompaction(sess.ID, entry)

	// BuildContext 现在应包含摘要注入 + 保留的消息。
	msgs := store.BuildContext(sess.ID)
	if len(msgs) == 0 {
		t.Fatal("BuildContext returned empty after compaction")
	}

	// 第一条消息应该是摘要。
	if !strings.Contains(msgs[0].Content, "旧上下文摘要") {
		t.Errorf("first message should be summary, got: %s", msgs[0].Content[:100])
	}

	// 后续消息应为保留的最近消息。
	// keepRecent=4，但有 user+assistant 成对，所以保留 4 条 entry → 4 条消息。
	if len(msgs) < 3 {
		t.Errorf("expected at least 3 messages (summary + 2 kept), got %d", len(msgs))
	}
}

func TestSessionStore_CompactIfNeeded_NoTrigger(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	store.AppendMessage(sess.ID, schema.UserMessage("hi"))
	store.AppendMessage(sess.ID, schema.AssistantMessage("hello", nil))

	// 消息数远小于阈值。
	entry := store.CompactIfNeeded(sess.ID, 64000, 8)
	if entry != nil {
		t.Error("CompactIfNeeded should return nil when under budget")
	}
}

func TestSessionStore_BuildContext_MultipleCompactions(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	// 添加消息并触发第一次压缩。
	longMsg := strings.Repeat("y", 200)
	for i := 0; i < 20; i++ {
		store.AppendMessage(sess.ID, schema.UserMessage(longMsg))
		store.AppendMessage(sess.ID, schema.AssistantMessage(longMsg, nil))
	}

	entry1 := store.CompactIfNeeded(sess.ID, 500, 4)
	if entry1 == nil {
		t.Fatal("first compaction returned nil")
	}
	store.AppendCompaction(sess.ID, entry1)

	// 继续添加更多消息。
	for i := 0; i < 20; i++ {
		store.AppendMessage(sess.ID, schema.UserMessage(longMsg))
		store.AppendMessage(sess.ID, schema.AssistantMessage(longMsg, nil))
	}

	// 第二次压缩。
	entry2 := store.CompactIfNeeded(sess.ID, 500, 4)
	if entry2 == nil {
		t.Fatal("second compaction returned nil")
	}
	store.AppendCompaction(sess.ID, entry2)

	// BuildContext 应该处理两次压缩：只使用最新的。
	msgs := store.BuildContext(sess.ID)
	if len(msgs) == 0 {
		t.Fatal("BuildContext returned empty")
	}
	if !strings.Contains(msgs[0].Content, "旧上下文摘要") {
		t.Errorf("first message should be summary, got: %s", msgs[0].Content[:100])
	}
}

func TestSessionStore_Delete_CleansUpEntries(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	store.AppendMessage(sess.ID, schema.UserMessage("test"))
	if store.SessionCount() != 1 {
		t.Fatalf("SessionCount = %d, want 1", store.SessionCount())
	}

	store.Delete(sess.ID)

	if store.SessionCount() != 0 {
		t.Fatalf("SessionCount = %d, want 0 after delete", store.SessionCount())
	}

	// 确认 entries 也被清理。
	path := store.pathToLeaf(sess.ID)
	if len(path) != 0 {
		t.Errorf("pathToLeaf after delete = %d entries, want 0", len(path))
	}
}

func TestContextBuilder_Build_NoCompactionNeeded(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	// Add some messages.
	store.AppendMessage(sess.ID, schema.UserMessage("hello"))
	store.AppendMessage(sess.ID, schema.AssistantMessage("hi", nil))

	// Create context builder without LLM client (deterministic summary only).
	cb := NewContextBuilder(store, nil, nil)
	cb.MaxTokens = 64000 // High threshold — no compaction needed.

	result := cb.Build(t.Context(), BuildOptions{
		Session:  sess,
		Question: "how are you?",
	})

	if result.Compaction != nil {
		t.Error("Build should not trigger compaction when under budget")
	}
	if len(result.Messages) < 3 {
		t.Fatalf("Messages len = %d, want at least 3 (system + history + question)", len(result.Messages))
	}

	// First message should be system prompt.
	if result.Messages[0].Role != schema.System {
		t.Errorf("first message role = %s, want system", result.Messages[0].Role)
	}

	// Last message should be the question.
	last := result.Messages[len(result.Messages)-1]
	if last.Role != schema.User || last.Content != "how are you?" {
		t.Errorf("last message = %s: %s, want user: how are you?", last.Role, last.Content)
	}
}

func TestContextBuilder_Build_WithCompaction(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	// Add many messages to trigger compaction.
	longMsg := strings.Repeat("z", 200)
	for i := 0; i < 30; i++ {
		store.AppendMessage(sess.ID, schema.UserMessage(longMsg))
		store.AppendMessage(sess.ID, schema.AssistantMessage(longMsg, nil))
	}

	// Use very low token threshold to force compaction.
	cb := NewContextBuilder(store, nil, nil)
	cb.MaxTokens = 500
	cb.KeepRecent = 4

	result := cb.Build(t.Context(), BuildOptions{
		Session:  sess,
		Question: "summarize please",
	})

	if result.Compaction == nil {
		t.Error("Build should trigger compaction when over budget")
	}
	if result.Compaction.Summary == "" {
		t.Error("compaction summary is empty")
	}

	// Messages should include system prompt + summary + kept + question.
	if len(result.Messages) < 3 {
		t.Fatalf("Messages len = %d, want at least 3", len(result.Messages))
	}

	// First should be system.
	if result.Messages[0].Role != schema.System {
		t.Errorf("first message role = %s, want system", result.Messages[0].Role)
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

func TestContextBuilder_prependSystemPrompt(t *testing.T) {
	cb := NewContextBuilder(NewSessionStore(), nil, nil)
	msgs := []*schema.Message{
		schema.UserMessage("hello"),
	}

	result := cb.prependSystemPrompt(msgs, nil)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].Role != schema.System {
		t.Errorf("first message role = %s, want system", result[0].Role)
	}
	if !strings.Contains(result[0].Content, "基础设施运维助手") {
		t.Error("system prompt should contain BasePrompt")
	}
}

func TestContextBuilder_prependSystemPrompt_WithProject(t *testing.T) {
	cb := NewContextBuilder(NewSessionStore(), nil, nil)
	msgs := []*schema.Message{
		schema.UserMessage("hello"),
	}

	project := &ProjectContext{
		ID:          "proj-1",
		Name:        "TestProject",
		Description: "A test project",
		GitHubRepo:  "https://github.com/user/repo",
		NodeletIDs:  []string{"node1", "node2"},
	}

	result := cb.prependSystemPrompt(msgs, project)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if !strings.Contains(result[0].Content, "TestProject") {
		t.Error("system prompt should contain project name")
	}
	if !strings.Contains(result[0].Content, "https://github.com/user/repo") {
		t.Error("system prompt should contain GitHub repo URL")
	}
	if !strings.Contains(result[0].Content, "node1") {
		t.Error("system prompt should contain nodelet IDs")
	}
	if !strings.Contains(result[0].Content, "repo_sync") {
		t.Error("system prompt should mention repo tools when repo is set")
	}
}
