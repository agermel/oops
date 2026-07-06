package budget

import (
	"strconv"

	"github.com/cloudwego/eino/schema"
)

// ContextBudget 定义上下文窗口的 token 预算。
type ContextBudget struct {
	MaxTokens     int // 最大输入 token 数（默认 64000，留给输出 8K 空间用于 deepseek 等）
	ReserveOutput int // 留给输出的 token 数，未直接使用但在预算计算中考虑
}

// DefaultBudget 是默认上下文预算。
var DefaultBudget = ContextBudget{
	MaxTokens:     64000,
	ReserveOutput: 8192,
}

// TrimResult 是裁剪操作的结果。
type TrimResult struct {
	Messages    []*schema.Message // 裁剪后的消息列表
	TotalTokens int               // 裁剪后的估算 token 数
	Trimmed     int               // 被裁剪的消息数
}

// EstimateTokens 粗略估算消息列表的 token 数。
// 使用字符数 / 2.5 的简单近似（适用于中英文混合）。
func EstimateTokens(msgs []*schema.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMessageTokens(m)
	}
	return total
}

// estimateMessageTokens 估算单条消息的 token 数。
func estimateMessageTokens(msg *schema.Message) int {
	n := len([]rune(msg.Content))
	// 字符数 / 2.5 ≈ token 数（中英文混合的粗略近似）
	tokens := int(float64(n) / 2.5)
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// TrimToBudget 将消息列表裁剪到 token 预算以内。
// 保留 system prompt（第一条），从旧到新移除历史消息，保留最近 N 轮。
// 工具结果超过 2000 字符时截断。
func TrimToBudget(msgs []*schema.Message, budget ContextBudget) TrimResult {
	if budget.MaxTokens <= 0 {
		budget = DefaultBudget
	}

	// 第一步：单条工具结果截断。
	for _, m := range msgs {
		if m.Role == schema.Tool {
			runes := []rune(m.Content)
			if len(runes) > 2000 {
				// 保留首 1000 + 尾 1000 字符。
				head := string(runes[:1000])
				tail := string(runes[len(runes)-1000:])
				m.Content = head + "\n...[已截断 " + strconv.Itoa(len(runes)-2000) + " 字符]...\n" + tail
			}
		}
	}

	total := EstimateTokens(msgs)
	if total <= budget.MaxTokens {
		return TrimResult{Messages: msgs, TotalTokens: total, Trimmed: 0}
	}

	// 第二步：从旧到新移除历史消息（保留 system prompt）。
	// system prompt 始终是第一条。
	result := make([]*schema.Message, len(msgs))
	copy(result, msgs)

	sysIdx := -1
	for i, m := range result {
		if m.Role == schema.System {
			sysIdx = i
			break
		}
	}

	trimmed := 0
	// 从 sysIdx+1 开始移除，即移除 system prompt 之后最早的消息。
	for total > budget.MaxTokens {
		start := sysIdx + 1
		if start >= len(result) {
			break
		}

		// 移除结果中第一条非 system 消息。
		removed := result[start]
		total -= estimateMessageTokens(removed)
		result = append(result[:start], result[start+1:]...)
		trimmed++
	}

	// 第三步：如果仍然超预算（极少情况），插入省略提示。
	if trimmed > 0 {
		summary := schema.SystemMessage("（之前的 " + strconv.Itoa(trimmed) + " 条历史消息因超出上下文限制已被省略）")
		// 插入到 system prompt 之后。
		if sysIdx >= 0 && sysIdx < len(result) {
			result = append(result[:sysIdx+1], append([]*schema.Message{summary}, result[sysIdx+1:]...)...)
		}
	}

	return TrimResult{
		Messages:    result,
		TotalTokens: EstimateTokens(result),
		Trimmed:     trimmed,
	}
}
