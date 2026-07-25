package api

import (
	"context"
	"testing"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
	agentruntime "oops/internal/agent/runtime"
	"oops/internal/config"
	"oops/internal/mcp"
	"oops/internal/nodelet"
	"oops/internal/project"

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
	runtimeTools, _, err := agentruntime.FromInvokableTools(context.Background(), []tool.InvokableTool{wrapped})
	if err != nil {
		t.Fatalf("FromInvokableTools() error = %v", err)
	}
	runner, err := toolruntime.NewRunner(toolruntime.RunnerConfig{
		Tools: runtimeTools,
		Hooks: toolruntime.Hooks{
			BeforeToolCall: func(_ context.Context, input toolruntime.BeforeToolCallContext) (toolruntime.BeforeToolCallResult, error) {
				beforeName = input.ToolCall.Name
				return toolruntime.BeforeToolCallResult{Arguments: input.RawArguments}, nil
			},
			AfterToolCall: func(_ context.Context, input toolruntime.AfterToolCallContext) (toolruntime.AfterToolCallResult, error) {
				afterName = input.ToolCall.Name
				return toolruntime.AfterToolCallResult{}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	toolCall := protocol.NewToolCallContent("call-1", "redis_info", []byte(`{"section":"default"}`))
	_, err = runner.ExecuteTools(context.Background(), toolruntime.ToolRunRequest{
		AssistantMessage: protocol.AssistantMessage{
			Content: protocol.ContentList{
				protocol.NewTextContent("need redis"),
				toolCall,
			},
		},
		ToolCalls: []protocol.ToolCallContent{toolCall},
		Emit: func(context.Context, protocol.AgentEvent) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTools() error = %v", err)
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
	if err := projectStore.Add(project.Project{ID: "proj-1", Name: "Project", NodeletIDs: []string{"node-a"}}); err != nil {
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

func TestClientFiltersNamespacedMCPToolsFromCurrentSnapshot(t *testing.T) {
	server := &Server{}
	cfg := config.LLMConfig{
		Model:   "gpt-4o-mini",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
	}
	ctx := context.Background()
	client, err := agentruntime.NewClient(ctx, cfg, agentruntime.ClientOptions{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	server.llmClient = client

	nativeTools, err := agentruntime.NewTools(server, nil)
	if err != nil {
		t.Fatalf("NewTools: %v", err)
	}
	mcpEntries := namespaceMCPTools([]mcp.ConnectionTool{{
		ConnectionID:   "cache",
		ConnectionName: "Redis",
		ConnectionType: "redis",
		OriginalName:   "info",
		Tool:           &invokableToolForTest{name: "info", desc: "Redis info", output: "ok"},
	}}, nativeToolNames(ctx, nativeTools))
	allTools := append([]tool.InvokableTool{}, nativeTools...)
	for _, entry := range mcpEntries {
		invokable, ok := entry.Tool.(tool.InvokableTool)
		if !ok {
			t.Fatalf("MCP tool %q is not invokable", entry.OriginalName)
		}
		allTools = append(allTools, namespacedMCPTool{
			modelName: entry.ModelName,
			inner:     invokable,
		})
	}

	client.SetToolEnabled("redis_info", false)
	active := client.EnabledTools(allTools)
	assertToolCount(t, active, len(nativeTools))
	assertToolAbsent(t, active, "redis_info")
	if disabled := client.DisabledTools(); !disabled["redis_info"] {
		t.Fatalf("DisabledTools = %#v, want redis_info disabled", disabled)
	}

	client.SetToolEnabled("redis_info", true)
	active = client.EnabledTools(allTools)
	assertToolCount(t, active, len(nativeTools)+1)
	assertToolPresent(t, active, "redis_info")
}

func assertToolCount(t *testing.T, tools []tool.InvokableTool, want int) {
	t.Helper()
	if len(tools) != want {
		t.Fatalf("tool count = %d, want %d", len(tools), want)
	}
}

func assertToolPresent(t *testing.T, tools []tool.InvokableTool, name string) {
	t.Helper()
	if !hasToolNamed(t, tools, name) {
		t.Fatalf("tool %q missing", name)
	}
}

func assertToolAbsent(t *testing.T, tools []tool.InvokableTool, name string) {
	t.Helper()
	if hasToolNamed(t, tools, name) {
		t.Fatalf("tool %q present", name)
	}
}

func hasToolNamed(t *testing.T, tools []tool.InvokableTool, name string) bool {
	t.Helper()
	for _, item := range tools {
		info, err := item.Info(context.Background())
		if err != nil {
			t.Fatalf("tool Info: %v", err)
		}
		if info.Name == name {
			return true
		}
	}
	return false
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
