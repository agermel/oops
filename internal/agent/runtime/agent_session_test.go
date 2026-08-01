package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	agentcore "oops/internal/agent/core"
)

func TestPromptPersistsMessagesAndFileBackedReload(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	stream := streamSequence(textStream("answer"))
	runtime := NewRuntime(RuntimeOptions{
		Repo:   repo,
		Loader: StaticResourceLoader{Snapshot: ResourceSnapshot{SystemPrompt: "system"}},
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
	reloadedRepo := NewRepository(storage)
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

func TestAgentSessionSteerQueuesMessageForActiveRun(t *testing.T) {
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

func TestAgentSessionAbortClearsLiveQueuesAndPreservesNextTurn(t *testing.T) {
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
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := as.WaitForIdle(context.Background()); err != nil {
		t.Fatal(err)
	}

	messages, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("explicit")})
	if err != nil {
		t.Fatal(err)
	}
	assertMessageTexts(t, messages, []string{"queued-next", "explicit", "after"})
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
		"Branch summary:\n\nabandoned branch",
	})
	leaf, ok := as.session.Entry(snapshot.LeafID)
	if !ok || leaf.Type != EntryBranchSummary || leaf.ParentID != root.ID {
		t.Fatalf("leaf = %#v ok=%v, want branch summary under %q", leaf, ok, root.ID)
	}
}

func TestRuntimeConcurrentResumeReturnsSingleWrapper(t *testing.T) {
	storage, err := NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	runtime := NewRuntime(RuntimeOptions{
		Repo:   repo,
		Loader: StaticResourceLoader{},
		Config: agentcore.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
	})
	created, err := runtime.NewSession(context.Background(), NewSessionOptions{ID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	runtime.active = map[string]*AgentSession{}

	const workers = 8
	results := make([]*AgentSession, workers)
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

func TestNewSessionPersistsInitialConfig(t *testing.T) {
	storage, err := NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(storage)
	tool := fakeTool{name: "known", text: "ok"}
	runtime := NewRuntime(RuntimeOptions{
		Repo: repo,
		Loader: StaticResourceLoader{Snapshot: ResourceSnapshot{
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
	reloadedRepo := NewRepository(storage)
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
	snapshot.Events[0].Type = protocol.AgentEventAgentEnd
	if next := as.Snapshot(); next.Events[0].Type == protocol.AgentEventAgentEnd {
		t.Fatal("snapshot events alias session events")
	}
}

func newHarnessSession(t *testing.T, config agentcore.AgentLoopConfig, tools []agentcore.Tool) *AgentSession {
	t.Helper()
	as, err := NewAgentSession(AgentSessionOptions{
		Session: New("s1"),
		Repo:    NewRepository(nil),
		Resources: ResourceSnapshot{
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
