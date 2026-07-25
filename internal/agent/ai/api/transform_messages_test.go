package api

import (
	"encoding/json"
	"testing"

	protocol "oops/internal/agent/ai"
)

func TestTransformMessagesReplacesUnsupportedImages(t *testing.T) {
	image := protocol.ImageContent{Type: protocol.ContentTypeImage, Data: "aW1hZ2U=", MIMEType: "image/png"}
	messages := protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{
			protocol.NewTextContent("before"), image, image,
			protocol.NewTextContent(nonVisionUserImagePlaceholder), image,
		}},
		protocol.ToolResultMessage{ToolCallID: "call_1", ToolName: "inspect", Content: protocol.ContentList{image, image}},
	}

	transformed := TransformMessages(messages, TransformMessagesOptions{})
	user := transformed[0].(protocol.UserMessage)
	if len(user.Content) != 3 || !textEquals(user.Content[1], nonVisionUserImagePlaceholder) || !textEquals(user.Content[2], nonVisionUserImagePlaceholder) {
		t.Fatalf("user content = %#v", user.Content)
	}
	tool := transformed[1].(protocol.ToolResultMessage)
	if len(tool.Content) != 1 || !textEquals(tool.Content[0], nonVisionToolImagePlaceholder) {
		t.Fatalf("tool content = %#v", tool.Content)
	}

	withImages := TransformMessages(messages, TransformMessagesOptions{SupportsImages: true})
	if got := len(withImages[0].(protocol.UserMessage).Content); got != 5 {
		t.Fatalf("image-aware user content count = %d, want 5", got)
	}
}

func TestTransformMessagesConvertsCrossModelReplayFieldsAndToolIDs(t *testing.T) {
	messages := protocol.MessageList{
		protocol.AssistantMessage{
			Provider: "source-provider",
			Model:    "source-model",
			Content: protocol.ContentList{
				protocol.ThinkingContent{Type: protocol.ContentTypeThinking, Thinking: "visible reasoning", ThinkingSignature: "signature"},
				protocol.ThinkingContent{Type: protocol.ContentTypeThinking, Thinking: "hidden", Redacted: true},
				protocol.ThinkingContent{Type: protocol.ContentTypeThinking, ThinkingSignature: "empty-signature"},
				protocol.TextContent{Type: protocol.ContentTypeText, Text: "answer", TextSignature: "text-signature"},
				protocol.ToolCallContent{Type: protocol.ContentTypeToolCall, ID: "old id", Name: "inspect", Arguments: json.RawMessage(`{"path":"a"}`), ThoughtSignature: "tool-signature"},
			},
			StopReason: protocol.StopReasonToolUse,
		},
		protocol.ToolResultMessage{ToolCallID: "old id", ToolName: "inspect", Content: protocol.ContentList{protocol.NewTextContent("ok")}},
	}

	transformed := TransformMessages(messages, TransformMessagesOptions{
		Provider: "target-provider",
		Model:    "target-model",
		NormalizeToolCallID: func(id string, _ protocol.AssistantMessage) string {
			return "normalized_" + id
		},
	})
	assistant := transformed[0].(protocol.AssistantMessage)
	if len(assistant.Content) != 3 {
		t.Fatalf("assistant content = %#v", assistant.Content)
	}
	if _, ok := assistant.Content[0].(protocol.ThinkingContent); ok || !textEquals(assistant.Content[0], "visible reasoning") {
		t.Fatalf("thinking conversion = %#v", assistant.Content[0])
	}
	text := assistant.Content[1].(protocol.TextContent)
	if text.TextSignature != "" {
		t.Fatalf("text signature = %q, want empty", text.TextSignature)
	}
	call := assistant.Content[2].(protocol.ToolCallContent)
	if call.ID != "normalized_old id" || call.ThoughtSignature != "" {
		t.Fatalf("tool call = %#v", call)
	}
	result := transformed[1].(protocol.ToolResultMessage)
	if result.ToolCallID != call.ID {
		t.Fatalf("tool result id = %q, want %q", result.ToolCallID, call.ID)
	}
}

func TestTransformMessagesPreservesSameModelReplayFields(t *testing.T) {
	message := protocol.AssistantMessage{
		Provider: "provider",
		Model:    "model",
		Content: protocol.ContentList{
			protocol.ThinkingContent{Type: protocol.ContentTypeThinking, ThinkingSignature: "signature"},
			protocol.ThinkingContent{Type: protocol.ContentTypeThinking, Thinking: "redacted", Redacted: true},
			protocol.TextContent{Type: protocol.ContentTypeText, Text: "answer", TextSignature: "text-signature"},
			protocol.ToolCallContent{Type: protocol.ContentTypeToolCall, ID: "call_1", Name: "inspect", Arguments: json.RawMessage(`{}`), ThoughtSignature: "tool-signature"},
		},
		StopReason: protocol.StopReasonToolUse,
	}

	transformed := TransformMessages(protocol.MessageList{message}, TransformMessagesOptions{
		Provider: "provider",
		Model:    "model",
		NormalizeToolCallID: func(string, protocol.AssistantMessage) string {
			return "unexpected"
		},
	})
	assistant := transformed[0].(protocol.AssistantMessage)
	if len(assistant.Content) != len(message.Content) {
		t.Fatalf("assistant content = %#v", assistant.Content)
	}
	if got := assistant.Content[0].(protocol.ThinkingContent).ThinkingSignature; got != "signature" {
		t.Fatalf("thinking signature = %q", got)
	}
	if got := assistant.Content[2].(protocol.TextContent).TextSignature; got != "text-signature" {
		t.Fatalf("text signature = %q", got)
	}
	if got := assistant.Content[3].(protocol.ToolCallContent); got.ID != "call_1" || got.ThoughtSignature != "tool-signature" {
		t.Fatalf("tool call = %#v", got)
	}
}

func TestTransformMessagesCompletesToolResultsAndSkipsIncompleteAssistants(t *testing.T) {
	messages := protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("start")}},
		protocol.AssistantMessage{
			Content: protocol.ContentList{
				protocol.NewToolCallContent("call_1", "inspect", json.RawMessage(`{"path":"a"}`)),
				protocol.NewToolCallContent("call_2", "inspect", json.RawMessage(`{"path":"b"}`)),
			},
			StopReason: protocol.StopReasonToolUse,
		},
		protocol.ToolResultMessage{ToolCallID: "call_1", ToolName: "inspect", Content: protocol.ContentList{protocol.NewTextContent("a")}},
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("continue")}},
		protocol.AssistantMessage{Content: protocol.ContentList{protocol.NewTextContent("partial")}, StopReason: protocol.StopReasonAborted},
	}

	transformed := TransformMessages(messages, TransformMessagesOptions{SupportsImages: true})
	if len(transformed) != 5 {
		t.Fatalf("message count = %d, want 5: %#v", len(transformed), transformed)
	}
	synthetic, ok := transformed[3].(protocol.ToolResultMessage)
	if !ok || synthetic.ToolCallID != "call_2" || synthetic.ToolName != "inspect" || !synthetic.IsError || !textEquals(synthetic.Content[0], missingToolResultText) {
		t.Fatalf("synthetic tool result = %#v", transformed[3])
	}
	if _, ok := transformed[4].(protocol.UserMessage); !ok {
		t.Fatalf("interrupted user message = %#v", transformed[4])
	}
	if err := protocol.ValidateProviderMessageSequence(transformed); err != nil {
		t.Fatalf("ValidateProviderMessageSequence() error = %v", err)
	}
}
