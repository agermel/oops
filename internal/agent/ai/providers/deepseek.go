package providers

import (
	protocol "oops/internal/agent/ai"
	"oops/internal/agent/ai/auth"
)

const (
	DeepSeekID      = "deepseek"
	deepSeekBaseURL = "https://api.deepseek.com"
)

// DeepSeekProvider binds the DeepSeek model collection to the completion API.
func DeepSeekProvider(openAICompletion protocol.ProviderFactory) protocol.Provider {
	return protocol.Provider{
		ID:      DeepSeekID,
		Name:    "DeepSeek",
		BaseURL: deepSeekBaseURL,
		Models:  deepSeekModels,
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("DeepSeek API key", "DEEPSEEK_API_KEY"),
		},
		API: openAICompletion,
	}
}
