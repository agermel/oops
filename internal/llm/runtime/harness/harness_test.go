package harness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"oops/internal/llm/ai/protocol"
	coreagent "oops/internal/llm/core/agent"
	"oops/internal/llm/core/toolruntime"
	"oops/internal/llm/runtime/session"
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
		Loader: StaticResourceLoader{Snapshot: ResourceSnapshot{SystemPrompt: "system"}},
		Config: coreagent.AgentLoopConfig{Stream: stream},
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
	stream := func(ctx context.Context, req coreagent.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return textStream("done"), nil
	}
	as := newHarnessSession(t, coreagent.AgentLoopConfig{Stream: stream}, nil)

	done := make(chan error, 1)
	go func() {
		_, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("hold")})
		done <- err
	}()
	<-started
	if _, err := as.NavigateTreeSnapshot(as.session.LeafID()); !errors.Is(err, coreagent.ErrAgentBusy) {
		t.Fatalf("NavigateTreeSnapshot err = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNavigateTreeWithSummarySnapshotReturnsEditorTextForUserTarget(t *testing.T) {
	as := newHarnessSession(t, coreagent.AgentLoopConfig{Stream: streamSequence(textStream("done"))}, nil)
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
		Loader: StaticResourceLoader{},
		Config: coreagent.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
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
	storage, err := session.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	tool := fakeTool{name: "known", text: "ok"}
	runtime := NewRuntime(RuntimeOptions{
		Repo: repo,
		Loader: StaticResourceLoader{Snapshot: ResourceSnapshot{
			Tools: []toolruntime.Tool{tool},
		}},
		Config: coreagent.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
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

func TestSessionSnapshotAndListenExposeRunState(t *testing.T) {
	as := newHarnessSession(t, coreagent.AgentLoopConfig{Stream: streamSequence(textStream("done"))}, []toolruntime.Tool{
		fakeTool{name: "known", text: "ok"},
	})
	var observed []protocol.AgentEvent
	unsubscribe := as.Listen(func(_ context.Context, event protocol.AgentEvent, _ coreagent.AgentState) error {
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

func newHarnessSession(t *testing.T, config coreagent.AgentLoopConfig, tools []toolruntime.Tool) *AgentSession {
	t.Helper()
	as, err := NewAgentSession(AgentSessionOptions{
		Session: session.New("s1"),
		Repo:    session.NewRepository(nil),
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

func (t fakeTool) ExecutionMode() toolruntime.ExecutionMode { return toolruntime.ExecutionModeParallel }

func (t fakeTool) PrepareArguments(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return raw, nil
}

func (t fakeTool) Execute(context.Context, toolruntime.ToolCall, toolruntime.ToolUpdateSink) (protocol.ToolResult, error) {
	return protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent(t.text)}}, nil
}

func streamSequence(streams ...*protocol.AssistantMessageEventStream) coreagent.StreamFn {
	index := 0
	return func(context.Context, coreagent.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
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
