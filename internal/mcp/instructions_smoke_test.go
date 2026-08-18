package mcp

import (
	"context"
	"testing"
	"time"

	"oops/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestConnectWithLogSSECapturesInstructions 验证 initialize 握手返回的
// instructions 被 ConnectWithLog 捕获并作为返回值透出（client.go 的改动）。
func TestConnectWithLogSSECapturesInstructions(t *testing.T) {
	s := server.NewMCPServer("instr-test", "1.0.0",
		server.WithInstructions("用于查询数据库状态的只读工具"),
	)
	s.AddTool(mcp.NewTool("ping"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	ts := server.NewTestServer(s)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, tools, instructions, closer, err := ConnectWithLog(ctx, config.MCPConfig{
		Transport: "sse",
		URL:       ts.URL + "/sse",
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer closer()

	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	if instructions != "用于查询数据库状态的只读工具" {
		t.Fatalf("instructions = %q, want the configured value", instructions)
	}
	_ = session
}
