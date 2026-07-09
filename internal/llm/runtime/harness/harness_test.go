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
	if err := as.SetModel("provider", "model-2"); err != nil {
		t.Fatal(err)
	}
	first := as.SessionContext().Messages[0]
	firstID := findFirstMessageEntry(t, as.session)
	if _, err := as.Compact("older", firstID, 10); err != nil {
		t.Fatal(err)
	}
	if err := as.NavigateTree(as.session.LeafID()); err != nil {
		t.Fatal(err)
	}

	reloadedRepo := session.NewRepository(storage)
	reloaded, err := reloadedRepo.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := reloaded.BuildContext()
	if ctx.Model != "model-2" || len(ctx.Messages) != 3 {
		t.Fatalf("reloaded context model=%q messages=%d", ctx.Model, len(ctx.Messages))
	}
	if got := textOf(t, first); got != "hello" {
		t.Fatalf("first message = %q", got)
	}
	if _, err := storage.Load("s1"); err != nil {
		t.Fatal(err)
	}
	if path := filepath.Join(dir, "s1.jsonl"); path == "" {
		t.Fatal("empty path")
	}
}

func TestPendingWritesFlushAndPrepareNextTurnUpdatesToolRunner(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls int
	stream := func(ctx context.Context, req coreagent.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		calls++
		if calls == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return toolCallStream(toolCall("call_first", "first_tool"))
		}
		if calls == 2 {
			return toolCallStream(toolCall("call_second", "second_tool"))
		}
		return textStream("done"), nil
	}
	as := newHarnessSession(t, coreagent.AgentLoopConfig{Stream: stream}, []toolruntime.Tool{
		fakeTool{name: "first_tool", text: "first-result"},
		fakeTool{name: "second_tool", text: "second-result"},
	})

	done := make(chan error, 1)
	go func() {
		_, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("run")})
		done <- err
	}()
	<-firstStarted
	if err := as.SetModel("provider", "model-next"); err != nil {
		t.Fatal(err)
	}
	if err := as.SetReasoning("high"); err != nil {
		t.Fatal(err)
	}
	if err := as.SetActiveTools([]string{"second_tool"}); err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ctx := as.SessionContext()
	if ctx.Model != "model-next" || ctx.Provider != "provider" || ctx.Reasoning != "high" {
		t.Fatalf("context state provider=%q model=%q reasoning=%q", ctx.Provider, ctx.Model, ctx.Reasoning)
	}
	assertMessageTexts(t, ctx.Messages, []string{"run", "", "first-result", "", "second-result", "done"})
}

func TestActiveToolsValidationAndBusyTreeOperations(t *testing.T) {
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
	as := newHarnessSession(t, coreagent.AgentLoopConfig{Stream: stream}, []toolruntime.Tool{
		fakeTool{name: "known", text: "ok"},
	})
	if err := as.SetActiveTools([]string{"known", "known"}); err == nil {
		t.Fatal("expected duplicate active tool error")
	}
	if err := as.SetActiveTools([]string{"missing"}); err == nil {
		t.Fatal("expected unknown active tool error")
	}

	done := make(chan error, 1)
	go func() {
		_, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("hold")})
		done <- err
	}()
	<-started
	if _, err := as.Compact("summary", findFirstMessageEntry(t, as.session), 1); !errors.Is(err, coreagent.ErrAgentBusy) {
		t.Fatalf("Compact err = %v", err)
	}
	if err := as.NavigateTree(as.session.LeafID()); !errors.Is(err, coreagent.ErrAgentBusy) {
		t.Fatalf("NavigateTree err = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCompactStoresSummaryDetails(t *testing.T) {
	call := protocol.NewToolCallContent("call_read", "read", json.RawMessage(`{"path":"README.md"}`))
	as := newHarnessSession(t, coreagent.AgentLoopConfig{
		Stream: streamSequence(mustToolCallStream(t, call), textStream("done")),
	}, []toolruntime.Tool{
		fakeTool{name: "read", text: "file"},
	})
	if _, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("inspect")}); err != nil {
		t.Fatal(err)
	}
	finalAssistant := findLastMessageEntry(t, as.session, func(message protocol.AgentMessage) bool {
		assistant, ok := message.(protocol.AssistantMessage)
		return ok && len(assistant.Content) == 1 && textOf(t, assistant) == "done"
	})
	entry, err := as.Compact("older work", finalAssistant.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var details session.SummaryDetails
	if err := json.Unmarshal(entry.Details, &details); err != nil {
		t.Fatal(err)
	}
	if len(details.ReadFiles) != 1 || details.ReadFiles[0] != "README.md" {
		t.Fatalf("read files = %#v", details.ReadFiles)
	}
}

func TestNavigateTreeWithSummaryAppendsBranchSummary(t *testing.T) {
	as := newHarnessSession(t, coreagent.AgentLoopConfig{Stream: streamSequence(textStream("done"))}, nil)
	root, err := as.session.AppendMessage(userMessage("root"))
	if err != nil {
		t.Fatal(err)
	}
	left, err := as.session.AppendMessage(protocol.AssistantMessage{
		Content: protocol.ContentList{
			protocol.NewToolCallContent("call_read", "read", json.RawMessage(`{"path":"left.md"}`)),
		},
		StopReason: protocol.StopReasonToolUse,
		Timestamp:  time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := as.session.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}
	right, err := as.session.AppendMessage(userMessage("right"))
	if err != nil {
		t.Fatal(err)
	}
	if err := as.session.MoveTo(left.ID); err != nil {
		t.Fatal(err)
	}
	if err := as.NavigateTreeWithSummary(right.ID, "left branch read left.md"); err != nil {
		t.Fatal(err)
	}
	ctx := as.SessionContext()
	assertMessageTexts(t, ctx.Messages, []string{
		"root",
		"Branch summary:\n\nleft branch read left.md",
	})
	leaf, ok := as.session.Entry(as.session.LeafID())
	if !ok || leaf.Type != session.EntryBranchSummary || leaf.ParentID != root.ID {
		t.Fatalf("leaf = %#v ok=%v", leaf, ok)
	}
	snapshot := as.Snapshot()
	if snapshot.EditorText != "" {
		t.Fatalf("snapshot editor text = %q, want empty outside branch response", snapshot.EditorText)
	}
	var details session.SummaryDetails
	if err := json.Unmarshal(leaf.Details, &details); err != nil {
		t.Fatal(err)
	}
	if len(details.ReadFiles) != 1 || details.ReadFiles[0] != "left.md" {
		t.Fatalf("read files = %#v", details.ReadFiles)
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

func TestRuntimeResumeForkAndSwitchCWD(t *testing.T) {
	loader := &recordingLoader{}
	storage, err := session.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	runtime := NewRuntime(RuntimeOptions{
		Repo:   repo,
		Loader: loader,
		Config: coreagent.AgentLoopConfig{Stream: streamSequence(textStream("done"))},
		CWD:    "/one",
	})
	as, err := runtime.NewSession(context.Background(), NewSessionOptions{ID: "s1", Name: "name"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("hello")}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SwitchCWD(context.Background(), "/two"); err != nil {
		t.Fatal(err)
	}
	resumed, err := runtime.Resume(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if resumed != as {
		t.Fatal("resume should return active session")
	}
	forked, err := runtime.Fork(context.Background(), "s1", "s2", as.session.LeafID())
	if err != nil {
		t.Fatal(err)
	}
	if forked.session.ID() != "s2" {
		t.Fatalf("fork id = %q", forked.session.ID())
	}
	if len(loader.requests) < 2 || loader.requests[0].CWD != "/one" || loader.requests[len(loader.requests)-1].CWD != "/two" {
		t.Fatalf("loader requests = %#v", loader.requests)
	}
	reloadedRepo := session.NewRepository(storage)
	reloaded, err := reloadedRepo.Load("s2")
	if err != nil {
		t.Fatal(err)
	}
	if info := reloaded.Info(); info.CWD != "/two" || info.LeafID != as.session.LeafID() {
		t.Fatalf("fork reload info cwd=%q leaf=%q", info.CWD, info.LeafID)
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

func TestIdleConfigWritesRebuildAgentForNextPrompt(t *testing.T) {
	seenModels := []string{}
	stream := func(_ context.Context, req coreagent.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		seenModels = append(seenModels, req.Model)
		return textStream("done"), nil
	}
	as := newHarnessSession(t, coreagent.AgentLoopConfig{Stream: stream}, []toolruntime.Tool{
		fakeTool{name: "first_tool", text: "first"},
		fakeTool{name: "second_tool", text: "second"},
	})
	if err := as.SetModel("provider", "model-2"); err != nil {
		t.Fatal(err)
	}
	if err := as.SetActiveTools([]string{"second_tool"}); err != nil {
		t.Fatal(err)
	}
	state := as.State()
	if state.Model != "model-2" {
		t.Fatalf("state model = %q", state.Model)
	}
	if len(state.Tools) != 1 || state.Tools[0].Name != "second_tool" {
		t.Fatalf("state tools = %#v", state.Tools)
	}
	if _, err := as.Prompt(context.Background(), protocol.MessageList{userMessage("next")}); err != nil {
		t.Fatal(err)
	}
	if len(seenModels) != 1 || seenModels[0] != "model-2" {
		t.Fatalf("seen models = %#v", seenModels)
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
	if as.Events()[0].Type == protocol.AgentEventAgentEnd {
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

type recordingLoader struct {
	requests []ResourceRequest
}

func (l *recordingLoader) Load(_ context.Context, req ResourceRequest) (ResourceSnapshot, error) {
	l.requests = append(l.requests, req)
	return ResourceSnapshot{SystemPrompt: "cwd:" + req.CWD}, nil
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

func toolCallStream(call protocol.ToolCallContent) (*protocol.AssistantMessageEventStream, error) {
	stream := protocol.NewAssistantMessageEventStream(4)
	index := 0
	message := protocol.AssistantMessage{
		Content:    protocol.ContentList{call},
		StopReason: protocol.StopReasonToolUse,
		Timestamp:  time.Now().UnixMilli(),
	}
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &protocol.AssistantMessage{Content: protocol.ContentList{}, StopReason: protocol.StopReasonStop}}); err != nil {
		return nil, err
	}
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventToolCallEnd, ContentIndex: &index, ToolCall: &call, Partial: &message}); err != nil {
		return nil, err
	}
	if err := stream.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventDone, Reason: protocol.StopReasonToolUse, Message: &message}); err != nil {
		return nil, err
	}
	return stream, nil
}

func mustToolCallStream(t *testing.T, call protocol.ToolCallContent) *protocol.AssistantMessageEventStream {
	t.Helper()
	stream, err := toolCallStream(call)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func toolCall(id, name string) protocol.ToolCallContent {
	return protocol.NewToolCallContent(id, name, json.RawMessage(`{}`))
}

func userMessage(text string) protocol.UserMessage {
	return protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent(text)}, Timestamp: time.Now().UnixMilli()}
}

func findFirstMessageEntry(t *testing.T, s *session.Session) string {
	t.Helper()
	for _, entry := range s.Entries() {
		if entry.Type == session.EntryMessage {
			return entry.ID
		}
	}
	t.Fatal("message entry not found")
	return ""
}

func findLastMessageEntry(t *testing.T, s *session.Session, match func(protocol.AgentMessage) bool) session.Entry {
	t.Helper()
	entries := s.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Type == session.EntryMessage && match(entry.Message) {
			return entry
		}
	}
	t.Fatal("matching message entry not found")
	return session.Entry{}
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
