package api

import (
	"context"
	"slices"
	"strings"
	"testing"

	"oops/internal/mcp"

	mcpproto "github.com/mark3labs/mcp-go/mcp"
)

// fakeAccessor 是 mcpContentAccessor 的测试替身：返回预设结果并记录调用参数。
type fakeAccessor struct {
	listResourcesRes *mcpproto.ListResourcesResult
	listResourcesErr error
	readResourceRes  *mcpproto.ReadResourceResult
	readResourceErr  error
	listPromptsRes   *mcpproto.ListPromptsResult
	listPromptsErr   error
	getPromptRes     *mcpproto.GetPromptResult
	getPromptErr     error

	// 记录最近一次调用的参数，便于断言参数解析。
	connID        string
	readURI       string
	getPromptName string
	getPromptArgs map[string]string
}

func (f *fakeAccessor) ListResources(_ context.Context, connID string) (*mcpproto.ListResourcesResult, error) {
	f.connID = connID
	return f.listResourcesRes, f.listResourcesErr
}

func (f *fakeAccessor) ReadResource(_ context.Context, connID, uri string) (*mcpproto.ReadResourceResult, error) {
	f.connID = connID
	f.readURI = uri
	return f.readResourceRes, f.readResourceErr
}

func (f *fakeAccessor) ListPrompts(_ context.Context, connID string) (*mcpproto.ListPromptsResult, error) {
	f.connID = connID
	return f.listPromptsRes, f.listPromptsErr
}

func (f *fakeAccessor) GetPrompt(_ context.Context, connID, name string, args map[string]string) (*mcpproto.GetPromptResult, error) {
	f.connID = connID
	f.getPromptName = name
	f.getPromptArgs = args
	return f.getPromptRes, f.getPromptErr
}

func newAccessorToolForTest(kind accessorKind, accessor mcpContentAccessor, connID, name string) mcpAccessorTool {
	return mcpAccessorTool{
		accessor: accessor,
		connID:   connID,
		info:     accessorToolInfo(name),
		kind:     kind,
	}
}

func TestAccessorToolListResources(t *testing.T) {
	fa := &fakeAccessor{
		listResourcesRes: &mcpproto.ListResourcesResult{
			Resources: []mcpproto.Resource{{URI: "info://readme", Name: "readme"}},
		},
	}
	tool := newAccessorToolForTest(accessorListResources, fa, "conn-1", "list_resources")

	out, err := tool.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if fa.connID != "conn-1" {
		t.Errorf("connID = %q, want conn-1", fa.connID)
	}
	if !strings.Contains(out, "info://readme") {
		t.Errorf("output %q missing resource uri", out)
	}
}

func TestAccessorToolReadResource(t *testing.T) {
	fa := &fakeAccessor{
		readResourceRes: &mcpproto.ReadResourceResult{
			Contents: []mcpproto.ResourceContents{
				mcpproto.TextResourceContents{URI: "info://readme", MIMEType: "text/plain", Text: "hello"},
			},
		},
	}
	tool := newAccessorToolForTest(accessorReadResource, fa, "conn-1", "read_resource")

	out, err := tool.InvokableRun(context.Background(), `{"uri":"info://readme"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if fa.readURI != "info://readme" {
		t.Errorf("readURI = %q, want info://readme", fa.readURI)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("output %q missing content", out)
	}
}

func TestAccessorToolReadResourceMissingURI(t *testing.T) {
	fa := &fakeAccessor{}
	tool := newAccessorToolForTest(accessorReadResource, fa, "conn-1", "read_resource")

	if _, err := tool.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("expected error for missing uri, got nil")
	} else if !strings.Contains(err.Error(), "uri is required") {
		t.Errorf("error = %q, want uri is required", err.Error())
	}
}

func TestAccessorToolReadResourceBadJSON(t *testing.T) {
	fa := &fakeAccessor{}
	tool := newAccessorToolForTest(accessorReadResource, fa, "conn-1", "read_resource")

	if _, err := tool.InvokableRun(context.Background(), `not-json`); err == nil {
		t.Fatal("expected error for bad json, got nil")
	}
}

func TestAccessorToolListPrompts(t *testing.T) {
	fa := &fakeAccessor{
		listPromptsRes: &mcpproto.ListPromptsResult{
			Prompts: []mcpproto.Prompt{{Name: "check-status"}},
		},
	}
	tool := newAccessorToolForTest(accessorListPrompts, fa, "conn-1", "list_prompts")

	out, err := tool.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "check-status") {
		t.Errorf("output %q missing prompt name", out)
	}
}

func TestAccessorToolGetPrompt(t *testing.T) {
	fa := &fakeAccessor{
		getPromptRes: &mcpproto.GetPromptResult{
			Messages: []mcpproto.PromptMessage{
				mcpproto.NewPromptMessage(mcpproto.RoleAssistant, mcpproto.TextContent{Type: "text", Text: "hi"}),
			},
		},
	}
	tool := newAccessorToolForTest(accessorGetPrompt, fa, "conn-1", "get_prompt")

	out, err := tool.InvokableRun(context.Background(), `{"name":"check-status","arguments":{"x":"y"}}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if fa.getPromptName != "check-status" {
		t.Errorf("getPromptName = %q, want check-status", fa.getPromptName)
	}
	if fa.getPromptArgs["x"] != "y" {
		t.Errorf("getPromptArgs = %v, want x=y", fa.getPromptArgs)
	}
	if !strings.Contains(out, "hi") {
		t.Errorf("output %q missing message text", out)
	}
}

func TestAccessorToolGetPromptMissingName(t *testing.T) {
	fa := &fakeAccessor{}
	tool := newAccessorToolForTest(accessorGetPrompt, fa, "conn-1", "get_prompt")

	if _, err := tool.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("expected error for missing name, got nil")
	} else if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error = %q, want name is required", err.Error())
	}
}

func TestAccessorToolInfo(t *testing.T) {
	wantParams := map[string]bool{
		"list_resources": false,
		"read_resource":  true,
		"list_prompts":   false,
		"get_prompt":     true,
	}
	for name, hasParams := range wantParams {
		info := accessorToolInfo(name)
		if info.Name != name {
			t.Errorf("accessorToolInfo(%q).Name = %q", name, info.Name)
		}
		if info.Desc == "" {
			t.Errorf("accessorToolInfo(%q).Desc is empty", name)
		}
		if (info.ParamsOneOf != nil) != hasParams {
			t.Errorf("accessorToolInfo(%q).ParamsOneOf = %v, want non-nil=%v", name, info.ParamsOneOf != nil, hasParams)
		}
	}

	readSchema, err := accessorToolInfo("read_resource").ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("read_resource ToJSONSchema: %v", err)
	}
	if !slices.Contains(readSchema.Required, "uri") {
		t.Errorf("read_resource required = %v, want uri", readSchema.Required)
	}

	getSchema, err := accessorToolInfo("get_prompt").ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("get_prompt ToJSONSchema: %v", err)
	}
	if !slices.Contains(getSchema.Required, "name") {
		t.Errorf("get_prompt required = %v, want name", getSchema.Required)
	}
}

func TestBuildSyntheticAccessorEntries(t *testing.T) {
	fa := &fakeAccessor{}
	conns := []mcp.ConnectionWithStatus{
		{
			ConnectionConfig: mcp.ConnectionConfig{ID: "a", Name: "A", Type: "mysql", NodeletID: "n1"},
			Status:           "running",
			Resources:        []mcp.ResourceInfo{{URI: "info://x"}},
			Prompts:          []mcp.PromptInfo{{Name: "p"}},
		},
		{
			ConnectionConfig: mcp.ConnectionConfig{ID: "b", Name: "B", Type: "redis"},
			Status:           "running",
			Resources:        []mcp.ResourceInfo{{URI: "info://y"}},
		},
		{
			ConnectionConfig: mcp.ConnectionConfig{ID: "c", Name: "C", Type: "pg"},
			Status:           "running",
		},
		{
			ConnectionConfig: mcp.ConnectionConfig{ID: "d", Name: "D", Type: "es"},
			Status:           "stopped",
			Resources:        []mcp.ResourceInfo{{URI: "info://z"}},
			Prompts:          []mcp.PromptInfo{{Name: "q"}},
		},
	}

	entries := buildSyntheticAccessorEntries(conns, fa)

	// a -> 4 工具，b -> 2 工具，c -> 0，d（非 running）-> 0。
	if len(entries) != 6 {
		t.Fatalf("len(entries) = %d, want 6", len(entries))
	}

	namesByConn := map[string][]string{}
	for _, e := range entries {
		namesByConn[e.ConnectionID] = append(namesByConn[e.ConnectionID], e.OriginalName)
		if e.ConnectionType == "" {
			t.Errorf("entry %q missing ConnectionType", e.OriginalName)
		}
		if e.NodeletID == "" && e.ConnectionID == "a" {
			t.Errorf("entry %q for conn a missing NodeletID", e.OriginalName)
		}
	}

	wantA := []string{"list_resources", "read_resource", "list_prompts", "get_prompt"}
	if got := namesByConn["a"]; !slices.Equal(got, wantA) {
		t.Errorf("conn a names = %v, want %v", got, wantA)
	}
	wantB := []string{"list_resources", "read_resource"}
	if got := namesByConn["b"]; !slices.Equal(got, wantB) {
		t.Errorf("conn b names = %v, want %v", got, wantB)
	}
	if _, ok := namesByConn["c"]; ok {
		t.Errorf("conn c should produce no entries, got %v", namesByConn["c"])
	}
	if _, ok := namesByConn["d"]; ok {
		t.Errorf("conn d (stopped) should produce no entries, got %v", namesByConn["d"])
	}

	// 每个条目的 Tool 都应是带正确 connID/kind 的 mcpAccessorTool。
	for _, e := range entries {
		at, ok := e.Tool.(mcpAccessorTool)
		if !ok {
			t.Errorf("entry %q Tool is %T, want mcpAccessorTool", e.OriginalName, e.Tool)
			continue
		}
		if at.connID != e.ConnectionID {
			t.Errorf("entry %q connID = %q, want %q", e.OriginalName, at.connID, e.ConnectionID)
		}
	}
}
