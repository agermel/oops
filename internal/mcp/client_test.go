package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"oops/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestConnectWithLogSSEWritesConnectionEventsToSSEStream(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	mcpServer.AddTool(mcp.NewTool("test-tool"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	testServer := server.NewTestServer(mcpServer)
	defer testServer.Close()

	hub := NewConnectionLogHub("conn-1")
	defer hub.Close()
	logWriter := hub.LineWriter(connectionLogStream("sse"))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, tools, _, closer, err := ConnectWithLog(ctx, config.MCPConfig{
		Transport: "sse",
		URL:       testServer.URL + "/sse",
	}, logWriter)
	if err != nil {
		t.Fatalf("ConnectWithLog: %v", err)
	}
	if session == nil {
		t.Fatal("ConnectWithLog returned nil session")
	}
	if len(tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(tools))
	}
	closer()
	if err := logWriter.Close(); err != nil {
		t.Fatalf("close log writer: %v", err)
	}

	logs := hub.Snapshot(20)
	if len(logs) == 0 {
		t.Fatal("SSE connection logs are empty")
	}
	var messages []string
	for _, entry := range logs {
		if entry.Stream != "sse" {
			t.Fatalf("log stream = %q, want sse", entry.Stream)
		}
		messages = append(messages, entry.Message)
	}
	joined := strings.Join(messages, "\n")
	for _, want := range []string{
		"SSE client created",
		"SSE transport started",
		"SSE initialize succeeded",
		"SSE discovered 1 tools",
		"SSE transport closed",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("SSE logs do not contain %q: %s", want, joined)
		}
	}
}

func TestConnectionLogStream(t *testing.T) {
	tests := map[string]string{
		"sse":     "sse",
		"stdio":   "stderr",
		"":        "stderr",
		"unknown": "stderr",
	}
	for transport, want := range tests {
		if got := connectionLogStream(transport); got != want {
			t.Errorf("connectionLogStream(%q) = %q, want %q", transport, got, want)
		}
	}
}

func TestWriteTransportLog(t *testing.T) {
	var buf bytes.Buffer
	writeTransportLog(&buf, "SSE discovered %d tools", 2)
	if got, want := buf.String(), "SSE discovered 2 tools\n"; got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}
