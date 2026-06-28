package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"oops/internal/config"
	"oops/internal/logutil"

	"github.com/cloudwego/eino/components/tool"
	"go.uber.org/zap"
)

// allowedCommands returns the list of MCP stdio commands permitted to execute.
// Controlled via OOPS_MCP_ALLOWED_COMMANDS (comma-separated). When the env var
// is not set, all commands are allowed.
func allowedCommands() []string {
	extra := os.Getenv("OOPS_MCP_ALLOWED_COMMANDS")
	if extra == "" {
		return nil
	}
	var cmds []string
	for _, cmd := range strings.Split(extra, ",") {
		cmd = strings.TrimSpace(cmd)
		if cmd != "" {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// validateCommand checks whether cmd is in the allowed list.
// If OOPS_MCP_ALLOWED_COMMANDS is not set, all commands pass.
// Allowed entries may be either a full binary path or a bare name
// (e.g. "mysql-mcp-server" matches "/usr/local/bin/mysql-mcp-server").
func validateCommand(cmd string) error {
	allowed := allowedCommands()
	if len(allowed) == 0 {
		return nil // no restriction — allow all
	}

	base := filepath.Base(cmd)
	for _, a := range allowed {
		if cmd == a || base == a || filepath.Base(a) == base {
			return nil
		}
	}
	return fmt.Errorf("command %q is not in the allowed list", cmd)
}

// ConnectionConfig defines a single MCP server connection managed by the panel.
type ConnectionConfig struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`    // mysql, redis, etc.
	Command string   `json:"command"` // path to community MCP server binary
	Args    []string `json:"args"`    // e.g. ["--read-only"]
	Env     []string `json:"env"`     // e.g. ["MYSQL_DSN=user:pass@tcp(host:3306)/db"]
	Enabled bool     `json:"enabled"`
}

// ConnectionWithStatus is the public-facing view of a connection.
type ConnectionWithStatus struct {
	ConnectionConfig
	Status    string `json:"status"` // "running" | "stopped" | "error"
	Error     string `json:"error,omitempty"`
	ToolCount int    `json:"toolCount"`
}

// ManagerConfig is the top-level structure of mcp_connections.json.
type ManagerConfig struct {
	Connections []ConnectionConfig `json:"connections"`
}

type managedProcess struct {
	cfg    ConnectionConfig
	closer func()
	tools  []tool.BaseTool
}

// Manager manages MCP server subprocess lifecycles and persists configuration.
type Manager struct {
	mu         sync.Mutex
	configPath string
	config     ManagerConfig
	processes  map[string]*managedProcess // id → running process
	errors     map[string]string          // id → last error
	onChange   func([]tool.BaseTool)
}

// NewManager loads persisted connections, starts all enabled ones,
// and calls onChange whenever the tool list changes.
func NewManager(configPath string, onChange func([]tool.BaseTool)) (*Manager, error) {
	m := &Manager{
		configPath: configPath,
		processes:  make(map[string]*managedProcess),
		errors:     make(map[string]string),
		onChange:   onChange,
	}

	if err := m.load(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("load mcp config: %w", err)
		}
		m.config = ManagerConfig{Connections: []ConnectionConfig{}}
	}

	// Start all enabled connections on startup.
	m.mu.Lock()
	for i := range m.config.Connections {
		cfg := m.config.Connections[i]
		if !cfg.Enabled {
			continue
		}
		if err := m.startLocked(cfg); err != nil {
			logutil.Error("mcp: start", zap.String("id", cfg.ID), zap.Error(err))
			m.errors[cfg.ID] = err.Error()
		}
	}
	m.mu.Unlock()

	m.notifyChange()
	return m, nil
}

// List returns all connections with their current runtime status.
func (m *Manager) List() []ConnectionWithStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]ConnectionWithStatus, len(m.config.Connections))
	for i, cfg := range m.config.Connections {
		item := ConnectionWithStatus{ConnectionConfig: cfg}
		if proc, ok := m.processes[cfg.ID]; ok {
			item.Status = "running"
			item.ToolCount = len(proc.tools)
		} else if cfg.Enabled {
			item.Status = "error"
			item.Error = m.errors[cfg.ID]
		} else {
			item.Status = "stopped"
		}
		result[i] = item
	}
	return result
}

// Add persists a new connection and starts it if enabled.
func (m *Manager) Add(cfg ConnectionConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.ID == "" {
		return fmt.Errorf("id is required")
	}
	if cfg.Command != "" {
		if err := validateCommand(cfg.Command); err != nil {
			return err
		}
	}
	for _, existing := range m.config.Connections {
		if existing.ID == cfg.ID {
			return fmt.Errorf("connection %q already exists", cfg.ID)
		}
	}

	m.config.Connections = append(m.config.Connections, cfg)
	if err := m.saveLocked(); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	if cfg.Enabled {
		if err := m.startLocked(cfg); err != nil {
			logutil.Error("mcp: add start", zap.String("id", cfg.ID), zap.Error(err))
			m.errors[cfg.ID] = err.Error()
			// Persisted successfully, start failure is non-fatal.
		}
	}

	m.notifyChangeLocked()
	return nil
}

// Update persists changes to an existing connection, stops the old subprocess,
// and starts a new one if enabled.
func (m *Manager) Update(cfg ConnectionConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Command != "" {
		if err := validateCommand(cfg.Command); err != nil {
			return err
		}
	}

	idx := -1
	for i, existing := range m.config.Connections {
		if existing.ID == cfg.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("connection %q not found", cfg.ID)
	}

	m.stopLocked(cfg.ID)
	delete(m.errors, cfg.ID)

	m.config.Connections[idx] = cfg
	if err := m.saveLocked(); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	if cfg.Enabled {
		if err := m.startLocked(cfg); err != nil {
			logutil.Error("mcp: update start", zap.String("id", cfg.ID), zap.Error(err))
			m.errors[cfg.ID] = err.Error()
		}
	}

	m.notifyChangeLocked()
	return nil
}

// Remove deletes a connection and stops its subprocess.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, existing := range m.config.Connections {
		if existing.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("connection %q not found", id)
	}

	m.stopLocked(id)
	delete(m.errors, id)

	m.config.Connections = append(m.config.Connections[:idx], m.config.Connections[idx+1:]...)
	if err := m.saveLocked(); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	m.notifyChangeLocked()
	return nil
}

// Test attempts a temporary connection to verify the config works.
// It does not persist or affect running processes.
func (m *Manager) Test(cfg ConnectionConfig) error {
	if cfg.Command != "" {
		if err := validateCommand(cfg.Command); err != nil {
			return err
		}
	}

	mcpCfg := config.MCPConfig{
		Enabled:   true,
		Transport: "stdio",
		Command:   cfg.Command,
		Args:      expandEnvSlice(cfg.Args),
		Env:       expandEnvSlice(cfg.Env),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, _, closer, err := Connect(ctx, mcpCfg)
	if err != nil {
		return fmt.Errorf("test connect: %w", err)
	}
	closer()
	return nil
}

// Close stops all running subprocesses.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.processes {
		m.stopLocked(id)
	}
}

// --- internal (caller must hold m.mu) ---

// expandEnvSlice 展开字符串切片中的 ${VAR} 环境变量引用。
func expandEnvSlice(vals []string) []string {
	if len(vals) == 0 {
		return vals
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = os.ExpandEnv(v)
	}
	return out
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &m.config)
}

func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(m.configPath, data, 0600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (m *Manager) startLocked(cfg ConnectionConfig) error {
	mcpCfg := config.MCPConfig{
		Enabled:   true,
		Transport: "stdio",
		Command:   cfg.Command,
		Args:      expandEnvSlice(cfg.Args),
		Env:       expandEnvSlice(cfg.Env),
	}

	const maxRetries = 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)

		session, tools, closer, err := Connect(ctx, mcpCfg)
		cancel()

		if err == nil {
			m.processes[cfg.ID] = &managedProcess{
				cfg:    cfg,
				closer: closer,
				tools:  tools,
			}
			logutil.Info("mcp: started",
				zap.String("id", cfg.ID),
				zap.Int("tools", len(tools)),
			)

			// 连接建立后主动验证后端服务确实可达。
			if verifyErr := Verify(context.Background(), session, tools); verifyErr != nil {
				closer()
				delete(m.processes, cfg.ID)
				logutil.Error("mcp: verify failed",
					zap.String("id", cfg.ID),
					zap.Error(verifyErr),
				)
				lastErr = verifyErr
				if attempt < maxRetries {
					time.Sleep(2 * time.Second)
					continue
				}
				return fmt.Errorf("verify (%d attempts): %w", maxRetries, verifyErr)
			}
			return nil
		}

		lastErr = err
		if attempt < maxRetries {
			logutil.Warn("mcp: connect retry",
				zap.String("id", cfg.ID),
				zap.Int("attempt", attempt),
				zap.Int("max", maxRetries),
				zap.Error(err),
			)
			time.Sleep(2 * time.Second)
		}
	}

	return fmt.Errorf("connect (%d attempts): %w", maxRetries, lastErr)
}

func (m *Manager) stopLocked(id string) {
	proc, ok := m.processes[id]
	if !ok {
		return
	}
	proc.closer()
	delete(m.processes, id)
	logutil.Info("mcp: stopped", zap.String("id", id))
}

func (m *Manager) collectToolsLocked() []tool.BaseTool {
	var all []tool.BaseTool
	for _, proc := range m.processes {
		all = append(all, proc.tools...)
	}
	return all
}

func (m *Manager) notifyChange() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifyChangeLocked()
}

func (m *Manager) notifyChangeLocked() {
	if m.onChange == nil {
		return
	}
	tools := m.collectToolsLocked()
	m.onChange(tools)
}
