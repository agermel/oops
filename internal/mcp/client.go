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

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
)

// 收集 MCP 子进程写到 stderr 的错误日志
type StderrBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// 实现 io.Writer 来接收 stderr 内容
func (b *StderrBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// 实现 fmt.Stringer 来读取缓存文本
func (b *StderrBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	// Trim trailing newlines for cleaner error embedding.
	return strings.TrimRight(s, "\n")
}

// 用来建立MCP连接
func ConnectWithLog(ctx context.Context, cfg config.MCPConfig, logWriter io.Writer) (MCPSession, []tool.BaseTool, string, func(), error) {
	switch cfg.Transport {
	case "stdio":
		return connectStdio(ctx, cfg, logWriter)
	case "sse":
		return connectSSE(ctx, cfg, logWriter)
	default:
		return nil, nil, "", nil, fmt.Errorf("unsupported mcp transport: %q", cfg.Transport)
	}
}

func connectStdio(ctx context.Context, cfg config.MCPConfig, logWriter io.Writer) (MCPSession, []tool.BaseTool, string, func(), error) {
	// 新连接 Client
	c, err := mcpclient.NewStdioMCPClient(cfg.Command, cfg.Env, cfg.Args...)
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("stdio: create client: %w", err)
	}

	stderrBuf := &StderrBuffer{}

	// stderr 同时写入连接专属日志和错误缓冲；Console 由 Manager 注入。
	stderrReader, hasStderr := mcpclient.GetStderr(c)
	if hasStderr {
		writers := []io.Writer{stderrBuf}
		if logWriter != nil {
			writers = append(writers, logWriter)
		}
		go func() {
			_, _ = io.Copy(io.MultiWriter(writers...), stderrReader)
			if closer, ok := logWriter.(io.Closer); ok {
				_ = closer.Close()
			}
		}()
	}

	// 握手请求
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "oops",
		Version: "1.0.0",
	}

	initResult, err := c.Initialize(ctx, initReq)
	if err != nil {
		c.Close()
		stderr := stderrBuf.String()
		if stderr != "" {
			return nil, nil, "", nil, fmt.Errorf("stdio: initialize: %w\nstderr: %s", err, stderr)
		}
		return nil, nil, "", nil, fmt.Errorf("stdio: initialize: %w", err)
	}
	instructions := initResult.Instructions

	// 获取工具
	tools, err := mcpp.GetTools(ctx, &mcpp.Config{Cli: c})
	if err != nil {
		c.Close()
		stderr := stderrBuf.String()
		if stderr != "" {
			return nil, nil, "", nil, fmt.Errorf("stdio: get tools: %w\nstderr: %s", err, stderr)
		}
		return nil, nil, "", nil, fmt.Errorf("stdio: get tools: %w", err)
	}

	return c, tools, instructions, func() { c.Close() }, nil
}

func connectSSE(ctx context.Context, cfg config.MCPConfig, logWriter io.Writer) (MCPSession, []tool.BaseTool, string, func(), error) {
	c, err := mcpclient.NewSSEMCPClient(cfg.URL)
	if err != nil {
		writeTransportLog(logWriter, "SSE client create error: %v", err)
		return nil, nil, "", nil, fmt.Errorf("sse: create client: %w", err)
	}
	writeTransportLog(logWriter, "SSE client created")

	if err := c.Start(ctx); err != nil {
		writeTransportLog(logWriter, "SSE transport start error: %v", err)
		c.Close()
		return nil, nil, "", nil, fmt.Errorf("sse: start: %w", err)
	}
	writeTransportLog(logWriter, "SSE transport started")

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "oops",
		Version: "1.0.0",
	}

	initResult, err := c.Initialize(ctx, initReq)
	if err != nil {
		writeTransportLog(logWriter, "SSE initialize error: %v", err)
		c.Close()
		return nil, nil, "", nil, fmt.Errorf("sse: initialize: %w", err)
	}
	instructions := initResult.Instructions
	writeTransportLog(logWriter, "SSE initialize succeeded")

	tools, err := mcpp.GetTools(ctx, &mcpp.Config{Cli: c})
	if err != nil {
		writeTransportLog(logWriter, "SSE tool discovery error: %v", err)
		c.Close()
		return nil, nil, "", nil, fmt.Errorf("sse: get tools: %w", err)
	}
	writeTransportLog(logWriter, "SSE discovered %d tools", len(tools))

	return c, tools, instructions, func() {
		writeTransportLog(logWriter, "SSE transport closed")
		c.Close()
	}, nil
}

func writeTransportLog(logWriter io.Writer, format string, args ...any) {
	if logWriter == nil {
		return
	}
	_, _ = fmt.Fprintf(logWriter, format+"\n", args...)
}

// ---- 连接验证 ----

// MCPSession 抽象 MCP 连接的核心能力（stdio 和 sse 共有的接口）。
type MCPSession interface {
	CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
	Ping(ctx context.Context) error
	ListPrompts(ctx context.Context, req mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error)
	ListResources(ctx context.Context, req mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error)
	ReadResource(ctx context.Context, req mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error)
}

// Verify checks that an MCP session is alive by sending a protocol-level ping.
// This is transport-agnostic and safe — it does not invoke any MCP tool, so
// there is zero risk of accidentally triggering a destructive operation.
func Verify(ctx context.Context, session MCPSession) error {
	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := session.Ping(verifyCtx); err != nil {
		return fmt.Errorf("mcp verify: ping: %w", err)
	}
	return nil
}
