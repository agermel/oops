package api

import (
	"testing"

	protocol "oops/internal/agent/ai"

	"github.com/cloudwego/eino/components/model"
)

func TestClampMaxTokensToContext(t *testing.T) {
	ctx := protocol.Context{Messages: protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("12345678")}},
	}}
	if got := ClampMaxTokensToContext(5098, ctx, 2000); got != 1000 {
		t.Fatalf("ClampMaxTokensToContext() = %d, want 1000", got)
	}
	if got := ClampMaxTokensToContext(0, ctx, 2000); got != 2000 {
		t.Fatalf("ClampMaxTokensToContext() = %d, want 2000", got)
	}
}

func TestBuildBaseOptions(t *testing.T) {
	temperature := float32(0.2)
	options := BuildBaseOptions(Options{
		ContextWindow: 5098,
		MaxTokens:     2000,
		Temperature:   &temperature,
	}, protocol.Context{Messages: protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("12345678")}},
	}})
	resolved := model.GetCommonOptions(nil, options...)
	if resolved.MaxTokens == nil || *resolved.MaxTokens != 1000 {
		t.Fatalf("MaxTokens = %#v, want 1000", resolved.MaxTokens)
	}
	if resolved.Temperature == nil || *resolved.Temperature != temperature {
		t.Fatalf("Temperature = %#v, want %f", resolved.Temperature, temperature)
	}
}

func TestClampReasoning(t *testing.T) {
	if got := ClampReasoning(ThinkingLevelXHigh); got != ThinkingLevelHigh {
		t.Fatalf("ClampReasoning(xhigh) = %q, want high", got)
	}
	if got := ClampReasoning(ThinkingLevelMedium); got != ThinkingLevelMedium {
		t.Fatalf("ClampReasoning(medium) = %q, want medium", got)
	}
}
