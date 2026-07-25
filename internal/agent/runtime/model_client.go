package runtime

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
	stream     protocol.StreamFunc
	provider   string
	disabledMu sync.RWMutex
	disabled   map[string]bool
}

// ClientOptions provides host-owned authentication dependencies for model
// requests. A zero value uses the in-memory store and process environment.
type ClientOptions struct {
	Credentials auth.CredentialStore
	AuthContext auth.AuthContext
}

func NewClient(ctx context.Context, cfg config.LLMConfig, options ClientOptions) (*Client, error) {
	models := providers.BuiltinModels(api.OpenAICompletionFactory, protocol.ModelsOptions{
		Credentials: options.Credentials,
		AuthContext: options.AuthContext,
	})
	stream, resolved, err := models.CreateStream(protocol.ProviderConfig{
		Provider:      cfg.Provider,
		Model:         cfg.Model,
		APIKey:        cfg.APIKey,
		BaseURL:       cfg.BaseURL,
		ContextWindow: cfg.ContextWindow,
		MaxTokens:     cfg.MaxTokens,
		Temperature:   cfg.Temperature,
	})
	if err != nil {
		return nil, err
	}

	return &Client{
		stream:   stream,
		provider: resolved.Provider,
		disabled: make(map[string]bool),
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

func (c *Client) Stream() protocol.StreamFunc {
	return c.stream
}

func (c *Client) Provider() string {
	return c.provider
}

func (c *Client) EnabledTools(tools []tool.InvokableTool) []tool.InvokableTool {
	c.disabledMu.RLock()
	defer c.disabledMu.RUnlock()
	filtered := make([]tool.InvokableTool, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(context.Background())
		if err != nil || c.disabled[info.Name] {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}
