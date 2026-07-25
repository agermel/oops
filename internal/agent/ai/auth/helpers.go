package auth

import (
	"context"
	"errors"
	"strings"
)

// EnvAPIKeyAuth resolves a stored API key before checking environment
// variables in declaration order.
func EnvAPIKeyAuth(name string, environmentVariables ...string) *APIKeyAuth {
	variables := append([]string(nil), environmentVariables...)
	return &APIKeyAuth{
		Name: name,
		Login: func(ctx context.Context, callbacks LoginCallbacks) (APIKeyCredential, error) {
			if callbacks == nil {
				return APIKeyCredential{}, errors.New("login callbacks are nil")
			}
			key, err := callbacks.Prompt(ctx, Prompt{Type: PromptSecret, Message: "Enter " + name})
			if err != nil {
				return APIKeyCredential{}, err
			}
			return APIKeyCredential{Key: key}, nil
		},
		Resolve: func(_ context.Context, input APIKeyResolveInput) (AuthResult, bool, error) {
			if input.Credential != nil && strings.TrimSpace(input.Credential.Key) != "" {
				return AuthResult{Auth: ModelAuth{APIKey: input.Credential.Key}, Source: "stored credential"}, true, nil
			}
			for _, variable := range variables {
				if value, ok := input.Context.Env(variable); ok {
					return AuthResult{Auth: ModelAuth{APIKey: value}, Source: variable}, true, nil
				}
			}
			return AuthResult{}, false, nil
		},
	}
}
