package model

import (
	"context"
	"sync"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/ai/api"
	"oops/internal/agent/ai/auth"
	"oops/internal/agent/ai/providers"
	"oops/internal/config"

	"github.com/cloudwego/eino/components/tool"
)

// Client owns the model handle and per-tool disabled state.
type Client struct {
	config      protocol.ProviderConfig
	credentials auth.CredentialStore
	authContext auth.AuthContext
	provider    string
	model       protocol.Model

	requestMu      sync.RWMutex
	requestOptions RequestOptions

	disabledMu sync.RWMutex
	disabled   map[string]bool
}

// Options provides host-owned authentication dependencies for model requests.
// A zero value uses the in-memory store and process environment.
type Options struct {
	Credentials auth.CredentialStore
	AuthContext auth.AuthContext
	Request     RequestOptions
}

func New(ctx context.Context, cfg config.LLMConfig, options Options) (*Client, error) {
	credentials := options.Credentials
	if credentials == nil {
		credentials = auth.NewInMemoryCredentialStore()
	}
	authContext := options.AuthContext
	if authContext == nil {
		authContext = auth.DefaultContext()
	}
	if err := validateRequestOptions(options.Request); err != nil {
		return nil, err
	}
	providerConfig := protocol.ProviderConfig{
		Provider:      cfg.Provider,
		Model:         cfg.Model,
		APIKey:        cfg.APIKey,
		BaseURL:       cfg.BaseURL,
		ContextWindow: cfg.ContextWindow,
		MaxTokens:     cfg.MaxTokens,
		Temperature:   cfg.Temperature,
	}
	models := providers.BuiltinModels(api.OpenAICompletionFactory, protocol.ModelsOptions{
		Credentials: credentials,
		AuthContext: authContext,
	})
	_, resolved, err := models.CreateStream(providerConfig)
	if err != nil {
		return nil, err
	}

	return &Client{
		config:         cloneProviderConfig(providerConfig),
		credentials:    credentials,
		authContext:    authContext,
		provider:       resolved.Provider,
		model:          resolved,
		requestOptions: cloneRequestOptions(options.Request),
		disabled:       make(map[string]bool),
	}, nil
}

func (c *Client) SetToolEnabled(name string, enabled bool) {
	c.disabledMu.Lock()
	defer c.disabledMu.Unlock()
	if enabled {
		delete(c.disabled, name)
	} else {
		c.disabled[name] = true
	}
}

func (c *Client) DisabledTools() map[string]bool {
	c.disabledMu.RLock()
	defer c.disabledMu.RUnlock()
	out := make(map[string]bool, len(c.disabled))
	for k, v := range c.disabled {
		out[k] = v
	}
	return out
}

func (c *Client) Provider() string {
	return c.provider
}

func (c *Client) Model() protocol.Model {
	if c == nil {
		return protocol.Model{}
	}
	return c.model
}

func (c *Client) EnabledTools(tools []tool.InvokableTool) []tool.InvokableTool {
	c.disabledMu.RLock()
	defer c.disabledMu.RUnlock()
	filtered := make([]tool.InvokableTool, 0, len(tools))
	for _, item := range tools {
		info, err := item.Info(context.Background())
		if err != nil || c.disabled[info.Name] {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}
