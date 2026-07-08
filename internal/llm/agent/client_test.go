package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"oops/internal/config"
	"oops/internal/llm/prompt"
	llmtools "oops/internal/llm/tools"
	"oops/internal/nodelet"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type fakeOpsData struct {
	nodelets []llmtools.NodeletSummary
}

func (f *fakeOpsData) ListNodelets(_ context.Context) ([]llmtools.NodeletSummary, error) {
	return f.nodelets, nil
}

func (f *fakeOpsData) ListContainers(_ context.Context, _ string, _ string, _ string) ([]nodelet.Container, error) {
	return nil, nil
}

func (f *fakeOpsData) GetLogs(_ context.Context, _, _ string, _ int) ([]nodelet.LogEntry, error) {
	return nil, nil
}

func (f *fakeOpsData) GetProjectRepo(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (f *fakeOpsData) ContainerExec(_ context.Context, _, _ string, _ []string) (nodelet.ExecResult, error) {
	return nodelet.ExecResult{}, nil
}

// TestClientNew 验证客户端创建。
func TestClientNew(t *testing.T) {
	ops := &fakeOpsData{}
	cfg := config.LLMConfig{
		Enabled: true,
		Model:   "gpt-4o-mini",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
	}

	tools, err := llmtools.NewTools(ops, nil)
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
	if len(client.tools) != 8 {
		t.Fatalf("len(client.tools) = %d, want 8", len(client.tools))
	}
}

func TestClientUpdateToolsKeepsDisabledState(t *testing.T) {
	alpha := invokableToolForTest{name: "alpha"}
	beta := invokableToolForTest{name: "beta"}
	gamma := invokableToolForTest{name: "gamma"}
	client := &Client{
		allTools: []tool.InvokableTool{alpha, beta},
		tools:    []tool.InvokableTool{alpha, beta},
		disabled: make(map[string]bool),
	}

	client.SetToolEnabled("beta", false)
	if got := toolNames(t, client.tools); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("tools after disable = %#v, want [alpha]", got)
	}

	client.UpdateTools([]tool.InvokableTool{beta, gamma})
	if got := toolNames(t, client.allTools); len(got) != 2 || got[0] != "beta" || got[1] != "gamma" {
		t.Fatalf("allTools after update = %#v, want [beta gamma]", got)
	}
	if got := toolNames(t, client.tools); len(got) != 1 || got[0] != "gamma" {
		t.Fatalf("tools after update = %#v, want [gamma]", got)
	}
	if disabled := client.DisabledTools(); !disabled["beta"] {
		t.Fatalf("DisabledTools = %#v, want beta disabled", disabled)
	}

	client.SetToolEnabled("beta", true)
	if got := toolNames(t, client.tools); len(got) != 2 || got[0] != "beta" || got[1] != "gamma" {
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

// TestClientCompleteSkipped 在没有真实 API key 时跳过测试。
func TestClientCompleteSkipped(t *testing.T) {
	if os.Getenv("OOPS_LLM_TEST_API_KEY") == "" {
		t.Skip("OOPS_LLM_TEST_API_KEY not set, skipping LLM integration test")
	}

	ops := &fakeOpsData{
		nodelets: []llmtools.NodeletSummary{
			{ID: "local", Name: "本机", Address: "http://127.0.0.1:8686", Available: true},
		},
	}

	cfg := config.LLMConfig{
		Enabled: true,
		Model:   "gpt-4o-mini",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  os.Getenv("OOPS_LLM_TEST_API_KEY"),
	}

	tools, err := llmtools.NewTools(ops, nil)
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
		schema.SystemMessage(prompt.BasePrompt),
		schema.UserMessage("用一句话回答：系统可以辅助排查容器问题。"),
	}
	answer, err := client.Complete(ctx, messages)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if answer == "" {
		t.Fatal("Complete() returned empty answer")
	}
	t.Logf("Answer: %s", answer)
}
