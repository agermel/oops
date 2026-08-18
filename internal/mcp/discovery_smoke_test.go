package mcp

import (
	"context"
	"testing"
	"time"

	"oops/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestDiscoverPromptsAndResources 验证连接后可拉取 server 暴露的 prompts 与 resources 列表，
// 且 ReadResource 能读到 resource 正文（本次 feature 的后端核心路径）。
func TestDiscoverPromptsAndResources(t *testing.T) {
	s := server.NewMCPServer("discovery-test", "1.0.0",
		server.WithToolCapabilities(false),
		server.WithPromptCapabilities(true),
		server.WithResourceCapabilities(true, false),
	)
	s.AddPrompt(
		mcp.NewPrompt("check-status", mcp.WithPromptDescription("检查服务状态的提示词")),
		func(context.Context, mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{}, nil
		},
	)
	s.AddResource(
		mcp.NewResource("info://readme", "readme",
			mcp.WithResourceDescription("服务说明文档"),
			mcp.WithMIMEType("text/plain"),
		),
		func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{URI: "info://readme", MIMEType: "text/plain", Text: "hello from readme"},
			}, nil
		},
	)
	ts := server.NewTestServer(s)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, _, _, closer, err := ConnectWithLog(ctx, config.MCPConfig{
		Transport: "sse",
		URL:       ts.URL + "/sse",
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer closer()

	prompts, resources := discoverPromptsAndResources(ctx, session, nil)
	if len(prompts) != 1 || prompts[0].Name != "check-status" {
		t.Fatalf("prompts = %+v, want [check-status]", prompts)
	}
	if len(resources) != 1 || resources[0].URI != "info://readme" {
		t.Fatalf("resources = %+v, want [info://readme]", resources)
	}

	result, err := session.ReadResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: "info://readme"},
	})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(result.Contents))
	}
	text, ok := result.Contents[0].(mcp.TextResourceContents)
	if !ok || text.Text != "hello from readme" {
		t.Fatalf("content = %+v, want text %q", result.Contents[0], "hello from readme")
	}
}
