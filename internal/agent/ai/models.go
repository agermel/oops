package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"oops/internal/agent/ai/auth"
)

const DefaultProviderID = "openai-compatible"

var (
	ErrUnknownProvider = errors.New("unknown model provider")
	ErrUnknownModel    = errors.New("unknown provider model")
)

// ProviderConfig selects one provider and model endpoint.
type ProviderConfig struct {
	Provider      string
	Model         string
	APIKey        string
	BaseURL       string
	Headers       map[string]string
	Env           map[string]string
	ContextWindow int
	MaxTokens     int
	Temperature   *float32
}

// Model describes one model exposed by a provider.
type Model struct {
	Provider      string
	ID            string
	BaseURL       string
	ContextWindow int
	MaxTokens     int
}

// ProviderFactory prepares a protocol stream for one resolved provider configuration.
type ProviderFactory func(context.Context, ProviderConfig) (StreamFunc, error)

// Provider binds one model collection to its API implementation.
type Provider struct {
	ID      string
	Name    string
	BaseURL string
	Models  []Model
	Auth    auth.ProviderAuth
	API     ProviderFactory
}

// ModelsOptions provides host-owned authentication dependencies.
type ModelsOptions struct {
	Credentials auth.CredentialStore
	AuthContext auth.AuthContext
}

// Models owns provider lookup, model resolution, authentication, and stream dispatch.
type Models struct {
	providers   []Provider
	credentials auth.CredentialStore
	authContext auth.AuthContext
}

// NewModels creates one provider collection with request-scoped authentication.
func NewModels(providers []Provider, options ModelsOptions) *Models {
	if options.Credentials == nil {
		options.Credentials = auth.NewInMemoryCredentialStore()
	}
	if options.AuthContext == nil {
		options.AuthContext = auth.DefaultContext()
	}
	return &Models{providers: providers, credentials: options.Credentials, authContext: options.AuthContext}
}

// GetProviders returns the registered providers in registration order.
func (m *Models) GetProviders() []Provider {
	return append([]Provider(nil), m.providers...)
}

// GetProvider returns one registered provider by identifier.
func (m *Models) GetProvider(id string) (Provider, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, item := range m.providers {
		if item.ID == id {
			return item, true
		}
	}
	return Provider{}, false
}

// GetModels returns the known models for one provider or every provider.
func (m *Models) GetModels(providerID string) []Model {
	if strings.TrimSpace(providerID) != "" {
		item, ok := m.GetProvider(providerID)
		if !ok {
			return nil
		}
		return append([]Model(nil), item.Models...)
	}

	var models []Model
	for _, item := range m.providers {
		models = append(models, item.Models...)
	}
	return models
}

// GetModel returns one known catalog model.
func (m *Models) GetModel(providerID, modelID string) (Model, bool) {
	item, ok := m.GetProvider(providerID)
	if !ok {
		return Model{}, false
	}
	return item.GetModel(modelID)
}

// CreateStream resolves one configured provider and returns a stream that
// applies credentials immediately before each model request.
func (m *Models) CreateStream(cfg ProviderConfig) (StreamFunc, Model, error) {
	item, cfg, resolved, err := m.resolveProvider(cfg)
	if err != nil {
		return nil, Model{}, err
	}
	return func(ctx context.Context, request StreamRequest) (*AssistantMessageEventStream, error) {
		requestConfig, err := m.applyAuth(ctx, item, cfg, resolved)
		if err != nil {
			return nil, err
		}
		stream, _, err := item.CreateStream(ctx, requestConfig)
		if err != nil {
			return nil, err
		}
		request.Provider = resolved.Provider
		request.Model = resolved.ID
		return stream(ctx, request)
	}, resolved, nil
}

// GetAuth resolves one configured provider's current authentication. Hosts
// use Source to show whether the request will use a configured key, an
// environment value, or OAuth.
func (m *Models) GetAuth(ctx context.Context, cfg ProviderConfig) (*auth.AuthResult, error) {
	item, cfg, resolved, err := m.resolveProvider(cfg)
	if err != nil {
		return nil, err
	}
	return m.resolveAuth(ctx, item, cfg, resolved)
}

// GetModel returns one model from this provider.
func (p Provider) GetModel(id string) (Model, bool) {
	for _, model := range p.Models {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

// CreateStream resolves the configured model and delegates to the provider API.
func (p Provider) CreateStream(ctx context.Context, cfg ProviderConfig) (StreamFunc, Model, error) {
	resolved, err := p.ResolveModel(cfg)
	if err != nil {
		return nil, Model{}, err
	}
	if p.API == nil {
		return nil, Model{}, fmt.Errorf("provider %s has no API implementation", p.ID)
	}
	stream, err := p.API(ctx, ProviderConfig{
		Provider:      p.ID,
		Model:         resolved.ID,
		APIKey:        cfg.APIKey,
		BaseURL:       resolved.BaseURL,
		Headers:       cfg.Headers,
		Env:           cfg.Env,
		ContextWindow: resolved.ContextWindow,
		MaxTokens:     resolved.MaxTokens,
		Temperature:   cfg.Temperature,
	})
	if err != nil {
		return nil, Model{}, fmt.Errorf("create %s stream: %w", p.ID, err)
	}
	return stream, resolved, nil
}

// ResolveModel combines provider defaults, catalog metadata, and caller overrides.
func (p Provider) ResolveModel(cfg ProviderConfig) (Model, error) {
	resolved := Model{
		Provider: p.ID,
		ID:       cfg.Model,
		BaseURL:  p.BaseURL,
	}
	if len(p.Models) > 0 {
		model, ok := p.GetModel(cfg.Model)
		if !ok {
			return Model{}, fmt.Errorf("%w: %s/%s", ErrUnknownModel, p.ID, cfg.Model)
		}
		resolved = model
		resolved.Provider = p.ID
		if resolved.BaseURL == "" {
			resolved.BaseURL = p.BaseURL
		}
	}
	if cfg.BaseURL != "" {
		resolved.BaseURL = cfg.BaseURL
	}
	if cfg.ContextWindow > 0 {
		resolved.ContextWindow = cfg.ContextWindow
	}
	if cfg.MaxTokens > 0 {
		resolved.MaxTokens = cfg.MaxTokens
	}
	return resolved, nil
}

func (m *Models) resolveProvider(cfg ProviderConfig) (Provider, ProviderConfig, Model, error) {
	providerID := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if providerID == "" {
		providerID = DefaultProviderID
	}
	item, ok := m.GetProvider(providerID)
	if !ok {
		return Provider{}, ProviderConfig{}, Model{}, fmt.Errorf("%w: %s", ErrUnknownProvider, providerID)
	}
	cfg.Provider = providerID
	resolved, err := item.ResolveModel(cfg)
	if err != nil {
		return Provider{}, ProviderConfig{}, Model{}, err
	}
	return item, cfg, resolved, nil
}

func (m *Models) applyAuth(ctx context.Context, item Provider, cfg ProviderConfig, model Model) (ProviderConfig, error) {
	resolution, err := m.resolveAuth(ctx, item, cfg, model)
	if err != nil || resolution == nil {
		return cfg, err
	}
	if cfg.APIKey == "" && resolution.Auth.APIKey != "" {
		cfg.APIKey = resolution.Auth.APIKey
	}
	if resolution.Auth.BaseURL != "" {
		cfg.BaseURL = resolution.Auth.BaseURL
	}
	cfg.Headers = mergeProviderHeaders(resolution.Auth.Headers, cfg.Headers)
	cfg.Env = auth.MergeEnvironment(resolution.Env, cfg.Env)
	return cfg, nil
}

func (m *Models) resolveAuth(ctx context.Context, item Provider, cfg ProviderConfig, model Model) (*auth.AuthResult, error) {
	overrides := auth.Overrides{Env: cfg.Env}
	if cfg.APIKey != "" {
		apiKey := cfg.APIKey
		overrides.APIKey = &apiKey
	}
	return auth.ResolveProviderAuth(ctx, auth.Target{
		ProviderID: item.ID,
		ModelID:    model.ID,
		BaseURL:    model.BaseURL,
	}, item.Auth, m.credentials, m.authContext, overrides)
}

func mergeProviderHeaders(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		merged[http.CanonicalHeaderKey(key)] = value
	}
	for key, value := range override {
		merged[http.CanonicalHeaderKey(key)] = value
	}
	return merged
}
