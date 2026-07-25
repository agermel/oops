package auth

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"
)

// ErrorCode classifies failures while resolving provider authentication.
type ErrorCode string

const (
	ErrorCredentialStore ErrorCode = "credential_store"
	ErrorAPIKey          ErrorCode = "api_key"
	ErrorOAuth           ErrorCode = "oauth"
)

// ResolutionError preserves the provider and failure category for callers.
type ResolutionError struct {
	Code       ErrorCode
	ProviderID string
	Err        error
}

func (e *ResolutionError) Error() string {
	return fmt.Sprintf("resolve authentication for %s: %v", e.ProviderID, e.Err)
}

func (e *ResolutionError) Unwrap() error {
	return e.Err
}

// Overrides contains explicit request values. APIKey is nil when the caller
// delegates key selection to stored credentials and ambient sources.
type Overrides struct {
	APIKey *string
	Env    map[string]string
}

// ResolveProviderAuth resolves authentication in this order: explicit request
// values, stored credentials, then ambient provider configuration.
func ResolveProviderAuth(ctx context.Context, target Target, providerAuth ProviderAuth, credentials CredentialStore, authContext AuthContext, overrides Overrides) (*AuthResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if authContext == nil {
		authContext = DefaultContext()
	}
	requestContext := withEnvironment(authContext, overrides.Env)

	if overrides.APIKey != nil && providerAuth.APIKey != nil {
		return resolveAPIKey(ctx, target, requestContext, providerAuth.APIKey, &APIKeyCredential{
			Key: *overrides.APIKey,
			Env: maps.Clone(overrides.Env),
		})
	}

	if credentials != nil {
		stored, err := credentials.Read(ctx, target.ProviderID)
		if err != nil {
			return nil, resolutionError(ErrorCredentialStore, target.ProviderID, err)
		}
		if credential, ok := oauthCredentialValue(stored); ok {
			if providerAuth.OAuth == nil {
				return nil, nil
			}
			return resolveStoredOAuth(ctx, target, credentials, providerAuth.OAuth, credential)
		}
		if credential, ok := apiKeyCredentialValue(stored); ok {
			if providerAuth.APIKey == nil {
				return nil, nil
			}
			credential.Env = MergeEnvironment(credential.Env, overrides.Env)
			return resolveAPIKey(ctx, target, requestContext, providerAuth.APIKey, &credential)
		}
	}

	if providerAuth.APIKey == nil {
		return nil, nil
	}
	return resolveAPIKey(ctx, target, requestContext, providerAuth.APIKey, nil)
}

func resolveStoredOAuth(ctx context.Context, target Target, credentials CredentialStore, oauth OAuthAuth, stored OAuthCredential) (*AuthResult, error) {
	credential := stored
	if !credential.ExpiresAt.After(time.Now()) {
		updated, err := credentials.Modify(ctx, target.ProviderID, func(refreshCtx context.Context, current Credential) (Credential, error) {
			currentCredential, ok := oauthCredentialValue(current)
			if !ok || currentCredential.ExpiresAt.After(time.Now()) {
				return nil, nil
			}
			refreshed, err := oauth.Refresh(refreshCtx, currentCredential)
			if err != nil {
				return nil, resolutionError(ErrorOAuth, target.ProviderID, fmt.Errorf("refresh credential: %w", err))
			}
			return refreshed, nil
		})
		if err != nil {
			var resolution *ResolutionError
			if errors.As(err, &resolution) {
				return nil, resolution
			}
			return nil, resolutionError(ErrorCredentialStore, target.ProviderID, err)
		}
		refreshed, ok := oauthCredentialValue(updated)
		if !ok {
			return nil, nil
		}
		credential = refreshed
	}

	resolved, err := oauth.ToAuth(ctx, credential)
	if err != nil {
		return nil, resolutionError(ErrorOAuth, target.ProviderID, fmt.Errorf("derive request authentication: %w", err))
	}
	return &AuthResult{Auth: resolved, Source: "OAuth"}, nil
}

func resolveAPIKey(ctx context.Context, target Target, authContext AuthContext, apiKeyAuth *APIKeyAuth, credential *APIKeyCredential) (*AuthResult, error) {
	if apiKeyAuth == nil || apiKeyAuth.Resolve == nil {
		return nil, nil
	}
	resolved, configured, err := apiKeyAuth.Resolve(ctx, APIKeyResolveInput{
		Target:     target,
		Context:    authContext,
		Credential: credential,
	})
	if err != nil {
		return nil, resolutionError(ErrorAPIKey, target.ProviderID, err)
	}
	if !configured {
		return nil, nil
	}
	return &resolved, nil
}

func resolutionError(code ErrorCode, providerID string, err error) *ResolutionError {
	return &ResolutionError{Code: code, ProviderID: providerID, Err: err}
}

type environmentContext struct {
	base     AuthContext
	override map[string]string
}

func withEnvironment(base AuthContext, override map[string]string) AuthContext {
	if len(override) == 0 {
		return base
	}
	return environmentContext{base: base, override: override}
}

func (c environmentContext) Env(name string) (string, bool) {
	if value, ok := c.override[name]; ok && value != "" {
		return value, true
	}
	return c.base.Env(name)
}

// MergeEnvironment applies request-scoped environment values over resolved values.
func MergeEnvironment(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := maps.Clone(base)
	if merged == nil {
		merged = make(map[string]string, len(override))
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}
