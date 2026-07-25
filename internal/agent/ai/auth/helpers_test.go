package auth

import (
	"context"
	"testing"
)

func TestEnvAPIKeyAuthLogin(t *testing.T) {
	keyAuth := EnvAPIKeyAuth("Test API key", "TEST_API_KEY")
	credential, err := keyAuth.Login(context.Background(), loginCallbacksForTest{response: "entered-key"})
	if err != nil {
		t.Fatal(err)
	}
	if credential.Key != "entered-key" {
		t.Fatalf("credential = %#v", credential)
	}
}

type loginCallbacksForTest struct {
	response string
}

func (c loginCallbacksForTest) Prompt(context.Context, Prompt) (string, error) {
	return c.response, nil
}

func (loginCallbacksForTest) Notify(Event) {}
