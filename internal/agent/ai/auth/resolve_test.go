package auth

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestResolveProviderAuthPriority(t *testing.T) {
	store := NewInMemoryCredentialStore()
	if _, err := store.Modify(context.Background(), "test", func(context.Context, Credential) (Credential, error) {
		return APIKeyCredential{Key: "stored-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	explicit := "request-key"
	resolved, err := ResolveProviderAuth(
		context.Background(),
		Target{ProviderID: "test", ModelID: "model"},
		ProviderAuth{APIKey: EnvAPIKeyAuth("Test API key", "TEST_API_KEY")},
		store,
		testAuthContext{environment: map[string]string{"TEST_API_KEY": "environment-key"}},
		Overrides{APIKey: &explicit},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.Auth.APIKey != "request-key" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestResolveProviderAuthUsesStoredCredentialBeforeEnvironment(t *testing.T) {
	store := NewInMemoryCredentialStore()
	if _, err := store.Modify(context.Background(), "test", func(context.Context, Credential) (Credential, error) {
		return APIKeyCredential{Key: "stored-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProviderAuth(
		context.Background(),
		Target{ProviderID: "test"},
		ProviderAuth{APIKey: EnvAPIKeyAuth("Test API key", "TEST_API_KEY")},
		store,
		testAuthContext{environment: map[string]string{"TEST_API_KEY": "environment-key"}},
		Overrides{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.Auth.APIKey != "stored-key" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestResolveProviderAuthUsesPointerStoredCredential(t *testing.T) {
	store := NewInMemoryCredentialStore()
	if _, err := store.Modify(context.Background(), "test", func(context.Context, Credential) (Credential, error) {
		return &APIKeyCredential{Key: "stored-key", Env: map[string]string{"scope": "test"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProviderAuth(
		context.Background(),
		Target{ProviderID: "test"},
		ProviderAuth{APIKey: EnvAPIKeyAuth("Test API key", "TEST_API_KEY")},
		store,
		testAuthContext{environment: map[string]string{"TEST_API_KEY": "environment-key"}},
		Overrides{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.Auth.APIKey != "stored-key" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestResolveProviderAuthUsesEnvironment(t *testing.T) {
	resolved, err := ResolveProviderAuth(
		context.Background(),
		Target{ProviderID: "test"},
		ProviderAuth{APIKey: EnvAPIKeyAuth("Test API key", "PRIMARY_KEY", "SECONDARY_KEY")},
		NewInMemoryCredentialStore(),
		testAuthContext{environment: map[string]string{"SECONDARY_KEY": "secondary-key"}},
		Overrides{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.Auth.APIKey != "secondary-key" || resolved.Source != "SECONDARY_KEY" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestInMemoryCredentialStoreSerializesModify(t *testing.T) {
	store := NewInMemoryCredentialStore()
	const writers = 16
	var group sync.WaitGroup
	for range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := store.Modify(context.Background(), "test", func(_ context.Context, current Credential) (Credential, error) {
				count := 0
				if credential, ok := current.(APIKeyCredential); ok {
					count, _ = strconv.Atoi(credential.Key)
				}
				return APIKeyCredential{Key: strconv.Itoa(count + 1)}, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	credential, err := store.Read(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	apiKey, ok := credential.(APIKeyCredential)
	if !ok || apiKey.Key != strconv.Itoa(writers) {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestInMemoryCredentialStoreCopiesPointerCredential(t *testing.T) {
	store := NewInMemoryCredentialStore()
	input := &APIKeyCredential{Key: "original", Env: map[string]string{"scope": "original"}}
	if _, err := store.Modify(context.Background(), "test", func(context.Context, Credential) (Credential, error) {
		return input, nil
	}); err != nil {
		t.Fatal(err)
	}
	input.Key = "mutated"
	input.Env["scope"] = "mutated"

	credential, err := store.Read(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := credential.(*APIKeyCredential)
	if !ok || stored.Key != "original" || stored.Env["scope"] != "original" {
		t.Fatalf("stored credential = %#v", credential)
	}
	stored.Env["scope"] = "read mutation"

	credential, err = store.Read(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok = credential.(*APIKeyCredential)
	if !ok || stored.Env["scope"] != "original" {
		t.Fatalf("stored credential after read mutation = %#v", credential)
	}
}

func TestInMemoryCredentialStoreCopiesOAuthExtra(t *testing.T) {
	store := NewInMemoryCredentialStore()
	input := &OAuthCredential{
		AccessToken:  "token",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
		Extra:        map[string]json.RawMessage{"enterprise": json.RawMessage(`"example.test"`)},
	}
	if _, err := store.Modify(context.Background(), "test", func(context.Context, Credential) (Credential, error) {
		return input, nil
	}); err != nil {
		t.Fatal(err)
	}
	input.Extra["enterprise"][1] = 'X'

	credential, err := store.Read(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := credential.(*OAuthCredential)
	if !ok || string(stored.Extra["enterprise"]) != `"example.test"` {
		t.Fatalf("stored credential = %#v", credential)
	}
	stored.Extra["enterprise"][1] = 'Y'
	credential, err = store.Read(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok = credential.(*OAuthCredential)
	if !ok || string(stored.Extra["enterprise"]) != `"example.test"` {
		t.Fatalf("stored credential after read mutation = %#v", credential)
	}
}

func TestResolveProviderAuthRefreshesExpiredOAuth(t *testing.T) {
	store := NewInMemoryCredentialStore()
	if _, err := store.Modify(context.Background(), "test", func(context.Context, Credential) (Credential, error) {
		return &OAuthCredential{AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Minute)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	oauth := &oauthForTest{}
	for range 2 {
		resolved, err := ResolveProviderAuth(
			context.Background(),
			Target{ProviderID: "test"},
			ProviderAuth{OAuth: oauth},
			store,
			testAuthContext{},
			Overrides{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if resolved == nil || resolved.Auth.Headers["Authorization"] != "Bearer refreshed" {
			t.Fatalf("resolved = %#v", resolved)
		}
	}
	if oauth.refreshCount != 1 {
		t.Fatalf("refreshes = %d", oauth.refreshCount)
	}
	credential, err := store.Read(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := oauthCredentialValue(credential)
	if !ok || stored.AccessToken != "refreshed" || !stored.ExpiresAt.After(time.Now()) {
		t.Fatalf("credential = %#v", credential)
	}
}

type testAuthContext struct {
	environment map[string]string
}

func (c testAuthContext) Env(name string) (string, bool) {
	value, ok := c.environment[name]
	return value, ok
}

type oauthForTest struct {
	refreshCount int
}

func (o *oauthForTest) Name() string {
	return "Test OAuth"
}

func (o *oauthForTest) Login(context.Context, LoginCallbacks) (OAuthCredential, error) {
	return OAuthCredential{AccessToken: "logged-in", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (o *oauthForTest) Refresh(context.Context, OAuthCredential) (OAuthCredential, error) {
	o.refreshCount++
	return OAuthCredential{AccessToken: "refreshed", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (o *oauthForTest) ToAuth(_ context.Context, credential OAuthCredential) (ModelAuth, error) {
	return ModelAuth{Headers: map[string]string{"Authorization": "Bearer " + credential.AccessToken}}, nil
}
