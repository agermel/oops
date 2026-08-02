package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/runtime/session"
)

func TestEstimateContextTokensUsesLatestValidUsage(t *testing.T) {
	messages := protocol.MessageList{
		testUserMessage("before"),
		testAssistantMessage("measured", protocol.Usage{TotalTokens: 120}),
		testAssistantMessageWithStop("ignored", protocol.StopReasonError, protocol.Usage{TotalTokens: 999}),
		testUserMessage("12345678"),
	}
	estimate := EstimateContextTokens(messages)
	if !estimate.HasUsage || estimate.LastUsageIndex != 1 {
		t.Fatalf("usage location = %#v", estimate)
	}
	if estimate.UsageTokens != 120 || estimate.TrailingTokens != 4 || estimate.Tokens != 124 {
		t.Fatalf("estimate = %#v", estimate)
	}

	unmeasured := EstimateContextTokens(protocol.MessageList{testUserMessage("12345678")})
	if unmeasured.HasUsage || unmeasured.LastUsageIndex != -1 || unmeasured.Tokens != 2 {
		t.Fatalf("unmeasured estimate = %#v", unmeasured)
	}
}

func TestShouldCompactUsesReserveThreshold(t *testing.T) {
	settings := Settings{Enabled: true, ReserveTokens: 10000, KeepRecentTokens: 20000}
	if !ShouldCompact(95000, 100000, settings) {
		t.Fatal("expected compaction above reserve threshold")
	}
	if ShouldCompact(90000, 100000, settings) {
		t.Fatal("threshold is strict")
	}
	settings.Enabled = false
	if ShouldCompact(95000, 100000, settings) {
		t.Fatal("disabled settings triggered compaction")
	}
}

func TestFindCutPointKeepsToolUnitsAndDetectsSplitTurn(t *testing.T) {
	entries := []session.Entry{
		testEntry("u0", "", session.EntryMessage, testUserMessage("old request")),
		testEntry("a0", "u0", session.EntryMessage, testAssistantMessage("old response", protocol.Usage{})),
		testEntry("u1", "a0", session.EntryMessage, testUserMessage("large turn")),
		testEntry("call", "u1", session.EntryMessage, testToolCallAssistant("call-1", "read", "large.txt")),
		testEntry("result", "call", session.EntryMessage, protocol.ToolResultMessage{
			ToolCallID: "call-1",
			ToolName:   "read",
			Content:    protocol.ContentList{protocol.NewTextContent(strings.Repeat("x", 1200))},
		}),
		testEntry("suffix", "result", session.EntryMessage, testAssistantMessage("kept", protocol.Usage{})),
	}
	cut := FindCutPoint(entries, 0, len(entries), 100)
	if cut.FirstKeptEntryIndex != 5 || cut.TurnStartIndex != 2 || !cut.SplitTurn {
		t.Fatalf("cut = %#v", cut)
	}
	if entries[cut.FirstKeptEntryIndex].Message.MessageRole() == protocol.RoleToolResult {
		t.Fatal("cut retained a detached tool result")
	}

	if protected := protectToolUnit(entries, 4, 0); protected != 3 {
		t.Fatalf("protected tool boundary = %d, want 3", protected)
	}
}

func TestPrepareUsesActivePathAndPreviousDetails(t *testing.T) {
	current := session.New("compaction-test")
	u1 := appendSessionMessage(t, current, testUserMessage("initial request"))
	call := appendSessionMessage(t, current, testToolCallAssistant("call-1", "write", "written.go"))
	appendSessionMessage(t, current, protocol.ToolResultMessage{
		ToolCallID: "call-1",
		ToolName:   "write",
		Content:    protocol.ContentList{protocol.NewTextContent("ok")},
		Details:    map[string]any{"path": "written.go"},
	})
	if _, err := current.AppendCompactionWithDetails("previous summary", u1.ID, 1000, session.SummaryDetails{
		ReadFiles:     []string{"old-read.go"},
		ModifiedFiles: []string{"old-edit.go"},
	}); err != nil {
		t.Fatal(err)
	}
	appendSessionMessage(t, current, testUserMessage("large turn"))
	appendSessionMessage(t, current, testAssistantMessage("recent suffix", protocol.Usage{TotalTokens: 5000}))

	preparation, err := Prepare(current, Settings{Enabled: true, ReserveTokens: 100, KeepRecentTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if preparation == nil {
		t.Fatal("missing preparation")
	}
	if preparation.PreviousSummary != "previous summary" || !preparation.SplitTurn {
		t.Fatalf("preparation = %#v", preparation)
	}
	if len(preparation.TurnPrefix) != 1 || preparation.TurnPrefix[0].MessageRole() != protocol.RoleUser {
		t.Fatalf("turn prefix = %#v", preparation.TurnPrefix)
	}
	if _, ok := preparation.FileOperations.Read["old-read.go"]; !ok {
		t.Fatal("previous read detail was not merged")
	}
	if _, ok := preparation.FileOperations.Edited["old-edit.go"]; !ok {
		t.Fatal("previous modified detail was not merged")
	}
	if _, ok := preparation.FileOperations.Written["written.go"]; !ok {
		t.Fatal("write tool call was not retained")
	}
	if preparation.TokensBefore != 5000 {
		t.Fatalf("tokens before = %d, want 5000", preparation.TokensBefore)
	}
	if call.ID == "" {
		t.Fatal("tool call entry missing id")
	}
}

func TestPrepareExcludesInactiveBranches(t *testing.T) {
	current := session.New("branch-path-test")
	root := appendSessionMessage(t, current, testUserMessage("root"))
	appendSessionMessage(t, current, testAssistantMessage("inactive branch", protocol.Usage{}))
	if err := current.MoveTo(root.ID); err != nil {
		t.Fatal(err)
	}
	appendSessionMessage(t, current, testUserMessage("active branch"))
	appendSessionMessage(t, current, testAssistantMessage("active suffix", protocol.Usage{}))

	preparation, err := Prepare(current, Settings{Enabled: true, ReserveTokens: 100, KeepRecentTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if preparation == nil {
		t.Fatal("missing preparation")
	}
	selected := append(protocol.CloneMessageList(preparation.Messages), preparation.TurnPrefix...)
	serialized := SerializeConversation(selected)
	if strings.Contains(serialized, "inactive branch") {
		t.Fatalf("inactive branch entered preparation: %s", serialized)
	}
}

func TestPrepareReturnsNilForEmptyOrCompactedSession(t *testing.T) {
	empty, err := Prepare(session.New("empty"), DefaultSettings)
	if err != nil || empty != nil {
		t.Fatalf("empty preparation = %#v, err = %v", empty, err)
	}

	current := session.New("already-compacted")
	user := appendSessionMessage(t, current, testUserMessage("request"))
	if _, err := current.AppendCompaction("summary", user.ID, 10); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(current, DefaultSettings)
	if err != nil || prepared != nil {
		t.Fatalf("compacted preparation = %#v, err = %v", prepared, err)
	}
}

func TestGenerateSummaryBuildsBoundedRequest(t *testing.T) {
	recorder := &completionRecorder{response: testAssistantMessage("## Goal\nsummary", protocol.Usage{})}
	summary, err := GenerateSummary(context.Background(), protocol.MessageList{testUserMessage("summarize")}, SummaryOptions{
		Completer:          recorder.Complete,
		Model:              protocol.Model{Provider: "provider", ID: "model", MaxTokens: 1200},
		ReserveTokens:      2000,
		CustomInstructions: "focus on files",
		PreviousSummary:    "old summary",
		Reasoning:          "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary != "## Goal\nsummary" {
		t.Fatalf("summary = %q", summary)
	}
	requests := recorder.Requests()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	request := requests[0]
	if request.MaxTokens != 1200 || request.Reasoning != "high" {
		t.Fatalf("request options = %#v", request)
	}
	user, ok := protocol.AsUserMessage(request.Context.Messages[0])
	if !ok {
		t.Fatalf("summary request message = %#v", request.Context.Messages[0])
	}
	prompt := textFromContent(user.Content)
	for _, fragment := range []string{"<previous-summary>\nold summary\n</previous-summary>", "Additional focus: focus on files"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("prompt missing %q: %s", fragment, prompt)
		}
	}
}

func TestGenerateSummaryClassifiesCompletionFailures(t *testing.T) {
	tests := []struct {
		name      string
		completer Completer
		code      ErrorCode
		message   string
	}{
		{
			name: "model error",
			completer: func(context.Context, CompleteRequest) (protocol.AssistantMessage, error) {
				return testAssistantMessageWithError(protocol.StopReasonError, "boom"), nil
			},
			code:    CodeSummarizationFailed,
			message: "Summarization failed: boom",
		},
		{
			name: "aborted response",
			completer: func(context.Context, CompleteRequest) (protocol.AssistantMessage, error) {
				return testAssistantMessageWithError(protocol.StopReasonAborted, "stopped"), nil
			},
			code:    CodeAborted,
			message: "stopped",
		},
		{
			name: "transport error",
			completer: func(context.Context, CompleteRequest) (protocol.AssistantMessage, error) {
				return protocol.AssistantMessage{}, errors.New("offline")
			},
			code:    CodeSummarizationFailed,
			message: "Summarization failed: offline",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := GenerateSummary(context.Background(), protocol.MessageList{testUserMessage("x")}, SummaryOptions{
				Completer:     test.completer,
				Model:         protocol.Model{MaxTokens: 100},
				ReserveTokens: 100,
			})
			requireOperationError(t, err, test.code, test.message)
		})
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := GenerateSummary(canceled, protocol.MessageList{testUserMessage("x")}, SummaryOptions{
		Completer: func(context.Context, CompleteRequest) (protocol.AssistantMessage, error) {
			t.Fatal("completion called after cancellation")
			return protocol.AssistantMessage{}, nil
		},
	})
	requireOperationError(t, err, CodeAborted, context.Canceled.Error())
}

func TestCompactCombinesSplitTurnAndFileDetails(t *testing.T) {
	recorder := &completionRecorder{respond: func(request CompleteRequest) protocol.AssistantMessage {
		user, _ := protocol.AsUserMessage(request.Context.Messages[0])
		if strings.Contains(textFromContent(user.Content), "## Original Request") {
			return testAssistantMessage("prefix summary", protocol.Usage{})
		}
		return testAssistantMessage("history summary", protocol.Usage{})
	}}
	operations := NewFileOperations()
	operations.Read["changed.go"] = struct{}{}
	operations.Read["read.go"] = struct{}{}
	operations.Edited["changed.go"] = struct{}{}
	operations.Written["written.go"] = struct{}{}
	result, err := Compact(context.Background(), Preparation{
		FirstKeptEntryID: "keep",
		Messages:         protocol.MessageList{testUserMessage("history")},
		TurnPrefix:       protocol.MessageList{testUserMessage("prefix")},
		SplitTurn:        true,
		TokensBefore:     5000,
		FileOperations:   operations,
		Settings:         Settings{ReserveTokens: 1000},
	}, CompactOptions{
		Completer: recorder.Complete,
		Model:     protocol.Model{MaxTokens: 600},
		Reasoning: "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"history summary", "**Turn Context (split turn):**", "prefix summary", "<read-files>\nread.go", "<modified-files>\nchanged.go\nwritten.go"} {
		if !strings.Contains(result.Summary, fragment) {
			t.Fatalf("summary missing %q: %s", fragment, result.Summary)
		}
	}
	if len(result.Details.ReadFiles) != 1 || result.Details.ReadFiles[0] != "read.go" {
		t.Fatalf("read files = %#v", result.Details.ReadFiles)
	}
	requests := recorder.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %d", len(requests))
	}
	limits := map[int]bool{}
	for _, request := range requests {
		limits[request.MaxTokens] = true
		if request.Reasoning != "medium" {
			t.Fatalf("reasoning = %q", request.Reasoning)
		}
	}
	if !limits[600] || !limits[500] {
		t.Fatalf("max token limits = %#v", limits)
	}

	_, err = Compact(context.Background(), Preparation{}, CompactOptions{})
	requireOperationError(t, err, CodeInvalidSession, "compaction retained entry requires an id")
}

type completionRecorder struct {
	mu       sync.Mutex
	requests []CompleteRequest
	response protocol.AssistantMessage
	respond  func(CompleteRequest) protocol.AssistantMessage
}

func (r *completionRecorder) Complete(_ context.Context, request CompleteRequest) (protocol.AssistantMessage, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	respond := r.respond
	response := r.response
	r.mu.Unlock()
	if respond != nil {
		return respond(request), nil
	}
	return response, nil
}

func (r *completionRecorder) Requests() []CompleteRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]CompleteRequest(nil), r.requests...)
}

func appendSessionMessage(t *testing.T, current *session.Session, message protocol.AgentMessage) session.Entry {
	t.Helper()
	entry, err := current.AppendMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func testEntry(id, parentID string, entryType session.EntryType, message protocol.AgentMessage) session.Entry {
	return session.Entry{
		Type:      entryType,
		Version:   1,
		ID:        id,
		ParentID:  parentID,
		Timestamp: time.UnixMilli(1),
		Message:   message,
	}
}

func testUserMessage(text string) protocol.UserMessage {
	return protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent(text)},
		Timestamp: 1,
	}
}

func testAssistantMessage(text string, usage protocol.Usage) protocol.AssistantMessage {
	return protocol.AssistantMessage{
		Content:    protocol.ContentList{protocol.NewTextContent(text)},
		Usage:      usage,
		StopReason: protocol.StopReasonStop,
		Timestamp:  1,
	}
}

func testAssistantMessageWithStop(text string, stopReason protocol.StopReason, usage protocol.Usage) protocol.AssistantMessage {
	message := testAssistantMessage(text, usage)
	message.StopReason = stopReason
	return message
}

func testAssistantMessageWithError(stopReason protocol.StopReason, message string) protocol.AssistantMessage {
	return protocol.AssistantMessage{
		Content:      protocol.ContentList{},
		StopReason:   stopReason,
		ErrorMessage: message,
		Timestamp:    1,
	}
}

func testToolCallAssistant(id, name, path string) protocol.AssistantMessage {
	return protocol.AssistantMessage{
		Content:    protocol.ContentList{protocol.NewToolCallContent(id, name, json.RawMessage(`{"path":`+mustJSONString(path)+`}`))},
		StopReason: protocol.StopReasonToolUse,
		Timestamp:  1,
	}
}

func mustJSONString(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func requireOperationError(t *testing.T, err error, code ErrorCode, message string) {
	t.Helper()
	var operationErr *OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("error = %v, want OperationError", err)
	}
	if operationErr.Code != code || operationErr.Error() != message {
		t.Fatalf("operation error = %#v, message = %q", operationErr, operationErr.Error())
	}
}
