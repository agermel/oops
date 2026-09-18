package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	agentcore "oops/internal/agent/core"
	"oops/internal/agent/runtime/model"
	agentresources "oops/internal/agent/runtime/resources"
	"oops/internal/agent/runtime/session"
	agentskills "oops/internal/agent/runtime/skills"
)

func TestPromptPersistsMessagesAndFileBackedReload(t *testing.T) {
	dir := t.TempDir()
	storage, err := session.NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	stream := streamSequence(textStream("answer"))
	runtime := NewRuntime(RuntimeOptions{
		Repo:   repo,
		Loader: agentresources.StaticLoader{Snapshot: agentresources.Snapshot{SystemPrompt: "system"}},
		Config: agentcore.AgentLoopConfig{Stream: stream},
		Model:  "model",
	})
	as, err := runtime.NewSession(context.Background(), NewSessionOptions{ID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("hello")}); err != nil {
		t.Fatal(err)
	}
	reloadedRepo := session.NewRepository(storage)
	reloaded, err := reloadedRepo.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := reloaded.BuildContext()
	if len(ctx.Messages) != 2 {
		t.Fatalf("reloaded messages = %d", len(ctx.Messages))
	}
	if _, err := storage.Load("s1"); err != nil {
		t.Fatal(err)
	}
	if path := filepath.Join(dir, "s1.jsonl"); path == "" {
		t.Fatal("empty path")
	}
}

func TestNavigateTreeSnapshotRejectsBusySession(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	stream := func(ctx context.Context, req agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return textStream("done"), nil
	}
	as := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: stream}, nil)

	done := make(chan error, 1)
	go func() {
		_, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("hold")})
		done <- err
	}()
	<-started
	if _, err := as.NavigateTreeSnapshot(as.session.LeafID()); !errors.Is(err, agentcore.ErrAgentBusy) {
		t.Fatalf("NavigateTreeSnapshot err = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAgentHarnessSteerQueuesMessageForActiveRun(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	stream := func(ctx context.Context, _ agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		calls++
		if calls == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return textStream("first"), nil
		}
		return textStream("second"), nil
	}
	as := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: stream}, nil)

	type promptResult struct {
		messages protocol.MessageList
		err      error
	}
	done := make(chan promptResult, 1)
	go func() {
		messages, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
		done <- promptResult{messages: messages, err: err}
	}()
	<-started
	if err := as.Steer(protocol.MessageList{userMessage("redirect")}); err != nil {
		t.Fatal(err)
	}
	close(release)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	assertMessageTexts(t, result.messages, []string{"start", "first", "redirect", "second"})
	assertMessageTexts(t, as.session.BuildContext().Messages, []string{"start", "first", "redirect", "second"})
}

func TestAgentHarnessAbortClearsLiveQueuesAndPreservesNextTurn(t *testing.T) {
	started := make(chan struct{})
	calls := 0
	stream := func(ctx context.Context, _ agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		calls++
		if calls == 1 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return textStream("after"), nil
	}
	as := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: stream}, nil)

	firstDone := make(chan error, 1)
	go func() {
		_, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
		firstDone <- err
	}()
	<-started
	if err := as.Steer(protocol.MessageList{userMessage("redirect")}); err != nil {
		t.Fatal(err)
	}
	if err := as.FollowUp(protocol.MessageList{userMessage("afterward")}); err != nil {
		t.Fatal(err)
	}
	if err := as.NextTurn(protocol.MessageList{userMessage("queued-next")}); err != nil {
		t.Fatal(err)
	}
	aborted, err := as.Abort(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertMessageTexts(t, aborted.ClearedSteering, []string{"redirect"})
	assertMessageTexts(t, aborted.ClearedFollowUp, []string{"afterward"})

	messages, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("explicit")})
	if err != nil {
		t.Fatal(err)
	}
	assertMessageTexts(t, messages, []string{"queued-next", "explicit", "after"})
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := as.WaitForIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAgentHarnessAbortWaitsForActiveCoreListeners(t *testing.T) {
	started := make(chan struct{})
	listenerEntered := make(chan struct{})
	releaseListener := make(chan struct{})
	as := newHarnessSession(t, agentcore.AgentLoopConfig{
		Stream: func(ctx context.Context, _ agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}, nil)
	as.Listen(func(_ context.Context, event protocol.AgentEvent, _ agentcore.AgentState) error {
		if event.Type == protocol.AgentEventAgentEnd {
			close(listenerEntered)
			<-releaseListener
		}
		return nil
	})

	promptDone := make(chan error, 1)
	go func() {
		_, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
		promptDone <- err
	}()
	<-started

	abortDone := make(chan error, 1)
	go func() {
		_, err := as.Abort(context.Background())
		abortDone <- err
	}()
	<-listenerEntered
	select {
	case err := <-abortDone:
		t.Fatalf("Abort() returned before listener settlement with error %v", err)
	default:
	}
	close(releaseListener)
	if err := <-abortDone; err != nil {
		t.Fatal(err)
	}
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
}

func TestNavigateTreeWithSummarySnapshotReturnsEditorTextForUserTarget(t *testing.T) {
	as := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))}, nil)
	root, err := as.session.AppendMessage(userMessage("root"))
	if err != nil {
		t.Fatal(err)
	}
	right, err := as.session.AppendMessage(userMessage("right"))
	if err != nil {
		t.Fatal(err)
	}
	if err := as.session.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}

	snapshot, err := as.NavigateTreeWithSummarySnapshot(right.ID, "abandoned branch")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EditorText != "right" {
		t.Fatalf("snapshot editor text = %q, want right", snapshot.EditorText)
	}
	assertMessageTexts(t, snapshot.Messages, []string{
		"root",
		"The following is a summary of a branch that this conversation came back from:\n\n<summary>\nabandoned branch</summary>",
	})
	leaf, ok := as.session.Entry(snapshot.LeafID)
	if !ok || leaf.Type != session.EntryBranchSummary || leaf.ParentID != root.ID {
		t.Fatalf("leaf = %#v ok=%v, want branch summary under %q", leaf, ok, root.ID)
	}
}

func TestRuntimeConcurrentResumeReturnsSingleWrapper(t *testing.T) {
	storage, err := session.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	runtime := NewRuntime(RuntimeOptions{
		Repo:   repo,
		Loader: agentresources.StaticLoader{},
		Config: agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
	})
	created, err := runtime.NewSession(context.Background(), NewSessionOptions{ID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	runtime.active = map[string]*AgentHarness{}

	const workers = 8
	results := make([]*AgentHarness, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			resumed, err := runtime.Resume(context.Background(), "s1")
			if err != nil {
				t.Errorf("Resume() error = %v", err)
				return
			}
			results[i] = resumed
		}()
	}
	wg.Wait()
	for i, result := range results {
		if result == nil {
			t.Fatalf("result %d nil", i)
		}
		if result != results[0] {
			t.Fatalf("result %d differs from result 0", i)
		}
	}
	if created == results[0] {
		t.Fatal("test setup failed: active cache was not cleared")
	}
}

func TestRuntimeNewSessionRejectsExistingIDWithoutReplacingHarness(t *testing.T) {
	storage, err := session.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	runtime := NewRuntime(RuntimeOptions{
		Repo:   repo,
		Loader: agentresources.StaticLoader{},
		Config: agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
	})
	first, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "same-session", Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	entriesBefore := first.Snapshot().Entries

	if _, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "same-session", Name: "second"}); !errors.Is(err, session.ErrSessionExists) {
		t.Fatalf("NewSession() error = %v, want ErrSessionExists", err)
	}
	cached, ok := repo.Get("same-session")
	if !ok || cached != first.session {
		t.Fatal("duplicate create replaced the repository session")
	}
	runtime.mu.Lock()
	active := runtime.active["same-session"]
	runtime.mu.Unlock()
	if active != first {
		t.Fatal("duplicate create replaced the active harness")
	}
	if entriesAfter := first.Snapshot().Entries; !reflect.DeepEqual(entriesAfter, entriesBefore) {
		t.Fatalf("duplicate create changed entries: before=%#v after=%#v", entriesBefore, entriesAfter)
	}
}

func TestRuntimeNewSessionBlocksResumeAndDuplicateWhileCreating(t *testing.T) {
	dir := t.TempDir()
	storage, err := session.NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	otherRepo := session.NewRepository(storage)
	entered := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	loader := resourceLoaderFunc(func(context.Context, agentresources.Request) (agentresources.Snapshot, error) {
		close(entered)
		<-release
		return agentresources.Snapshot{}, nil
	})
	runtime := NewRuntime(RuntimeOptions{Repo: repo, Loader: loader})
	type createResult struct {
		harness *AgentHarness
		err     error
	}
	created := make(chan createResult, 1)
	go func() {
		harness, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "creating-session"})
		created <- createResult{harness: harness, err: err}
	}()
	<-entered

	if _, ok := repo.Get("creating-session"); ok {
		t.Fatal("creating session was visible in repository cache")
	}
	if _, err := repo.Load("creating-session"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load() error = %v, want fs.ErrNotExist", err)
	}
	infos, err := repo.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("List() = %#v, want empty during creation", infos)
	}
	if _, err := otherRepo.Load("creating-session"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("other repository Load() error = %v, want fs.ErrNotExist", err)
	}
	otherInfos, err := otherRepo.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(otherInfos) != 0 {
		t.Fatalf("other repository List() = %#v, want empty during creation", otherInfos)
	}
	if _, err := runtime.Resume(t.Context(), "creating-session"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("Resume() error = %v, want ErrSessionBusy", err)
	}
	if _, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "creating-session"}); !errors.Is(err, session.ErrSessionExists) {
		t.Fatalf("concurrent NewSession() error = %v, want ErrSessionExists", err)
	}
	close(release)
	released = true
	result := <-created
	if result.err != nil || result.harness == nil {
		t.Fatalf("first NewSession() harness=%v error=%v", result.harness, result.err)
	}
	if _, err := otherRepo.Load("creating-session"); err != nil {
		t.Fatalf("other repository Load() after publication: %v", err)
	}
}

func TestRuntimeNewSessionFailureLeavesRepositoryAndStorageEmpty(t *testing.T) {
	loadErr := errors.New("resource load failed")
	tests := []struct {
		name    string
		loader  agentresources.Loader
		options NewSessionOptions
	}{
		{
			name: "loader",
			loader: resourceLoaderFunc(func(context.Context, agentresources.Request) (agentresources.Snapshot, error) {
				return agentresources.Snapshot{}, loadErr
			}),
		},
		{
			name: "active_tools",
			options: NewSessionOptions{
				Resources:       &agentresources.Snapshot{},
				ActiveToolNames: []string{"missing"},
			},
		},
		{
			name: "harness_tools",
			options: NewSessionOptions{Resources: &agentresources.Snapshot{Tools: []agentcore.Tool{
				fakeTool{name: "duplicate"},
				fakeTool{name: "duplicate"},
			}}},
		},
		{
			name: "provider_binding",
			options: NewSessionOptions{
				Resources:      &agentresources.Snapshot{},
				RequestOptions: &model.RequestOptions{},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			storage, err := session.NewFileStorage(dir)
			if err != nil {
				t.Fatal(err)
			}
			repo := session.NewRepository(storage)
			loader := test.loader
			if loader == nil {
				loader = agentresources.StaticLoader{}
			}
			runtime := NewRuntime(RuntimeOptions{Repo: repo, Loader: loader})
			options := test.options
			options.ID = "failed-" + test.name
			if _, err := runtime.NewSession(t.Context(), options); err == nil {
				t.Fatal("NewSession() error = nil")
			}
			if _, ok := repo.Get(options.ID); ok {
				t.Fatal("failed session remained in repository")
			}
			ids, err := storage.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(ids) != 0 {
				t.Fatalf("session journal IDs = %v, want empty", ids)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("session directory entries = %#v, want empty", entries)
			}
			runtime.mu.Lock()
			_, active := runtime.active[options.ID]
			_, creating := runtime.creating[options.ID]
			runtime.mu.Unlock()
			if active || creating {
				t.Fatalf("failed session state active=%v creating=%v", active, creating)
			}
		})
	}
}

func TestNewAgentHarnessRegistersSessionAfterSuccessfulConstruction(t *testing.T) {
	repo := session.NewRepository(nil)
	sess := session.New("harness-register")
	harness, err := NewAgentHarness(AgentHarnessOptions{Session: sess, Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	if cached, ok := repo.Get(sess.ID()); !ok || cached != harness.session {
		t.Fatal("successful harness construction did not register its session")
	}

	failedRepo := session.NewRepository(nil)
	failedSession := session.New("harness-failure")
	if _, err := NewAgentHarness(AgentHarnessOptions{
		Session: failedSession,
		Repo:    failedRepo,
		Resources: agentresources.Snapshot{Tools: []agentcore.Tool{
			fakeTool{name: "duplicate"},
			fakeTool{name: "duplicate"},
		}},
	}); err == nil {
		t.Fatal("NewAgentHarness() error = nil")
	}
	if _, ok := failedRepo.Get(failedSession.ID()); ok {
		t.Fatal("failed harness construction registered its session")
	}
}

func TestRuntimeNewSessionPublishesInitialEntriesWithCreate(t *testing.T) {
	storage := &initialCreateStorage{}
	repo := session.NewRepository(storage)
	runtime := NewRuntime(RuntimeOptions{Repo: repo})
	if _, err := runtime.NewSession(t.Context(), NewSessionOptions{
		ID:              "initial-create",
		Model:           "model",
		Provider:        "provider",
		Reasoning:       "high",
		ActiveToolNames: []string{},
		Resources:       &agentresources.Snapshot{},
	}); err != nil {
		t.Fatal(err)
	}
	entries, createCalls, appendCalls := storage.snapshot()
	if createCalls != 1 || appendCalls != 0 {
		t.Fatalf("storage create/append calls = %d/%d, want 1/0", createCalls, appendCalls)
	}
	wantTypes := []session.EntryType{
		session.EntrySessionInfo,
		session.EntryModelChange,
		session.EntryThinkingLevelChange,
		session.EntryActiveToolsChange,
	}
	if len(entries) != len(wantTypes) {
		t.Fatalf("initial entries = %d, want %d", len(entries), len(wantTypes))
	}
	for i, want := range wantTypes {
		if entries[i].Type != want {
			t.Fatalf("entry %d type = %q, want %q", i, entries[i].Type, want)
		}
	}
}

func TestRuntimeDiscardSessionRemovesExactHarnessAndAllowsRecreate(t *testing.T) {
	dir := t.TempDir()
	storage, err := session.NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	runtime := NewRuntime(RuntimeOptions{Repo: repo})
	harness, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "discard-session"})
	if err != nil {
		t.Fatal(err)
	}
	other := &AgentHarness{session: harness.session}
	if err := runtime.DiscardSession(other); err == nil {
		t.Fatal("DiscardSession() accepted a different harness instance")
	}
	if cached, ok := repo.Get("discard-session"); !ok || cached != harness.session {
		t.Fatal("instance mismatch changed repository state")
	}
	if err := runtime.DiscardSession(harness); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.Get("discard-session"); ok {
		t.Fatal("discarded session remained in repository")
	}
	ids, err := storage.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("storage session IDs = %v, want empty", ids)
	}
	if _, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "discard-session"}); err != nil {
		t.Fatalf("recreate discarded session: %v", err)
	}
}

func TestRuntimeDiscardSessionBlocksReuseAndRestoresOnDeleteFailure(t *testing.T) {
	deleteErr := errors.New("delete failed")
	storage := newBlockingDeleteStorage(deleteErr)
	repo := session.NewRepository(storage)
	runtime := NewRuntime(RuntimeOptions{Repo: repo})
	harness, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "discard-failure"})
	if err != nil {
		t.Fatal(err)
	}
	discarded := make(chan error, 1)
	go func() {
		discarded <- runtime.DiscardSession(harness)
	}()
	<-storage.deleteEntered

	if _, err := runtime.Resume(t.Context(), "discard-failure"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("Resume() error = %v, want ErrSessionBusy", err)
	}
	if _, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "discard-failure"}); !errors.Is(err, session.ErrSessionExists) {
		t.Fatalf("NewSession() error = %v, want ErrSessionExists", err)
	}
	close(storage.deleteRelease)
	if err := <-discarded; !errors.Is(err, deleteErr) {
		t.Fatalf("DiscardSession() error = %v, want delete failure", err)
	}
	runtime.mu.Lock()
	active := runtime.active["discard-failure"]
	_, creating := runtime.creating["discard-failure"]
	runtime.mu.Unlock()
	if active != harness || creating {
		t.Fatalf("restored runtime state active=%p creating=%v", active, creating)
	}
	if cached, ok := repo.Get("discard-failure"); !ok || cached != harness.session {
		t.Fatal("delete failure did not restore a usable session")
	}
	resumed, err := runtime.Resume(t.Context(), "discard-failure")
	if err != nil {
		t.Fatal(err)
	}
	if resumed != harness {
		t.Fatal("Resume() returned a different harness after delete failure")
	}
}

func TestNewSessionPersistsInitialConfig(t *testing.T) {
	storage, err := session.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	tool := fakeTool{name: "known", text: "ok"}
	runtime := NewRuntime(RuntimeOptions{
		Repo: repo,
		Loader: agentresources.StaticLoader{Snapshot: agentresources.Snapshot{
			Tools: []agentcore.Tool{tool},
		}},
		Config: agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
	})
	if _, err := runtime.NewSession(context.Background(), NewSessionOptions{
		ID:              "s1",
		Provider:        "provider",
		Model:           "model",
		Reasoning:       "high",
		ActiveToolNames: []string{"known"},
	}); err != nil {
		t.Fatal(err)
	}
	reloadedRepo := session.NewRepository(storage)
	reloaded, err := reloadedRepo.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := reloaded.BuildContext()
	if ctx.Provider != "provider" || ctx.Model != "model" || ctx.Reasoning != "high" {
		t.Fatalf("context state provider=%q model=%q reasoning=%q", ctx.Provider, ctx.Model, ctx.Reasoning)
	}
	if len(ctx.ToolNames) != 1 || ctx.ToolNames[0] != "known" {
		t.Fatalf("tool names = %#v", ctx.ToolNames)
	}
}

func TestSessionSnapshotJSONEmptyArrays(t *testing.T) {
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{}, nil)
	for name, snapshot := range map[string]SessionSnapshot{
		"zero":          {},
		"id only":       {SessionID: "s1"},
		"empty harness": harness.Snapshot(),
	} {
		t.Run(name, func(t *testing.T) {
			before := snapshot
			data, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"messages", "events", "tools", "entries"} {
				var items []json.RawMessage
				if err := json.Unmarshal(fields[field], &items); err != nil || items == nil {
					t.Errorf("%s = %s, want JSON array (err=%v)", field, fields[field], err)
				}
				if name != "empty harness" && string(fields[field]) != "[]" {
					t.Errorf("%s = %s, want []", field, fields[field])
				}
			}
			if !reflect.DeepEqual(snapshot, before) {
				t.Fatal("marshaling changed the source snapshot")
			}
		})
	}
}

func TestSessionSnapshotJSONPreservesEncodingErrors(t *testing.T) {
	for _, snapshot := range []SessionSnapshot{
		{Messages: protocol.MessageList{nil}},
		{Tools: []protocol.ToolDefinition{{Name: "invalid", Parameters: json.RawMessage("{")}}},
	} {
		if _, err := json.Marshal(snapshot); err == nil {
			t.Fatal("invalid nested data was silently encoded")
		}
	}
}

func TestSessionSnapshotAndListenExposeRunState(t *testing.T) {
	as := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))}, []agentcore.Tool{
		fakeTool{name: "known", text: "ok"},
	})
	var observed []protocol.AgentEvent
	unsubscribe := as.Listen(func(_ context.Context, event protocol.AgentEvent, _ agentcore.AgentState) error {
		observed = append(observed, event)
		return nil
	})
	if _, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("hello")}); err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	if len(observed) == 0 || observed[0].Type != protocol.AgentEventAgentStart {
		t.Fatalf("observed events = %#v", observed)
	}
	snapshot := as.Snapshot()
	if snapshot.SessionID != "s1" {
		t.Fatalf("snapshot session id = %q", snapshot.SessionID)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("snapshot messages = %d, want 2", len(snapshot.Messages))
	}
	if len(snapshot.Events) != len(observed) {
		t.Fatalf("snapshot events = %d, observed %d", len(snapshot.Events), len(observed))
	}
	if len(snapshot.Tools) != 1 || snapshot.Tools[0].Name != "known" {
		t.Fatalf("snapshot tools = %#v", snapshot.Tools)
	}
	if len(snapshot.Entries) < 2 {
		t.Fatalf("snapshot entries = %d, want at least 2", len(snapshot.Entries))
	}
	snapshot.EditorText = "keep editor text"
	type plainSnapshot SessionSnapshot
	wantJSON, err := json.Marshal(plainSnapshot(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatal("snapshot JSON changed populated fields")
	}
	snapshot.Events[0].Type = protocol.AgentEventAgentEnd
	if next := as.Snapshot(); next.Events[0].Type == protocol.AgentEventAgentEnd {
		t.Fatal("snapshot events alias session events")
	}
}

func TestAgentHarnessListenerSurvivesCoreRebuild(t *testing.T) {
	as := newHarnessSession(t, agentcore.AgentLoopConfig{
		Stream: streamSequence(textStream("first"), textStream("second")),
	}, nil)
	starts := 0
	unsubscribe := as.Listen(func(_ context.Context, event protocol.AgentEvent, _ agentcore.AgentState) error {
		if event.Type == protocol.AgentEventAgentStart {
			starts++
		}
		return nil
	})
	defer unsubscribe()

	if _, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("one")}); err != nil {
		t.Fatal(err)
	}
	if _, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("two")}); err != nil {
		t.Fatal(err)
	}
	if starts != 2 {
		t.Fatalf("agent_start events = %d, want 2", starts)
	}
}

func TestAgentHarnessClosesLiveQueuesBeforeAgentEndListeners(t *testing.T) {
	as := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))}, nil)
	var steerErr error
	var followUpErr error
	as.Listen(func(_ context.Context, event protocol.AgentEvent, _ agentcore.AgentState) error {
		if event.Type == protocol.AgentEventAgentEnd {
			steerErr = as.Steer(protocol.MessageList{userMessage("late steering")})
			followUpErr = as.FollowUp(protocol.MessageList{userMessage("late follow-up")})
		}
		return nil
	})

	if _, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("start")}); err != nil {
		t.Fatal(err)
	}
	if steerErr == nil || followUpErr == nil {
		t.Fatalf("agent_end queue errors = %v/%v, want both rejected", steerErr, followUpErr)
	}
}

func TestAgentHarnessPromptValidationPreservesNextTurn(t *testing.T) {
	as := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))}, nil)
	if err := as.NextTurn(protocol.MessageList{userMessage("queued")}); err != nil {
		t.Fatal(err)
	}
	if _, err := as.Prompt(context.Background(), protocol.MessageList{nil}); err == nil {
		t.Fatal("Prompt() error = nil, want invalid message error")
	}

	messages, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("explicit")})
	if err != nil {
		t.Fatal(err)
	}
	assertMessageTexts(t, messages, []string{"queued", "explicit", "done"})
}

func TestAgentHarnessTurnFlushesPendingBeforeSavePoint(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var harness *AgentHarness
	runtime := NewRuntime(RuntimeOptions{
		Config: agentcore.AgentLoopConfig{Stream: func(context.Context, agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			close(started)
			<-release
			return textStream("done"), nil
		}},
		Model: "model",
	})
	var err error
	harness, err = runtime.NewSession(context.Background(), NewSessionOptions{ID: "s1"})
	if err != nil {
		t.Fatal(err)
	}

	var lifecycle []string
	var savePoint SavePointEvent
	queued := false
	harness.ListenRunEvents(func(_ context.Context, event RunEvent) error {
		name := event.EventName()
		if name == string(protocol.AgentEventMessageEnd) {
			coreEvent, ok := event.(CoreAgentEvent)
			if !ok {
				return fmt.Errorf("message_end event = %T", event)
			}
			agentEvent := protocol.AgentEvent(coreEvent)
			if !queued && agentEvent.Message != nil && agentEvent.Message.MessageRole() == protocol.RoleAssistant {
				queued = true
				if err := harness.AppendMessage(userMessage("external")); err != nil {
					return err
				}
			}
		}
		switch name {
		case string(protocol.AgentEventTurnEnd):
			lifecycle = append(lifecycle, name)
			if got := len(harness.session.BuildContext().Messages); got != 2 {
				return fmt.Errorf("messages before save point = %d, want 2", got)
			}
		case "save_point":
			lifecycle = append(lifecycle, name)
			var ok bool
			savePoint, ok = event.(SavePointEvent)
			if !ok {
				return fmt.Errorf("save_point event = %T", event)
			}
			if got := len(harness.session.BuildContext().Messages); got != 3 {
				return fmt.Errorf("messages at save point = %d, want 3", got)
			}
		case string(protocol.AgentEventAgentEnd), "settled":
			lifecycle = append(lifecycle, name)
		}
		return nil
	})

	promptDone := make(chan error, 1)
	go func() {
		_, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
		promptDone <- err
	}()
	<-started
	if got := harness.Phase(); got != AgentHarnessPhaseTurn {
		t.Fatalf("phase = %q, want %q", got, AgentHarnessPhaseTurn)
	}
	close(release)
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
	if !savePoint.HadPendingMutations {
		t.Fatal("save point did not report pending mutations")
	}
	wantLifecycle := []string{"turn_end", "save_point", "agent_end", "settled"}
	if !reflect.DeepEqual(lifecycle, wantLifecycle) {
		t.Fatalf("lifecycle = %#v, want %#v", lifecycle, wantLifecycle)
	}
	if got := harness.Phase(); got != AgentHarnessPhaseIdle {
		t.Fatalf("phase = %q, want %q", got, AgentHarnessPhaseIdle)
	}
}

func TestAgentHarnessWaitForIdleIncludesSettledListener(t *testing.T) {
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{
		Stream: streamSequence(textStream("first"), textStream("second")),
	}, nil)
	settledEntered := make(chan struct{})
	releaseSettled := make(chan struct{})
	settledCount := 0
	harness.ListenRunEvents(func(_ context.Context, event RunEvent) error {
		if event.EventName() != "settled" {
			return nil
		}
		settledCount++
		if settledCount == 1 {
			close(settledEntered)
			<-releaseSettled
		}
		return nil
	})

	promptDone := make(chan error, 1)
	go func() {
		_, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
		promptDone <- err
	}()
	<-settledEntered
	if got := harness.Phase(); got != AgentHarnessPhaseIdle {
		t.Fatalf("phase during settlement = %q, want %q", got, AgentHarnessPhaseIdle)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := harness.WaitForIdle(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForIdle() error = %v, want deadline exceeded", err)
	}
	if _, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("too early")}); !errors.Is(err, agentcore.ErrAgentBusy) {
		t.Fatalf("Prompt() during settlement error = %v, want busy", err)
	}
	close(releaseSettled)
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
	if err := harness.WaitForIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("next")}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentHarnessSettledListenerReceivesLiveContext(t *testing.T) {
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))}, nil)
	harness.ListenRunEvents(func(ctx context.Context, event RunEvent) error {
		if event.EventName() == "settled" {
			return ctx.Err()
		}
		return nil
	})

	if _, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")}); err != nil {
		t.Fatalf("Prompt() error = %v, want live settled context", err)
	}
}

func TestAgentHarnessSettledListenerObservesAbort(t *testing.T) {
	started := make(chan struct{})
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{
		Stream: func(ctx context.Context, _ agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}, nil)
	settledCanceled := make(chan bool, 1)
	harness.ListenRunEvents(func(ctx context.Context, event RunEvent) error {
		if event.EventName() == "settled" {
			settledCanceled <- errors.Is(ctx.Err(), context.Canceled)
		}
		return nil
	})

	promptDone := make(chan error, 1)
	go func() {
		_, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
		promptDone <- err
	}()
	<-started
	if _, err := harness.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if canceled := <-settledCanceled; !canceled {
		t.Fatal("settled listener received a live context after Abort")
	}
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
}

func TestAgentHarnessSettledPanicStillClosesBarrier(t *testing.T) {
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{
		Stream: streamSequence(textStream("first"), textStream("second")),
	}, nil)
	unsubscribe := harness.ListenRunEvents(func(context.Context, RunEvent) error {
		panic("listener panic")
	})

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Prompt() did not propagate listener panic")
			}
		}()
		_, _ = harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
	}()
	unsubscribe()
	if err := harness.WaitForIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("next")}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentHarnessAbortDeadlineSurvivesBlockedStorage(t *testing.T) {
	storage := &blockingSessionStorage{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	harness, err := NewAgentHarness(AgentHarnessOptions{
		Session: session.New("s1"),
		Repo:    session.NewRepository(storage),
		Config:  agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
		Model:   "model",
	})
	if err != nil {
		t.Fatal(err)
	}

	promptDone := make(chan error, 1)
	go func() {
		_, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
		promptDone <- err
	}()
	<-storage.entered

	abortCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	abortDone := make(chan error, 1)
	go func() {
		_, err := harness.Abort(abortCtx)
		abortDone <- err
	}()
	select {
	case err := <-abortDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Abort() error = %v, want deadline exceeded", err)
		}
	case <-time.After(200 * time.Millisecond):
		close(storage.release)
		t.Fatal("Abort() remained blocked behind session storage")
	}

	close(storage.release)
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
}

func TestAgentHarnessAbortSeesPreflightBarrier(t *testing.T) {
	storage := &blockingSessionStorage{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	harness, err := NewAgentHarness(AgentHarnessOptions{
		Session: session.New("s1"),
		Repo:    session.NewRepository(storage),
		Config:  agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
		Model:   "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.mu.Lock()
	harness.phase = AgentHarnessPhaseTurn
	harness.mu.Unlock()
	if err := harness.SetModel("provider", "queued"); err != nil {
		t.Fatal(err)
	}
	harness.mu.Lock()
	harness.phase = AgentHarnessPhaseIdle
	harness.mu.Unlock()

	promptDone := make(chan error, 1)
	go func() {
		_, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
		promptDone <- err
	}()
	<-storage.entered

	abortCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := harness.Abort(abortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Abort() error = %v, want preflight barrier deadline", err)
	}
	close(storage.release)
	if err := <-promptDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Prompt() error = %v, want canceled preflight", err)
	}
}

func TestAgentHarnessPendingMutationsRemainFIFOAfterStorageFailure(t *testing.T) {
	storage := &modelRecordingStorage{failModel: "first"}
	harness, err := NewAgentHarness(AgentHarnessOptions{
		Session: session.New("s1"),
		Repo:    session.NewRepository(storage),
		Config:  agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
		Model:   "base",
	})
	if err != nil {
		t.Fatal(err)
	}

	harness.mu.Lock()
	harness.phase = AgentHarnessPhaseTurn
	harness.mu.Unlock()
	if err := harness.SetModel("provider", "first"); err != nil {
		t.Fatal(err)
	}
	harness.mu.Lock()
	harness.phase = AgentHarnessPhaseIdle
	harness.mu.Unlock()

	if err := harness.SetModel("provider", "second"); err == nil {
		t.Fatal("SetModel() error = nil while the queue head is failing")
	}
	if got := storage.models(); len(got) != 0 {
		t.Fatalf("persisted models = %#v, want none", got)
	}
	storage.allow("first")
	if err := harness.SetModel("provider", "third"); err != nil {
		t.Fatal(err)
	}
	if got, want := storage.models(), []string{"first", "second", "third"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted models = %#v, want %#v", got, want)
	}
}

func TestAgentHarnessCarriesLateFollowUpIntoNextTurn(t *testing.T) {
	polled := make(chan struct{})
	releasePoll := make(chan struct{})
	var pollOnce sync.Once
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{
		Stream: streamSequence(textStream("first"), textStream("second")),
		GetFollowUpMessages: func(context.Context) (protocol.MessageList, error) {
			pollOnce.Do(func() {
				close(polled)
				<-releasePoll
			})
			return nil, nil
		},
	}, nil)

	firstDone := make(chan error, 1)
	go func() {
		_, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")})
		firstDone <- err
	}()
	<-polled
	if err := harness.FollowUp(protocol.MessageList{userMessage("late")}); err != nil {
		t.Fatal(err)
	}
	close(releasePoll)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}

	messages, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("explicit")})
	if err != nil {
		t.Fatal(err)
	}
	assertMessageTexts(t, messages, []string{"late", "explicit", "second"})
}

func TestAgentHarnessSettersSupportExplicitDisabledState(t *testing.T) {
	var toolCount int
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{
		Stream: func(_ context.Context, request agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			toolCount = len(request.Context.Tools)
			return textStream("done"), nil
		},
	}, []agentcore.Tool{fakeTool{name: "known", text: "ok"}})

	if err := harness.SetModel("", "next"); err == nil {
		t.Fatal("SetModel() error = nil for empty provider")
	}
	if err := harness.SetReasoning(""); err != nil {
		t.Fatal(err)
	}
	if err := harness.SetActiveTools([]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")}); err != nil {
		t.Fatal(err)
	}
	ctx := harness.session.BuildContext()
	if ctx.Reasoning != "off" {
		t.Fatalf("reasoning = %q, want off", ctx.Reasoning)
	}
	if !ctx.ToolNamesSet || len(ctx.ToolNames) != 0 {
		t.Fatalf("tool state = set:%v names:%#v, want explicit empty", ctx.ToolNamesSet, ctx.ToolNames)
	}
	if toolCount != 0 {
		t.Fatalf("provider tool count = %d, want 0", toolCount)
	}
}

func TestAgentHarnessTurnEndListenerErrorFlushesPendingAndReleasesBarrier(t *testing.T) {
	listenerErr := errors.New("turn listener failed")
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{
		Stream: streamSequence(textStream("first"), textStream("second")),
	}, nil)
	queued := false
	failed := false
	savePointSeen := false
	settledSeen := false
	harness.ListenRunEvents(func(_ context.Context, event RunEvent) error {
		name := event.EventName()
		if name == string(protocol.AgentEventMessageEnd) {
			coreEvent, ok := event.(CoreAgentEvent)
			if !ok {
				return fmt.Errorf("message_end event = %T", event)
			}
			agentEvent := protocol.AgentEvent(coreEvent)
			if !queued && agentEvent.Message != nil && agentEvent.Message.MessageRole() == protocol.RoleAssistant {
				queued = true
				if err := harness.AppendMessage(userMessage("external")); err != nil {
					return err
				}
			}
		}
		if name == string(protocol.AgentEventTurnEnd) && !failed {
			failed = true
			return listenerErr
		}
		if name == "save_point" && failed {
			savePointSeen = true
		}
		if name == "settled" {
			settledSeen = true
		}
		return nil
	})

	if _, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("start")}); !errors.Is(err, listenerErr) {
		t.Fatalf("Prompt() error = %v, want listener error", err)
	}
	assertMessageTexts(t, harness.session.BuildContext().Messages, []string{"start", "first", "external"})
	if savePointSeen {
		t.Fatal("save point emitted after turn listener failure")
	}
	if !settledSeen {
		t.Fatal("settled event was not emitted")
	}
	if got := harness.Phase(); got != AgentHarnessPhaseIdle {
		t.Fatalf("phase = %q, want %q", got, AgentHarnessPhaseIdle)
	}
	if err := harness.WaitForIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.Prompt(context.Background(), protocol.MessageList{userMessage("next")}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentHarnessPromptFromTemplateUsesFirstExactMatch(t *testing.T) {
	var providerMessages protocol.MessageList
	runtime := NewRuntime(RuntimeOptions{
		Config: agentcore.AgentLoopConfig{Stream: func(_ context.Context, request agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			providerMessages = protocol.CloneMessageList(request.Context.Messages)
			return textStream("done"), nil
		}},
		Model: "model",
		PromptTemplates: []PromptTemplate{
			{Name: "review", Content: "first $1"},
			{Name: "review", Content: "second $1"},
		},
	})
	harness, err := runtime.NewSession(context.Background(), NewSessionOptions{ID: "s1"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := harness.PromptFromTemplate(context.Background(), "Review", []string{"target"}); err == nil {
		t.Fatal("PromptFromTemplate() error = nil for case-mismatched name")
	}
	if _, err := harness.PromptFromTemplate(context.Background(), "review", []string{"target"}); err != nil {
		t.Fatal(err)
	}
	assertMessageTexts(t, providerMessages, []string{"first target"})
}

func TestAgentHarnessPromptTextRoutesCommandsFromHarnessResources(t *testing.T) {
	requests := make([]agentcore.StreamRequest, 0, 2)
	runtime := NewRuntime(RuntimeOptions{
		Config: agentcore.AgentLoopConfig{Stream: func(_ context.Context, request agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			requests = append(requests, request)
			return textStream("done"), nil
		}},
		Model: "model",
		PromptTemplates: []PromptTemplate{
			{Name: "review", Content: "review $1"},
			{Name: "diagnose", Content: "template $1"},
		},
	})
	resources := agentresources.Snapshot{Skills: []agentskills.Skill{
		{Name: "diagnose", Content: "skill body", FilePath: "/skills/diagnose/SKILL.md", Enabled: true},
		{Name: "disabled", Content: "disabled body", FilePath: "/skills/disabled/SKILL.md", Enabled: false},
	}}
	harness, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "prompt-routing", Resources: &resources})
	if err != nil {
		t.Fatal(err)
	}

	if err := harness.PromptText(t.Context(), "/review target", 1); err != nil {
		t.Fatal(err)
	}
	if err := harness.PromptText(t.Context(), "/skill:diagnose inspect target", 2); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("stream requests = %d, want 2", len(requests))
	}
	assertMessageTexts(t, requests[0].Context.Messages, []string{"review target"})
	assertMessageTexts(t, requests[1].Context.Messages, []string{
		"review target",
		"done",
		"<skill name=\"diagnose\" location=\"/skills/diagnose/SKILL.md\">\nReferences are relative to /skills/diagnose.\n\nskill body\n</skill>\n\ninspect target",
	})
	if _, err := harness.Skill(t.Context(), "disabled", ""); err == nil {
		t.Fatal("Skill() accepted a disabled skill")
	}
}

func TestRuntimeResumeRefreshesActiveHarnessState(t *testing.T) {
	oldTool := fakeTool{name: "old", text: "old"}
	newTool := fakeTool{name: "new", text: "new"}
	oldSkill := agentskills.Skill{Name: "old-skill", Content: "old instructions", Enabled: true}
	newSkill := agentskills.Skill{Name: "new-skill", Content: "new instructions", Enabled: true}
	oldCalls := 0
	newRequests := make([]protocol.StreamRequest, 0, 2)
	oldConfig := agentcore.AgentLoopConfig{
		MaxTurns: 2,
		Stream: func(context.Context, agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			oldCalls++
			return textStream("old response"), nil
		},
	}
	newConfig := agentcore.AgentLoopConfig{
		MaxTurns: 7,
		Stream: func(_ context.Context, request agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			newRequests = append(newRequests, request)
			return textStream("new response"), nil
		},
	}
	runtime := NewRuntime(RuntimeOptions{Model: "model"})
	oldResources := agentresources.Snapshot{
		SystemPrompt: "old system",
		Tools:        []agentcore.Tool{oldTool},
		ToolNames:    []string{"old"},
		Skills:       []agentskills.Skill{oldSkill},
	}
	harness, err := runtime.NewSession(t.Context(), NewSessionOptions{
		ID:              "refresh-state",
		Resources:       &oldResources,
		Config:          &oldConfig,
		PromptTemplates: []PromptTemplate{{Name: "old-template", Content: "old $1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	newResources := agentresources.Snapshot{
		SystemPrompt: "new system",
		Tools:        []agentcore.Tool{newTool},
		ToolNames:    []string{"new"},
		Skills:       []agentskills.Skill{newSkill},
	}
	resumed, err := runtime.ResumeWithOptions(t.Context(), harness.session.ID(), ResumeSessionOptions{
		Resources:       &newResources,
		Config:          &newConfig,
		PromptTemplates: []PromptTemplate{{Name: "new-template", Content: "new $1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed != harness {
		t.Fatal("ResumeWithOptions() returned a different active harness")
	}
	harness.mu.Lock()
	maxTurns := harness.config.MaxTurns
	harness.mu.Unlock()
	if maxTurns != 7 {
		t.Fatalf("MaxTurns = %d, want 7", maxTurns)
	}
	if _, err := harness.PromptFromTemplate(t.Context(), "old-template", []string{"target"}); err == nil {
		t.Fatal("old prompt template remained available")
	}
	if _, err := harness.PromptFromTemplate(t.Context(), "new-template", []string{"target"}); err != nil {
		t.Fatal(err)
	}
	if len(newRequests) != 1 {
		t.Fatalf("new stream requests = %d, want 1", len(newRequests))
	}
	request := newRequests[0]
	if request.Context.SystemPrompt != "new system" {
		t.Fatalf("system prompt = %q, want new system", request.Context.SystemPrompt)
	}
	if len(request.Context.Tools) != 1 || request.Context.Tools[0].Name != "new" {
		t.Fatalf("tools = %#v, want new", request.Context.Tools)
	}
	assertMessageTexts(t, request.Context.Messages, []string{"new target"})

	if _, err := harness.Skill(t.Context(), "New-Skill", ""); err == nil {
		t.Fatal("Skill() accepted a case-mismatched name")
	}
	if _, err := harness.Skill(t.Context(), "new-skill", "apply carefully"); err != nil {
		t.Fatal(err)
	}
	if len(newRequests) != 2 {
		t.Fatalf("new stream requests = %d, want 2", len(newRequests))
	}
	wantInvocation := agentskills.FormatInvocation(&newSkill, "apply carefully")
	skillRequest := newRequests[1]
	assertMessageTexts(t, skillRequest.Context.Messages[len(skillRequest.Context.Messages)-1:], []string{wantInvocation})
	persisted := harness.session.BuildContext().Messages
	assertMessageTexts(t, persisted[len(persisted)-2:], []string{wantInvocation, "new response"})
	if oldCalls != 0 {
		t.Fatalf("old stream calls = %d, want 0", oldCalls)
	}
}

func TestRuntimeResumeRefreshFailureRestoresActiveHarness(t *testing.T) {
	tests := []struct {
		name      string
		resources agentresources.Snapshot
	}{
		{
			name: "duplicate tools",
			resources: agentresources.Snapshot{Tools: []agentcore.Tool{
				fakeTool{name: "duplicate", text: "one"},
				fakeTool{name: "duplicate", text: "two"},
			}},
		},
		{
			name: "unknown tool name",
			resources: agentresources.Snapshot{
				Tools:     []agentcore.Tool{fakeTool{name: "new", text: "new"}},
				ToolNames: []string{"missing"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldCalls := 0
			newCalls := 0
			oldConfig := agentcore.AgentLoopConfig{
				MaxTurns: 3,
				Stream: func(_ context.Context, request agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
					oldCalls++
					if request.Context.SystemPrompt != "old system" {
						t.Errorf("system prompt = %q, want old system", request.Context.SystemPrompt)
					}
					return textStream("old response"), nil
				},
			}
			newConfig := agentcore.AgentLoopConfig{
				MaxTurns: 9,
				Stream: func(context.Context, agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
					newCalls++
					return textStream("new response"), nil
				},
			}
			oldSkill := agentskills.Skill{Name: "old-skill", Content: "old instructions", Enabled: true}
			oldResources := agentresources.Snapshot{
				SystemPrompt: "old system",
				Tools:        []agentcore.Tool{fakeTool{name: "old", text: "old"}},
				ToolNames:    []string{"old"},
				Skills:       []agentskills.Skill{oldSkill},
			}
			runtime := NewRuntime(RuntimeOptions{Model: "model"})
			harness, err := runtime.NewSession(t.Context(), NewSessionOptions{
				ID:              "refresh-rollback-" + test.name,
				Resources:       &oldResources,
				Config:          &oldConfig,
				PromptTemplates: []PromptTemplate{{Name: "old-template", Content: "old"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.ResumeWithOptions(t.Context(), harness.session.ID(), ResumeSessionOptions{
				Resources:       &test.resources,
				Config:          &newConfig,
				PromptTemplates: []PromptTemplate{{Name: "new-template", Content: "new"}},
			}); err == nil {
				t.Fatal("ResumeWithOptions() error = nil for invalid refresh")
			}

			harness.mu.Lock()
			stateOK := harness.resources.SystemPrompt == "old system" &&
				harness.config.MaxTurns == 3 &&
				len(harness.resources.ToolNames) == 1 && harness.resources.ToolNames[0] == "old" &&
				len(harness.promptTemplates) == 1 && harness.promptTemplates[0].Name == "old-template" &&
				len(harness.resources.Skills) == 1 && harness.resources.Skills[0].Name == "old-skill"
			harness.mu.Unlock()
			if !stateOK {
				t.Fatal("failed refresh changed active harness state")
			}
			if _, err := harness.PromptFromTemplate(t.Context(), "old-template", nil); err != nil {
				t.Fatal(err)
			}
			if _, err := harness.Skill(t.Context(), "old-skill", ""); err != nil {
				t.Fatal(err)
			}
			if oldCalls != 2 || newCalls != 0 {
				t.Fatalf("stream calls old/new = %d/%d, want 2/0", oldCalls, newCalls)
			}
		})
	}
}

func TestRuntimeResumeRejectsBusyRefreshAndKeepsCurrentResources(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	oldCalls := 0
	newCalls := 0
	oldRequests := make([]protocol.StreamRequest, 0, 2)
	oldConfig := agentcore.AgentLoopConfig{Stream: func(_ context.Context, request agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		oldCalls++
		oldRequests = append(oldRequests, request)
		if oldCalls == 1 {
			close(started)
			<-release
		}
		return textStream("old response"), nil
	}}
	newConfig := agentcore.AgentLoopConfig{Stream: func(context.Context, agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		newCalls++
		return textStream("new response"), nil
	}}
	oldResources := agentresources.Snapshot{
		SystemPrompt: "old system",
		Skills:       []agentskills.Skill{{Name: "old-skill", Content: "old instructions", Enabled: true}},
	}
	runtime := NewRuntime(RuntimeOptions{Model: "model"})
	harness, err := runtime.NewSession(t.Context(), NewSessionOptions{
		ID:        "busy-refresh",
		Resources: &oldResources,
		Config:    &oldConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	promptDone := make(chan error, 1)
	go func() {
		_, err := harness.Prompt(t.Context(), protocol.MessageList{userMessage("start")})
		promptDone <- err
	}()
	<-started
	newResources := agentresources.Snapshot{SystemPrompt: "new system"}
	if _, err := runtime.ResumeWithOptions(t.Context(), harness.session.ID(), ResumeSessionOptions{
		Resources: &newResources,
		Config:    &newConfig,
	}); !errors.Is(err, agentcore.ErrAgentBusy) {
		t.Fatalf("ResumeWithOptions() error = %v, want busy", err)
	}
	if _, err := harness.Skill(t.Context(), "old-skill", ""); !errors.Is(err, agentcore.ErrAgentBusy) {
		t.Fatalf("Skill() error = %v, want busy", err)
	}
	close(release)
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
	if _, err := harness.Prompt(t.Context(), protocol.MessageList{userMessage("next")}); err != nil {
		t.Fatal(err)
	}
	if oldCalls != 2 || newCalls != 0 {
		t.Fatalf("stream calls old/new = %d/%d, want 2/0", oldCalls, newCalls)
	}
	for i, request := range oldRequests {
		if request.Context.SystemPrompt != "old system" {
			t.Fatalf("request %d system prompt = %q, want old system", i, request.Context.SystemPrompt)
		}
	}
}

func TestAgentHarnessBeforeAgentStartUsesTurnSnapshotAndAppliesSettersNextTurn(t *testing.T) {
	firstTool := fakeTool{name: "first", text: "first result"}
	secondTool := fakeTool{name: "second", text: "second result"}
	var requests []agentcore.StreamRequest
	stream := func(_ context.Context, request agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		requests = append(requests, request)
		if len(requests) == 1 {
			return toolCallStream(protocol.NewToolCallContent("call-1", "first", json.RawMessage(`{}`))), nil
		}
		return textStream("done"), nil
	}
	override := "hook system"
	var hookInput BeforeAgentStartContext
	var harness *AgentHarness
	var err error
	harness, err = NewAgentHarness(AgentHarnessOptions{
		Session: session.New("before-start"),
		Repo:    session.NewRepository(nil),
		Resources: agentresources.Snapshot{
			SystemPrompt: "base system",
			Tools:        []agentcore.Tool{firstTool},
			Skills: []agentskills.Skill{{
				Name: "inspect", Description: "inspect", Content: "skill body", Enabled: true,
			}},
		},
		Config:          agentcore.AgentLoopConfig{Stream: stream},
		Provider:        "provider",
		Model:           "old",
		ActiveToolNames: []string{"first"},
		PromptTemplates: []PromptTemplate{{Name: "review", Content: "review body"}},
		BeforeAgentStart: func(ctx context.Context, input BeforeAgentStartContext) (BeforeAgentStartResult, error) {
			hookInput = input
			if err := harness.SetModel("provider", "new"); err != nil {
				return BeforeAgentStartResult{}, err
			}
			if err := harness.SetTools([]agentcore.Tool{firstTool, secondTool}, []string{"second"}); err != nil {
				return BeforeAgentStartResult{}, err
			}
			return BeforeAgentStartResult{
				Messages:     protocol.MessageList{userMessage("hook")},
				SystemPrompt: &override,
			}, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.NextTurn(protocol.MessageList{userMessage("queued")}); err != nil {
		t.Fatal(err)
	}

	if _, err := harness.Prompt(t.Context(), protocol.MessageList{userMessage("explicit")}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("stream requests = %d, want 2", len(requests))
	}
	assertMessageTexts(t, requests[0].Context.Messages, []string{"queued", "explicit", "hook"})
	assertMessageTexts(t, hookInput.Messages, []string{"explicit"})
	if hookInput.SystemPrompt != "base system" {
		t.Fatalf("hook system prompt = %q, want base system", hookInput.SystemPrompt)
	}
	if len(hookInput.Resources.Skills) != 1 || hookInput.Resources.Skills[0].Name != "inspect" {
		t.Fatalf("hook skills = %#v", hookInput.Resources.Skills)
	}
	if len(hookInput.Resources.PromptTemplates) != 1 || hookInput.Resources.PromptTemplates[0].Name != "review" {
		t.Fatalf("hook prompt templates = %#v", hookInput.Resources.PromptTemplates)
	}
	if requests[0].Context.SystemPrompt != override || requests[1].Context.SystemPrompt != "base system" {
		t.Fatalf("system prompts = %q, %q", requests[0].Context.SystemPrompt, requests[1].Context.SystemPrompt)
	}
	if requests[0].Model != "old" || requests[1].Model != "new" {
		t.Fatalf("models = %q, %q", requests[0].Model, requests[1].Model)
	}
	if got := toolDefinitionNames(requests[0].Context.Tools); !reflect.DeepEqual(got, []string{"first"}) {
		t.Fatalf("first request tools = %#v", got)
	}
	if got := toolDefinitionNames(requests[1].Context.Tools); !reflect.DeepEqual(got, []string{"second"}) {
		t.Fatalf("second request tools = %#v", got)
	}
	sessionContext := harness.session.BuildContext()
	if sessionContext.Model != "new" || !reflect.DeepEqual(sessionContext.ToolNames, []string{"second"}) {
		t.Fatalf("session state model=%q tools=%#v", sessionContext.Model, sessionContext.ToolNames)
	}
}

func TestAgentHarnessBeforeAgentStartErrorRestoresQueueAndRunBarrier(t *testing.T) {
	hookErr := errors.New("before start failed")
	hookCalls := 0
	streamCalls := 0
	var harness *AgentHarness
	var err error
	harness, err = NewAgentHarness(AgentHarnessOptions{
		Session: session.New("before-start-error"),
		Repo:    session.NewRepository(nil),
		Config: agentcore.AgentLoopConfig{Stream: func(context.Context, agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			streamCalls++
			return textStream("done"), nil
		}},
		Model: "model",
		BeforeAgentStart: func(context.Context, BeforeAgentStartContext) (BeforeAgentStartResult, error) {
			hookCalls++
			if hookCalls == 1 {
				if err := harness.NextTurn(protocol.MessageList{userMessage("late")}); err != nil {
					return BeforeAgentStartResult{}, err
				}
				return BeforeAgentStartResult{}, hookErr
			}
			return BeforeAgentStartResult{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	settled := 0
	harness.ListenRunEvents(func(_ context.Context, event RunEvent) error {
		if event.EventName() == "settled" {
			settled++
		}
		return nil
	})
	if err := harness.NextTurn(protocol.MessageList{userMessage("queued")}); err != nil {
		t.Fatal(err)
	}

	if _, err := harness.Prompt(t.Context(), protocol.MessageList{userMessage("failed")}); !errors.Is(err, hookErr) {
		t.Fatalf("Prompt() error = %v, want hook error", err)
	}
	if harness.Phase() != AgentHarnessPhaseIdle {
		t.Fatalf("phase = %q, want idle", harness.Phase())
	}
	if err := harness.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	messages, err := harness.Prompt(t.Context(), protocol.MessageList{userMessage("explicit")})
	if err != nil {
		t.Fatal(err)
	}
	assertMessageTexts(t, messages, []string{"queued", "late", "explicit", "done"})
	if streamCalls != 1 || settled != 2 {
		t.Fatalf("stream calls=%d settled=%d, want 1/2", streamCalls, settled)
	}
}

func TestAgentHarnessSetToolsPreservesSelectionAndClonesEvents(t *testing.T) {
	inspect := fakeTool{name: "inspect", text: "inspect"}
	search := fakeTool{name: "search", text: "search"}
	extra := fakeTool{name: "extra", text: "extra"}
	providerToolCounts := []int{}
	harness, err := NewAgentHarness(AgentHarnessOptions{
		Session: session.New("set-tools"),
		Repo:    session.NewRepository(nil),
		Resources: agentresources.Snapshot{
			Tools: []agentcore.Tool{inspect, search},
		},
		Config: agentcore.AgentLoopConfig{Stream: func(_ context.Context, request agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			providerToolCounts = append(providerToolCounts, len(request.Context.Tools))
			return textStream("done"), nil
		}},
		Model:           "model",
		ActiveToolNames: []string{"inspect"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var updates []ToolsUpdateEvent
	harness.ListenRunEvents(func(_ context.Context, event RunEvent) error {
		if update, ok := event.(ToolsUpdateEvent); ok {
			if len(update.ToolNames) > 0 {
				update.ToolNames[0] = "mutated"
			}
			if len(update.ActiveToolNames) > 0 {
				update.ActiveToolNames[0] = "mutated"
			}
		}
		return nil
	})
	harness.ListenRunEvents(func(_ context.Context, event RunEvent) error {
		if update, ok := event.(ToolsUpdateEvent); ok {
			updates = append(updates, update)
		}
		return nil
	})

	if err := harness.SetTools([]agentcore.Tool{inspect, extra}, nil); err != nil {
		t.Fatal(err)
	}
	if err := harness.SetTools([]agentcore.Tool{extra}, nil); err == nil {
		t.Fatal("SetTools() preserved a missing active tool")
	}
	if err := harness.SetTools([]agentcore.Tool{inspect, inspect}, []string{"inspect"}); err == nil {
		t.Fatal("SetTools() accepted duplicate tool names")
	}
	if err := harness.SetTools([]agentcore.Tool{inspect, extra}, []string{}); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 {
		t.Fatalf("tools updates = %d, want 2", len(updates))
	}
	if !reflect.DeepEqual(updates[0].ToolNames, []string{"inspect", "extra"}) ||
		!reflect.DeepEqual(updates[0].PreviousToolNames, []string{"inspect", "search"}) ||
		!reflect.DeepEqual(updates[0].ActiveToolNames, []string{"inspect"}) ||
		!reflect.DeepEqual(updates[0].PreviousActiveToolNames, []string{"inspect"}) {
		t.Fatalf("first tools update = %#v", updates[0])
	}
	if len(updates[1].ActiveToolNames) != 0 || !reflect.DeepEqual(updates[1].PreviousActiveToolNames, []string{"inspect"}) {
		t.Fatalf("second tools update = %#v", updates[1])
	}
	if _, err := harness.Prompt(t.Context(), protocol.MessageList{userMessage("run")}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(providerToolCounts, []int{0}) {
		t.Fatalf("provider tool counts = %#v, want [0]", providerToolCounts)
	}
	sessionContext := harness.session.BuildContext()
	if !sessionContext.ToolNamesSet || len(sessionContext.ToolNames) != 0 {
		t.Fatalf("session tools set=%v names=%#v", sessionContext.ToolNamesSet, sessionContext.ToolNames)
	}
}

func newHarnessSession(t *testing.T, config agentcore.AgentLoopConfig, tools []agentcore.Tool) *AgentHarness {
	t.Helper()
	as, err := NewAgentHarness(AgentHarnessOptions{
		Session: session.New("s1"),
		Repo:    session.NewRepository(nil),
		Resources: agentresources.Snapshot{
			SystemPrompt: "system",
			Tools:        tools,
		},
		Config: config,
		Model:  "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return as
}

type fakeTool struct {
	name string
	text string
}

type resourceLoaderFunc func(context.Context, agentresources.Request) (agentresources.Snapshot, error)

func (f resourceLoaderFunc) Load(ctx context.Context, request agentresources.Request) (agentresources.Snapshot, error) {
	return f(ctx, request)
}

type blockingSessionStorage struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *blockingSessionStorage) Append(string, session.Entry) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return nil
}

func (*blockingSessionStorage) Create(string, []session.Entry) error { return nil }
func (*blockingSessionStorage) Load(string) ([]session.Entry, error) { return nil, nil }
func (*blockingSessionStorage) List() ([]string, error)              { return nil, nil }
func (*blockingSessionStorage) Delete(string) (bool, error)          { return false, nil }

type modelRecordingStorage struct {
	mu        sync.Mutex
	failModel string
	persisted []string
}

func (s *modelRecordingStorage) Append(_ string, entry session.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry.Type == session.EntryModelChange && entry.Model == s.failModel {
		return errors.New("model write failed")
	}
	if entry.Type == session.EntryModelChange {
		s.persisted = append(s.persisted, entry.Model)
	}
	return nil
}

func (*modelRecordingStorage) Create(string, []session.Entry) error { return nil }
func (*modelRecordingStorage) Load(string) ([]session.Entry, error) { return nil, nil }
func (*modelRecordingStorage) List() ([]string, error)              { return nil, nil }
func (*modelRecordingStorage) Delete(string) (bool, error)          { return false, nil }

func (s *modelRecordingStorage) allow(model string) {
	s.mu.Lock()
	if s.failModel == model {
		s.failModel = ""
	}
	s.mu.Unlock()
}

func (s *modelRecordingStorage) models() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.persisted...)
}

func (t fakeTool) Definition() protocol.ToolDefinition {
	return protocol.ToolDefinition{Name: t.name, Parameters: json.RawMessage(`{"type":"object"}`)}
}

func (t fakeTool) ExecutionMode() agentcore.ExecutionMode { return agentcore.ExecutionModeParallel }

func (t fakeTool) PrepareArguments(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return raw, nil
}

func (t fakeTool) Execute(context.Context, agentcore.ToolCall, agentcore.ToolUpdateSink) (protocol.ToolResult, error) {
	return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent(t.text)}}, nil
}

func streamSequence(streams ...*protocol.AssistantMessageEventStream) agentcore.StreamFn {
	index := 0
	return func(context.Context, agentcore.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		if index >= len(streams) {
			return textStream("done"), nil
		}
		stream := streams[index]
		index++
		return stream, nil
	}
}

func textStream(text string) *protocol.AssistantMessageEventStream {
	stream := protocol.NewAssistantMessageEventStream(4)
	index := 0
	message := protocol.AssistantMessage{
		Content:    protocol.ContentList{protocol.NewTextContent(text)},
		StopReason: protocol.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &protocol.AssistantMessage{Content: protocol.ContentList{}, StopReason: protocol.StopReasonStop}}); err != nil {
		panic(err)
	}
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventTextDelta, ContentIndex: &index, Delta: text, Partial: &message}); err != nil {
		panic(err)
	}
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventDone, Reason: protocol.StopReasonStop, Message: &message}); err != nil {
		panic(err)
	}
	return stream
}

func toolCallStream(call protocol.ToolCallContent) *protocol.AssistantMessageEventStream {
	stream := protocol.NewAssistantMessageEventStream(4)
	message := protocol.AssistantMessage{
		Content:    protocol.ContentList{call},
		StopReason: protocol.StopReasonToolUse,
		Timestamp:  time.Now().UnixMilli(),
	}
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &protocol.AssistantMessage{Content: protocol.ContentList{}, StopReason: protocol.StopReasonStop}}); err != nil {
		panic(err)
	}
	index := 0
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventToolCallEnd, ContentIndex: &index, ToolCall: &call, Partial: &message}); err != nil {
		panic(err)
	}
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventDone, Reason: protocol.StopReasonToolUse, Message: &message}); err != nil {
		panic(err)
	}
	return stream
}

func toolDefinitionNames(definitions []protocol.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func userMessage(text string) protocol.UserMessage {
	return protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent(text)}, Timestamp: time.Now().UnixMilli()}
}

func assertMessageTexts(t *testing.T, messages protocol.MessageList, want []string) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("message count = %d, want %d", len(messages), len(want))
	}
	for i, message := range messages {
		if got := textOf(t, message); got != want[i] {
			t.Fatalf("message %d text = %q, want %q", i, got, want[i])
		}
	}
}

func textOf(t *testing.T, message protocol.AgentMessage) string {
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
		t.Fatalf("unexpected message %T", message)
	}
	for _, item := range content {
		if text, ok := item.(protocol.TextContent); ok {
			return text.Text
		}
	}
	return ""
}

func TestRuntimeDiscardSessionPreservesReplacementSession(t *testing.T) {
	storage, err := session.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	runtime := NewRuntime(RuntimeOptions{Repo: repo})
	harness, err := runtime.NewSession(t.Context(), NewSessionOptions{ID: "discard-replaced"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Delete(harness.session.ID()); err != nil {
		t.Fatal(err)
	}
	replacement := session.New(harness.session.ID())
	if _, err := replacement.AppendSessionInfo("/replacement", "replacement"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateSession(replacement); err != nil {
		t.Fatal(err)
	}

	if err := runtime.DiscardSession(harness); !errors.Is(err, session.ErrSessionMismatch) {
		t.Fatalf("DiscardSession() error = %v, want ErrSessionMismatch", err)
	}
	if cached, ok := repo.Get(replacement.ID()); !ok || cached != replacement {
		t.Fatal("replacement session changed after stale harness discard")
	}
	resumed, err := runtime.Resume(t.Context(), replacement.ID())
	if err != nil {
		t.Fatal(err)
	}
	if resumed == harness || resumed.session != replacement {
		t.Fatal("Resume() did not bind the replacement session")
	}
}

type initialCreateStorage struct {
	mu          sync.Mutex
	entries     []session.Entry
	createCalls int
	appendCalls int
}

func (s *initialCreateStorage) Create(_ string, entries []session.Entry) error {
	s.mu.Lock()
	s.createCalls++
	s.entries = append([]session.Entry(nil), entries...)
	s.mu.Unlock()
	return nil
}

func (s *initialCreateStorage) Append(string, session.Entry) error {
	s.mu.Lock()
	s.appendCalls++
	s.mu.Unlock()
	return nil
}

func (*initialCreateStorage) Load(string) ([]session.Entry, error) { return nil, nil }
func (*initialCreateStorage) List() ([]string, error)              { return nil, nil }
func (*initialCreateStorage) Delete(string) (bool, error)          { return false, nil }

func (s *initialCreateStorage) snapshot() ([]session.Entry, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]session.Entry(nil), s.entries...), s.createCalls, s.appendCalls
}

type blockingDeleteStorage struct {
	mu            sync.Mutex
	entries       map[string][]session.Entry
	deleteErr     error
	deleteEntered chan struct{}
	deleteRelease chan struct{}
	deleteOnce    sync.Once
}

func newBlockingDeleteStorage(deleteErr error) *blockingDeleteStorage {
	return &blockingDeleteStorage{
		entries:       make(map[string][]session.Entry),
		deleteErr:     deleteErr,
		deleteEntered: make(chan struct{}),
		deleteRelease: make(chan struct{}),
	}
}

func (s *blockingDeleteStorage) Create(sessionID string, entries []session.Entry) error {
	s.mu.Lock()
	s.entries[sessionID] = append([]session.Entry(nil), entries...)
	s.mu.Unlock()
	return nil
}

func (s *blockingDeleteStorage) Append(sessionID string, entry session.Entry) error {
	s.mu.Lock()
	s.entries[sessionID] = append(s.entries[sessionID], entry)
	s.mu.Unlock()
	return nil
}

func (s *blockingDeleteStorage) Load(sessionID string) ([]session.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, ok := s.entries[sessionID]
	if !ok {
		return nil, nil
	}
	return append([]session.Entry(nil), entries...), nil
}

func (s *blockingDeleteStorage) List() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *blockingDeleteStorage) Delete(sessionID string) (bool, error) {
	s.deleteOnce.Do(func() { close(s.deleteEntered) })
	<-s.deleteRelease
	if s.deleteErr != nil {
		return false, s.deleteErr
	}
	s.mu.Lock()
	_, exists := s.entries[sessionID]
	delete(s.entries, sessionID)
	s.mu.Unlock()
	return exists, nil
}
