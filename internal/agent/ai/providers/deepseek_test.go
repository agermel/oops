package providers

import (
	"context"
	"errors"
	"testing"

	protocol "oops/internal/agent/ai"
)

func TestDeepSeekProviderReturnsModelDefaults(t *testing.T) {
	factory := func(context.Context, protocol.ProviderConfig) (protocol.StreamFunc, error) {
		return nil, nil
	}

	definition := DeepSeekProvider(factory)
	model, ok := definition.GetModel("deepseek-v4-flash")
	if !ok {
		t.Fatal("GetModel() did not find deepseek-v4-flash")
	}
	if definition.ID != DeepSeekID || definition.Name != "DeepSeek" {
		t.Fatalf("provider = %#v", definition)
	}
	if model.Provider != DeepSeekID || model.ID != "deepseek-v4-flash" {
		t.Fatalf("model identity = %#v", model)
	}
	if model.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("model BaseURL = %q", model.BaseURL)
	}
	if model.ContextWindow != 1_000_000 || model.MaxTokens != 384_000 {
		t.Fatalf("model limits = (%d, %d)", model.ContextWindow, model.MaxTokens)
	}
}

func TestDeepSeekProviderRejectsUnknownModel(t *testing.T) {
	factory := func(context.Context, protocol.ProviderConfig) (protocol.StreamFunc, error) {
		return nil, nil
	}

	_, _, err := DeepSeekProvider(factory).CreateStream(context.Background(), protocol.ProviderConfig{
		Provider: DeepSeekID,
		Model:    "deepseek-unknown",
	})
	if !errors.Is(err, protocol.ErrUnknownModel) {
		t.Fatalf("CreateStream() error = %v, want ErrUnknownModel", err)
	}
}

func TestDeepSeekProviderPassesResolvedConfigToAPI(t *testing.T) {
	var received protocol.ProviderConfig
	factory := func(_ context.Context, cfg protocol.ProviderConfig) (protocol.StreamFunc, error) {
		received = cfg
		return func(context.Context, protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			return nil, nil
		}, nil
	}

	_, resolved, err := DeepSeekProvider(factory).CreateStream(context.Background(), protocol.ProviderConfig{
		Provider:      DeepSeekID,
		Model:         "deepseek-v4-pro",
		APIKey:        "test-key",
		ContextWindow: 32_000,
	})
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if received.Provider != DeepSeekID || received.Model != "deepseek-v4-pro" || received.APIKey != "test-key" {
		t.Fatalf("factory identity config = %#v", received)
	}
	if received.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("factory BaseURL = %q", received.BaseURL)
	}
	if received.ContextWindow != 32_000 || received.MaxTokens != 384_000 {
		t.Fatalf("factory limits = (%d, %d)", received.ContextWindow, received.MaxTokens)
	}
	if received.BaseURL != resolved.BaseURL || received.ContextWindow != resolved.ContextWindow || received.MaxTokens != resolved.MaxTokens {
		t.Fatalf("factory config = %#v, resolved model = %#v", received, resolved)
	}
}

func TestDeepSeekProviderExposesCompleteModelMetadata(t *testing.T) {
	factory := func(context.Context, protocol.ProviderConfig) (protocol.StreamFunc, error) {
		return nil, nil
	}
	definition := DeepSeekProvider(factory)
	if len(definition.Models) != 2 {
		t.Fatalf("models count = %d, want 2", len(definition.Models))
	}
	for _, model := range definition.Models {
		if model.Provider != DeepSeekID || model.BaseURL != definition.BaseURL {
			t.Fatalf("model metadata = %#v", model)
		}
	}
	if definition.API == nil {
		t.Fatal("provider API is nil")
	}
	if definition.Auth.APIKey == nil {
		t.Fatal("provider API-key authentication is nil")
	}
}

func TestBuiltinProvidersReturnsOpenAICompatibleAndDeepSeek(t *testing.T) {
	factory := func(context.Context, protocol.ProviderConfig) (protocol.StreamFunc, error) {
		return nil, nil
	}
	registered := BuiltinProviders(factory)
	if len(registered) != 2 {
		t.Fatalf("providers count = %d, want 2", len(registered))
	}
	if registered[0].ID != protocol.DefaultProviderID || registered[1].ID != DeepSeekID {
		t.Fatalf("providers = %#v", registered)
	}
	models := BuiltinModels(factory, protocol.ModelsOptions{})
	if got := models.GetProviders(); len(got) != len(registered) {
		t.Fatalf("BuiltinModels() providers = %#v", got)
	}
}
