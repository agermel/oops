package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
)

func TestRepositoryCreateRejectsConcurrentDuplicateID(t *testing.T) {
	repo := NewRepository(nil)
	const workers = 16
	errorsByWorker := make([]error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for i := range workers {
		i := i
		go func() {
			defer wait.Done()
			_, errorsByWorker[i] = repo.Create("same-session")
		}()
	}
	wait.Wait()

	created := 0
	duplicates := 0
	for _, err := range errorsByWorker {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrSessionExists):
			duplicates++
		default:
			t.Fatalf("Create() error = %v", err)
		}
	}
	if created != 1 || duplicates != workers-1 {
		t.Fatalf("created/duplicates = %d/%d, want 1/%d", created, duplicates, workers-1)
	}
}

func TestRepositoryCreateRejectsExistingFile(t *testing.T) {
	storage, err := NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewRepository(storage).Create("same-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRepository(storage).Create(first.ID()); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("Create() error = %v, want ErrSessionExists", err)
	}
}

func TestRepositoryCreateReservationIsInvisibleAndRejectsDuplicate(t *testing.T) {
	storage := newGatedCreateStorage()
	repo := NewRepository(storage)
	otherRepo := NewRepository(storage)
	sess := New("reserved-session")
	if _, err := sess.AppendSessionInfo("/tmp/project", "reserved"); err != nil {
		t.Fatal(err)
	}
	created := make(chan error, 1)
	go func() {
		created <- repo.CreateSession(sess)
	}()
	<-storage.entered

	if _, ok := repo.Get(sess.ID()); ok {
		t.Fatal("reserved session was visible in repository cache")
	}
	if _, err := repo.Load(sess.ID()); !errors.Is(err, ErrSessionCreating) {
		t.Fatalf("Load() error = %v, want ErrSessionCreating", err)
	}
	infos, err := repo.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("List() = %#v, want reserved session hidden", infos)
	}
	if _, err := otherRepo.Load(sess.ID()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("other repository Load() error = %v, want fs.ErrNotExist", err)
	}
	if _, ok := otherRepo.Get(sess.ID()); ok {
		t.Fatal("other repository cached a session before publication")
	}
	otherInfos, err := otherRepo.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(otherInfos) != 0 {
		t.Fatalf("other repository List() = %#v, want empty", otherInfos)
	}
	if _, err := repo.Create(sess.ID()); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate Create() error = %v, want ErrSessionExists", err)
	}

	close(storage.release)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	loaded, err := otherRepo.Load(sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Info().Name != "reserved" {
		t.Fatalf("loaded session name = %q, want reserved", loaded.Info().Name)
	}
}

func TestRepositoryCreateAllowsStorageReentry(t *testing.T) {
	storage := &reentrantCreateStorage{}
	repo := NewRepository(storage)
	storage.repo = repo
	result := make(chan error, 1)
	go func() {
		_, err := repo.Create("reentrant-session")
		result <- err
	}()

	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Create() deadlocked during storage reentry")
	}
	if _, ok := repo.Get("reentrant-session"); !ok {
		t.Fatal("created session missing from repository")
	}
}

func TestRepositoryDeleteReservationIsInvisibleDuringStorageReentry(t *testing.T) {
	storage := &reentrantDeleteStorage{}
	repo := NewRepository(storage)
	storage.repo = repo
	sess, err := repo.Create("deleting-session")
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := repo.DeleteSession(sess)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("DeleteSession() deleted = false")
	}
	if _, ok := repo.Get(sess.ID()); ok {
		t.Fatal("deleted session remained in repository")
	}
}

func TestRepositoryDeleteSessionRejectsReplacedInstance(t *testing.T) {
	storage, err := NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	oldSession, err := repo.Create("replaced-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Delete(oldSession.ID()); err != nil {
		t.Fatal(err)
	}
	replacement := New(oldSession.ID())
	if _, err := replacement.AppendSessionInfo("/replacement", "replacement"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateSession(replacement); err != nil {
		t.Fatal(err)
	}

	deleted, err := repo.DeleteSession(oldSession)
	if !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("DeleteSession() error = %v, want ErrSessionMismatch", err)
	}
	if deleted {
		t.Fatal("DeleteSession() deleted replacement session")
	}
	if cached, ok := repo.Get(oldSession.ID()); !ok || cached != replacement {
		t.Fatal("replacement session changed after stale delete")
	}
}

func TestRepositoryCreateSessionIsExclusiveAcrossRepositories(t *testing.T) {
	dir := t.TempDir()
	storageA, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	storageB, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repositories := []*Repository{NewRepository(storageA), NewRepository(storageB)}
	sessions := []*Session{New("shared-session"), New("shared-session")}
	for i, sess := range sessions {
		if _, err := sess.AppendSessionInfo("/tmp/project", fmt.Sprintf("writer-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	errs := make([]error, len(repositories))
	var wait sync.WaitGroup
	wait.Add(len(repositories))
	for i := range repositories {
		i := i
		go func() {
			defer wait.Done()
			<-start
			errs[i] = repositories[i].CreateSession(sessions[i])
		}()
	}
	close(start)
	wait.Wait()

	winner := -1
	for i, err := range errs {
		switch {
		case err == nil:
			if winner >= 0 {
				t.Fatalf("multiple repositories created the same session: errors=%v", errs)
			}
			winner = i
		case errors.Is(err, ErrSessionExists):
		default:
			t.Fatalf("CreateSession() error = %v", err)
		}
	}
	if winner < 0 {
		t.Fatalf("no repository created the session: errors=%v", errs)
	}
	entries, err := storageA.Load("shared-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != fmt.Sprintf("writer-%d", winner) {
		t.Fatalf("published entries = %#v, winner=%d", entries, winner)
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirEntries) != 1 || dirEntries[0].Name() != filepath.Base("shared-session.jsonl") {
		t.Fatalf("session directory entries = %#v", dirEntries)
	}
}

func TestRepositoryAppendCompactionPersistsDetails(t *testing.T) {
	storage, err := NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	current := mustCreateRepositorySession(t, repo, "compact-entry")
	first, err := repo.AppendEntry(current.ID(), Entry{Type: EntryMessage, Message: repositoryUserMessage("keep")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendEntry(current.ID(), Entry{Type: EntryMessage, Message: repositoryAssistantMessage("latest")}); err != nil {
		t.Fatal(err)
	}
	wantDetails := SummaryDetails{ReadFiles: []string{"README.md"}, ModifiedFiles: []string{"main.go"}}
	entry, err := repo.AppendCompaction(current.ID(), "checkpoint", first.ID, 42, wantDetails)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Type != EntryCompaction || entry.Summary != "checkpoint" || entry.FirstKeptEntryID != first.ID || entry.TokensBefore != 42 {
		t.Fatalf("entry = %#v", entry)
	}
	var details SummaryDetails
	if err := json.Unmarshal(entry.Details, &details); err != nil {
		t.Fatal(err)
	}
	if len(details.ReadFiles) != 1 || details.ReadFiles[0] != "README.md" || len(details.ModifiedFiles) != 1 || details.ModifiedFiles[0] != "main.go" {
		t.Fatalf("details = %#v", details)
	}

	reloaded, err := NewRepository(storage).Load(current.ID())
	if err != nil {
		t.Fatal(err)
	}
	reloadedEntry, ok := reloaded.Entry(entry.ID)
	if !ok || reloadedEntry.Type != EntryCompaction || reloaded.LeafID() != entry.ID {
		t.Fatalf("reloaded entry = %#v ok=%v leaf=%q", reloadedEntry, ok, reloaded.LeafID())
	}
}

func repositoryUserMessage(text string) protocol.UserMessage {
	return protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent(text)},
		Timestamp: time.Now().UnixMilli(),
	}
}

func repositoryAssistantMessage(text string) protocol.AssistantMessage {
	return protocol.AssistantMessage{
		Content:    protocol.ContentList{protocol.NewTextContent(text)},
		StopReason: protocol.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

type gatedCreateStorage struct {
	mu        sync.Mutex
	entered   chan struct{}
	release   chan struct{}
	id        string
	entries   []Entry
	published bool
}

func newGatedCreateStorage() *gatedCreateStorage {
	return &gatedCreateStorage{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *gatedCreateStorage) Create(sessionID string, entries []Entry) error {
	s.mu.Lock()
	s.id = sessionID
	s.entries = cloneEntries(entries)
	s.mu.Unlock()
	close(s.entered)
	<-s.release
	s.mu.Lock()
	s.published = true
	s.mu.Unlock()
	return nil
}

func (*gatedCreateStorage) Append(string, Entry) error { return nil }

func (s *gatedCreateStorage) Load(sessionID string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID != s.id || !s.published {
		return nil, nil
	}
	return cloneEntries(s.entries), nil
}

func (s *gatedCreateStorage) List() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id == "" {
		return nil, nil
	}
	return []string{s.id}, nil
}

func (*gatedCreateStorage) Delete(string) (bool, error) { return false, nil }

type reentrantCreateStorage struct {
	repo *Repository
}

func (s *reentrantCreateStorage) Create(sessionID string, _ []Entry) error {
	if _, ok := s.repo.Get(sessionID); ok {
		return errors.New("reserved session was cached during Create")
	}
	if _, err := s.repo.Load(sessionID); !errors.Is(err, ErrSessionCreating) {
		return fmt.Errorf("reentrant Load() error = %v, want ErrSessionCreating", err)
	}
	infos, err := s.repo.List()
	if err != nil {
		return err
	}
	if len(infos) != 0 {
		return fmt.Errorf("reentrant List() = %#v, want empty", infos)
	}
	return nil
}

func (*reentrantCreateStorage) Append(string, Entry) error { return nil }
func (*reentrantCreateStorage) Load(string) ([]Entry, error) {
	return nil, nil
}
func (*reentrantCreateStorage) List() ([]string, error) {
	return []string{"reentrant-session"}, nil
}
func (*reentrantCreateStorage) Delete(string) (bool, error) { return false, nil }

type reentrantDeleteStorage struct {
	repo    *Repository
	id      string
	entries []Entry
}

func (s *reentrantDeleteStorage) Create(sessionID string, entries []Entry) error {
	s.id = sessionID
	s.entries = cloneEntries(entries)
	return nil
}

func (*reentrantDeleteStorage) Append(string, Entry) error { return nil }

func (s *reentrantDeleteStorage) Load(sessionID string) ([]Entry, error) {
	if sessionID != s.id {
		return nil, nil
	}
	return cloneEntries(s.entries), nil
}

func (s *reentrantDeleteStorage) List() ([]string, error) {
	if s.id == "" {
		return nil, nil
	}
	return []string{s.id}, nil
}

func (s *reentrantDeleteStorage) Delete(sessionID string) (bool, error) {
	if _, ok := s.repo.Get(sessionID); ok {
		return false, errors.New("deleting session remained visible in repository cache")
	}
	if _, err := s.repo.Load(sessionID); !errors.Is(err, ErrSessionDeleting) {
		return false, fmt.Errorf("reentrant Load() error = %v, want ErrSessionDeleting", err)
	}
	infos, err := s.repo.List()
	if err != nil {
		return false, err
	}
	if len(infos) != 0 {
		return false, fmt.Errorf("reentrant List() = %#v, want empty", infos)
	}
	s.id = ""
	s.entries = nil
	return true, nil
}
