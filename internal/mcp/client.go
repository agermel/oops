// Package mcp provides an MCP (Model Context Protocol) client
// that connects to community MCP servers and discovers their tools.
package mcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"oops/internal/config"
	"oops/internal/console"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/cloudwego/eino/components/tool"
	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"
)

// StderrBuffer is a thread-safe buffer that captures stderr output from an
// MCP subprocess. It implements io.Writer and can be read at any time via
// String() to retrieve the accumulated output — useful for surfacing the
// real error when a subprocess exits unexpectedly.
type StderrBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *StderrBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns the accumulated stderr output.
func (b *StderrBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	// Trim trailing newlines for cleaner error embedding.
	return strings.TrimRight(s, "\n")
}

// Connect connects to an MCP server as configured in cfg, initializes the
// session, and returns every tool the server exposes. The returned closer
// function should be called to tear down the connection. The exitCh is
// closed when the underlying subprocess exits (always nil for SSE).
// The stderrBuf captures the subprocess stderr stream; it is nil for SSE
// transports.
//
// Two transports are supported:
//
//	transport: "stdio"  → launches a child process (command + args)
//	transport: "sse"    → connects to a remote SSE endpoint (url)
func Connect(ctx context.Context, cfg config.MCPConfig) (MCPSession, []tool.BaseTool, func(), <-chan struct{}, *StderrBuffer, error) {
	switch cfg.Transport {
	case "stdio":
		return connectStdio(ctx, cfg)
	case "sse":
		return connectSSE(ctx, cfg)
	default:
		return nil, nil, nil, nil, nil, fmt.Errorf("unsupported mcp transport: %q", cfg.Transport)
	}
}

func connectStdio(ctx context.Context, cfg config.MCPConfig) (MCPSession, []tool.BaseTool, func(), <-chan struct{}, *StderrBuffer, error) {
	c, err := mcpclient.NewStdioMCPClient(cfg.Command, cfg.Env, cfg.Args...)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("stdio: create client: %w", err)
	}

	exitCh := make(chan struct{})
	stderrBuf := &StderrBuffer{}

	// MCP 子进程 stderr 同时输出到控制台（实时可见）和 buffer（错误时回显）。
	stderrReader, hasStderr := mcpclient.GetStderr(c)
	if hasStderr {
		go func() {
			_, _ = io.Copy(io.MultiWriter(
				console.NewLineWriter(fmt.Sprintf("mcp-stderr(%s)", cfg.Command)),
				stderrBuf,
			), stderrReader)
			close(exitCh) // stderr pipe closed ⟹ process exited
		}()
	} else {
		// 没有 stderr 时无法检测退出，关闭 exitCh 以避免监听者永久阻塞。
		close(exitCh)
	}

	// Give the MCP server time to connect to its backend before init.
	select {
	case <-ctx.Done():
		c.Close()
		return nil, nil, nil, nil, stderrBuf, ctx.Err()
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
		stderr := stderrBuf.String()
		if stderr != "" {
			return nil, nil, nil, nil, stderrBuf, fmt.Errorf("stdio: initialize: %w\nstderr: %s", err, stderr)
		}
		return nil, nil, nil, nil, stderrBuf, fmt.Errorf("stdio: initialize: %w", err)
	}

	tools, err := mcpp.GetTools(ctx, &mcpp.Config{Cli: c})
	if err != nil {
		c.Close()
		stderr := stderrBuf.String()
		if stderr != "" {
			return nil, nil, nil, nil, stderrBuf, fmt.Errorf("stdio: get tools: %w\nstderr: %s", err, stderr)
		}
		return nil, nil, nil, nil, stderrBuf, fmt.Errorf("stdio: get tools: %w", err)
	}

	return c, tools, func() { c.Close() }, exitCh, stderrBuf, nil
}

func connectSSE(ctx context.Context, cfg config.MCPConfig) (MCPSession, []tool.BaseTool, func(), <-chan struct{}, *StderrBuffer, error) {
	c, err := mcpclient.NewSSEMCPClient(cfg.URL)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("sse: create client: %w", err)
	}

	if err := c.Start(ctx); err != nil {
		c.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("sse: start: %w", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "oops",
		Version: "1.0.0",
	}

	if _, err = c.Initialize(ctx, initReq); err != nil {
		c.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("sse: initialize: %w", err)
	}

	tools, err := mcpp.GetTools(ctx, &mcpp.Config{Cli: c})
	if err != nil {
		c.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("sse: get tools: %w", err)
	}

	// SSE 连接没有子进程退出概念，返回 nil channel 和 nil stderr。
	return c, tools, func() { c.Close() }, nil, nil, nil
}

// ---- 连接验证 ----

// MCPSession 抽象 MCP 连接的核心能力（stdio 和 sse 共有的接口）。
type MCPSession interface {
	CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
	Ping(ctx context.Context) error
}

// safeVerifyTools 是连接验证时优先尝试的安全工具名列表。
var safeVerifyTools = []string{
	"ping", "echo", "dbsize", "info", "server_info",
	"list_databases", "list_keys", "get_config",
}

// Verify probes an MCP connection by calling safe tools in order.
// Returns nil if any safe tool responds without error.
func Verify(ctx context.Context, session MCPSession, tools []tool.BaseTool) error {
	if len(tools) == 0 {
		return fmt.Errorf("no tools available for verification")
	}

	// Build ordered list of probe candidates from safeVerifyTools that exist.
	var probes []string
	for _, name := range safeVerifyTools {
		for _, t := range tools {
			info, err := t.Info(ctx)
			if err != nil {
				continue
			}
			if info.Name == name {
				probes = append(probes, name)
				break
			}
		}
	}
	// Fallback: if no safe tool found, use the first available tool.
	if len(probes) == 0 {
		info, err := tools[0].Info(ctx)
		if err != nil {
			return fmt.Errorf("get tool info: %w", err)
		}
		probes = append(probes, info.Name)
	}

	var lastErr error
	for _, probeName := range probes {
		console.Feed("mcp: verifying connection with %q ...", probeName)

		verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		req := mcp.CallToolRequest{}
		req.Params.Name = probeName
		req.Params.Arguments = map[string]any{}

		result, err := session.CallTool(verifyCtx, req)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("verify %q: call: %w", probeName, err)
			continue
		}
		if result.IsError {
			var msgs []string
			for _, block := range result.Content {
				if tb, ok := block.(mcp.TextContent); ok {
					msgs = append(msgs, tb.Text)
				}
			}
			errMsg := joinStrings(msgs, "; ")
			if errMsg == "" {
				errMsg = "no error detail"
			}
			lastErr = fmt.Errorf("verify %q: %s", probeName, errMsg)
			continue
		}

		console.Feed("mcp: verify %q ok", probeName)
		return nil
	}

	return fmt.Errorf("all verify probes failed, last: %w", lastErr)
}

func joinStrings(ss []string, sep string) string {
	r := ""
	for i, s := range ss {
		if i > 0 {
			r += sep
		}
		r += s
	}
	return r
}
