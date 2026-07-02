package llm

import (
	"context"
	"os"
	"testing"
	"time"

	"oops/internal/config"

	"github.com/cloudwego/eino/schema"
)

// TestClientNew 验证客户端创建。
func TestClientNew(t *testing.T) {
	ops := &fakeOpsData{}
	cfg := config.LLMConfig{
		Enabled: true,
		Model:   "gpt-4o-mini",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
	}

	tools, err := NewTools(ops, nil)
	if err != nil {
		t.Fatalf("NewTools() error = %v", err)
	}

	client, err := NewClient(context.Background(), cfg, tools)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned nil")
	}
	if client.model == nil {
		t.Fatal("client.model is nil")
	}
	if len(client.tools) != 7 {
		t.Fatalf("len(client.tools) = %d, want 7", len(client.tools))
	}
}

// TestClientAskSkipped 在没有真实 API key 时跳过测试。
func TestClientAskSkipped(t *testing.T) {
	if os.Getenv("OOPS_LLM_TEST_API_KEY") == "" {
		t.Skip("OOPS_LLM_TEST_API_KEY not set, skipping LLM integration test")
	}

	ops := &fakeOpsData{
		nodelets: []NodeletSummary{
			{ID: "local", Name: "本机", Address: "http://127.0.0.1:8686", Available: true},
		},
	}

	cfg := config.LLMConfig{
		Enabled: true,
		Model:   "gpt-4o-mini",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  os.Getenv("OOPS_LLM_TEST_API_KEY"),
	}

	tools, err := NewTools(ops, nil)
	if err != nil {
		t.Fatalf("NewTools() error = %v", err)
	}

	client, err := NewClient(context.Background(), cfg, tools)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages := []*schema.Message{
		schema.SystemMessage(BasePrompt),
		schema.UserMessage("有哪些机器？如果不止一台，请列出它们的名称。"),
	}
	events, err := client.Ask(ctx, messages, nil, 15)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	var answer string
	for evt := range events {
		if evt.Type == "answer" {
			answer = evt.Content
		}
	}
	if answer == "" {
		t.Fatal("Ask() returned empty answer")
	}
	t.Logf("Answer: %s", answer)
}
