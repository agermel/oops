package session

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"

	"github.com/google/uuid"
)

func TestSessionForkCopiesCurrentActivePath(t *testing.T) {
	source := New("source")
	root := mustAppendMessage(t, source, "root")
	abandoned := mustAppendAssistantMessage(t, source, "abandoned")
	if err := source.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}
	active := mustAppendAssistantMessage(t, source, "active")
	sourceEntries := source.Entries()

	fork, err := source.Fork(ForkOptions{ID: "fork"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryIDs(fork.Entries()), []string{root.ID, active.ID}; !slices.Equal(got, want) {
		t.Fatalf("fork entries = %v, want %v", got, want)
	}
	if _, ok := fork.Entry(abandoned.ID); ok {
		t.Fatalf("fork retained abandoned entry %q", abandoned.ID)
	}
	if got := entryIDs(source.Entries()); !slices.Equal(got, entryIDs(sourceEntries)) {
		t.Fatalf("source entries changed: got %v want %v", got, entryIDs(sourceEntries))
	}
}

func TestSessionForkBeforeAndAt(t *testing.T) {
	source := New("source")
	root := mustAppendMessage(t, source, "root")
	assistant := mustAppendAssistantMessage(t, source, "answer")
	target := mustAppendMessage(t, source, "retry")

	before, err := source.Fork(ForkOptions{ID: "before", EntryID: target.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryIDs(before.Entries()), []string{root.ID, assistant.ID}; !slices.Equal(got, want) {
		t.Fatalf("before entries = %v, want %v", got, want)
	}

	at, err := source.Fork(ForkOptions{ID: "at", EntryID: target.ID, Position: ForkAt})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryIDs(at.Entries()), []string{root.ID, assistant.ID, target.ID}; !slices.Equal(got, want) {
		t.Fatalf("at entries = %v, want %v", got, want)
	}
	atAssistant, err := source.Fork(ForkOptions{ID: "at-assistant", EntryID: assistant.ID, Position: ForkAt})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryIDs(atAssistant.Entries()), []string{root.ID, assistant.ID}; !slices.Equal(got, want) {
		t.Fatalf("assistant at entries = %v, want %v", got, want)
	}

	empty, err := source.Fork(ForkOptions{ID: "empty", EntryID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := empty.Entries(); len(got) != 0 {
		t.Fatalf("root before entries = %v, want empty", entryIDs(got))
	}

	if _, err := source.Fork(ForkOptions{EntryID: assistant.ID}); err == nil || !strings.Contains(err.Error(), "not a user message") {
		t.Fatalf("assistant before error = %v", err)
	}
	if _, err := source.Fork(ForkOptions{EntryID: "missing", Position: ForkAt}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing target error = %v", err)
	}
	if _, err := source.Fork(ForkOptions{EntryID: target.ID, Position: ForkPosition("after")}); err == nil || !strings.Contains(err.Error(), "invalid fork position") {
		t.Fatalf("invalid position error = %v", err)
	}
}

func TestSessionForkAtLeafCopiesReferencedPath(t *testing.T) {
	source := New("source")
	root := mustAppendMessage(t, source, "root")
	abandoned := mustAppendAssistantMessage(t, source, "abandoned")
	leaf, err := source.append(Entry{Type: EntryLeaf, ParentID: abandoned.ID, LeafID: root.ID})
	if err != nil {
		t.Fatal(err)
	}

	fork, err := source.Fork(ForkOptions{ID: "fork", EntryID: leaf.ID, Position: ForkAt})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryIDs(fork.Entries()), []string{root.ID, leaf.ID}; !slices.Equal(got, want) {
		t.Fatalf("fork entries = %v, want %v", got, want)
	}
	if got := fork.LeafID(); got != root.ID {
		t.Fatalf("fork leaf = %q, want %q", got, root.ID)
	}
	forkLeaf, ok := fork.Entry(leaf.ID)
	if !ok || forkLeaf.ParentID != root.ID {
		t.Fatalf("fork leaf entry = %#v ok=%v", forkLeaf, ok)
	}

	reloaded := New("reloaded")
	if err := reloaded.Load(fork.Entries()); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.LeafID(); got != root.ID {
		t.Fatalf("reloaded leaf = %q, want %q", got, root.ID)
	}
}

func TestRepositoryForkPersistsPathAndEmptyFork(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	source := mustCreateRepositorySession(t, repo, "source")
	if _, err := repo.AppendEntry(source.ID(), Entry{
		Type:      EntrySessionInfo,
		CWD:       "/tmp/source",
		ProjectID: "project-1",
		Name:      "source name",
		Title:     "source title",
	}); err != nil {
		t.Fatal(err)
	}
	root, err := repo.AppendEntry(source.ID(), Entry{Type: EntryMessage, Message: forkUserMessage("root")})
	if err != nil {
		t.Fatal(err)
	}
	abandoned, err := repo.AppendEntry(source.ID(), Entry{Type: EntryMessage, Message: forkAssistantMessage("abandoned")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendEntry(source.ID(), Entry{Type: EntryLeaf, LeafID: root.ID}); err != nil {
		t.Fatal(err)
	}
	active, err := repo.AppendEntry(source.ID(), Entry{Type: EntryMessage, Message: forkAssistantMessage("active")})
	if err != nil {
		t.Fatal(err)
	}

	fork, err := repo.Fork(source.ID(), ForkOptions{ID: "persisted-fork"})
	if err != nil {
		t.Fatal(err)
	}
	if cached, ok := repo.Get(fork.ID()); !ok || cached != fork {
		t.Fatal("fork was not registered in repository")
	}
	if got, want := conversationEntryIDs(fork.Entries()), []string{root.ID, active.ID}; !slices.Equal(got, want) {
		t.Fatalf("fork entries = %v, want %v", got, want)
	}
	if _, ok := fork.Entry(abandoned.ID); ok {
		t.Fatalf("persisted fork retained abandoned entry %q", abandoned.ID)
	}

	reloaded, err := NewRepository(storage).Load(fork.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := conversationEntryIDs(reloaded.Entries()), []string{root.ID, active.ID}; !slices.Equal(got, want) {
		t.Fatalf("reloaded fork entries = %v, want %v", got, want)
	}
	info := reloaded.Info()
	if info.CWD != "/tmp/source" || info.ProjectID != "project-1" || info.Name != "source name" || info.Title != "source title" {
		t.Fatalf("reloaded fork info = %+v", info)
	}
	if _, err := NewRepository(storage).Fork(source.ID(), ForkOptions{ID: fork.ID()}); err == nil {
		t.Fatal("fork replaced an existing session file")
	}

	empty, err := repo.Fork(source.ID(), ForkOptions{ID: "empty-fork", EntryID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := conversationEntryIDs(empty.Entries()); len(got) != 0 {
		t.Fatalf("empty fork conversation entries = %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "empty-fork.jsonl")); err != nil {
		t.Fatalf("empty fork file: %v", err)
	}
	emptyReloaded, err := NewRepository(storage).Load(empty.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := conversationEntryIDs(emptyReloaded.Entries()); len(got) != 0 || emptyReloaded.LeafID() != "" {
		t.Fatalf("reloaded empty fork = entries:%v leaf:%q", got, emptyReloaded.LeafID())
	}
}

func TestRepositoryForkRegistersGeneratedSessionInMemory(t *testing.T) {
	repo := NewRepository(nil)
	source := mustCreateRepositorySession(t, repo, "source")
	root, err := repo.AppendEntry(source.ID(), Entry{Type: EntryMessage, Message: forkUserMessage("root")})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := repo.Fork(source.ID(), ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(fork.ID())
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("generated fork id = %q version=%d err=%v", fork.ID(), parsed.Version(), err)
	}
	if cached, ok := repo.Get(fork.ID()); !ok || cached != fork {
		t.Fatal("generated fork was not registered in memory repository")
	}
	if got, want := entryIDs(fork.Entries()), []string{root.ID}; !slices.Equal(got, want) {
		t.Fatalf("generated fork entries = %v, want %v", got, want)
	}
}

func TestRepositoryForkCreateFailureLeavesCacheAndStorageUnchanged(t *testing.T) {
	writeErr := errors.New("write failed")
	storage := &forkFailureStorage{}
	repo := NewRepository(storage)
	source := mustCreateRepositorySession(t, repo, "source")
	mustAppendMessage(t, source, "root")
	mustAppendAssistantMessage(t, source, "answer")
	storage.createErr = writeErr

	_, err := repo.Fork(source.ID(), ForkOptions{ID: "failed-fork"})
	if !errors.Is(err, writeErr) {
		t.Fatalf("Fork() error = %v, want write failure", err)
	}
	if storage.createdID != "failed-fork" || len(storage.createdEntries) != 2 {
		t.Fatalf("storage create = %q entries=%d", storage.createdID, len(storage.createdEntries))
	}
	if storage.appendCalls != 0 || storage.deletedID != "" {
		t.Fatalf("storage append/delete = %d/%q, want none", storage.appendCalls, storage.deletedID)
	}
	if _, ok := repo.Get("failed-fork"); ok {
		t.Fatal("failed fork was registered in repository")
	}
}

func TestSessionLabelLookupOverwriteAndClear(t *testing.T) {
	s := New("labels")
	target := mustAppendMessage(t, s, "target")

	first, err := s.AppendLabel(target.ID, "  checkpoint  ")
	if err != nil {
		t.Fatal(err)
	}
	if first.TargetID != target.ID {
		t.Fatalf("label target = %q, want %q", first.TargetID, target.ID)
	}
	if label, ok := s.Label(target.ID); !ok || label != "checkpoint" {
		t.Fatalf("label = %q ok=%v", label, ok)
	}
	if _, err := s.AppendLabel(target.ID, "updated"); err != nil {
		t.Fatal(err)
	}
	if label, ok := s.Label(target.ID); !ok || label != "updated" {
		t.Fatalf("updated label = %q ok=%v", label, ok)
	}
	if _, err := s.AppendLabel(target.ID, " \t "); err != nil {
		t.Fatal(err)
	}
	if label, ok := s.Label(target.ID); ok {
		t.Fatalf("cleared label = %q ok=%v", label, ok)
	}
	leafBefore := s.LeafID()
	if _, err := s.AppendLabel("missing", "label"); err == nil || !strings.Contains(err.Error(), "not in session") {
		t.Fatalf("missing target error = %v", err)
	}
	if s.LeafID() != leafBefore {
		t.Fatalf("failed label append changed leaf: got %q want %q", s.LeafID(), leafBefore)
	}
}

func TestRepositoryLabelCacheRebuildsFromFile(t *testing.T) {
	storage, err := NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	s := mustCreateRepositorySession(t, repo, "labels")
	target, err := repo.AppendEntry(s.ID(), Entry{Type: EntryMessage, Message: forkUserMessage("target")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendLabel(s.ID(), target.ID, " checkpoint "); err != nil {
		t.Fatal(err)
	}

	reloadedRepo := NewRepository(storage)
	reloaded, err := reloadedRepo.Load(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if label, ok := reloaded.Label(target.ID); !ok || label != "checkpoint" {
		t.Fatalf("reloaded label = %q ok=%v", label, ok)
	}
	if _, err := reloadedRepo.AppendLabel(s.ID(), target.ID, ""); err != nil {
		t.Fatal(err)
	}

	cleared, err := NewRepository(storage).Load(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if label, ok := cleared.Label(target.ID); ok {
		t.Fatalf("reloaded cleared label = %q ok=%v", label, ok)
	}
	labelEntries := 0
	for _, entry := range cleared.Entries() {
		if entry.Type == EntryLabel {
			labelEntries++
			if entry.TargetID != target.ID {
				t.Fatalf("label history target = %q, want %q", entry.TargetID, target.ID)
			}
		}
	}
	if labelEntries != 2 {
		t.Fatalf("label history entries = %d, want 2", labelEntries)
	}
}

func TestUUIDv7SessionAndEntryIDs(t *testing.T) {
	sessionID := New("").ID()
	parsed, err := uuid.Parse(sessionID)
	if err != nil {
		t.Fatalf("parse session id %q: %v", sessionID, err)
	}
	if parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 {
		t.Fatalf("session id = %q version=%d variant=%v", sessionID, parsed.Version(), parsed.Variant())
	}
	previous := sessionID
	for range 100 {
		next := newSessionID()
		if next <= previous {
			t.Fatalf("uuidv7 order is not monotonic: previous=%q next=%q", previous, next)
		}
		previous = next
	}
	s := New("entry-ids")
	for i := range 3 {
		entry := mustAppendMessage(t, s, "entry")
		switch len(entry.ID) {
		case 8:
			if _, err := hex.DecodeString(entry.ID); err != nil {
				t.Fatalf("entry %d short id %q: %v", i, entry.ID, err)
			}
		case 36:
			parsedEntry, err := uuid.Parse(entry.ID)
			if err != nil || parsedEntry.Version() != 7 {
				t.Fatalf("entry %d full id %q version=%d err=%v", i, entry.ID, parsedEntry.Version(), err)
			}
		default:
			t.Fatalf("entry %d id %q has unexpected length", i, entry.ID)
		}
	}

	const generated = "01234567-89ab-7cde-8abc-0123456789ab"
	if got := generateEntryID(nil, func() string { return generated }); got != "01234567" {
		t.Fatalf("short entry id = %q, want %q", got, "01234567")
	}
	existing := map[string]Entry{"01234567": {ID: "01234567"}}
	calls := 0
	if got := generateEntryID(existing, func() string {
		calls++
		return generated
	}); got != generated || calls != 101 {
		t.Fatalf("collision fallback = %q calls=%d", got, calls)
	}
}

func entryIDs(entries []Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func conversationEntryIDs(entries []Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != EntrySessionInfo {
			ids = append(ids, entry.ID)
		}
	}
	return ids
}

func mustAppendAssistantMessage(t *testing.T, s *Session, text string) Entry {
	t.Helper()
	entry, err := s.AppendMessage(forkAssistantMessage(text))
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func forkUserMessage(text string) protocol.UserMessage {
	return protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent(text)},
		Timestamp: time.Now().UnixMilli(),
	}
}

func forkAssistantMessage(text string) protocol.AssistantMessage {
	return protocol.AssistantMessage{
		Content:    protocol.ContentList{protocol.NewTextContent(text)},
		StopReason: protocol.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

type forkFailureStorage struct {
	createdID      string
	createdEntries []Entry
	createErr      error
	deletedID      string
	appendCalls    int
}

func (s *forkFailureStorage) Create(sessionID string, entries []Entry) error {
	s.createdID = sessionID
	s.createdEntries = cloneEntries(entries)
	return s.createErr
}

func (s *forkFailureStorage) Append(string, Entry) error {
	s.appendCalls++
	return nil
}

func (s *forkFailureStorage) Load(string) ([]Entry, error) {
	return nil, nil
}

func (s *forkFailureStorage) List() ([]string, error) {
	return nil, nil
}

func (s *forkFailureStorage) Delete(sessionID string) (bool, error) {
	s.deletedID = sessionID
	return true, nil
}
