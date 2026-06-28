// Package mcp provides an MCP (Model Context Protocol) client
// that connects to community MCP servers and discovers their tools.
package mcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"oops/internal/config"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/cloudwego/eino/components/tool"
	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"
)

// Connect connects to an MCP server as configured in cfg, initializes the
// session, and returns every tool the server exposes. The returned closer
// function should be called to tear down the connection.
//
// Two transports are supported:
//
//	transport: "stdio"  → launches a child process (command + args)
//	transport: "sse"    → connects to a remote SSE endpoint (url)
func Connect(ctx context.Context, cfg config.MCPConfig) ([]tool.BaseTool, func(), error) {
	switch cfg.Transport {
	case "stdio":
		return connectStdio(ctx, cfg)
	case "sse":
		return connectSSE(ctx, cfg)
	default:
		return nil, nil, fmt.Errorf("unsupported mcp transport: %q", cfg.Transport)
	}
}

func connectStdio(ctx context.Context, cfg config.MCPConfig) ([]tool.BaseTool, func(), error) {
	c, err := mcpclient.NewStdioMCPClient(cfg.Command, cfg.Env, cfg.Args...)
	if err != nil {
		return nil, nil, fmt.Errorf("stdio: create client: %w", err)
	}

	// Read subprocess stderr in background for debugging.
	stderrReader, hasStderr := mcpclient.GetStderr(c)
	if hasStderr {
		go func() {
			var buf bytes.Buffer
			_, _ = io.Copy(&buf, stderrReader)
			if s := strings.TrimSpace(buf.String()); s != "" {
				log.Printf("mcp: %q stderr: %s", cfg.Command, s)
			}
		}()
	}

	// Give the MCP server time to connect to its backend before init.
	select {
	case <-ctx.Done():
		c.Close()
		return nil, nil, ctx.Err()
	case <-time.After(2 * time.Second):
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "oops",
		Version: "1.0.0",
	}

	if _, err = c.Initialize(ctx, initReq); err != nil {
		c.Close()
		return nil, nil, fmt.Errorf("stdio: initialize: %w", err)
	}

	tools, err := mcpp.GetTools(ctx, &mcpp.Config{Cli: c})
	if err != nil {
		c.Close()
		return nil, nil, fmt.Errorf("stdio: get tools: %w", err)
	}

	return tools, func() { c.Close() }, nil
}

func connectSSE(ctx context.Context, cfg config.MCPConfig) ([]tool.BaseTool, func(), error) {
	c, err := mcpclient.NewSSEMCPClient(cfg.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("sse: create client: %w", err)
	}

	if err := c.Start(ctx); err != nil {
		c.Close()
		return nil, nil, fmt.Errorf("sse: start: %w", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "oops",
		Version: "1.0.0",
	}

	if _, err = c.Initialize(ctx, initReq); err != nil {
		c.Close()
		return nil, nil, fmt.Errorf("sse: initialize: %w", err)
	}

	tools, err := mcpp.GetTools(ctx, &mcpp.Config{Cli: c})
	if err != nil {
		c.Close()
		return nil, nil, fmt.Errorf("sse: get tools: %w", err)
	}

	return tools, func() { c.Close() }, nil
}
