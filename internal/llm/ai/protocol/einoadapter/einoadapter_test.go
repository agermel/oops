package einoadapter

import (
	"encoding/json"
	"errors"
	"testing"

	"oops/internal/llm/ai/protocol"

	"github.com/cloudwego/eino/schema"
)

func TestFromEinoContextExtractsSystemPrompt(t *testing.T) {
	ctx, err := FromEinoContext([]*schema.Message{
		schema.SystemMessage("system A"),
		schema.SystemMessage("system B"),
		schema.UserMessage("hello"),
	})
	if err != nil {
		t.Fatalf("FromEinoContext() error = %v", err)
	}
	if ctx.SystemPrompt != "system A\n\nsystem B" {
		t.Fatalf("SystemPrompt = %q", ctx.SystemPrompt)
	}
	if len(ctx.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(ctx.Messages))
	}
}

func TestFromEinoMessageRejectsSystemRole(t *testing.T) {
	_, err := FromEinoMessage(schema.SystemMessage("system"))
	if !errors.Is(err, ErrSystemBoundary) {
		t.Fatalf("FromEinoMessage(system) error = %v, want ErrSystemBoundary", err)
	}
}

func TestEinoUserMultimodalRoundTrip(t *testing.T) {
	imageData := "aW1hZ2U="
	msg := &schema.Message{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeText, Text: "look"},
			{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{Base64Data: &imageData, MIMEType: "image/png"},
					Detail:            schema.ImageURLDetailHigh,
				},
			},
		},
	}
	converted, err := FromEinoMessage(msg)
	if err != nil {
		t.Fatalf("FromEinoMessage() error = %v", err)
	}
	back, err := ToEinoMessage(converted)
	if err != nil {
		t.Fatalf("ToEinoMessage() error = %v", err)
	}
	if back.Role != schema.User || len(back.UserInputMultiContent) != 2 {
		t.Fatalf("roundtrip user message = %#v", back)
	}
	if back.UserInputMultiContent[1].Image == nil || back.UserInputMultiContent[1].Image.MIMEType != "image/png" {
		t.Fatalf("roundtrip image = %#v", back.UserInputMultiContent[1].Image)
	}
}

func TestEinoAssistantRoundTripPreservesToolUsageAndReasoning(t *testing.T) {
	reasoning := 7
	msg := protocol.AssistantMessage{
		Content: protocol.ContentList{
			protocol.NewThinkingContent("considering"),
			protocol.NewTextContent("using tool"),
			protocol.NewToolCallContent("call_1", "lookup", json.RawMessage(`{"query":"pods"}`)),
		},
		Usage: protocol.Usage{
			Input:       11,
			Output:      13,
			CacheRead:   5,
			Reasoning:   &reasoning,
			TotalTokens: 24,
		},
		StopReason: protocol.StopReasonToolUse,
		Timestamp:  1700000000000,
	}

	einoMsg, err := ToEinoMessage(msg)
	if err != nil {
		t.Fatalf("ToEinoMessage() error = %v", err)
	}
	if einoMsg.Role != schema.Assistant {
		t.Fatalf("Role = %q, want assistant", einoMsg.Role)
	}
	if einoMsg.ResponseMeta == nil || einoMsg.ResponseMeta.FinishReason != "tool_calls" {
		t.Fatalf("ResponseMeta = %#v", einoMsg.ResponseMeta)
	}
	if einoMsg.ResponseMeta.Usage.PromptTokens != 11 ||
		einoMsg.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens != 7 ||
		einoMsg.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 5 {
		t.Fatalf("Usage = %#v", einoMsg.ResponseMeta.Usage)
	}
	if len(einoMsg.ToolCalls) != 1 || einoMsg.ToolCalls[0].Function.Arguments != `{"query":"pods"}` {
		t.Fatalf("ToolCalls = %#v", einoMsg.ToolCalls)
	}

	converted, err := FromEinoMessage(einoMsg)
	if err != nil {
		t.Fatalf("FromEinoMessage() error = %v", err)
	}
	assistant, ok := converted.(protocol.AssistantMessage)
	if !ok {
		t.Fatalf("converted = %T, want AssistantMessage", converted)
	}
	if assistant.StopReason != protocol.StopReasonToolUse {
		t.Fatalf("StopReason = %q", assistant.StopReason)
	}
	if assistant.Usage.Input != 11 || assistant.Usage.CacheRead != 5 || assistant.Usage.Reasoning == nil || *assistant.Usage.Reasoning != 7 {
		t.Fatalf("Usage = %#v", assistant.Usage)
	}
	if len(assistant.Content) != 3 {
		t.Fatalf("len(Content) = %d, want 3: %#v", len(assistant.Content), assistant.Content)
	}
	if thinking, ok := assistant.Content[0].(protocol.ThinkingContent); !ok || thinking.Thinking != "considering" {
		t.Fatalf("Content[0] = %#v, want thinking considering", assistant.Content[0])
	}
	if text, ok := assistant.Content[1].(protocol.TextContent); !ok || text.Text != "using tool" {
		t.Fatalf("Content[1] = %#v, want text using tool", assistant.Content[1])
	}
	if toolCall, ok := assistant.Content[2].(protocol.ToolCallContent); !ok || string(toolCall.Arguments) != `{"query":"pods"}` {
		t.Fatalf("Content[2] = %#v, want tool call", assistant.Content[2])
	}
}

func TestEinoToolResultRoundTrip(t *testing.T) {
	msg := protocol.ToolResultMessage{
		ToolCallID: "call_1",
		ToolName:   "lookup",
		Content:    protocol.ContentList{protocol.NewTextContent("found")},
	}
	einoMsg, err := ToEinoMessage(msg)
	if err != nil {
		t.Fatalf("ToEinoMessage() error = %v", err)
	}
	if einoMsg.Role != schema.Tool || einoMsg.ToolCallID != "call_1" || einoMsg.ToolName != "lookup" {
		t.Fatalf("eino tool message = %#v", einoMsg)
	}
	converted, err := FromEinoMessage(einoMsg)
	if err != nil {
		t.Fatalf("FromEinoMessage() error = %v", err)
	}
	toolResult, ok := converted.(protocol.ToolResultMessage)
	if !ok {
		t.Fatalf("converted = %T, want ToolResultMessage", converted)
	}
	if toolResult.ToolName != "lookup" {
		t.Fatalf("ToolName = %q, want lookup", toolResult.ToolName)
	}
}

func TestToEinoContextAddsSystemMessage(t *testing.T) {
	messages, err := ToEinoContext(protocol.Context{
		SystemPrompt: "system",
		Messages: protocol.MessageList{
			protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}},
		},
	})
	if err != nil {
		t.Fatalf("ToEinoContext() error = %v", err)
	}
	if len(messages) != 2 || messages[0].Role != schema.System || messages[1].Role != schema.User {
		t.Fatalf("messages = %#v", messages)
	}
}
