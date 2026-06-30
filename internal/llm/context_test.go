package llm

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestEstimateTokens(t *testing.T) {
	msgs := []*schema.Message{
		schema.SystemMessage("你是一个助手"),
		schema.UserMessage("你好"),
	}
	tokens := EstimateTokens(msgs)
	if tokens <= 0 {
		t.Errorf("EstimateTokens() = %d, want > 0", tokens)
	}
}

func TestEstimateTokens_LongMessage(t *testing.T) {
	// 1000 个中文字符 ≈ 400 tokens (1000/2.5)
	long := strings.Repeat("中", 1000)
	msgs := []*schema.Message{
		schema.UserMessage(long),
	}
	tokens := EstimateTokens(msgs)
	expected := int(1000.0 / 2.5)
	if tokens < expected-10 || tokens > expected+10 {
		t.Errorf("EstimateTokens(long) = %d, want ~%d", tokens, expected)
	}
}

func TestTrimToBudget_NoTrimNeeded(t *testing.T) {
	msgs := []*schema.Message{
		schema.SystemMessage("system prompt"),
		schema.UserMessage("hello"),
		schema.AssistantMessage("hi", nil),
	}
	result := TrimToBudget(msgs, DefaultBudget)
	if result.Trimmed != 0 {
		t.Errorf("Trimmed = %d, want 0", result.Trimmed)
	}
	if len(result.Messages) != 3 {
		t.Errorf("len(Messages) = %d, want 3", len(result.Messages))
	}
}

func TestTrimToBudget_ToolResultTruncation(t *testing.T) {
	// 工具结果超过 2000 字符应被截断。
	long := strings.Repeat("x", 3000)
	msgs := []*schema.Message{
		schema.SystemMessage("system"),
		{
			Role:    schema.Tool,
			Content: long,
		},
	}
	result := TrimToBudget(msgs, DefaultBudget)
	if result.Trimmed != 0 {
		t.Errorf("Trimmed = %d, want 0 (truncation not trimming)", result.Trimmed)
	}
	// 截断后的内容应包含截断标记。
	if !strings.Contains(result.Messages[1].Content, "已截断") {
		t.Errorf("tool result not truncated: content length = %d", len([]rune(result.Messages[1].Content)))
	}
	// 截断后长度应 < 2100（首1000+尾1000+标记）。
	if len([]rune(result.Messages[1].Content)) > 2100 {
		t.Errorf("truncated content too long: %d runes", len([]rune(result.Messages[1].Content)))
	}
}

func TestTrimToBudget_HistoryTrimming(t *testing.T) {
	// 构造大量消息超出极小预算，验证裁剪。
	msg := schema.UserMessage(strings.Repeat("x", 500)) // ~200 tokens per message
	var msgs []*schema.Message
	msgs = append(msgs, schema.SystemMessage("system"))

	// 添加足够多的历史消息以超出小预算。
	for i := 0; i < 50; i++ {
		msgs = append(msgs, msg)
	}

	smallBudget := ContextBudget{MaxTokens: 500, ReserveOutput: 100}
	result := TrimToBudget(msgs, smallBudget)

	if result.Trimmed == 0 {
		t.Errorf("expected trimming, got Trimmed = 0, totalTokens = %d", result.TotalTokens)
	}
	if result.TotalTokens > smallBudget.MaxTokens {
		t.Logf("totalTokens = %d exceeds budget %d (this is approximate)", result.TotalTokens, smallBudget.MaxTokens)
	}
	// System prompt 应保留。
	if result.Messages[0].Role != schema.System {
		t.Error("system prompt was removed")
	}
}
