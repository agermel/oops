package llm

import (
	"context"
	"fmt"
	"time"

	"oops/internal/logutil"

	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// ContextBuilder 封装上下文构建逻辑。
// BuildContext() → compactIfNeeded() → 注入 compaction 摘要 → 组装最终消息列表。
type ContextBuilder struct {
	store      *SessionStore
	skillStore *SkillStore
	llmClient  *Client // 可选：用于 compaction LLM 摘要（nil 则使用确定性拼接）

	// Compaction 配置。
	MaxTokens    int  // 触发压缩的 token 阈值（默认 48000 = 64000 * 0.75）
	KeepRecent   int  // 始终保留的最近消息数（默认 8）
	LLMSummarize bool // 是否使用 LLM 生成摘要（默认 true，但需要 llmClient 可用）
}

// DefaultCompactionConfig 返回默认压缩配置。
func DefaultCompactionConfig() (maxTokens int, keepRecent int) {
	return int(float64(DefaultBudget.MaxTokens) * 0.75), 8
}

// BuildOptions 是 Build() 的输入参数。
type BuildOptions struct {
	Session  *Session
	Question string
}

// BuildResult 是 Build() 的返回值。
type BuildResult struct {
	Messages   []*schema.Message // 最终消息列表（system + history + summary + question）
	Compaction *SessionEntry     // 本次触发的压缩（nil 表示无需压缩）
	Tokens     int               // 估算 token 数
	Trimmed    int               // 被裁剪的消息数（trim 回退时使用）
}

// NewContextBuilder 创建上下文构建器。
func NewContextBuilder(store *SessionStore, skillStore *SkillStore, llmClient *Client) *ContextBuilder {
	maxTokens, keepRecent := DefaultCompactionConfig()
	return &ContextBuilder{
		store:        store,
		skillStore:   skillStore,
		llmClient:    llmClient,
		MaxTokens:    maxTokens,
		KeepRecent:   keepRecent,
		LLMSummarize: true,
	}
}

// Build 构建完整的 LLM 上下文消息列表。
//
// 流程：
//  1. 从 session DAG 重建历史消息（BuildContext 处理已有 compaction）
//  2. 添加当前用户问题
//  3. 估算 token → 若超出阈值则触发 CompactIfNeeded
//  4. 若 LLM 可用则改进摘要 → 持久化 compaction 到 store
//  5. 重新 BuildContext（注入新的 compaction 摘要）
//  6. 组装 system prompt
//  7. 返回最终消息列表
func (cb *ContextBuilder) Build(ctx context.Context, opts BuildOptions) BuildResult {
	// 1. 从 DAG 重建历史消息。
	historyMsgs := cb.store.BuildContext(opts.Session.ID)

	// 2. 添加当前用户问题。
	userMsg := schema.UserMessage(opts.Question)
	currentMsgs := append(append([]*schema.Message{}, historyMsgs...), userMsg)

	// 3. 估算 token，必要时触发压缩。
	tokens := EstimateTokens(currentMsgs)
	var compaction *SessionEntry

	if tokens > cb.MaxTokens {
		logutil.Infof("context: tokens %d > threshold %d, checking compaction",
			tokens, cb.MaxTokens)

		compaction = cb.store.CompactIfNeeded(opts.Session.ID, cb.MaxTokens, cb.KeepRecent)

		if compaction != nil {
			// 若 llmClient 可用且启用 LLM 摘要，用 LLM 改进摘要内容。
			if cb.LLMSummarize && cb.llmClient != nil {
				if improved, err := cb.llmSummarize(ctx, opts.Session.ID, compaction); err == nil {
					compaction.Summary = improved
				} else {
					logutil.Warn("context: llm summarization failed, using deterministic summary",
						zap.Error(err))
				}
			}

			// 持久化 compaction 到 store（加入 DAG + 写入 JSONL）。
			cb.store.AppendCompaction(opts.Session.ID, compaction)

			// 压缩后重建上下文（BuildContext 会看到新的 compaction 并注入摘要）。
			historyMsgs = cb.store.BuildContext(opts.Session.ID)
			currentMsgs = append(append([]*schema.Message{}, historyMsgs...), userMsg)
			tokens = EstimateTokens(currentMsgs)
		} else {
			// 压缩未触发（消息数不足），回退到 TrimToBudget。
			logutil.Info("context: compaction not triggered, falling back to TrimToBudget")
			trimResult := TrimToBudget(currentMsgs, DefaultBudget)
			return BuildResult{
				Messages: cb.prependSystemPrompt(trimResult.Messages),
				Tokens:   trimResult.TotalTokens,
				Trimmed:  trimResult.Trimmed,
			}
		}
	}

	// 4. 组装最终消息列表：system prompt + history + question。
	return BuildResult{
		Messages:   cb.prependSystemPrompt(currentMsgs),
		Compaction: compaction,
		Tokens:     tokens,
	}
}

// prependSystemPrompt 在消息列表前插入 system prompt。
func (cb *ContextBuilder) prependSystemPrompt(msgs []*schema.Message) []*schema.Message {
	prompt := BasePrompt

	if cb.skillStore != nil {
		available := cb.skillStore.RenderAvailable()
		if available != "" {
			prompt = prompt + "\n\n" + available
		}
	}

	systemMsg := schema.SystemMessage(prompt)
	return append([]*schema.Message{systemMsg}, msgs...)
}

// llmSummarize 使用 LLM 生成对话摘要。
// sessionID 用于从 store 获取当前叶节点路径以收集被摘要的消息内容。
func (cb *ContextBuilder) llmSummarize(ctx context.Context, sessionID string, entry *SessionEntry) (string, error) {
	if cb.llmClient == nil {
		return "", fmt.Errorf("llm client not available")
	}

	// 获取当前叶节点路径（压缩前的完整消息历史）。
	path := cb.store.pathToLeaf(sessionID)

	// 收集将被摘要的消息内容（firstKeptEntryID 之前的消息）。
	var summarized []string
	foundFirstKept := false
	for _, e := range path {
		if e.ID == entry.FirstKeptEntryID {
			foundFirstKept = true
			break
		}
		if e.Type == EntryMessage && e.Message != nil {
			content := e.Message.Content
			if len([]rune(content)) > 500 {
				content = string([]rune(content)[:500]) + "..."
			}
			summarized = append(summarized,
				fmt.Sprintf("[%s]: %s", e.Message.Role, content))
		}
	}
	_ = foundFirstKept // 即使没找到也继续用已有内容

	if len(summarized) == 0 {
		return entry.Summary, nil // 无内容可摘要，保留确定性版本
	}

	// 构造摘要 prompt。
	summarizePrompt := fmt.Sprintf(
		`请用200-400字总结以下对话的关键发现、用户意图和已执行的操作：

%s

要求：
- 聚焦关键发现和已执行操作的结果
- 保留用户明确表达的偏好和约束
- 使用中文`,
		joinLines(summarized, "\n"),
	)

	summaryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	summaryMsgs := []*schema.Message{
		schema.SystemMessage("你是一个对话摘要助手。请简洁准确地总结对话内容。"),
		schema.UserMessage(summarizePrompt),
	}

	result, err := cb.llmClient.Complete(summaryCtx, summaryMsgs)
	if err != nil {
		return "", fmt.Errorf("llm complete: %w", err)
	}

	return result, nil
}
