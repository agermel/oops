package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	agentcore "oops/internal/agent/core"
	agentresources "oops/internal/agent/runtime/resources"
	"oops/internal/agent/runtime/session"
)

func TestAgentHarnessCompactPersistsRebuildsAndEmitsEvents(t *testing.T) {
	storage, err := session.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewRepository(storage)
	var request protocol.StreamRequest
	harness, err := NewAgentHarness(AgentHarnessOptions{
		Session: session.New("compact-success"),
		Repo:    repo,
		Resources: agentresources.Snapshot{
			SystemPrompt: "system",
		},
		Config: agentcore.AgentLoopConfig{Stream: func(_ context.Context, current protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			request = current
			return textStream("generated checkpoint"), nil
		}},
		Model:     "summary-model",
		Provider:  "summary-provider",
		Reasoning: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	appendCompactionHistory(t, harness)

	var eventNames []string
	harness.ListenRunEvents(func(_ context.Context, event RunEvent) error {
		if !strings.HasPrefix(event.EventName(), "session_") {
			return nil
		}
		if _, err := json.Marshal(event); err != nil {
			return err
		}
		eventNames = append(eventNames, event.EventName())
		return nil
	})

	result, err := harness.Compact(t.Context(), "preserve exact decisions")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Summary, "generated checkpoint") {
		t.Fatalf("summary = %q", result.Summary)
	}
	if got, want := eventNames, []string{"session_before_compact", "session_compact"}; !slices.Equal(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	if got := harness.Phase(); got != AgentHarnessPhaseIdle {
		t.Fatalf("phase = %q, want %q", got, AgentHarnessPhaseIdle)
	}
	if request.Model != "summary-model" || request.Provider != "summary-provider" || request.Reasoning != "high" || request.SessionID != "compact-success" {
		t.Fatalf("summary request = %#v", request)
	}
	promptText := protocol.TextFromContent(request.Context.Messages[0].(protocol.UserMessage).Content)
	if !strings.Contains(promptText, "preserve exact decisions") {
		t.Fatalf("summary prompt does not contain custom instructions: %q", promptText)
	}

	entries := harness.session.Entries()
	compactionEntry := entries[len(entries)-1]
	if compactionEntry.Type != session.EntryCompaction || compactionEntry.FirstKeptEntryID != result.FirstKeptEntryID {
		t.Fatalf("compaction entry = %#v", compactionEntry)
	}
	contextMessages := harness.session.BuildContext().Messages
	if len(contextMessages) != 3 {
		t.Fatalf("context message count = %d, want 3", len(contextMessages))
	}
	if got := textOf(t, contextMessages[0]); !strings.HasPrefix(got, "The conversation history before this point was compacted into the following summary:\n\n<summary>\ngenerated checkpoint\n</summary>") {
		t.Fatalf("context summary = %q", got)
	}

	reloaded, err := session.NewRepository(storage).Load("compact-success")
	if err != nil {
		t.Fatal(err)
	}
	reloadedEntries := reloaded.Entries()
	if got := reloadedEntries[len(reloadedEntries)-1].Type; got != session.EntryCompaction {
		t.Fatalf("reloaded last entry type = %q", got)
	}
}

func TestAgentHarnessCompactRestoresIdleAndFlushesConcurrentMutation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	streamErr := errors.New("summary stream failed")
	var once sync.Once
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: func(ctx context.Context, _ protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return nil, streamErr
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}, nil)
	appendCompactionHistory(t, harness)

	compactDone := make(chan error, 1)
	go func() {
		_, err := harness.Compact(context.Background(), "")
		compactDone <- err
	}()
	<-started
	if got := harness.Phase(); got != AgentHarnessPhaseCompaction {
		t.Fatalf("phase = %q, want %q", got, AgentHarnessPhaseCompaction)
	}
	if _, err := harness.NavigateTree(t.Context(), harness.session.LeafID(), NavigateTreeOptions{}); !errors.Is(err, agentcore.ErrAgentBusy) {
		t.Fatalf("NavigateTree() error = %v, want busy", err)
	}
	if err := harness.SetReasoning("high"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-compactDone; !errors.Is(err, streamErr) {
		t.Fatalf("Compact() error = %v, want %v", err, streamErr)
	}
	if got := harness.Phase(); got != AgentHarnessPhaseIdle {
		t.Fatalf("phase = %q, want %q", got, AgentHarnessPhaseIdle)
	}
	if got := harness.session.BuildContext().Reasoning; got != "high" {
		t.Fatalf("reasoning = %q, want high", got)
	}
}

func TestAgentHarnessNavigateTreeGeneratesSummaryAndEmitsEvents(t *testing.T) {
	var request protocol.StreamRequest
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: func(_ context.Context, current protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		request = current
		return textStream("generated branch facts"), nil
	}}, nil)
	root, err := harness.repo.AppendEntry(harness.session.ID(), session.Entry{Type: session.EntryMessage, Message: userMessage("root")})
	if err != nil {
		t.Fatal(err)
	}
	oldLeaf, err := harness.repo.AppendEntry(harness.session.ID(), session.Entry{Type: session.EntryMessage, Message: harnessAssistantMessage("old branch")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.repo.AppendEntry(harness.session.ID(), session.Entry{Type: session.EntryLeaf, LeafID: root.ID}); err != nil {
		t.Fatal(err)
	}
	target, err := harness.repo.AppendEntry(harness.session.ID(), session.Entry{Type: session.EntryMessage, Message: userMessage("retry text")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.repo.AppendEntry(harness.session.ID(), session.Entry{Type: session.EntryLeaf, LeafID: oldLeaf.ID}); err != nil {
		t.Fatal(err)
	}
	leafEntriesBefore := countEntryType(harness.session.Entries(), session.EntryLeaf)

	var eventNames []string
	harness.ListenRunEvents(func(_ context.Context, event RunEvent) error {
		if event.EventName() != "session_before_tree" && event.EventName() != "session_tree" {
			return nil
		}
		if _, err := json.Marshal(event); err != nil {
			return err
		}
		eventNames = append(eventNames, event.EventName())
		return nil
	})

	snapshot, err := harness.NavigateTree(t.Context(), target.ID, NavigateTreeOptions{
		Summarize:           true,
		CustomInstructions:  "focus on decisions",
		ReplaceInstructions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EditorText != "retry text" {
		t.Fatalf("editor text = %q", snapshot.EditorText)
	}
	if got, want := eventNames, []string{"session_before_tree", "session_tree"}; !slices.Equal(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	if request.SessionID != harness.session.ID() || request.Model != "model" {
		t.Fatalf("branch request = %#v", request)
	}
	if got := protocol.TextFromContent(request.Context.Messages[0].(protocol.UserMessage).Content); !strings.Contains(got, "focus on decisions") {
		t.Fatalf("branch prompt = %q", got)
	}
	leaf, ok := harness.session.Entry(snapshot.LeafID)
	if !ok || leaf.Type != session.EntryBranchSummary || leaf.ParentID != root.ID || !strings.Contains(leaf.Summary, "generated branch facts") {
		t.Fatalf("summary leaf = %#v ok=%v", leaf, ok)
	}
	if got := countEntryType(harness.session.Entries(), session.EntryLeaf); got != leafEntriesBefore {
		t.Fatalf("leaf marker count = %d, want %d", got, leafEntriesBefore)
	}
	if got := harness.Phase(); got != AgentHarnessPhaseIdle {
		t.Fatalf("phase = %q, want %q", got, AgentHarnessPhaseIdle)
	}
}

func TestAgentHarnessNavigateTreeWithoutSummaryPersistsLeafMarker(t *testing.T) {
	harness := newHarnessSession(t, agentcore.AgentLoopConfig{Stream: streamSequence(textStream("unused"))}, nil)
	if _, err := harness.repo.AppendEntry(harness.session.ID(), session.Entry{Type: session.EntryMessage, Message: userMessage("root")}); err != nil {
		t.Fatal(err)
	}
	target, err := harness.repo.AppendEntry(harness.session.ID(), session.Entry{Type: session.EntryMessage, Message: harnessAssistantMessage("target")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.repo.AppendEntry(harness.session.ID(), session.Entry{Type: session.EntryMessage, Message: userMessage("other")}); err != nil {
		t.Fatal(err)
	}
	other, err := harness.repo.AppendEntry(harness.session.ID(), session.Entry{Type: session.EntryMessage, Message: harnessAssistantMessage("other answer")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.NavigateTree(t.Context(), target.ID, NavigateTreeOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := harness.session.Entries()
	last := entries[len(entries)-1]
	if last.Type != session.EntryLeaf || last.LeafID != target.ID || harness.session.LeafID() != target.ID {
		t.Fatalf("last entry = %#v, prior leaf = %q", last, other.ID)
	}
}

func appendCompactionHistory(t *testing.T, harness *AgentHarness) {
	t.Helper()
	messages := protocol.MessageList{
		userMessage("old question"),
		harnessAssistantMessage("old answer"),
		userMessage(strings.Repeat("r", 90_000)),
		harnessAssistantMessage("recent answer"),
	}
	for _, message := range messages {
		if err := harness.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
}

func harnessAssistantMessage(text string) protocol.AssistantMessage {
	return protocol.AssistantMessage{
		Content:    protocol.ContentList{protocol.NewTextContent(text)},
		StopReason: protocol.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func countEntryType(entries []session.Entry, entryType session.EntryType) int {
	count := 0
	for _, entry := range entries {
		if entry.Type == entryType {
			count++
		}
	}
	return count
}
