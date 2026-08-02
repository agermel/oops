package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"time"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/ai/api"
	"oops/internal/agent/ai/providers"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

// RequestOptions configures one provider request. Client snapshots it before dispatch.
type RequestOptions struct {
	Timeout      time.Duration
	Headers      map[string]string
	PayloadHook  PayloadHook
	ResponseHook ResponseHook
}

// PayloadHook transforms the final JSON payload before the HTTP request is sent.
type PayloadHook func(context.Context, protocol.StreamRequest, json.RawMessage) (json.RawMessage, error)

// ResponseHook observes response status and headers before the response body is consumed.
type ResponseHook func(context.Context, protocol.StreamRequest, ResponseInfo) error

type ResponseInfo struct {
	StatusCode int
	Headers    http.Header
}

func (options RequestOptions) Clone() RequestOptions {
	return cloneRequestOptions(options)
}

func (options RequestOptions) Validate() error {
	return validateRequestOptions(options)
}

func (c *Client) SetRequestOptions(options RequestOptions) error {
	if err := validateRequestOptions(options); err != nil {
		return err
	}
	c.requestMu.Lock()
	c.requestOptions = cloneRequestOptions(options)
	c.requestMu.Unlock()
	return nil
}

func (c *Client) RequestOptions() RequestOptions {
	c.requestMu.RLock()
	defer c.requestMu.RUnlock()
	return cloneRequestOptions(c.requestOptions)
}

func (c *Client) Stream() protocol.StreamFunc {
	return func(ctx context.Context, request protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		return c.streamRequest(ctx, request, c.RequestOptions())
	}
}

// StreamWithOptions returns a stream function bound to an immutable option snapshot.
func (c *Client) StreamWithOptions(options RequestOptions) (protocol.StreamFunc, error) {
	if err := validateRequestOptions(options); err != nil {
		return nil, err
	}
	snapshot := cloneRequestOptions(options)
	return func(ctx context.Context, request protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		return c.streamRequest(ctx, request, snapshot)
	}, nil
}

func (c *Client) streamRequest(ctx context.Context, request protocol.StreamRequest, options RequestOptions) (*protocol.AssistantMessageEventStream, error) {
	if c == nil {
		return nil, errors.New("model client is nil")
	}
	request = cloneStreamRequest(request)
	options = cloneRequestOptions(options)
	providerConfig := cloneProviderConfig(c.config)
	providerConfig.Headers = mergeHeaders(providerConfig.Headers, options.Headers)

	factory := func(factoryCtx context.Context, cfg protocol.ProviderConfig) (protocol.StreamFunc, error) {
		return newProviderStream(factoryCtx, cfg, request, options)
	}
	models := providers.BuiltinModels(factory, protocol.ModelsOptions{
		Credentials: c.credentials,
		AuthContext: c.authContext,
	})
	stream, resolved, err := models.CreateStream(providerConfig)
	if err != nil {
		return nil, err
	}
	request.Provider = resolved.Provider
	request.Model = resolved.ID
	return stream(ctx, request)
}

func newProviderStream(ctx context.Context, cfg protocol.ProviderConfig, request protocol.StreamRequest, options RequestOptions) (protocol.StreamFunc, error) {
	apiKey := cfg.APIKey
	if hasHeader(cfg.Headers, "Authorization") {
		apiKey = ""
	}
	transport := http.RoundTripper(http.DefaultTransport)
	if options.PayloadHook != nil || options.ResponseHook != nil {
		transport = requestTransport{
			base:         transport,
			request:      cloneStreamRequest(request),
			payloadHook:  options.PayloadHook,
			responseHook: options.ResponseHook,
		}
	}
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   cfg.Model,
		APIKey:  apiKey,
		BaseURL: cfg.BaseURL,
		HTTPClient: &http.Client{
			Transport: transport,
			Timeout:   options.Timeout,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create provider model: %w", err)
	}
	return api.NewOpenAICompletionStream(chatModel, api.Options{
		ContextWindow: cfg.ContextWindow,
		MaxTokens:     cfg.MaxTokens,
		Temperature:   cfg.Temperature,
		Headers:       maps.Clone(cfg.Headers),
	})
}

type requestTransport struct {
	base         http.RoundTripper
	request      protocol.StreamRequest
	payloadHook  PayloadHook
	responseHook ResponseHook
}

func (t requestTransport) RoundTrip(input *http.Request) (*http.Response, error) {
	request := input.Clone(input.Context())
	if t.payloadHook != nil {
		if request.Body == nil {
			return nil, errors.New("provider payload hook requires a request body")
		}
		payload, err := io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read provider payload: %w", err)
		}
		modified, err := t.payloadHook(request.Context(), cloneStreamRequest(t.request), json.RawMessage(bytes.Clone(payload)))
		if err != nil {
			return nil, fmt.Errorf("provider payload hook: %w", err)
		}
		if modified != nil {
			if !json.Valid(modified) {
				return nil, errors.New("provider payload hook returned invalid JSON")
			}
			payload = bytes.Clone(modified)
		}
		request.Body = io.NopCloser(bytes.NewReader(payload))
		request.ContentLength = int64(len(payload))
		request.Header.Del("Content-Length")
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(payload)), nil
		}
	}

	response, err := t.base.RoundTrip(request)
	if err != nil || response == nil || t.responseHook == nil {
		return response, err
	}
	info := ResponseInfo{StatusCode: response.StatusCode, Headers: response.Header.Clone()}
	if err := t.responseHook(request.Context(), cloneStreamRequest(t.request), info); err != nil {
		_ = response.Body.Close()
		return nil, fmt.Errorf("provider response hook: %w", err)
	}
	return response, nil
}

func validateRequestOptions(options RequestOptions) error {
	if options.Timeout < 0 {
		return errors.New("provider request timeout must be non-negative")
	}
	return nil
}

func cloneRequestOptions(options RequestOptions) RequestOptions {
	options.Headers = maps.Clone(options.Headers)
	return options
}

func cloneProviderConfig(cfg protocol.ProviderConfig) protocol.ProviderConfig {
	cfg.Headers = maps.Clone(cfg.Headers)
	cfg.Env = maps.Clone(cfg.Env)
	if cfg.Temperature != nil {
		temperature := *cfg.Temperature
		cfg.Temperature = &temperature
	}
	return cfg
}

func cloneStreamRequest(request protocol.StreamRequest) protocol.StreamRequest {
	request.Context.Messages = protocol.CloneMessageList(request.Context.Messages)
	request.Context.Tools = protocol.CloneTools(request.Context.Tools)
	return request
}

func mergeHeaders(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for name, value := range base {
		merged[http.CanonicalHeaderKey(name)] = value
	}
	for name, value := range override {
		merged[http.CanonicalHeaderKey(name)] = value
	}
	return merged
}

func hasHeader(headers map[string]string, target string) bool {
	canonicalTarget := http.CanonicalHeaderKey(target)
	for name := range headers {
		if http.CanonicalHeaderKey(name) == canonicalTarget {
			return true
		}
	}
	return false
}
