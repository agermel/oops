package auth

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
)

// InMemoryCredentialStore is the default credential store. Applications can
// provide a persistent CredentialStore when they add login flows.
type InMemoryCredentialStore struct {
	mu          sync.RWMutex
	credentials map[string]Credential
	locks       map[string]*sync.Mutex
}

// NewInMemoryCredentialStore creates an empty provider-scoped credential store.
func NewInMemoryCredentialStore() *InMemoryCredentialStore {
	return &InMemoryCredentialStore{
		credentials: make(map[string]Credential),
		locks:       make(map[string]*sync.Mutex),
	}
}

// Read returns a copy of the current provider credential.
func (s *InMemoryCredentialStore) Read(ctx context.Context, providerID string) (Credential, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	credential := cloneCredential(s.credentials[providerID])
	s.mu.RUnlock()
	return credential, nil
}

// Modify serializes mutation for one provider and returns the stored result.
func (s *InMemoryCredentialStore) Modify(ctx context.Context, providerID string, modify func(context.Context, Credential) (Credential, error)) (Credential, error) {
	if modify == nil {
		return nil, errors.New("credential modifier is nil")
	}
	lock := s.providerLock(providerID)
	lock.Lock()
	defer lock.Unlock()
	if err := contextError(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	current := cloneCredential(s.credentials[providerID])
	s.mu.RUnlock()
	next, err := modify(ctx, current)
	if err != nil {
		return nil, err
	}
	if next == nil {
		return current, nil
	}

	stored := cloneCredential(next)
	s.mu.Lock()
	if s.credentials == nil {
		s.credentials = make(map[string]Credential)
	}
	s.credentials[providerID] = stored
	s.mu.Unlock()
	return cloneCredential(stored), nil
}

// Delete removes one provider credential after pending mutations complete.
func (s *InMemoryCredentialStore) Delete(ctx context.Context, providerID string) error {
	lock := s.providerLock(providerID)
	lock.Lock()
	defer lock.Unlock()
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.credentials, providerID)
	s.mu.Unlock()
	return nil
}

func (s *InMemoryCredentialStore) providerLock(providerID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = make(map[string]*sync.Mutex)
	}
	lock := s.locks[providerID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[providerID] = lock
	}
	return lock
}

func cloneCredential(credential Credential) Credential {
	switch value := credential.(type) {
	case APIKeyCredential:
		value.Env = maps.Clone(value.Env)
		return value
	case *APIKeyCredential:
		if value == nil {
			return nil
		}
		copy := *value
		copy.Env = maps.Clone(value.Env)
		return &copy
	case OAuthCredential:
		return cloneOAuthCredential(value)
	case *OAuthCredential:
		if value == nil {
			return nil
		}
		copy := cloneOAuthCredential(*value)
		return &copy
	default:
		return nil
	}
}

func apiKeyCredentialValue(credential Credential) (APIKeyCredential, bool) {
	switch value := credential.(type) {
	case APIKeyCredential:
		value.Env = maps.Clone(value.Env)
		return value, true
	case *APIKeyCredential:
		if value == nil {
			return APIKeyCredential{}, false
		}
		copy := *value
		copy.Env = maps.Clone(value.Env)
		return copy, true
	default:
		return APIKeyCredential{}, false
	}
}

func oauthCredentialValue(credential Credential) (OAuthCredential, bool) {
	switch value := credential.(type) {
	case OAuthCredential:
		return cloneOAuthCredential(value), true
	case *OAuthCredential:
		if value == nil {
			return OAuthCredential{}, false
		}
		return cloneOAuthCredential(*value), true
	default:
		return OAuthCredential{}, false
	}
}

func cloneOAuthCredential(credential OAuthCredential) OAuthCredential {
	if len(credential.Extra) == 0 {
		return credential
	}
	extra := credential.Extra
	credential.Extra = make(map[string]json.RawMessage, len(extra))
	for key, value := range extra {
		credential.Extra[key] = append(json.RawMessage(nil), value...)
	}
	return credential
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
