package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/ai/auth"
	"oops/internal/config"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestNew(t *testing.T) {
	cfg := config.LLMConfig{
		Enabled:  true,
		Provider: "openai-compatible",
		Model:    "gpt-4o-mini",
		BaseURL:  "https://api.openai.com/v1",
		APIKey:   "test-key",
	}

	client, err := New(context.Background(), cfg, Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client == nil {
		t.Fatal("New() returned nil")
	}
	if client.Stream() == nil {
		t.Fatal("client stream is nil")
	}
	if client.Provider() != "openai-compatible" {
		t.Fatalf("Provider() = %q", client.Provider())
	}
}

func TestUsesInjectedCredentialStore(t *testing.T) {
	credentials := auth.NewInMemoryCredentialStore()
	if _, err := credentials.Modify(context.Background(), "openai-compatible", func(context.Context, auth.Credential) (auth.Credential, error) {
		return auth.APIKeyCredential{Key: "stored-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	receivedAuthorization := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthorization <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New(context.Background(), config.LLMConfig{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  server.URL,
	}, Options{Credentials: credentials})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Stream()(context.Background(), protocol.StreamRequest{Context: protocol.Context{
		Messages: protocol.MessageList{protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events() {
	}
	if _, err := stream.Result(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := <-receivedAuthorization; got != "Bearer stored-key" {
		t.Fatalf("Authorization = %q", got)
	}
	if _, err := credentials.Modify(context.Background(), "openai-compatible", func(context.Context, auth.Credential) (auth.Credential, error) {
		return auth.APIKeyCredential{Key: "refreshed-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	stream, err = client.Stream()(context.Background(), protocol.StreamRequest{Context: protocol.Context{
		Messages: protocol.MessageList{protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("again")}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events() {
	}
	if _, err := stream.Result(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := <-receivedAuthorization; got != "Bearer refreshed-key" {
		t.Fatalf("refreshed Authorization = %q", got)
	}
}

func TestFiltersCurrentToolsWithDisabledState(t *testing.T) {
	alpha := invokableToolForTest{name: "alpha"}
	beta := invokableToolForTest{name: "beta"}
	gamma := invokableToolForTest{name: "gamma"}
	client := &Client{
		disabled: make(map[string]bool),
	}

	client.SetToolEnabled("beta", false)
	if got := toolNames(t, client.EnabledTools([]tool.InvokableTool{alpha, beta})); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("tools after disable = %#v, want [alpha]", got)
	}

	if got := toolNames(t, client.EnabledTools([]tool.InvokableTool{beta, gamma})); len(got) != 1 || got[0] != "gamma" {
		t.Fatalf("current tools after update = %#v, want [gamma]", got)
	}
	if disabled := client.DisabledTools(); !disabled["beta"] {
		t.Fatalf("DisabledTools = %#v, want beta disabled", disabled)
	}

	client.SetToolEnabled("beta", true)
	if got := toolNames(t, client.EnabledTools([]tool.InvokableTool{beta, gamma})); len(got) != 2 || got[0] != "beta" || got[1] != "gamma" {
		t.Fatalf("tools after re-enable = %#v, want [beta gamma]", got)
	}
}

type invokableToolForTest struct {
	name string
}

func (t invokableToolForTest) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: t.name}, nil
}

func (t invokableToolForTest) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "ok", nil
}

func toolNames(t *testing.T, tools []tool.InvokableTool) []string {
	t.Helper()
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		info, err := item.Info(context.Background())
		if err != nil {
			t.Fatalf("tool Info: %v", err)
		}
		names = append(names, info.Name)
	}
	return names
}
