// Package auth resolves provider credentials into request authentication.
package auth

import (
	"context"
	"encoding/json"
	"time"
)

// ModelAuth contains the authentication values attached to one model request.
type ModelAuth struct {
	APIKey  string
	Headers map[string]string
	BaseURL string
}

// APIKeyCredential stores one provider API key and provider-scoped values.
type APIKeyCredential struct {
	Key string
	Env map[string]string
}

func (APIKeyCredential) credential() {}

// OAuthCredential stores the values required to refresh an OAuth access token.
type OAuthCredential struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	// Extra retains provider-specific OAuth state such as an enterprise domain
	// or a model entitlement list. Each value is an independent JSON value so
	// persistent stores can round-trip it without depending on a provider.
	Extra map[string]json.RawMessage
}

func (OAuthCredential) credential() {}

// Credential is one stored credential owned by a provider.
type Credential interface {
	credential()
}

// CredentialStore stores one credential per provider. Modify serializes all
// read-modify-write operations for one provider. Returning nil from modify
// leaves the stored credential unchanged.
type CredentialStore interface {
	Read(ctx context.Context, providerID string) (Credential, error)
	Modify(ctx context.Context, providerID string, modify func(context.Context, Credential) (Credential, error)) (Credential, error)
	Delete(ctx context.Context, providerID string) error
}

// AuthContext provides ambient provider configuration.
type AuthContext interface {
	Env(name string) (string, bool)
}

// AuthResult is the resolved request authentication and its source.
type AuthResult struct {
	Auth   ModelAuth
	Env    map[string]string
	Source string
}

// PromptType describes the value requested during interactive provider setup.
type PromptType string

const (
	PromptText       PromptType = "text"
	PromptSecret     PromptType = "secret"
	PromptSelect     PromptType = "select"
	PromptManualCode PromptType = "manual_code"
)

// PromptOption is one selectable provider-setup value.
type PromptOption struct {
	ID          string
	Label       string
	Description string
}

// Prompt describes one interaction requested by API-key or OAuth setup.
// Context cancellation replaces a separate prompt abort signal.
type Prompt struct {
	Type        PromptType
	Message     string
	Placeholder string
	Options     []PromptOption
}

// EventType describes a non-blocking update emitted during provider setup.
type EventType string

const (
	EventAuthURL    EventType = "auth_url"
	EventDeviceCode EventType = "device_code"
	EventProgress   EventType = "progress"
)

// Event carries provider-setup guidance for the host UI.
type Event struct {
	Type            EventType
	URL             string
	Instructions    string
	UserCode        string
	VerificationURL string
	IntervalSeconds int
	ExpiresIn       time.Duration
	Message         string
}

// LoginCallbacks lets the host own prompts and status display while provider
// authentication owns the protocol-specific setup flow.
type LoginCallbacks interface {
	Prompt(context.Context, Prompt) (string, error)
	Notify(Event)
}

// Target identifies the provider model currently being requested.
type Target struct {
	ProviderID string
	ModelID    string
	BaseURL    string
}

// APIKeyResolveInput is passed to a provider API-key resolver.
type APIKeyResolveInput struct {
	Target     Target
	Context    AuthContext
	Credential *APIKeyCredential
}

// APIKeyAuth resolves API-key credentials and ambient provider configuration.
type APIKeyAuth struct {
	Name    string
	Login   func(context.Context, LoginCallbacks) (APIKeyCredential, error)
	Resolve func(context.Context, APIKeyResolveInput) (AuthResult, bool, error)
}

// OAuthAuth performs interactive setup, refreshes credentials, and derives
// request authentication.
type OAuthAuth interface {
	Name() string
	Login(context.Context, LoginCallbacks) (OAuthCredential, error)
	Refresh(context.Context, OAuthCredential) (OAuthCredential, error)
	ToAuth(context.Context, OAuthCredential) (ModelAuth, error)
}

// ProviderAuth declares every authentication method supported by a provider.
type ProviderAuth struct {
	APIKey *APIKeyAuth
	OAuth  OAuthAuth
}
