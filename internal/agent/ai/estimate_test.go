package ai

import "testing"

func TestEstimateContextTokensUsesLatestAssistantUsage(t *testing.T) {
	ctx := Context{
		SystemPrompt: "ignored by reported usage",
		Messages: MessageList{
			UserMessage{Content: ContentList{NewTextContent("first")}},
			AssistantMessage{
				Content:    ContentList{NewTextContent("answer")},
				Usage:      Usage{TotalTokens: 100},
				StopReason: StopReasonStop,
			},
			UserMessage{Content: ContentList{NewTextContent("12345678")}},
		},
	}
	if got := EstimateContextTokens(ctx); got != 102 {
		t.Fatalf("EstimateContextTokens() = %d, want 102", got)
	}
}

func TestEstimateContextTokensSkipsFailedUsage(t *testing.T) {
	ctx := Context{Messages: MessageList{
		UserMessage{Content: ContentList{NewTextContent("1234")}},
		AssistantMessage{
			Content:    ContentList{NewTextContent("failed")},
			Usage:      Usage{TotalTokens: 100},
			StopReason: StopReasonError,
		},
		UserMessage{Content: ContentList{NewTextContent("1234")}},
	}}
	if got := EstimateContextTokens(ctx); got != 4 {
		t.Fatalf("EstimateContextTokens() = %d, want 4", got)
	}
}

func TestEstimateContextTokensIncludesToolsWithoutUsage(t *testing.T) {
	ctx := Context{
		SystemPrompt: "1234",
		Messages: MessageList{
			UserMessage{Content: ContentList{NewTextContent("1234")}},
		},
		Tools: []ToolDefinition{{Name: "tool", Parameters: []byte(`{"type":"object"}`)}},
	}
	if got := EstimateContextTokens(ctx); got <= 2 {
		t.Fatalf("EstimateContextTokens() = %d, want system, message, and tool tokens", got)
	}
}

func TestEstimateContextTokensIncludesInvalidToolParameters(t *testing.T) {
	ctx := Context{
		SystemPrompt: "1234",
		Messages: MessageList{
			UserMessage{Content: ContentList{NewTextContent("1234")}},
		},
		Tools: []ToolDefinition{{Name: "tool", Parameters: []byte(`{`)}},
	}
	if got := EstimateContextTokens(ctx); got <= 2 {
		t.Fatalf("EstimateContextTokens() = %d, want system, message, and raw tool parameters", got)
	}
}
