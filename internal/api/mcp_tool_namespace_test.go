package api

import (
	"context"
	"reflect"
	"testing"

	"oops/internal/config"
	"oops/internal/llm/agent"
	llmtools "oops/internal/llm/tools"
	"oops/internal/mcp"
	"oops/internal/nodelet"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestNamespacedMCPToolUsesModelNameAndDelegatesRun(t *testing.T) {
	inner := &invokableToolForTest{name: "info", desc: "Redis info", output: "ok"}
	wrapped := namespacedMCPTool{
		modelName: "redis_info",
		inner:     inner,
	}

	info, err := wrapped.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != "redis_info" {
		t.Fatalf("Info().Name = %q, want redis_info", info.Name)
	}
	if info.Desc != "Redis info" {
		t.Fatalf("Info().Desc = %q, want Redis info", info.Desc)
	}

	got, err := wrapped.InvokableRun(context.Background(), `{"section":"default"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if got != "ok" {
		t.Fatalf("InvokableRun() = %q, want ok", got)
	}
	if inner.lastArgs != `{"section":"default"}` {
		t.Fatalf("inner args = %q, want forwarded args", inner.lastArgs)
	}
}

func TestMCPToolHooksUseModelFacingToolName(t *testing.T) {
	inner := &invokableToolForTest{name: "info", desc: "Redis info", output: "ok"}
	wrapped := namespacedMCPTool{
		modelName: "redis_info",
		inner:     inner,
	}
	var beforeName string
	var afterName string
	model := &mcpHookTestModel{
		generate: func(call int, input []*schema.Message) (*schema.Message, error) {
			if call == 1 {
				return schema.AssistantMessage("need redis", []schema.ToolCall{{
					ID: "call-1",
					Function: schema.FunctionCall{
						Name:      "redis_info",
						Arguments: `{"section":"default"}`,
					},
				}}), nil
			}
			return schema.AssistantMessage("done", nil), nil
		},
	}

	events, err := agent.NewRunner().Run(context.Background(), agent.RunRequest{
		Model:    model,
		Tools:    []tool.InvokableTool{wrapped},
		Messages: []*schema.Message{schema.UserMessage("use redis info")},
		MaxStep:  3,
		Hooks: agent.RunHooks{
			BeforeToolCall: func(_ context.Context, input agent.ToolCallInput) (string, error) {
				beforeName = input.Name
				return input.Arguments, nil
			},
			SemanticAfterToolCall: func(_ context.Context, input agent.ToolCallInput, output agent.ToolCallOutput) (string, error) {
				afterName = input.Name
				return output.Result, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for range events {
	}

	if beforeName != "redis_info" || afterName != "redis_info" {
		t.Fatalf("hook names before=%q after=%q, want model-facing name", beforeName, afterName)
	}
	if inner.lastArgs != `{"section":"default"}` {
		t.Fatalf("inner args = %q, want forwarded args", inner.lastArgs)
	}
}

func TestFilterMCPToolEntriesForProjectKeepsProjectNodelets(t *testing.T) {
	projectStore := testProjectStore(t)
	if err := projectStore.Add(config.Project{ID: "proj-1", Name: "Project", NodeletIDs: []string{"node-a"}}); err != nil {
		t.Fatalf("add project: %v", err)
	}
	entries := []mcp.ConnectionTool{
		{ConnectionID: "a", NodeletID: "node-a", OriginalName: "info"},
		{ConnectionID: "b", NodeletID: "node-b", OriginalName: "info"},
	}

	got := filterMCPToolEntriesForProject(entries, "proj-1", projectStore)
	if len(got) != 1 || got[0].ConnectionID != "a" {
		t.Fatalf("filtered entries = %+v, want only node-a connection", got)
	}
	if got := filterMCPToolEntriesForProject(entries, "", projectStore); len(got) != 2 {
		t.Fatalf("global entries = %+v, want all entries", got)
	}
}

func TestNamespaceMCPTools(t *testing.T) {
	tests := []struct {
		name     string
		reserved map[string]struct{}
		entries  []mcp.ConnectionTool
		want     []string
	}{
		{
			name: "single redis generic tool gets backend prefix",
			entries: []mcp.ConnectionTool{{
				ConnectionID:   "cache",
				ConnectionName: "Redis",
				ConnectionType: "redis",
				OriginalName:   "info",
			}},
			want: []string{"redis_info"},
		},
		{
			name: "already-prefixed etcd tool keeps name",
			entries: []mcp.ConnectionTool{{
				ConnectionID:   "etcd-main",
				ConnectionName: "Etcd",
				ConnectionType: "etcd",
				OriginalName:   "etcd_get",
			}},
			want: []string{"etcd_get"},
		},
		{
			name: "duplicate redis tools use server scoped names",
			entries: []mcp.ConnectionTool{
				{
					ConnectionID:   "cache-a",
					ConnectionName: "Redis",
					ConnectionType: "redis",
					NodeletID:      "node-a",
					ServerName:     "Cache A",
					OriginalName:   "info",
				},
				{
					ConnectionID:   "cache-b",
					ConnectionName: "Cache",
					ConnectionType: "redis",
					NodeletID:      "node-b",
					ServerName:     "Cache B",
					OriginalName:   "info",
				},
			},
			want: []string{"redis_server_cache_a_info", "redis_server_cache_b_info"},
		},
		{
			name: "duplicate tools on same server add connection scope",
			entries: []mcp.ConnectionTool{
				{
					ConnectionID:   "cache-a",
					ConnectionName: "Redis Cache A",
					ConnectionType: "redis",
					NodeletID:      "prod",
					ServerName:     "Prod",
					OriginalName:   "info",
				},
				{
					ConnectionID:   "cache-b",
					ConnectionName: "Redis Cache B",
					ConnectionType: "redis",
					NodeletID:      "prod",
					ServerName:     "Prod",
					OriginalName:   "info",
				},
			},
			want: []string{"redis_server_prod_cache_a_info", "redis_server_prod_cache_b_info"},
		},
		{
			name: "native name collision uses server scoped name",
			reserved: map[string]struct{}{
				"repo_sync": {},
			},
			entries: []mcp.ConnectionTool{{
				ConnectionID:   "repo-main",
				ConnectionName: "Repo",
				ConnectionType: "repo",
				NodeletID:      "code-host",
				ServerName:     "Code Host",
				OriginalName:   "sync",
			}},
			want: []string{"repo_server_code_host_sync"},
		},
		{
			name: "native name collision without server falls back to connection scope",
			reserved: map[string]struct{}{
				"repo_sync": {},
			},
			entries: []mcp.ConnectionTool{{
				ConnectionID:   "repo-main",
				ConnectionName: "Repo",
				ConnectionType: "repo",
				OriginalName:   "sync",
			}},
			want: []string{"repo_main_sync"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEntries := namespaceMCPTools(tt.entries, tt.reserved)
			if len(gotEntries) != len(tt.want) {
				t.Fatalf("len(namespaceMCPTools) = %d, want %d", len(gotEntries), len(tt.want))
			}
			for i, want := range tt.want {
				if gotEntries[i].ModelName != want {
					t.Fatalf("ModelName[%d] = %q, want %q", i, gotEntries[i].ModelName, want)
				}
				if gotEntries[i].OriginalName != tt.entries[i].OriginalName {
					t.Fatalf("OriginalName[%d] = %q, want %q", i, gotEntries[i].OriginalName, tt.entries[i].OriginalName)
				}
			}
		})
	}
}

func TestWithMCPToolServerNames(t *testing.T) {
	nm := testNodeletManager(t)
	if err := nm.Add(&nodelet.NodeletConfig{ID: "node-a", Name: "Cache Server A", Address: "http://nodelet", Token: "secret"}); err != nil {
		t.Fatalf("add nodelet: %v", err)
	}
	server := &Server{nodeletManager: nm}

	entries := []mcp.ConnectionTool{{
		ConnectionID:   "cache-a",
		ConnectionName: "Redis",
		ConnectionType: "redis",
		NodeletID:      "node-a",
		OriginalName:   "info",
	}}
	got := server.withMCPToolServerNames(entries)
	if got[0].ServerName != "Cache Server A" {
		t.Fatalf("ServerName = %q, want Cache Server A", got[0].ServerName)
	}
	if entries[0].ServerName != "" {
		t.Fatalf("input ServerName mutated to %q", entries[0].ServerName)
	}
}

func TestOnMCPToolsChangedUpdatesClientWithNamespacedTools(t *testing.T) {
	server := &Server{}
	cfg := config.LLMConfig{
		Model:   "gpt-4o-mini",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
	}
	client, err := agent.NewClient(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	server.llmClient = client

	nativeTools, err := llmtools.NewTools(server, nil)
	if err != nil {
		t.Fatalf("NewTools: %v", err)
	}
	client.SetToolEnabled("redis_info", false)
	server.onMCPToolsChanged([]mcp.ConnectionTool{{
		ConnectionID:   "cache",
		ConnectionName: "Redis",
		ConnectionType: "redis",
		OriginalName:   "info",
		Tool:           &invokableToolForTest{name: "info", desc: "Redis info", output: "ok"},
	}})

	active, all := clientToolLens(t, client)
	if all != len(nativeTools)+1 {
		t.Fatalf("all tool count = %d, want %d", all, len(nativeTools)+1)
	}
	if active != len(nativeTools) {
		t.Fatalf("active tool count = %d, want %d after redis_info disabled", active, len(nativeTools))
	}
	if disabled := client.DisabledTools(); !disabled["redis_info"] {
		t.Fatalf("DisabledTools = %#v, want redis_info disabled", disabled)
	}

	client.SetToolEnabled("redis_info", true)
	active, all = clientToolLens(t, client)
	if all != len(nativeTools)+1 || active != len(nativeTools)+1 {
		t.Fatalf("tool counts after re-enable active=%d all=%d, want %d", active, all, len(nativeTools)+1)
	}
}

func clientToolLens(t *testing.T, client *agent.Client) (active int, all int) {
	t.Helper()
	value := reflect.ValueOf(client).Elem()
	return value.FieldByName("tools").Len(), value.FieldByName("allTools").Len()
}

type invokableToolForTest struct {
	name     string
	desc     string
	output   string
	lastArgs string
}

func (t *invokableToolForTest) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: t.desc}, nil
}

func (t *invokableToolForTest) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	t.lastArgs = argumentsInJSON
	return t.output, nil
}

type mcpHookTestModel struct {
	calls    int
	generate func(call int, input []*schema.Message) (*schema.Message, error)
}

func (m *mcpHookTestModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.calls++
	return m.generate(m.calls, input)
}

func (m *mcpHookTestModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *mcpHookTestModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
