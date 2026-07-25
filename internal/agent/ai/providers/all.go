package providers

import (
	protocol "oops/internal/agent/ai"
	"oops/internal/agent/ai/auth"
)

// BuiltinProviders returns every built-in provider as a fresh value.
func BuiltinProviders(openAICompletion protocol.ProviderFactory) []protocol.Provider {
	return []protocol.Provider{
		{
			ID:   protocol.DefaultProviderID,
			Name: "OpenAI Compatible",
			Auth: auth.ProviderAuth{
				APIKey: auth.EnvAPIKeyAuth("OpenAI-compatible API key", "OOPS_LLM_API_KEY", "OPENAI_API_KEY"),
			},
			API: openAICompletion,
		},
		DeepSeekProvider(openAICompletion),
	}
}

// BuiltinModels creates a model collection with every built-in provider registered.
func BuiltinModels(openAICompletion protocol.ProviderFactory, options protocol.ModelsOptions) *protocol.Models {
	return protocol.NewModels(BuiltinProviders(openAICompletion), options)
}
