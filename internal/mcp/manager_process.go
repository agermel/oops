package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// managedTool keeps metadata obtained before a process becomes visible to the
// manager. Metadata reads can perform transport work, so runtime snapshots only
// use this cached value.
type managedTool struct {
	base tool.BaseTool
	info *schema.ToolInfo
}

// managedProcess owns one MCP connection and its in-flight callers.
// A lease keeps the underlying transport alive while a tool call or health
// probe uses it. Draining rejects new leases and lets existing callers finish.
type managedProcess struct {
	cfg      ConnectionConfig
	session  MCPSession
	closer   func()
	tools    []tool.BaseTool
	metadata []managedTool

	mu        sync.Mutex
	draining  bool
	lifecycle context.Context
	cancel    context.CancelFunc
	leases    sync.WaitGroup
	workers   sync.WaitGroup
	closeOnce sync.Once
}

func (p *managedProcess) prepare(metadataCtx, lifecycleParent context.Context, cfg ConnectionConfig) error {
	if metadataCtx == nil {
		metadataCtx = context.Background()
	}
	if lifecycleParent == nil {
		lifecycleParent = context.Background()
	}

	p.mu.Lock()
	if p.lifecycle != nil {
		p.mu.Unlock()
		return nil
	}
	metadata := p.metadata
	tools := append([]tool.BaseTool(nil), p.tools...)
	p.mu.Unlock()
	if metadata == nil {
		var err error
		metadata, err = collectManagedTools(metadataCtx, tools)
		if err != nil {
			return err
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lifecycle != nil {
		return nil
	}
	p.cfg = cloneConnectionConfig(cfg)
	p.tools = tools
	p.metadata = metadata
	p.lifecycle, p.cancel = context.WithCancel(lifecycleParent)
	if p.closer == nil {
		p.closer = func() {}
	}
	return nil
}

func (p *managedProcess) context() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lifecycle != nil {
		return p.lifecycle
	}
	return context.Background()
}

func collectManagedTools(ctx context.Context, tools []tool.BaseTool) ([]managedTool, error) {
	metadata := make([]managedTool, 0, len(tools))
	for _, base := range tools {
		info, err := base.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("read tool metadata: %w", err)
		}
		if info == nil || info.Name == "" {
			return nil, fmt.Errorf("read tool metadata: missing tool name")
		}
		copied, err := cloneToolInfo(info)
		if err != nil {
			return nil, fmt.Errorf("copy tool metadata %q: %w", info.Name, err)
		}
		metadata = append(metadata, managedTool{base: base, info: copied})
	}
	return metadata, nil
}

func cloneToolInfo(info *schema.ToolInfo) (*schema.ToolInfo, error) {
	if info == nil {
		return nil, nil
	}
	data, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	var copied schema.ToolInfo
	if err := json.Unmarshal(data, &copied); err != nil {
		return nil, err
	}
	return &copied, nil
}

func (p *managedProcess) acquire() (func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.draining {
		return nil, ErrConnectionDraining
	}
	p.leases.Add(1)
	return p.leases.Done, nil
}

func (p *managedProcess) beginDrain() {
	p.mu.Lock()
	if p.draining {
		p.mu.Unlock()
		return
	}
	p.draining = true
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (p *managedProcess) close() {
	p.beginDrain()
	p.workers.Wait()
	p.leases.Wait()
	p.closeOnce.Do(func() {
		p.mu.Lock()
		closer := p.closer
		p.mu.Unlock()
		if closer != nil {
			closer()
		}
	})
}

func (p *managedProcess) toolNames() []string {
	names := make([]string, 0, len(p.metadata))
	for _, metadata := range p.metadata {
		names = append(names, metadata.info.Name)
	}
	return names
}

func (p *managedProcess) connectionTools() []ConnectionTool {
	entries := make([]ConnectionTool, 0, len(p.metadata))
	for _, metadata := range p.metadata {
		entries = append(entries, ConnectionTool{
			ConnectionID:   p.cfg.ID,
			ConnectionName: p.cfg.Name,
			ConnectionType: p.cfg.Type,
			NodeletID:      p.cfg.NodeletID,
			OriginalName:   metadata.info.Name,
			Description:    metadata.info.Desc,
			Tool:           leasedToolFor(p, metadata),
		})
	}
	return entries
}

type leasedBaseTool struct {
	process *managedProcess
	base    tool.BaseTool
	info    *schema.ToolInfo
}

func (t leasedBaseTool) Info(context.Context) (*schema.ToolInfo, error) {
	return cloneToolInfo(t.info)
}

type leasedInvokableTool struct {
	leasedBaseTool
	invokable tool.InvokableTool
}

func (t leasedInvokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	release, err := t.process.acquire()
	if err != nil {
		return "", err
	}
	defer release()
	return t.invokable.InvokableRun(ctx, argumentsInJSON, opts...)
}

func leasedToolFor(process *managedProcess, metadata managedTool) tool.BaseTool {
	base := leasedBaseTool{process: process, base: metadata.base, info: metadata.info}
	if invokable, ok := metadata.base.(tool.InvokableTool); ok {
		return leasedInvokableTool{leasedBaseTool: base, invokable: invokable}
	}
	return base
}
