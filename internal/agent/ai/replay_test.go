package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateProviderMessageSequenceAcceptsCompleteToolResults(t *testing.T) {
	messages := MessageList{
		UserMessage{Content: ContentList{NewTextContent("start")}},
		AssistantMessage{
			Content: ContentList{
				NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`)),
				NewToolCallContent("call_2", "read", json.RawMessage(`{"path":"b.md"}`)),
			},
			StopReason: StopReasonToolUse,
		},
		ToolResultMessage{ToolCallID: "call_2", ToolName: "read", Content: ContentList{NewTextContent("b")}},
		ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: ContentList{NewTextContent("a")}},
		UserMessage{Content: ContentList{NewTextContent("next")}},
	}

	if err := ValidateProviderMessageSequence(messages); err != nil {
		t.Fatalf("ValidateProviderMessageSequence() error = %v", err)
	}
}

func TestValidateProviderMessageSequenceRejectsInvalidToolResults(t *testing.T) {
	tests := []struct {
		name     string
		messages MessageList
		want     string
	}{
		{
			name: "missing",
			messages: MessageList{
				AssistantMessage{
					Content:    ContentList{NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`))},
					StopReason: StopReasonToolUse,
				},
				UserMessage{Content: ContentList{NewTextContent("next")}},
			},
			want: "need 1 tool results, got 0",
		},
		{
			name: "duplicate",
			messages: MessageList{
				AssistantMessage{
					Content:    ContentList{NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`))},
					StopReason: StopReasonToolUse,
				},
				ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: ContentList{NewTextContent("a")}},
				ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: ContentList{NewTextContent("again")}},
			},
			want: "duplicate tool result",
		},
		{
			name: "mismatch",
			messages: MessageList{
				AssistantMessage{
					Content:    ContentList{NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`))},
					StopReason: StopReasonToolUse,
				},
				ToolResultMessage{ToolCallID: "call_2", ToolName: "read", Content: ContentList{NewTextContent("b")}},
			},
			want: "does not match",
		},
		{
			name: "orphan",
			messages: MessageList{
				UserMessage{Content: ContentList{NewTextContent("start")}},
				ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: ContentList{NewTextContent("a")}},
			},
			want: "no preceding assistant tool call",
		},
		{
			name: "filtered assistant",
			messages: MessageList{
				AssistantMessage{
					Content:    ContentList{NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`))},
					StopReason: StopReasonAborted,
				},
				ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: ContentList{NewTextContent("late")}},
			},
			want: "no preceding assistant tool call",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProviderMessageSequence(tt.messages)
			if err == nil {
				t.Fatal("ValidateProviderMessageSequence() nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestAnalyzeProviderMessageSequenceClassifiesRecoverableTail(t *testing.T) {
	messages := MessageList{
		UserMessage{Content: ContentList{NewTextContent("start")}},
		AssistantMessage{
			Content: ContentList{
				NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`)),
				NewToolCallContent("call_2", "read", json.RawMessage(`{"path":"b.md"}`)),
			},
			StopReason: StopReasonToolUse,
		},
		ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: ContentList{NewTextContent("a")}},
	}

	analysis := AnalyzeProviderMessageSequence(messages)
	if analysis.Status != ProviderSequenceRecoverable {
		t.Fatalf("status = %s, want recoverable", analysis.Status)
	}
	if analysis.PendingAssistantIndex != 1 {
		t.Fatalf("pending assistant index = %d, want 1", analysis.PendingAssistantIndex)
	}
	if err := ValidateProviderMessageSequence(messages); err == nil {
		t.Fatal("ValidateProviderMessageSequence() nil error, want incomplete sequence error")
	}
}
