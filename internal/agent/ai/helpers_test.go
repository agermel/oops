package ai

import (
	"encoding/json"
	"testing"

	aiutils "oops/internal/agent/ai/utils"
)

func TestTextFromContentJoinsTextBlocks(t *testing.T) {
	content := ContentList{
		NewTextContent("before"),
		NewThinkingContent("hidden"),
		ImageContent{Type: ContentTypeImage, Data: "aW1hZ2U=", MIMEType: "image/png"},
		NewTextContent(" after"),
	}

	if got := TextFromContent(content); got != "before after" {
		t.Fatalf("TextFromContent() = %q, want %q", got, "before after")
	}
}

func TestToolCallsFromAssistantIncludesValueAndPointer(t *testing.T) {
	pointerCall := NewToolCallContent("call_2", "write", json.RawMessage(`{"path":"b.md"}`))
	message := AssistantMessage{Content: ContentList{
		NewTextContent("working"),
		NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"a.md"}`)),
		&pointerCall,
	}}

	calls := ToolCallsFromAssistant(message)
	if len(calls) != 2 {
		t.Fatalf("ToolCallsFromAssistant() count = %d, want 2", len(calls))
	}
	if calls[0].ID != "call_1" || calls[1].ID != "call_2" {
		t.Fatalf("ToolCallsFromAssistant() = %#v", calls)
	}
}

func TestEstimateProtocolTokens(t *testing.T) {
	if got := aiutils.EstimateTextTokens("12345"); got != 2 {
		t.Fatalf("EstimateTextTokens() = %d, want 2", got)
	}
	content := ContentList{
		NewTextContent("1234"),
		NewThinkingContent("12345"),
		ImageContent{Type: ContentTypeImage, Data: "aW1hZ2U=", MIMEType: "image/png"},
		NewToolCallContent("call_1", "read", json.RawMessage(`{}`)),
	}
	if got := EstimateContentTokens(content); got != 1204 {
		t.Fatalf("EstimateContentTokens() = %d, want 1204", got)
	}
	if got := EstimateMessageTokens(UserMessage{Content: content}); got != 1204 {
		t.Fatalf("EstimateMessageTokens() = %d, want 1204", got)
	}
}
