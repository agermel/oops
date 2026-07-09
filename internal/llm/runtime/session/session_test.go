package session

import (
	"encoding/json"
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

func TestSummaryDetailsAndProtectedFirstKept(t *testing.T) {
	s := New("s1")
	mustAppendMessage(t, s, "start")
	assistant := mustAppendAssistantToolCall(t, s, "call_read", "read", `{"path":"README.md"}`)
	result := mustAppendToolResult(t, s, "call_read", "read", "ok", map[string]any{"path": "README.md"})
	after := mustAppendMessage(t, s, "after")

	protected, err := s.ProtectedFirstKeptEntryID(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if protected != assistant.ID {
		t.Fatalf("protected first kept = %q, want assistant %q", protected, assistant.ID)
	}

	details, err := s.SummaryDetailsBefore(after.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(details.ReadFiles) != 1 || details.ReadFiles[0] != "README.md" {
		t.Fatalf("read files = %#v", details.ReadFiles)
	}
	if len(details.ModifiedFiles) != 0 {
		t.Fatalf("modified files = %#v", details.ModifiedFiles)
	}
}

func TestSummaryDetailsMergePreviousDetails(t *testing.T) {
	s := New("s1")
	if _, err := s.AppendCompactionWithDetails("old summary", mustAppendMessage(t, s, "kept").ID, 10, SummaryDetails{
		ReadFiles: []string{"old.md"},
	}); err != nil {
		t.Fatal(err)
	}
	mustAppendAssistantToolCall(t, s, "call_write", "write", `{"path":"new.md"}`)
	after := mustAppendMessage(t, s, "after")

	details, err := s.SummaryDetailsBefore(after.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(details.ReadFiles) != 1 || details.ReadFiles[0] != "old.md" {
		t.Fatalf("read files = %#v", details.ReadFiles)
	}
	if len(details.ModifiedFiles) != 1 || details.ModifiedFiles[0] != "new.md" {
		t.Fatalf("modified files = %#v", details.ModifiedFiles)
	}
}

func TestBranchSummaryDetailsCollectAbandonedBranch(t *testing.T) {
	s := New("s1")
	root := mustAppendMessage(t, s, "root")
	left := mustAppendAssistantToolCall(t, s, "call_read", "read", `{"path":"left.md"}`)
	if err := s.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}
	right := mustAppendMessage(t, s, "right")
	if err := s.MoveTo(left.ID); err != nil {
		t.Fatal(err)
	}
	entries, common, err := s.EntriesForBranchSummary(right.ID)
	if err != nil {
		t.Fatal(err)
	}
	if common != root.ID || len(entries) != 1 || entries[0].ID != left.ID {
		t.Fatalf("entries=%#v common=%q", entries, common)
	}
	details, err := s.BranchSummaryDetails(right.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(details.ReadFiles) != 1 || details.ReadFiles[0] != "left.md" {
		t.Fatalf("read files = %#v", details.ReadFiles)
	}
}

func TestResolveNavigationTargetForUserMessageReturnsParentAndEditorText(t *testing.T) {
	s := New("s1")
	root := mustAppendMessage(t, s, "root")
	user := mustAppendMessage(t, s, "edit me")

	target, err := s.ResolveNavigationTarget(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID != root.ID || target.EditorText != "edit me" {
		t.Fatalf("target = %+v, want leaf %q editor text", target, root.ID)
	}
}

func TestResolveNavigationTargetForRootUserAllowsEmptyLeaf(t *testing.T) {
	s := New("s1")
	root := mustAppendMessage(t, s, "root")

	target, err := s.ResolveNavigationTarget(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID != "" || target.EditorText != "root" {
		t.Fatalf("target = %+v, want empty leaf and root text", target)
	}
	if _, err := s.AppendLeaf(target.LeafID); err != nil {
		t.Fatal(err)
	}
	if ctx := s.BuildContext(); len(ctx.Messages) != 0 || ctx.LeafID != "" {
		t.Fatalf("context = %+v, want empty root context", ctx)
	}
}

func TestResolveNavigationTargetForAssistantToolCallUsesCompleteResultBoundary(t *testing.T) {
	s := New("s1")
	mustAppendMessage(t, s, "start")
	assistant := mustAppendAssistantToolCall(t, s, "call_read", "read", `{"path":"README.md"}`)
	result := mustAppendToolResult(t, s, "call_read", "read", "ok", nil)
	mustAppendMessage(t, s, "after")

	target, err := s.ResolveNavigationTarget(assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID != result.ID {
		t.Fatalf("leaf = %q, want result %q", target.LeafID, result.ID)
	}
}

func TestResolveNavigationTargetForDanglingAssistantToolCallReturnsParent(t *testing.T) {
	s := New("s1")
	root := mustAppendMessage(t, s, "start")
	assistant := mustAppendAssistantToolCall(t, s, "call_read", "read", `{"path":"README.md"}`)

	target, err := s.ResolveNavigationTarget(assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID != root.ID {
		t.Fatalf("leaf = %q, want parent %q", target.LeafID, root.ID)
	}
}

func TestResolveNavigationTargetForMultiToolMiddleResultUsesLastResult(t *testing.T) {
	s := New("s1")
	mustAppendMessage(t, s, "start")
	assistant := mustAppendAssistantToolCalls(t, s,
		protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`)),
		protocol.NewToolCallContent("call_2", "read", json.RawMessage(`{"path":"b.md"}`)),
	)
	first := mustAppendToolResult(t, s, "call_1", "read", "a", nil)
	second := mustAppendToolResult(t, s, "call_2", "read", "b", nil)

	target, err := s.ResolveNavigationTarget(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID != second.ID {
		t.Fatalf("leaf = %q, want final result %q", target.LeafID, second.ID)
	}
	target, err = s.ResolveNavigationTarget(assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID != second.ID {
		t.Fatalf("assistant leaf = %q, want final result %q", target.LeafID, second.ID)
	}
}

func TestValidateContextRejectsProviderOrphanToolResult(t *testing.T) {
	s := New("s1")
	mustAppendMessage(t, s, "start")
	mustAppendToolResult(t, s, "call_missing", "read", "orphan", nil)

	if err := s.ValidateContext(); err == nil {
		t.Fatal("ValidateContext() nil, want orphan tool result error")
	}
}

func TestValidateContextFiltersAbortedAssistantBeforeCheckingToolResults(t *testing.T) {
	s := New("s1")
	mustAppendMessage(t, s, "start")
	if _, err := s.AppendMessage(protocol.AssistantMessage{
		Content: protocol.ContentList{
			protocol.NewToolCallContent("call_read", "read", json.RawMessage(`{"path":"README.md"}`)),
		},
		StopReason: protocol.StopReasonAborted,
		Timestamp:  time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	mustAppendToolResult(t, s, "call_read", "read", "late", nil)

	if err := s.ValidateContext(); err == nil {
		t.Fatal("ValidateContext() nil, want orphan after aborted assistant is filtered")
	}
}

func TestResolveNavigationTargetAmbiguousToolCallBranchesReturnsParent(t *testing.T) {
	s := New("s1")
	root := mustAppendMessage(t, s, "start")
	assistant := mustAppendAssistantToolCall(t, s, "call_read", "read", `{"path":"README.md"}`)
	first := mustAppendToolResult(t, s, "call_read", "read", "first", nil)
	if err := s.MoveTo(assistant.ID); err != nil {
		t.Fatal(err)
	}
	second := mustAppendToolResult(t, s, "call_read", "read", "second", nil)
	if err := s.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}

	target, err := s.ResolveNavigationTarget(assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID != root.ID {
		t.Fatalf("leaf = %q, want parent %q for ambiguous results %q/%q", target.LeafID, root.ID, first.ID, second.ID)
	}
}

func TestEmptyLeafRoundTrip(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	s := repo.Create("s1")
	root := mustAppendMessage(t, s, "root")
	if err := repo.SaveEntry(s.ID(), root); err != nil {
		t.Fatal(err)
	}
	leaf, err := s.AppendLeaf("")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEntry(s.ID(), leaf); err != nil {
		t.Fatal(err)
	}

	reloaded, err := repo.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := reloaded.BuildContext()
	if ctx.LeafID != "" || len(ctx.Messages) != 0 {
		t.Fatalf("context = %+v, want empty leaf context", ctx)
	}
}

func TestEntryDetailsRoundTrip(t *testing.T) {
	s := New("s1")
	entry, err := s.AppendBranchSummaryWithDetails("branch facts", SummaryDetails{ModifiedFiles: []string{"app.go"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Entry
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var details SummaryDetails
	if err := json.Unmarshal(decoded.Details, &details); err != nil {
		t.Fatal(err)
	}
	if len(details.ModifiedFiles) != 1 || details.ModifiedFiles[0] != "app.go" {
		t.Fatalf("details = %#v", details)
	}
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
	if info := s.Info(); info.CWD != "/tmp/project" || info.Summary != "hello" {
		t.Fatalf("cwd = %q", info.CWD)
	}
}

func TestFileStorageLoadsProjectID(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	s := repo.Create("s1")
	entry, err := s.AppendSessionInfoWithProject("/tmp/project", "work", "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEntry(s.ID(), entry); err != nil {
		t.Fatal(err)
	}

	reloadedRepo := NewRepository(storage)
	reloaded, err := reloadedRepo.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	info := reloaded.Info()
	if info.ProjectID != "proj-1" || info.CWD != "/tmp/project" || info.Name != "work" {
		t.Fatalf("info = %+v", info)
	}
	if info.Messages != 0 || info.Entries != 1 {
		t.Fatalf("counts = messages:%d entries:%d", info.Messages, info.Entries)
	}
}

func TestRepositoryDeleteRemovesFileAndCache(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	s := repo.Create("s1")
	entry, err := s.AppendSessionInfoWithProject("/tmp/project", "work", "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEntry(s.ID(), entry); err != nil {
		t.Fatal(err)
	}

	deleted, err := repo.Delete("s1")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("Delete() = false, want true")
	}
	if _, ok := repo.Get("s1"); ok {
		t.Fatal("deleted session still cached")
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("deleted file stat error = %v, want not exist", err)
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

func mustAppendAssistantToolCall(t *testing.T, s *Session, callID, name, args string) Entry {
	t.Helper()
	entry, err := s.AppendMessage(protocol.AssistantMessage{
		Content: protocol.ContentList{
			protocol.NewToolCallContent(callID, name, json.RawMessage(args)),
		},
		StopReason: protocol.StopReasonToolUse,
		Timestamp:  time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func mustAppendAssistantToolCalls(t *testing.T, s *Session, calls ...protocol.ToolCallContent) Entry {
	t.Helper()
	content := make(protocol.ContentList, 0, len(calls))
	for _, call := range calls {
		content = append(content, call)
	}
	entry, err := s.AppendMessage(protocol.AssistantMessage{
		Content:    content,
		StopReason: protocol.StopReasonToolUse,
		Timestamp:  time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func mustAppendToolResult(t *testing.T, s *Session, callID, name, text string, details any) Entry {
	t.Helper()
	entry, err := s.AppendMessage(protocol.ToolResultMessage{
		ToolCallID: callID,
		ToolName:   name,
		Content:    protocol.ContentList{protocol.NewTextContent(text)},
		Details:    details,
		Timestamp:  time.Now().UnixMilli(),
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
