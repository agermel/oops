package session

import (
	"encoding/json"
	"strings"
	"testing"

	protocol "oops/internal/agent/ai"
)

func TestValidateMessageSequenceAcceptsCompleteToolResults(t *testing.T) {
	messages := protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("start")}},
		protocol.AssistantMessage{
			Content: protocol.ContentList{
				protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`)),
				protocol.NewToolCallContent("call_2", "read", json.RawMessage(`{"path":"b.md"}`)),
			},
			StopReason: protocol.StopReasonToolUse,
		},
		protocol.ToolResultMessage{ToolCallID: "call_2", ToolName: "read", Content: protocol.ContentList{protocol.NewTextContent("b")}},
		protocol.ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: protocol.ContentList{protocol.NewTextContent("a")}},
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("next")}},
	}

	if err := validateMessageSequence(messages); err != nil {
		t.Fatalf("validateMessageSequence() error = %v", err)
	}
}

func TestValidateMessageSequenceRejectsInvalidToolResults(t *testing.T) {
	tests := []struct {
		name     string
		messages protocol.MessageList
		want     string
	}{
		{
			name: "missing",
			messages: protocol.MessageList{
				protocol.AssistantMessage{Content: protocol.ContentList{protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`))}, StopReason: protocol.StopReasonToolUse},
				protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("next")}},
			},
			want: "need 1 tool results, got 0",
		},
		{
			name: "duplicate",
			messages: protocol.MessageList{
				protocol.AssistantMessage{Content: protocol.ContentList{protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`))}, StopReason: protocol.StopReasonToolUse},
				protocol.ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: protocol.ContentList{protocol.NewTextContent("a")}},
				protocol.ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: protocol.ContentList{protocol.NewTextContent("again")}},
			},
			want: "duplicate tool result",
		},
		{
			name: "mismatch",
			messages: protocol.MessageList{
				protocol.AssistantMessage{Content: protocol.ContentList{protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`))}, StopReason: protocol.StopReasonToolUse},
				protocol.ToolResultMessage{ToolCallID: "call_2", ToolName: "read", Content: protocol.ContentList{protocol.NewTextContent("b")}},
			},
			want: "does not match",
		},
		{
			name: "orphan",
			messages: protocol.MessageList{
				protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("start")}},
				protocol.ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: protocol.ContentList{protocol.NewTextContent("a")}},
			},
			want: "no preceding assistant tool call",
		},
		{
			name: "filtered assistant",
			messages: protocol.MessageList{
				protocol.AssistantMessage{Content: protocol.ContentList{protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`))}, StopReason: protocol.StopReasonAborted},
				protocol.ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: protocol.ContentList{protocol.NewTextContent("late")}},
			},
			want: "no preceding assistant tool call",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMessageSequence(tt.messages)
			if err == nil {
				t.Fatal("validateMessageSequence() nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestAnalyzeMessageSequenceClassifiesIncompleteTail(t *testing.T) {
	messages := protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("start")}},
		protocol.AssistantMessage{
			Content: protocol.ContentList{
				protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`)),
				protocol.NewToolCallContent("call_2", "read", json.RawMessage(`{"path":"b.md"}`)),
			},
			StopReason: protocol.StopReasonToolUse,
		},
		protocol.ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: protocol.ContentList{protocol.NewTextContent("a")}},
	}

	analysis := analyzeMessageSequence(messages)
	if analysis.status != messageSequenceIncomplete {
		t.Fatalf("status = %s, want incomplete", analysis.status)
	}
	if analysis.pendingAssistantIndex != 1 {
		t.Fatalf("pending assistant index = %d, want 1", analysis.pendingAssistantIndex)
	}
	if err := validateMessageSequence(messages); err == nil {
		t.Fatal("validateMessageSequence() nil error, want incomplete sequence error")
	}
}
