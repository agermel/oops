package agent

import (
	"context"
	"fmt"

	"oops/internal/config"
	"oops/internal/llm/ai/provider"

	"github.com/cloudwego/eino/components/model"
)

// newModel 创建 OpenAI 兼容的 ChatModel。
func newModel(ctx context.Context, cfg config.LLMConfig) (model.ToolCallingChatModel, error) {
	chatModel, err := provider.NewOpenAICompatibleModel(ctx, provider.OpenAICompatibleConfig{
		Model:   cfg.Model,
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model: %w", err)
	}
	return chatModel, nil
}
