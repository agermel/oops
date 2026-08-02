package compaction

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/runtime/session"
)

func TestPrepareBranchEntriesAppliesBudgetAndPreservesSummaryDetails(t *testing.T) {
	details, err := json.Marshal(session.SummaryDetails{
		ReadFiles:     []string{"history-read.go"},
		ModifiedFiles: []string{"history-edit.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := []session.Entry{
		testEntry("old", "", session.EntryMessage, testUserMessage(strings.Repeat("o", 400))),
		{
			Type:      session.EntryBranchSummary,
			Version:   1,
			ID:        "summary",
			ParentID:  "old",
			Timestamp: time.UnixMilli(1),
			Summary:   "prior branch facts",
			Details:   details,
		},
		testEntry("call", "summary", session.EntryMessage, testToolCallAssistant("call-1", "read", "current.go")),
		testEntry("result", "call", session.EntryMessage, protocol.ToolResultMessage{
			ToolCallID: "call-1",
			ToolName:   "read",
			Content:    protocol.ContentList{protocol.NewTextContent("tool output")},
		}),
		testEntry("recent", "result", session.EntryMessage, testUserMessage("recent")),
	}
	prepared := PrepareBranchEntries(entries, 20)
	if len(prepared.Messages) == 0 {
		t.Fatal("branch preparation omitted all messages")
	}
	for _, message := range prepared.Messages {
		if message.MessageRole() == protocol.RoleToolResult {
			t.Fatal("tool result entered branch summary messages")
		}
	}
	if _, ok := prepared.FileOperations.Read["history-read.go"]; !ok {
		t.Fatal("nested branch read detail was not merged")
	}
	if _, ok := prepared.FileOperations.Edited["history-edit.go"]; !ok {
		t.Fatal("nested branch modified detail was not merged")
	}
	if _, ok := prepared.FileOperations.Read["current.go"]; !ok {
		t.Fatal("assistant file operation was not collected")
	}
	if strings.Contains(SerializeConversation(prepared.Messages), strings.Repeat("o", 100)) {
		t.Fatal("old message exceeded branch token budget")
	}
}

func TestGenerateBranchSummaryBuildsPromptAndDetails(t *testing.T) {
	entries := []session.Entry{
		testEntry("call", "", session.EntryMessage, testToolCallAssistant("call-1", "read", "read.go")),
		testEntry("user", "call", session.EntryMessage, testUserMessage("branch work")),
	}
	recorder := &completionRecorder{response: testAssistantMessage("## Goal\nbranch", protocol.Usage{})}
	result, err := GenerateBranchSummary(context.Background(), entries, BranchOptions{
		Completer:          recorder.Complete,
		Model:              protocol.Model{ContextWindow: 1000, MaxTokens: 100},
		CustomInstructions: "focus",
		ReserveTokens:      100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Summary, branchSummaryPreamble+"## Goal\nbranch") {
		t.Fatalf("summary = %q", result.Summary)
	}
	if len(result.ReadFiles) != 1 || result.ReadFiles[0] != "read.go" {
		t.Fatalf("read files = %#v", result.ReadFiles)
	}
	requests := recorder.Requests()
	if len(requests) != 1 || requests[0].MaxTokens != 2048 {
		t.Fatalf("requests = %#v", requests)
	}
	user, _ := protocol.AsUserMessage(requests[0].Context.Messages[0])
	if !strings.Contains(textFromContent(user.Content), "Additional focus: focus") {
		t.Fatalf("prompt = %s", textFromContent(user.Content))
	}
}

func TestGenerateBranchSummaryHandlesEmptyAndFailure(t *testing.T) {
	called := false
	empty, err := GenerateBranchSummary(context.Background(), nil, BranchOptions{
		Completer: func(context.Context, CompleteRequest) (protocol.AssistantMessage, error) {
			called = true
			return protocol.AssistantMessage{}, nil
		},
	})
	if err != nil || empty.Summary != "No content to summarize" || called {
		t.Fatalf("empty result = %#v, err = %v, called = %v", empty, err, called)
	}

	_, err = GenerateBranchSummary(context.Background(), []session.Entry{
		testEntry("user", "", session.EntryMessage, testUserMessage("work")),
	}, BranchOptions{
		Completer: func(context.Context, CompleteRequest) (protocol.AssistantMessage, error) {
			return testAssistantMessageWithError(protocol.StopReasonError, "boom"), nil
		},
		Model: protocol.Model{ContextWindow: 1000},
	})
	requireOperationError(t, err, CodeSummarizationFailed, "Branch summary failed: boom")
}
