package ai_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/ai/api"
	"oops/internal/agent/ai/auth"
	"oops/internal/agent/ai/providers"
)

func TestModelsCreatesStreamForResolvedProvider(t *testing.T) {
	called := false
	models := newModels([]protocol.Provider{{
		ID:   "test",
		Name: "Test",
		API: func(_ context.Context, cfg protocol.ProviderConfig) (protocol.StreamFunc, error) {
			called = cfg.Provider == "test" && cfg.Model == "test-model"
			return func(context.Context, protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
				return protocol.NewAssistantMessageEventStream(0), nil
			}, nil
		},
	}}, protocol.ModelsOptions{})

	stream, resolved, err := models.CreateStream(protocol.ProviderConfig{Provider: "test", Model: "test-model"})
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if stream == nil || called {
		t.Fatalf("CreateStream() stream = %v, factory called = %v", stream, called)
	}
	if _, err := stream(context.Background(), protocol.StreamRequest{}); err != nil {
		t.Fatalf("stream() error = %v", err)
	}
	if !called {
		t.Fatal("factory was not called for stream request")
	}
	if resolved.Provider != "test" || resolved.ID != "test-model" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestModelsRejectsUnknownProvider(t *testing.T) {
	_, _, err := newModels(nil, protocol.ModelsOptions{}).CreateStream(protocol.ProviderConfig{Provider: "unknown"})
	if !errors.Is(err, protocol.ErrUnknownProvider) {
		t.Fatalf("CreateStream() error = %v, want ErrUnknownProvider", err)
	}
}

func TestModelsCatalogReads(t *testing.T) {
	model := protocol.Model{Provider: "test", ID: "test-model"}
	models := newModels([]protocol.Provider{{ID: "test", Name: "Test", Models: []protocol.Model{model}}}, protocol.ModelsOptions{})

	providers := models.GetProviders()
	if len(providers) != 1 || providers[0].ID != "test" {
		t.Fatalf("GetProviders() = %#v", providers)
	}
	provider, ok := models.GetProvider("TEST")
	if !ok || provider.ID != "test" {
		t.Fatalf("GetProvider() = %#v, %v", provider, ok)
	}
	if got := models.GetModels("test"); len(got) != 1 || got[0] != model {
		t.Fatalf("GetModels(\"test\") = %#v", got)
	}
	if got := models.GetModels(""); len(got) != 1 || got[0] != model {
		t.Fatalf("GetModels(\"\") = %#v", got)
	}
	got, ok := models.GetModel("test", "test-model")
	if !ok || got != model {
		t.Fatalf("GetModel() = %#v, %v", got, ok)
	}
}

func TestModelsResolvesAPIKeySources(t *testing.T) {
	providerAuth := auth.ProviderAuth{APIKey: auth.EnvAPIKeyAuth("Test API key", "TEST_PROVIDER_API_KEY")}
	t.Run("configured key", func(t *testing.T) {
		store := auth.NewInMemoryCredentialStore()
		if _, err := store.Modify(context.Background(), "test", func(context.Context, auth.Credential) (auth.Credential, error) {
			return auth.APIKeyCredential{Key: "stored-key"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		got := invokeModelStream(t, providerAuth, store, protocol.ProviderConfig{APIKey: "configured-key"})
		if got.APIKey != "configured-key" {
			t.Fatalf("APIKey = %q", got.APIKey)
		}
	})
	t.Run("stored credential", func(t *testing.T) {
		store := auth.NewInMemoryCredentialStore()
		if _, err := store.Modify(context.Background(), "test", func(context.Context, auth.Credential) (auth.Credential, error) {
			return auth.APIKeyCredential{Key: "stored-key"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		got := invokeModelStream(t, providerAuth, store, protocol.ProviderConfig{})
		if got.APIKey != "stored-key" {
			t.Fatalf("APIKey = %q", got.APIKey)
		}
	})
	t.Run("environment", func(t *testing.T) {
		t.Setenv("TEST_PROVIDER_API_KEY", "environment-key")
		got := invokeModelStream(t, providerAuth, auth.NewInMemoryCredentialStore(), protocol.ProviderConfig{})
		if got.APIKey != "environment-key" {
			t.Fatalf("APIKey = %q", got.APIKey)
		}
	})
}

func TestModelsResolvesStoredAuthenticationForEachRequest(t *testing.T) {
	store := auth.NewInMemoryCredentialStore()
	if _, err := store.Modify(context.Background(), "test", func(context.Context, auth.Credential) (auth.Credential, error) {
		return auth.APIKeyCredential{Key: "first-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	var received []string
	models := newModels([]protocol.Provider{{
		ID:   "test",
		Name: "Test",
		Auth: auth.ProviderAuth{APIKey: auth.EnvAPIKeyAuth("Test API key", "TEST_PROVIDER_API_KEY")},
		API: func(_ context.Context, cfg protocol.ProviderConfig) (protocol.StreamFunc, error) {
			received = append(received, cfg.APIKey)
			return func(context.Context, protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
				return nil, nil
			}, nil
		},
	}}, protocol.ModelsOptions{Credentials: store})

	stream, _, err := models.CreateStream(protocol.ProviderConfig{Provider: "test", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream(context.Background(), protocol.StreamRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Modify(context.Background(), "test", func(context.Context, auth.Credential) (auth.Credential, error) {
		return auth.APIKeyCredential{Key: "second-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream(context.Background(), protocol.StreamRequest{}); err != nil {
		t.Fatal(err)
	}

	if len(received) != 2 || received[0] != "first-key" || received[1] != "second-key" {
		t.Fatalf("resolved API keys = %#v", received)
	}
}

func TestModelsGetAuthExposesSource(t *testing.T) {
	t.Setenv("TEST_PROVIDER_API_KEY", "environment-key")
	models := newModels([]protocol.Provider{{
		ID:   "test",
		Name: "Test",
		Auth: auth.ProviderAuth{APIKey: auth.EnvAPIKeyAuth("Test API key", "TEST_PROVIDER_API_KEY")},
	}}, protocol.ModelsOptions{Credentials: auth.NewInMemoryCredentialStore()})
	resolved, err := models.GetAuth(context.Background(), protocol.ProviderConfig{Provider: "test", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.Auth.APIKey != "environment-key" || resolved.Source != "TEST_PROVIDER_API_KEY" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestModelsUsesDeepSeekEnvironmentKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-environment-key")
	var received protocol.ProviderConfig
	models := newModels([]protocol.Provider{providers.DeepSeekProvider(func(_ context.Context, config protocol.ProviderConfig) (protocol.StreamFunc, error) {
		received = config
		return func(context.Context, protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
			return protocol.NewAssistantMessageEventStream(0), nil
		}, nil
	})}, protocol.ModelsOptions{})
	stream, _, err := models.CreateStream(protocol.ProviderConfig{
		Provider: providers.DeepSeekID,
		Model:    "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream(context.Background(), protocol.StreamRequest{}); err != nil {
		t.Fatal(err)
	}
	if received.APIKey != "deepseek-environment-key" {
		t.Fatalf("APIKey = %q", received.APIKey)
	}
}

func TestModelsMergesResolvedRequestAuthentication(t *testing.T) {
	providerAuth := auth.ProviderAuth{APIKey: &auth.APIKeyAuth{
		Name: "Test API key",
		Resolve: func(context.Context, auth.APIKeyResolveInput) (auth.AuthResult, bool, error) {
			return auth.AuthResult{
				Auth: auth.ModelAuth{
					APIKey:  "resolved-key",
					BaseURL: "https://resolved.example",
					Headers: map[string]string{"Authorization": "Bearer resolved", "X-Resolved": "yes"},
				},
				Env: map[string]string{"resolved": "yes"},
			}, true, nil
		},
	}}
	got := invokeModelStream(t, providerAuth, auth.NewInMemoryCredentialStore(), protocol.ProviderConfig{
		APIKey:  "configured-key",
		Headers: map[string]string{"Authorization": "Bearer configured", "X-Configured": "yes"},
		Env:     map[string]string{"resolved": "configured", "configured": "yes"},
	})
	if got.APIKey != "configured-key" || got.BaseURL != "https://resolved.example" {
		t.Fatalf("request config = %#v", got)
	}
	if got.Headers["Authorization"] != "Bearer configured" || got.Headers["X-Resolved"] != "yes" || got.Headers["X-Configured"] != "yes" {
		t.Fatalf("headers = %#v", got.Headers)
	}
	if got.Env["resolved"] != "configured" || got.Env["configured"] != "yes" {
		t.Fatalf("environment = %#v", got.Env)
	}
}

func TestModelsMergesAuthorizationHeadersCaseInsensitively(t *testing.T) {
	providerAuth := auth.ProviderAuth{APIKey: &auth.APIKeyAuth{
		Name: "Test API key",
		Resolve: func(context.Context, auth.APIKeyResolveInput) (auth.AuthResult, bool, error) {
			return auth.AuthResult{Auth: auth.ModelAuth{Headers: map[string]string{"authorization": "Bearer resolved"}}}, true, nil
		},
	}}
	got := invokeModelStream(t, providerAuth, auth.NewInMemoryCredentialStore(), protocol.ProviderConfig{
		Headers: map[string]string{"Authorization": "Bearer configured"},
	})
	if len(got.Headers) != 1 || got.Headers["Authorization"] != "Bearer configured" {
		t.Fatalf("headers = %#v", got.Headers)
	}
}

func TestBuiltinModelsCreateStreamWithDefaultProvider(t *testing.T) {
	stream, resolved, err := builtinModels(protocol.ModelsOptions{}).CreateStream(protocol.ProviderConfig{
		Model:   "gpt-4o-mini",
		APIKey:  "test-key",
		BaseURL: "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if stream == nil {
		t.Fatal("CreateStream() stream is nil")
	}
	if resolved.Provider != protocol.DefaultProviderID {
		t.Fatalf("provider = %q, want %q", resolved.Provider, protocol.DefaultProviderID)
	}
}

func TestBuiltinModelsCreateDeepSeekStream(t *testing.T) {
	stream, resolved, err := builtinModels(protocol.ModelsOptions{}).CreateStream(protocol.ProviderConfig{
		Provider: providers.DeepSeekID,
		Model:    "deepseek-v4-flash",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if stream == nil {
		t.Fatal("CreateStream() stream is nil")
	}
	if resolved.Provider != providers.DeepSeekID || resolved.ID != "deepseek-v4-flash" {
		t.Fatalf("resolved identity = %#v", resolved)
	}
	if resolved.BaseURL != "https://api.deepseek.com" || resolved.ContextWindow != 1_000_000 || resolved.MaxTokens != 384_000 {
		t.Fatalf("resolved model = %#v", resolved)
	}
}

func TestBuiltinModelsPreserveCustomAuthorizationHeader(t *testing.T) {
	const wantAuthorization = "Token custom-credential"
	receivedAuthorization := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		receivedAuthorization <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	stream, _, err := builtinModels(protocol.ModelsOptions{}).CreateStream(protocol.ProviderConfig{
		Model:   "test-model",
		APIKey:  "fallback-api-key",
		BaseURL: server.URL,
		Headers: map[string]string{"aUtHoRiZaTiOn": wantAuthorization},
	})
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	requestStream, err := stream(context.Background(), protocol.StreamRequest{Context: protocol.Context{
		Messages: protocol.MessageList{
			protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}},
		},
	}})
	if err != nil {
		t.Fatalf("stream() error = %v", err)
	}
	drainStream(t, requestStream)

	if got := <-receivedAuthorization; got != wantAuthorization {
		t.Fatalf("Authorization = %q, want %q", got, wantAuthorization)
	}
}

func newModels(providers []protocol.Provider, options protocol.ModelsOptions) *protocol.Models {
	return protocol.NewModels(providers, options)
}

func builtinModels(options protocol.ModelsOptions) *protocol.Models {
	return providers.BuiltinModels(api.OpenAICompletionFactory, options)
}

func invokeModelStream(t *testing.T, providerAuth auth.ProviderAuth, credentials auth.CredentialStore, cfg protocol.ProviderConfig) protocol.ProviderConfig {
	t.Helper()
	if cfg.Provider == "" {
		cfg.Provider = "test"
	}
	var received protocol.ProviderConfig
	models := newModels([]protocol.Provider{{
		ID:   "test",
		Name: "Test",
		Auth: providerAuth,
		API: func(_ context.Context, config protocol.ProviderConfig) (protocol.StreamFunc, error) {
			received = config
			return func(context.Context, protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
				return protocol.NewAssistantMessageEventStream(0), nil
			}, nil
		},
	}}, protocol.ModelsOptions{Credentials: credentials})
	stream, _, err := models.CreateStream(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream(context.Background(), protocol.StreamRequest{}); err != nil {
		t.Fatal(err)
	}
	return received
}

func drainStream(t *testing.T, stream *protocol.AssistantMessageEventStream) {
	t.Helper()
	for range stream.Events() {
	}
	if _, err := stream.Result(context.Background()); err != nil {
		t.Fatalf("stream result error = %v", err)
	}
}
