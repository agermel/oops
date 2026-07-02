package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"oops/internal/config"
	"oops/internal/console"
	"oops/internal/logutil"
	"oops/internal/store"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// Sentinel errors for TestTool to distinguish different failure modes.
var (
	// ErrConnectionNotRunning indicates the MCP connection process is not active.
	ErrConnectionNotRunning = errors.New("connection not running")
	// ErrToolCallFailed indicates the transport-level call to the MCP tool failed.
	ErrToolCallFailed = errors.New("tool call failed")
)

// DefaultConfigPath is the default path for the MCP connections file.
const DefaultConfigPath = "config/mcp_connections.json"

// allowedCommands returns the list of MCP stdio commands permitted to execute.
// Controlled via OOPS_MCP_ALLOWED_COMMANDS (comma-separated). When the env var
// is not set, all commands are allowed.
func allowedCommands() []string {
	extra := os.Getenv("OOPS_MCP_ALLOWED_COMMANDS")
	if extra == "" {
		return nil
	}
	var cmds []string
	for cmd := range strings.SplitSeq(extra, ",") {
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

// ToolInfo 是一个工具的基本信息，供前端工具管理面板使用。
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ConnectionConfig defines a single MCP server connection managed by the panel.
type ConnectionConfig struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`      // mysql, redis, etc.
	Transport   string   `json:"transport"` // "stdio" (default) | "sse"
	Command     string   `json:"command"`   // stdio: path to MCP server binary
	Args        []string `json:"args"`      // stdio: e.g. ["--read-only"]
	Env         []string `json:"env"`       // stdio: e.g. ["MYSQL_DSN=user:pass@tcp(host:3306)/db"]
	URL         string   `json:"url"`       // sse: endpoint address e.g. "http://10.0.0.1:19900/sse"
	Enabled     bool     `json:"enabled"`
	ContainerID string   `json:"containerId,omitempty"` // bound container, if any
	NodeletID   string   `json:"nodeletId,omitempty"`   // bound nodelet, if any
}

// ConnectionWithStatus is the public-facing view of a connection.
type ConnectionWithStatus struct {
	ConnectionConfig
	Status    string     `json:"status"` // "running" | "stopped" | "error"
	Error     string     `json:"error,omitempty"`
	ToolCount int        `json:"toolCount"`
	Tools     []ToolInfo `json:"tools,omitempty"`
}

// ManagerConfig is the top-level structure of mcp_connections.json.
type ManagerConfig struct {
	Connections []ConnectionConfig `json:"connections"`
}

type managedProcess struct {
	cfg       ConnectionConfig
	session   MCPSession // retained for per-tool testing
	closer    func()
	tools     []tool.BaseTool
	exitCh    <-chan struct{} // closed when the subprocess exits (nil for SSE)
	stderrBuf *StderrBuffer   // captured stderr; nil for SSE
}

// Manager manages MCP server subprocess lifecycles and persists configuration.
type Manager struct {
	mu         sync.Mutex
	configPath string
	config     ManagerConfig
	processes  map[string]*managedProcess // id → running process
	errors     map[string]string          // id → last error
	onChange   func([]tool.BaseTool)

	keepaliveStop chan struct{} // closed when keepalive goroutine should stop
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
		return nil, fmt.Errorf("load mcp config: %w", err)
	}

	// Start all enabled connections asynchronously on startup.
	// 每个连接独立 goroutine，互不阻塞；启动完成后通过 notifyChange 推送工具变更。
	m.mu.Lock()
	for i := range m.config.Connections {
		cfg := m.config.Connections[i]
		if !cfg.Enabled {
			continue
		}
		go func(cfg ConnectionConfig) {
			m.mu.Lock()
			err := m.startLocked(cfg)
			if err != nil {
				logutil.Error("mcp: start", zap.String("id", cfg.ID), zap.Error(err))
				m.errors[cfg.ID] = err.Error()
			}
			m.mu.Unlock()
			m.notifyChange()
		}(cfg)
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
			item.Tools = make([]ToolInfo, len(proc.tools))
			for j, bt := range proc.tools {
				info, err := bt.Info(context.Background())
				if err != nil {
					item.Tools[j] = ToolInfo{Name: "?", Description: err.Error()}
				} else {
					item.Tools[j] = ToolInfo{Name: info.Name, Description: info.Desc}
				}
			}
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

// FindByContainer returns the connection bound to a specific container, or nil if none exists.
func (m *Manager) FindByContainer(nodeletID, containerID string) *ConnectionWithStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.config.Connections {
		cfg := &m.config.Connections[i]
		if cfg.ContainerID == containerID && cfg.NodeletID == nodeletID {
			item := ConnectionWithStatus{ConnectionConfig: *cfg}
			if proc, ok := m.processes[cfg.ID]; ok {
				item.Status = "running"
				item.ToolCount = len(proc.tools)
			} else if cfg.Enabled {
				item.Status = "error"
				item.Error = m.errors[cfg.ID]
			} else {
				item.Status = "stopped"
			}
			return &item
		}
	}
	return nil
}

// GetConnectionTools 返回每个运行中连接的工具列表，按 connectionID 分组。
// 仅包含当前正在运行的连接；已停止或异常的连接不会出现在结果中。
func (m *Manager) GetConnectionTools() map[string][]ToolInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string][]ToolInfo, len(m.processes))
	for id, proc := range m.processes {
		tools := make([]ToolInfo, 0, len(proc.tools))
		for _, bt := range proc.tools {
			info, err := bt.Info(context.Background())
			if err != nil {
				continue
			}
			tools = append(tools, ToolInfo{
				Name:        info.Name,
				Description: info.Desc,
			})
		}
		result[id] = tools
	}
	return result
}

// Add persists a new connection and starts it asynchronously if enabled.
func (m *Manager) Add(cfg ConnectionConfig) error {
	m.mu.Lock()

	if cfg.ID == "" {
		m.mu.Unlock()
		return fmt.Errorf("id is required")
	}
	if cfg.Transport == "sse" {
		if cfg.URL == "" {
			m.mu.Unlock()
			return fmt.Errorf("url is required for sse transport")
		}
	} else if cfg.Command != "" {
		if err := validateCommand(cfg.Command); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	// Per-container uniqueness: one container can have at most one MCP connection.
	if cfg.ContainerID != "" && cfg.NodeletID != "" {
		for _, existing := range m.config.Connections {
			if existing.ContainerID == cfg.ContainerID && existing.NodeletID == cfg.NodeletID {
				m.mu.Unlock()
				return fmt.Errorf("container %q on nodelet %q already has connection %q", cfg.ContainerID, cfg.NodeletID, existing.ID)
			}
		}
	}
	// Global ID uniqueness (safety net).
	for _, existing := range m.config.Connections {
		if existing.ID == cfg.ID {
			m.mu.Unlock()
			return fmt.Errorf("connection %q already exists", cfg.ID)
		}
	}

	m.config.Connections = append(m.config.Connections, cfg)
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("save: %w", err)
	}

	// 先持久化并返回，后台异步启动子进程。
	if cfg.Enabled {
		go func() {
			m.mu.Lock()
			err := m.startLocked(cfg)
			if err != nil {
				logutil.Error("mcp: add start", zap.String("id", cfg.ID), zap.Error(err))
				m.errors[cfg.ID] = err.Error()
			}
			m.mu.Unlock()
			m.notifyChange()
		}()
	}

	m.notifyChangeLocked()
	m.mu.Unlock()
	return nil
}

// Update persists changes to an existing connection, stops the old subprocess,
// and starts a new one asynchronously if enabled.
func (m *Manager) Update(cfg ConnectionConfig) error {
	m.mu.Lock()

	if cfg.Transport == "sse" {
		if cfg.URL == "" {
			m.mu.Unlock()
			return fmt.Errorf("url is required for sse transport")
		}
	} else if cfg.Command != "" {
		if err := validateCommand(cfg.Command); err != nil {
			m.mu.Unlock()
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
		m.mu.Unlock()
		return fmt.Errorf("connection %q not found", cfg.ID)
	}

	// Per-container uniqueness: don't allow stealing another container's binding.
	if cfg.ContainerID != "" && cfg.NodeletID != "" {
		for i, existing := range m.config.Connections {
			if i == idx {
				continue
			}
			if existing.ContainerID == cfg.ContainerID && existing.NodeletID == cfg.NodeletID {
				m.mu.Unlock()
				return fmt.Errorf("container %q on nodelet %q already has connection %q", cfg.ContainerID, cfg.NodeletID, existing.ID)
			}
		}
	}

	// 同步停止旧进程。
	m.stopLocked(cfg.ID)
	delete(m.errors, cfg.ID)

	m.config.Connections[idx] = cfg
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("save: %w", err)
	}

	// 先持久化并返回，后台异步启动新进程。
	if cfg.Enabled {
		go func() {
			m.mu.Lock()
			err := m.startLocked(cfg)
			if err != nil {
				logutil.Error("mcp: update start", zap.String("id", cfg.ID), zap.Error(err))
				m.errors[cfg.ID] = err.Error()
			}
			m.mu.Unlock()
			m.notifyChange()
		}()
	}

	m.notifyChangeLocked()
	m.mu.Unlock()
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

// TestTool calls a single tool on a running connection and returns its output.
// It does not hold the manager lock during the call, so it doesn't block other operations.
// If the tool has a parameter schema, default arguments are generated for required fields.
func (m *Manager) TestTool(connID, toolName string) (string, error) {
	// Briefly hold lock to copy session reference and look up tool schema.
	m.mu.Lock()
	proc, ok := m.processes[connID]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("%w: %q", ErrConnectionNotRunning, connID)
	}
	session := proc.session
	cfgName := proc.cfg.Name

	// Find the tool's parameter schema to generate sensible defaults.
	var toolInfo *schema.ToolInfo
	for _, bt := range proc.tools {
		info, err := bt.Info(context.Background())
		if err != nil {
			continue
		}
		if info.Name == toolName {
			toolInfo = info
			break
		}
	}
	m.mu.Unlock()

	args := buildDefaultArgs(toolInfo)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args

	result, err := session.CallTool(ctx, req)
	if err != nil {
		logutil.Error("mcp: test tool call error",
			zap.String("conn", cfgName),
			zap.String("tool", toolName),
			zap.Error(err),
		)
		console.Feed("mcp error: test tool %q on %q: %v", toolName, cfgName, err)
		return "", fmt.Errorf("%w: call %q: %w", ErrToolCallFailed, toolName, err)
	}
	if result.IsError {
		var msgs []string
		for _, block := range result.Content {
			if tb, ok := block.(mcp.TextContent); ok {
				msgs = append(msgs, tb.Text)
			}
		}
		errMsg := strings.Join(msgs, "; ")
		if errMsg == "" {
			errMsg = fmt.Sprintf("tool %q returned error with no message", toolName)
		}
		logutil.Error("mcp: test tool returned error",
			zap.String("conn", cfgName),
			zap.String("tool", toolName),
			zap.String("error", errMsg),
		)
		console.Feed("mcp error: test tool %q on %q failed: %s", toolName, cfgName, errMsg)
		return "", fmt.Errorf("%s", errMsg)
	}

	// Collect text output.
	var parts []string
	for _, block := range result.Content {
		switch b := block.(type) {
		case mcp.TextContent:
			parts = append(parts, b.Text)
		default:
			parts = append(parts, fmt.Sprintf("[%T]", block))
		}
	}
	return strings.Join(parts, "\n"), nil
}

// buildDefaultArgs generates sensible default arguments for a tool based on
// its JSON Schema. Returns an empty map if the tool has no required params.
func buildDefaultArgs(toolInfo *schema.ToolInfo) map[string]any {
	if toolInfo == nil || toolInfo.ParamsOneOf == nil {
		return map[string]any{}
	}

	js, err := toolInfo.ParamsOneOf.ToJSONSchema()
	if err != nil || js == nil || js.Properties == nil {
		return map[string]any{}
	}

	args := make(map[string]any, len(js.Required))
	for _, name := range js.Required {
		prop, ok := js.Properties.Get(name)
		if !ok || prop == nil {
			args[name] = "test_" + name
			continue
		}
		args[name] = defaultValue(name, prop.Type)
	}
	return args
}

// defaultValue maps a JSON Schema type + parameter name to a sensible test value.
func defaultValue(name, typ string) any {
	switch typ {
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	default: // "string" or empty
		return inferStringDefault(name)
	}
}

// inferStringDefault picks a plausible string value based on the parameter name.
func inferStringDefault(name string) string {
	lower := strings.ToLower(name)
	switch {
	case lower == "host" || lower == "hostname":
		return "127.0.0.1"
	case lower == "port":
		return "6379"
	case lower == "password" || lower == "pass" || lower == "pwd":
		return ""
	case strings.Contains(lower, "key"):
		return "test_key"
	case strings.Contains(lower, "value"):
		return "test_value"
	case strings.Contains(lower, "field") || strings.Contains(lower, "name"):
		return "test"
	case strings.Contains(lower, "db") || strings.Contains(lower, "database"):
		return "0"
	case strings.Contains(lower, "index"):
		return "0"
	case strings.Contains(lower, "timeout"):
		return "1000"
	case strings.Contains(lower, "count") || strings.Contains(lower, "limit"):
		return "10"
	default:
		return "test_" + name
	}
}

// Test attempts a temporary connection to verify the config works.
// It does not persist or affect running processes.
func (m *Manager) Test(cfg ConnectionConfig) error {
	if cfg.Transport == "sse" {
		if cfg.URL == "" {
			return fmt.Errorf("url is required for sse transport")
		}
	} else {
		// stdio (default)
		if cfg.Command != "" {
			if err := validateCommand(cfg.Command); err != nil {
				return err
			}
		}
	}

	transport := cfg.Transport
	if transport == "" {
		transport = "stdio"
	}
	mcpCfg := config.MCPConfig{
		Enabled:   true,
		Transport: transport,
		Command:   cfg.Command,
		Args:      expandEnvSlice(cfg.Args),
		Env:       expandEnvSlice(cfg.Env),
		URL:       cfg.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, _, closer, _, _, err := Connect(ctx, mcpCfg)
	if err != nil {
		// stderr 已由 Connect/connectStdio 附在 error 中，此处不再重复拼接。
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
	if err := store.LoadJSON(m.configPath, &m.config); err != nil {
		return err
	}
	if m.config.Connections == nil {
		m.config.Connections = []ConnectionConfig{}
	}
	return nil
}

func (m *Manager) saveLocked() error {
	return store.SaveJSON(m.configPath, m.config)
}

func (m *Manager) startLocked(cfg ConnectionConfig) error {
	transport := cfg.Transport
	if transport == "" {
		transport = "stdio"
	}
	mcpCfg := config.MCPConfig{
		Enabled:   true,
		Transport: transport,
		Command:   cfg.Command,
		Args:      expandEnvSlice(cfg.Args),
		Env:       expandEnvSlice(cfg.Env),
		URL:       cfg.URL,
	}

	const maxRetries = 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)

		session, tools, closer, exitCh, stderrBuf, err := Connect(ctx, mcpCfg)
		cancel()

		if err == nil {
			m.processes[cfg.ID] = &managedProcess{
				cfg:       cfg,
				session:   session,
				closer:    closer,
				tools:     tools,
				exitCh:    exitCh,
				stderrBuf: stderrBuf,
			}
			logutil.Info("mcp: started",
				zap.String("id", cfg.ID),
				zap.Int("tools", len(tools)),
			)

			// 后台监控子进程退出：当 exitCh 关闭时，更新状态并通知前端。
			if exitCh != nil {
				go m.monitorExit(cfg.ID, exitCh)
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

// monitorExit 监控子进程退出 channel，退出时更新连接状态。
func (m *Manager) monitorExit(id string, exitCh <-chan struct{}) {
	<-exitCh

	// 先复制 session 引用再释放锁，避免在持锁期间进行网络调用。
	m.mu.Lock()
	proc, ok := m.processes[id]
	if !ok {
		m.mu.Unlock()
		return // already stopped/removed
	}
	session := proc.session
	stderrBuf := proc.stderrBuf
	m.mu.Unlock()

	// 进程已退出，检查是否还能通信。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := session.Ping(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()

	// 再次检查（可能在等待期间被 stop/remove）。
	if _, stillThere := m.processes[id]; !stillThere {
		return
	}

	if err != nil {
		errMsg := fmt.Sprintf("进程意外退出: %v", err)
		if stderrBuf != nil {
			if s := stderrBuf.String(); s != "" {
				errMsg += "\nstderr: " + s
			}
		}
		logutil.Error("mcp: process exited unexpectedly",
			zap.String("id", id),
			zap.Error(err),
		)
		console.Feed("mcp error: connection %q exited unexpectedly: %v", id, err)
		delete(m.processes, id)
		m.errors[id] = errMsg
	} else {
		// 虽然 stderr 管道关闭了，但 session 仍能通信（可能是 stderr 被关闭但进程还在）。
		// 暂时保留运行状态。
		logutil.Warn("mcp: stderr closed but session still alive", zap.String("id", id))
	}
	m.notifyChangeLocked()
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

// --- keepalive ---

// StartKeepalive 启动后台保活探测，对运行中的连接定期 ping。
// 由 api.Server 在初始化后调用。
func (m *Manager) StartKeepalive(interval time.Duration) {
	if m.keepaliveStop != nil {
		return // already running
	}
	m.keepaliveStop = make(chan struct{})
	go m.keepaliveLoop(interval)
	logutil.Info("mcp: keepalive started", zap.Duration("interval", interval))
}

// StopKeepalive 停止后台保活探测。
func (m *Manager) StopKeepalive() {
	if m.keepaliveStop == nil {
		return
	}
	close(m.keepaliveStop)
	m.keepaliveStop = nil
	logutil.Info("mcp: keepalive stopped")
}

func (m *Manager) keepaliveLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.keepaliveStop:
			return
		case <-ticker.C:
			m.pingRunning()
		}
	}
}

// pingRunning 对所有 running 状态的连接发送 MCP 协议 Ping。
// 参考 monitorExit 的锁外 ping 模式：先复制 session 引用再释放锁，
// 避免持锁期间进行网络调用。
func (m *Manager) pingRunning() {
	m.mu.Lock()
	type target struct {
		id        string
		session   MCPSession
		stderrBuf *StderrBuffer
	}
	targets := make([]target, 0, len(m.processes))
	for id, proc := range m.processes {
		targets = append(targets, target{id: id, session: proc.session, stderrBuf: proc.stderrBuf})
	}
	m.mu.Unlock()

	for _, t := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := t.session.Ping(ctx)
		cancel()

		if err != nil {
			m.mu.Lock()
			// 双重检查：可能在等待期间被 stop/remove 清理。
			if _, stillRunning := m.processes[t.id]; stillRunning {
				m.stopLocked(t.id)
				errMsg := fmt.Sprintf("keepalive ping failed: %v", err)
				if t.stderrBuf != nil {
					if s := t.stderrBuf.String(); s != "" {
						errMsg += "\nstderr: " + s
					}
				}
				m.errors[t.id] = errMsg
				logutil.Warn("mcp: keepalive ping failed",
					zap.String("connID", t.id),
					zap.Error(err),
				)
				m.notifyChangeLocked()
			}
			m.mu.Unlock()
		}
	}
}
