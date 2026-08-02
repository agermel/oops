package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/runtime/model"
)

type BeforeProviderRequestEvent struct {
	Type          string            `json:"type"`
	Model         string            `json:"model,omitempty"`
	Provider      string            `json:"provider,omitempty"`
	SessionID     string            `json:"sessionId,omitempty"`
	MaxTokens     int               `json:"maxTokens,omitempty"`
	TimeoutMillis int64             `json:"timeoutMillis,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
}

func (BeforeProviderRequestEvent) EventName() string { return "before_provider_request" }
func (BeforeProviderRequestEvent) runEvent()         {}

type BeforeProviderPayloadEvent struct {
	Type      string          `json:"type"`
	Model     string          `json:"model,omitempty"`
	Provider  string          `json:"provider,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

func (BeforeProviderPayloadEvent) EventName() string { return "before_provider_payload" }
func (BeforeProviderPayloadEvent) runEvent()         {}

type AfterProviderResponseEvent struct {
	Type      string      `json:"type"`
	Model     string      `json:"model,omitempty"`
	Provider  string      `json:"provider,omitempty"`
	SessionID string      `json:"sessionId,omitempty"`
	Status    int         `json:"status"`
	Headers   http.Header `json:"headers"`
}

func (AfterProviderResponseEvent) EventName() string { return "after_provider_response" }
func (AfterProviderResponseEvent) runEvent()         {}

func (s *AgentHarness) SetProviderRequestOptions(options model.RequestOptions) error {
	if err := options.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.providerClient == nil {
		return errors.New("provider client is not configured")
	}
	s.providerRequestOptions = options.Clone()
	return nil
}

func (s *AgentHarness) ProviderRequestOptions() model.RequestOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerRequestOptions.Clone()
}

func resolveProviderBinding(client *model.Client, options *model.RequestOptions) (*model.Client, model.RequestOptions, error) {
	if client == nil {
		if options != nil {
			return nil, model.RequestOptions{}, errors.New("provider request options require a client")
		}
		return nil, model.RequestOptions{}, nil
	}
	resolved := client.RequestOptions()
	if options != nil {
		resolved = options.Clone()
	}
	if err := resolved.Validate(); err != nil {
		return nil, model.RequestOptions{}, err
	}
	return client, resolved, nil
}

func (s *AgentHarness) captureProviderTurnOptionsLocked() {
	if s.providerClient == nil {
		s.providerTurnOptions = model.RequestOptions{}
		s.providerTurnOptionsSet = false
		return
	}
	s.providerTurnOptions = s.providerRequestOptions.Clone()
	s.providerTurnOptionsSet = true
}

func (s *AgentHarness) providerStream(ctx context.Context, request protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
	s.mu.Lock()
	client := s.providerClient
	options := s.providerRequestOptions.Clone()
	if s.phase == AgentHarnessPhaseTurn && s.providerTurnOptionsSet {
		options = s.providerTurnOptions.Clone()
	}
	s.mu.Unlock()
	if client == nil {
		return nil, errors.New("provider client is not configured")
	}

	if err := s.emitRunEvent(ctx, BeforeProviderRequestEvent{
		Type:          "before_provider_request",
		Model:         request.Model,
		Provider:      request.Provider,
		SessionID:     request.SessionID,
		MaxTokens:     request.MaxTokens,
		TimeoutMillis: options.Timeout.Milliseconds(),
		Headers:       options.Clone().Headers,
	}); err != nil {
		return nil, err
	}

	userPayloadHook := options.PayloadHook
	options.PayloadHook = func(hookCtx context.Context, hookRequest protocol.StreamRequest, payload json.RawMessage) (json.RawMessage, error) {
		eventErr := s.emitRunEvent(hookCtx, BeforeProviderPayloadEvent{
			Type:      "before_provider_payload",
			Model:     hookRequest.Model,
			Provider:  hookRequest.Provider,
			SessionID: hookRequest.SessionID,
			Payload:   json.RawMessage(bytes.Clone(payload)),
		})
		if userPayloadHook == nil {
			return payload, eventErr
		}
		modified, hookErr := userPayloadHook(hookCtx, hookRequest, json.RawMessage(bytes.Clone(payload)))
		return modified, errors.Join(eventErr, hookErr)
	}

	userResponseHook := options.ResponseHook
	options.ResponseHook = func(hookCtx context.Context, hookRequest protocol.StreamRequest, response model.ResponseInfo) error {
		eventErr := s.emitRunEvent(hookCtx, AfterProviderResponseEvent{
			Type:      "after_provider_response",
			Model:     hookRequest.Model,
			Provider:  hookRequest.Provider,
			SessionID: hookRequest.SessionID,
			Status:    response.StatusCode,
			Headers:   response.Headers.Clone(),
		})
		if userResponseHook == nil {
			return eventErr
		}
		return errors.Join(eventErr, userResponseHook(hookCtx, hookRequest, response))
	}

	stream, err := client.StreamWithOptions(options)
	if err != nil {
		return nil, err
	}
	return stream(ctx, request)
}
