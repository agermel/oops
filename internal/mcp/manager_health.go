package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oops/internal/logutil"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

type backendProbe struct {
	name string
	args map[string]any
}

func verifyBackend(ctx context.Context, cfg ConnectionConfig, session MCPSession, toolNames []string) error {
	for _, probe := range backendProbes(cfg) {
		if !containsToolName(toolNames, probe.name) {
			continue
		}
		return callBackendProbe(ctx, session, probe)
	}

	return Verify(ctx, session)
}

func callBackendProbe(ctx context.Context, session MCPSession, probe backendProbe) error {
	req := mcp.CallToolRequest{}
	req.Params.Name = probe.name
	req.Params.Arguments = callToolArguments(probe.args)

	result, err := session.CallTool(ctx, req)
	if err != nil {
		return fmt.Errorf("call %q: %w", probe.name, err)
	}
	if result.IsError {
		errMsg := toolErrorText(result)
		if errMsg == "" {
			errMsg = "returned error with no message"
		}
		return fmt.Errorf("call %q: %s", probe.name, errMsg)
	}
	if failure := probeFailureText(result); failure != "" {
		return fmt.Errorf("call %q: %s", probe.name, failure)
	}
	return nil
}

func backendProbes(cfg ConnectionConfig) []backendProbe {
	switch strings.ToLower(cfg.Type) {
	case "mysql":
		return []backendProbe{
			{name: "ping"},
			{name: "server_info"},
			{name: "list_databases"},
		}
	case "redis":
		return []backendProbe{
			{name: "info"},
			{name: "dbsize"},
		}
	case "postgres":
		return []backendProbe{
			{name: "query", args: map[string]any{"sql": "SELECT 1"}},
		}
	case "etcd":
		return []backendProbe{
			{name: "etcd_health"},
			{name: "etcd_status"},
		}
	case "elasticsearch":
		return []backendProbe{
			{name: "get_cluster_health"},
			{name: "list_indices"},
		}
	case "kafka":
		return []backendProbe{
			{name: "list-topics"},
		}
	case "nacos":
		return []backendProbe{{
			name: "search_mcp_server",
			args: map[string]any{
				"task_description": "健康检查\nhealth check",
				"key_words":        "health,nacos",
			},
		}}
	default:
		return nil
	}
}

func containsToolName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func toolErrorText(result *mcp.CallToolResult) string {
	var msgs []string
	for _, block := range result.Content {
		if tb, ok := block.(mcp.TextContent); ok {
			msgs = append(msgs, tb.Text)
		}
	}
	return strings.Join(msgs, "; ")
}

func probeFailureText(result *mcp.CallToolResult) string {
	text := toolErrorText(result)
	lower := strings.ToLower(text)
	if strings.HasPrefix(strings.TrimSpace(lower), "error ") {
		return text
	}
	markers := []string{
		"failed with message",
		"unexpected error",
		"unauthorized",
		"forbidden",
		"authentication failed",
		"connection refused",
		"no such host",
		"i/o timeout",
		"dial tcp",
		"could not connect",
		"server selection timeout",
		"error retrieving",
		"error getting",
		"mcp-confluent",
		"kafkaerror",
		"all brokers down",
		"no kafka clusters",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return text
		}
	}
	return ""
}

// Close stops all running subprocesses.

func (m *Manager) startHealthCheck(proc *managedProcess) {
	proc.workers.Add(1)
	go func() {
		defer proc.workers.Done()
		m.runHealthCheck(proc)
	}()
}

// runHealthCheck periodically verifies an attached connection. Every probe
// holds a lease, so process shutdown waits for it and cancellation stops it.
func (m *Manager) runHealthCheck(proc *managedProcess) {
	ctx := proc.context()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		release, err := proc.acquire()
		if err != nil {
			return
		}
		cfg := cloneConnectionConfig(proc.cfg)
		session := proc.session
		names := proc.toolNames()

		verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		verifyErr := verifyBackend(verifyCtx, cfg, session, names)
		cancel()
		release()
		if !m.applyHealthResult(proc, verifyErr) {
			return
		}
	}
}

// applyHealthResult records a result for the exact process that was probed.
// It returns false when the health worker should exit.
func (m *Manager) applyHealthResult(proc *managedProcess, verifyErr error) bool {
	m.mutationMu.Lock()
	defer m.mutationMu.Unlock()
	m.mu.Lock()
	current, attached := m.processes[proc.cfg.ID]
	if !attached || current != proc || m.closed {
		m.mu.Unlock()
		return false
	}
	if verifyErr == nil {
		delete(m.errors, proc.cfg.ID)
		m.mu.Unlock()
		return true
	}

	draining := m.detachProcessLocked(proc.cfg.ID)
	m.errors[proc.cfg.ID] = verifyErr.Error()
	change := m.refreshVisibleToolsLocked()
	m.mu.Unlock()
	m.enqueueToolChange(change)
	logutil.Error("mcp: health check failed",
		zap.String("id", proc.cfg.ID),
		zap.String("name", proc.cfg.Name),
		zap.Error(verifyErr),
	)
	m.appendConsole("mcp error: connection %q health check failed: %v", proc.cfg.Name, verifyErr)
	m.appendConnectionLog(proc.cfg.ID, "system", "error", "connection error: %v", verifyErr)
	if draining != nil {
		go m.closeDetachedProcess(draining)
	}
	return false
}

func (m *Manager) detachProcessLocked(id string) *managedProcess {
	if start, ok := m.starting[id]; ok {
		delete(m.starting, id)
		if start.cancel != nil {
			start.cancel()
		}
	}
	proc, ok := m.processes[id]
	if !ok {
		return nil
	}
	delete(m.processes, id)
	proc.beginDrain()
	if m.draining == nil {
		m.draining = make(map[*managedProcess]struct{})
	}
	m.draining[proc] = struct{}{}
	return proc
}

func (m *Manager) closeDetachedProcess(proc *managedProcess) {
	if proc == nil {
		return
	}
	proc.close()
	m.mu.Lock()
	delete(m.draining, proc)
	m.mu.Unlock()
}
