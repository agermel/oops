package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	protocol "oops/internal/agent/ai"
	"oops/internal/logutil"

	"go.uber.org/zap/zapcore"
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

func TestInfoCountsOnlyUserMessages(t *testing.T) {
	s := New("s1")
	mustAppendMessage(t, s, "question")
	mustAppendAssistantToolCall(t, s, "call_read", "read", `{"path":"README.md"}`)
	mustAppendToolResult(t, s, "call_read", "read", "ok", nil)
	if _, err := s.AppendMessage(protocol.AssistantMessage{
		Content:    protocol.ContentList{protocol.NewTextContent("answer")},
		StopReason: protocol.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	info := s.Info()
	if info.Questions != 1 {
		t.Fatalf("question count = %d, want 1 user question", info.Questions)
	}
}

func TestInfoCountsQuestionsOnActiveBranch(t *testing.T) {
	s := New("s1")
	root := mustAppendMessage(t, s, "root")
	mustAppendMessage(t, s, "abandoned")
	if err := s.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}
	mustAppendMessage(t, s, "active")

	info := s.Info()
	if info.Questions != 2 {
		t.Fatalf("question count = %d, want 2 active-branch user questions", info.Questions)
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
		"The conversation history before this point was compacted into the following summary:\n\n<summary>\nolder facts\n</summary>",
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

func TestAppendCompactionRejectsFirstKeptEntryOutsideCurrentPath(t *testing.T) {
	s := New("s1")
	root := mustAppendMessage(t, s, "root")
	left := mustAppendMessage(t, s, "left")
	if err := s.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}
	right := mustAppendMessage(t, s, "right")
	if err := s.MoveTo(left.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AppendCompaction("summary", right.ID, 10); err == nil || !strings.Contains(err.Error(), "not on parent path") {
		t.Fatalf("AppendCompaction() error = %v, want parent path error", err)
	}
	if got := s.LeafID(); got != left.ID {
		t.Fatalf("leaf = %q, want %q", got, left.ID)
	}
}

func TestLoadRejectsCompactionFirstKeptEntryOutsideParentPath(t *testing.T) {
	source := New("source")
	root := mustAppendMessage(t, source, "root")
	left := mustAppendMessage(t, source, "left")
	if err := source.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}
	right := mustAppendMessage(t, source, "right")
	entries := source.Entries()
	entries = append(entries, Entry{
		Type:             EntryCompaction,
		Version:          formatVersion,
		ID:               newUUIDv7(),
		ParentID:         left.ID,
		Timestamp:        time.Now(),
		Summary:          "summary",
		FirstKeptEntryID: right.ID,
		TokensBefore:     10,
	})

	loaded := New("loaded")
	if err := loaded.Load(entries); err == nil || !strings.Contains(err.Error(), "not on parent path") {
		t.Fatalf("Load() error = %v, want parent path error", err)
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
		"The following is a summary of a branch that this conversation came back from:\n\n<summary>\nprevious branch chose read path</summary>",
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
	s := mustCreateRepositorySession(t, repo, "s1")
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

func TestFileStorageRejectsLegacyJSONL(t *testing.T) {
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
	if _, err := repo.Load("s1"); err == nil {
		t.Fatal("Load() error = nil, want legacy JSONL rejection")
	} else if !strings.Contains(err.Error(), "s1.jsonl:1") {
		t.Fatalf("Load() error = %q, want filename and line", err)
	}
	if _, ok := repo.Get("s1"); ok {
		t.Fatal("failed legacy load populated repository cache")
	}
}

func TestFileStorageRejectsVersionThree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	validMessage := `{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1}`
	raw := `{"type":"message","version":3,"id":"m1","timestamp":"` + now + `","message":` + validMessage + `}` + "\n"
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	if _, err := repo.Load("s1"); err == nil {
		t.Fatal("Load() error = nil, want version three rejection")
	} else {
		for _, want := range []string{"s1.jsonl:1", "invalid_entry"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("Load() error = %q, want %q", err, want)
			}
		}
		if cause := errors.Unwrap(err); cause == nil || !strings.Contains(cause.Error(), "session entry version 3, want 1") {
			t.Fatalf("Load() cause = %v, want version rejection", cause)
		}
	}
	if _, ok := repo.Get("s1"); ok {
		t.Fatal("failed version three load populated repository cache")
	}
}

func TestFileStorageRejectsSessionIDsOutsideAllowlist(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(dir), "outside.jsonl")
	original := []byte("outside")
	if err := os.WriteFile(outside, original, 0644); err != nil {
		t.Fatal(err)
	}
	entry := mustAppendMessage(t, New("valid"), "hello")

	for _, sessionID := range []string{
		"../outside",
		"..%2foutside",
		"nested/session",
		`nested\session`,
		"with space",
		"session.jsonl",
		"UPPERCASE",
		"会话",
		strings.Repeat("a", 129),
	} {
		t.Run(sessionID, func(t *testing.T) {
			if err := storage.Append(sessionID, entry); err == nil {
				t.Fatal("Append() error = nil, want invalid session id rejection")
			}
			if _, err := storage.Load(sessionID); err == nil {
				t.Fatal("Load() error = nil, want invalid session id rejection")
			}
			if _, err := storage.Delete(sessionID); err == nil {
				t.Fatal("Delete() error = nil, want invalid session id rejection")
			}
		})
	}

	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("outside file = %q, want %q", got, original)
	}
}

func TestFileStorageRequiresExactFilenameCase(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	upperPath := filepath.Join(dir, "UPPER.jsonl")
	original := []byte("invalid uppercase file")
	if err := os.WriteFile(upperPath, original, 0644); err != nil {
		t.Fatal(err)
	}

	entries, err := storage.Load("upper")
	if err != nil {
		t.Fatalf("Load lowercase alias: %v", err)
	}
	if entries != nil {
		t.Fatalf("Load lowercase alias = %#v, want nil", entries)
	}
	deleted, err := storage.Delete("upper")
	if err != nil {
		t.Fatalf("Delete lowercase alias: %v", err)
	}
	if deleted {
		t.Fatal("Delete lowercase alias removed an inexact directory entry")
	}

	if _, err := os.Stat(filepath.Join(dir, "upper.jsonl")); err == nil {
		entry := mustAppendMessage(t, New("upper"), "hello")
		if err := storage.Append("upper", entry); err == nil {
			t.Fatal("Append lowercase alias succeeded on a case-insensitive filesystem")
		}
	}
	got, err := os.ReadFile(upperPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("uppercase file = %q, want unchanged", got)
	}
}

func TestFileStorageRootRejectsEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(dir), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.jsonl")); err != nil {
		t.Fatal(err)
	}

	if _, err := storage.Load("linked"); err == nil {
		t.Fatal("Load() error = nil, want root escape rejection")
	}
	entry := mustAppendMessage(t, New("linked"), "hello")
	if err := storage.Append("linked", entry); err == nil {
		t.Fatal("Append() error = nil, want root escape rejection")
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "outside" {
		t.Fatalf("outside file = %q, want unchanged", got)
	}
}

func TestEntryJSONUsesVersionOne(t *testing.T) {
	entry := mustAppendMessage(t, New("s1"), "hello")
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Version != 1 {
		t.Fatalf("version = %d, want 1", wire.Version)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	validMessage := `{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1}`
	var decoded Entry
	if err := json.Unmarshal([]byte(`{"type":"message","version":1,"id":"m1","timestamp":"`+now+`","message":`+validMessage+`}`), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(v1 entry): %v", err)
	}
	if decoded.Version != 1 {
		t.Fatalf("decoded version = %d, want 1", decoded.Version)
	}
}

func TestEntryUnmarshalRejectsUnsupportedVersionOrIncompleteWire(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	validMessage := `{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1}`
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "missing version", raw: `{"type":"message","id":"m1","timestamp":"` + now + `","message":` + validMessage + `}`, wantErr: "session entry version 0, want 1"},
		{name: "zero version", raw: `{"type":"message","version":0,"id":"m1","timestamp":"` + now + `","message":` + validMessage + `}`, wantErr: "session entry version 0, want 1"},
		{name: "version two", raw: `{"type":"message","version":2,"id":"m1","timestamp":"` + now + `","message":` + validMessage + `}`, wantErr: "session entry version 2, want 1"},
		{name: "version three", raw: `{"type":"message","version":3,"id":"m1","timestamp":"` + now + `","message":` + validMessage + `}`, wantErr: "session entry version 3, want 1"},
		{name: "future version", raw: `{"type":"message","version":99,"id":"m1","timestamp":"` + now + `","message":` + validMessage + `}`, wantErr: "session entry version 99, want 1"},
		{name: "missing version before message", raw: `{"type":"message","id":"m1","timestamp":"` + now + `","message":{"role":"user","content":"hello"}}`, wantErr: "session entry version 0, want 1"},
		{name: "version two before message", raw: `{"type":"message","version":2,"id":"m1","timestamp":"` + now + `","message":{"role":"user","content":"hello"}}`, wantErr: "session entry version 2, want 1"},
		{name: "version three before message", raw: `{"type":"message","version":3,"id":"m1","timestamp":"` + now + `","message":{"role":"user","content":"hello"}}`, wantErr: "session entry version 3, want 1"},
		{name: "entry type before message", raw: `{"type":"unknown","version":1,"id":"m1","timestamp":"` + now + `","message":{"role":"user","content":"hello"}}`, wantErr: `unknown session entry type "unknown"`},
		{name: "legacy entry type", raw: `{"type":"session","version":1,"id":"header","timestamp":"` + now + `"}`, wantErr: `unknown session entry type "session"`},
		{name: "missing id", raw: `{"type":"session_info","version":1,"timestamp":"` + now + `"}`, wantErr: "session entry requires id"},
		{name: "missing timestamp", raw: `{"type":"session_info","version":1,"id":"header"}`, wantErr: "session entry requires timestamp"},
		{name: "legacy message shape", raw: `{"type":"message","version":1,"id":"m1","timestamp":"` + now + `","message":{"role":"user","content":"hello"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var entry Entry
			err := json.Unmarshal([]byte(tt.raw), &entry)
			if err == nil {
				t.Fatalf("json.Unmarshal(%s) error = nil", tt.raw)
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("json.Unmarshal(%s) error = %q, want %q", tt.raw, err, tt.wantErr)
			}
		})
	}
}

func TestEntryUnmarshalRejectsUnknownField(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	validMessage := `{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1}`
	var entry Entry
	err := json.Unmarshal([]byte(`{"type":"message","version":1,"id":"m1","timestamp":"`+now+`","message":`+validMessage+`,"extra":true}`), &entry)
	if err == nil {
		t.Fatal("json.Unmarshal() error = nil, want unknown field rejection")
	}
}

func TestRepositoryLoadRecoverableToolTailUsesExecutableLeaf(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	source := New("s1")
	user := mustAppendMessage(t, source, "start")
	assistant := mustAppendAssistantToolCalls(t, source,
		protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`)),
		protocol.NewToolCallContent("call_2", "read", json.RawMessage(`{"path":"b.md"}`)),
	)
	partial := mustAppendToolResult(t, source, "call_1", "read", "a", nil)
	for _, entry := range []Entry{user, assistant, partial} {
		if err := storage.Append("s1", entry); err != nil {
			t.Fatal(err)
		}
	}

	repo := NewRepository(storage)
	reloaded, err := repo.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := reloaded.BuildContext()
	if ctx.LeafID != user.ID {
		t.Fatalf("leaf = %q, want executable parent %q", ctx.LeafID, user.ID)
	}
	assertTexts(t, ctx.Messages, []string{"start"})
	if len(reloaded.Entries()) != 3 {
		t.Fatalf("entries = %d, want recoverable tail preserved", len(reloaded.Entries()))
	}

	next, err := repo.AppendEntry("s1", Entry{Type: EntryMessage, Message: protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent("after")},
		Timestamp: time.Now().UnixMilli(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if next.ParentID != user.ID {
		t.Fatalf("next parent = %q, want executable leaf %q", next.ParentID, user.ID)
	}
}

func TestRepositoryLoadRejectsInvalidProviderHistoryBeforeCache(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	source := New("s1")
	user := mustAppendMessage(t, source, "start")
	assistant := mustAppendAssistantToolCall(t, source, "call_1", "read", `{"path":"a.md"}`)
	next := mustAppendMessage(t, source, "invalid")
	for _, entry := range []Entry{user, assistant, next} {
		if err := storage.Append("s1", entry); err != nil {
			t.Fatal(err)
		}
	}

	repo := NewRepository(storage)
	_, err = repo.Load("s1")
	var corruptErr *corruptSessionFileError
	if !errors.As(err, &corruptErr) {
		t.Fatalf("Load() error = %v, want corrupt session error", err)
	}
	if corruptErr.reason != corruptSessionInvalidHistory || corruptErr.line != 3 {
		t.Fatalf("corrupt error = %+v, want invalid_history at line 3", corruptErr)
	}
	if _, ok := repo.Get("s1"); ok {
		t.Fatal("invalid history populated repository cache")
	}
}

func TestSessionLoadIsAtomicOnInvalidSecondEntry(t *testing.T) {
	session := New("existing")
	existing := mustAppendMessage(t, session, "keep")
	before := session.Entries()
	beforeInfo := session.Info()

	candidate := New("candidate")
	valid := mustAppendMessage(t, candidate, "candidate")
	invalid := valid
	invalid.ID = ""
	if err := session.Load([]Entry{valid, invalid}); err == nil {
		t.Fatal("Load() error = nil, want invalid second entry rejection")
	}
	after := session.Entries()
	afterInfo := session.Info()
	if len(after) != len(before) || after[0].ID != existing.ID || afterInfo.LeafID != beforeInfo.LeafID {
		t.Fatalf("failed Load mutated session: entries=%#v info=%+v", after, afterInfo)
	}
}

func TestRepositoryListSkipsInvalidFileAndCachesValidSessions(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	validSession := New("valid")
	valid, err := validSession.AppendSessionInfo("/tmp/project", "work")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Append("valid", valid); err != nil {
		t.Fatal(err)
	}
	invalidData := []byte(`{"type":"do-not-log","version":1,"id":"entry","timestamp":"2026-07-13T00:00:00Z"}` + "\n")
	invalidPath := filepath.Join(dir, "invalid.jsonl")
	if err := os.WriteFile(invalidPath, invalidData, 0644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logutil.Init(false, zapcore.AddSync(&logs), "")
	t.Cleanup(func() { logutil.Init(false, nil, "") })

	repo := NewRepository(storage)
	infos, err := repo.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(infos) != 1 || infos[0].ID != "valid" {
		t.Fatalf("List() = %+v, want only valid session", infos)
	}
	if _, ok := repo.Get("valid"); !ok {
		t.Fatal("valid session was not cached")
	}
	if _, ok := repo.Get("invalid"); ok {
		t.Fatal("invalid session was cached")
	}
	gotInvalidData, err := os.ReadFile(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotInvalidData, invalidData) {
		t.Fatalf("invalid file changed: got %q, want %q", gotInvalidData, invalidData)
	}
	logText := logs.String()
	for _, want := range []string{
		"session: skip unreadable file",
		`"session_id": "invalid"`,
		`"file": "invalid.jsonl"`,
		`"line": 1`,
		`"reason": "invalid_entry"`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log = %q, want %q", logText, want)
		}
	}
	if strings.Contains(logText, "do-not-log") {
		t.Fatalf("log leaked session content: %q", logText)
	}
}

func TestDecodeSessionEntriesReturnsReadError(t *testing.T) {
	readErr := errors.New("read failed")
	source := io.MultiReader(strings.NewReader(`{"type":`), iotest.ErrReader(readErr))
	_, err := decodeSessionEntries(source, "s1.jsonl", "s1")
	if !errors.Is(err, readErr) {
		t.Fatalf("decodeSessionEntries() error = %v, want read failure", err)
	}
	var corruptErr *corruptSessionFileError
	if errors.As(err, &corruptErr) {
		t.Fatalf("read failure classified as corrupt session: %v", err)
	}
}

func TestDecodeSessionEntriesClassifiesOversizedLine(t *testing.T) {
	data := strings.NewReader(strings.Repeat("x", maxSessionLineBytes+1))
	_, err := decodeSessionEntries(data, "s1.jsonl", "s1")
	var corruptErr *corruptSessionFileError
	if !errors.As(err, &corruptErr) {
		t.Fatalf("decodeSessionEntries() error = %v, want corrupt session error", err)
	}
	if corruptErr.reason != corruptSessionLineTooLong || corruptErr.line != 1 {
		t.Fatalf("corrupt error = %+v, want line 1 and line_too_long", corruptErr)
	}
}

func TestRepositoryListReturnsDirectoryError(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRepository(storage).List(); err == nil {
		t.Fatal("List() error = nil, want storage directory error")
	}
}

func TestFileStorageLoadsProjectID(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	s := mustCreateRepositorySession(t, repo, "s1")
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
	if info.Questions != 0 || info.Entries != 1 {
		t.Fatalf("counts = questions:%d entries:%d", info.Questions, info.Entries)
	}
}

func TestRepositoryDeleteRemovesFileAndCache(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	s := mustCreateRepositorySession(t, repo, "s1")
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

func TestFileStorageAppendAddsSeparatorAfterFileWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	source := New("s1")
	first := mustAppendMessage(t, source, "first")
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	second := mustAppendMessage(t, source, "second")
	if err := storage.Append("s1", second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "s1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("}\n{")) {
		t.Fatalf("file = %q, want one separator newline", data)
	}
	entries, err := storage.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
}

func TestRepositoryAppendFailureKeepsSessionState(t *testing.T) {
	writeErr := errors.New("write failed")
	storage := &fakeStorage{appendErr: writeErr}
	repo := NewRepository(storage)
	s := mustCreateRepositorySession(t, repo, "s1")

	_, err := repo.AppendEntry("s1", Entry{Type: EntryMessage, Message: protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent("hello")},
		Timestamp: time.Now().UnixMilli(),
	}})
	if !errors.Is(err, writeErr) {
		t.Fatalf("AppendEntry() error = %v, want write failure", err)
	}
	if entries := s.Entries(); len(entries) != 0 {
		t.Fatalf("session entries = %d, want unchanged", len(entries))
	}
}

func TestRepositoryAppendPublishesCommittedDirectorySyncFailure(t *testing.T) {
	storage := &fakeStorage{appendErr: &StorageCommitError{
		Operation: "append",
		Phase:     "directory-sync",
		Committed: true,
		Err:       errors.New("sync failed"),
	}}
	repo := NewRepository(storage)
	s := mustCreateRepositorySession(t, repo, "s1")

	_, err := repo.AppendEntry("s1", Entry{Type: EntryMessage, Message: protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent("hello")},
		Timestamp: time.Now().UnixMilli(),
	}})
	if err != nil {
		t.Fatalf("AppendEntry() error = %v, want committed success", err)
	}
	if entries := s.Entries(); len(entries) != 1 {
		t.Fatalf("session entries = %d, want committed entry", len(entries))
	}
}

type fakeStorage struct {
	appendErr error
	entries   []Entry
}

func mustCreateRepositorySession(t *testing.T, repo *Repository, id string) *Session {
	t.Helper()
	sess, err := repo.Create(id)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func (s *fakeStorage) Append(string, Entry) error {
	return s.appendErr
}

func (s *fakeStorage) Create(string, []Entry) error {
	return nil
}

func (s *fakeStorage) Load(string) ([]Entry, error) {
	return cloneEntries(s.entries), nil
}

func (s *fakeStorage) List() ([]string, error) {
	return nil, nil
}

func (s *fakeStorage) Delete(string) (bool, error) {
	return false, nil
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
