package providers

import protocol "oops/internal/agent/ai"

var deepSeekModels = []protocol.Model{
	{
		Provider:      DeepSeekID,
		ID:            "deepseek-v4-flash",
		BaseURL:       deepSeekBaseURL,
		ContextWindow: 1_000_000,
		MaxTokens:     384_000,
	},
	{
		Provider:      DeepSeekID,
		ID:            "deepseek-v4-pro",
		BaseURL:       deepSeekBaseURL,
		ContextWindow: 1_000_000,
		MaxTokens:     384_000,
	},
}
