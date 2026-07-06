package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"oops/internal/config"
	"oops/internal/console"
	"oops/internal/logutil"
	runtimestore "oops/internal/store/runtime"

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
	// ErrTestConnectFailed indicates the transport-level connection attempt failed
	// during Test() (e.g., SSE unreachable, subprocess failed to start).
	ErrTestConnectFailed = errors.New("test connect failed")
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
	Name           string `json:"name"`
	Description    string `json:"description"`
	OriginalName   string `json:"originalName,omitempty"`
	ModelName      string `json:"modelName,omitempty"`
	ConnectionType string `json:"connectionType,omitempty"`
}

// ConnectionTool is a running MCP tool with the owning connection metadata.
type ConnectionTool struct {
	ConnectionID   string
	ConnectionName string
	ConnectionType string
	NodeletID      string
	ServerName     string
	OriginalName   string
	ModelName      string
	Description    string
	Tool           tool.BaseTool
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
	Status    string     `json:"status"` // "running" | "starting" | "stopped" | "error"
	Error     string     `json:"error,omitempty"`
	ToolCount int        `json:"toolCount"`
	Tools     []ToolInfo `json:"tools,omitempty"`
}

type managerState struct {
	Connections []ConnectionConfig `json:"connections"`
}

type managedProcess struct {
	cfg          ConnectionConfig
	session      MCPSession // retained for per-tool testing
	closer       func()
	tools        []tool.BaseTool
	healthCancel context.CancelFunc // cancel periodic health check on stop
}

type connectionStart struct {
	cfg   ConnectionConfig
	token int64
}

// Manager manages MCP server subprocess lifecycles and persists configuration.
type Manager struct {
	mu        sync.Mutex
	runtime   *runtimestore.Store
	config    managerState
	processes map[string]*managedProcess // id → running process
	starting  map[string]int64           // id → active start token
	nextStart int64
	errors    map[string]string // id → last error
	logs      map[string]*ConnectionLogHub
	onChange  func([]ConnectionTool)
	closed    bool
}

// NewManagerWithRuntime loads MCP connections from SQLite.
func NewManagerWithRuntime(runtime *runtimestore.Store, onChange func([]ConnectionTool)) (*Manager, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime store is required")
	}
	m := &Manager{
		runtime:   runtime,
		processes: make(map[string]*managedProcess),
		starting:  make(map[string]int64),
		errors:    make(map[string]string),
		logs:      make(map[string]*ConnectionLogHub),
		onChange:  onChange,
	}

	if err := m.load(); err != nil {
		return nil, fmt.Errorf("load mcp config: %w", err)
	}

	var starts []connectionStart
	m.mu.Lock()
	for i := range m.config.Connections {
		cfg := m.config.Connections[i]
		if !cfg.Enabled {
			continue
		}
		token := m.scheduleStartLocked(cfg.ID)
		starts = append(starts, connectionStart{cfg: cfg, token: token})
	}
	m.mu.Unlock()
	for _, start := range starts {
		go m.startAsync(start.cfg, start.token)
	}

	m.notifyChange()
	return m, nil
}

// List returns all connections with their current runtime status.
func (m *Manager) List() []ConnectionWithStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]ConnectionWithStatus, len(m.config.Connections))
	for i, cfg := range m.config.Connections {
		result[i] = m.connectionStatusLocked(cfg)
	}
	return result
}

func (m *Manager) connectionStatusLocked(cfg ConnectionConfig) ConnectionWithStatus {
	item := ConnectionWithStatus{ConnectionConfig: cfg}
	if proc, ok := m.processes[cfg.ID]; ok {
		item.Status = "running"
		item.ToolCount = len(proc.tools)
		item.Tools = m.toolInfosForConnectionLocked(cfg.ID)
	} else if cfg.Enabled {
		if _, ok := m.starting[cfg.ID]; ok {
			item.Status = "starting"
		} else {
			item.Status = "error"
			item.Error = m.errors[cfg.ID]
		}
	} else {
		item.Status = "stopped"
	}
	return item
}

// ConnectionLogs returns recent logs for a saved MCP connection.
func (m *Manager) ConnectionLogs(id string, tail int) ([]LogEntry, bool) {
	hub, ok := m.connectionLogHub(id)
	if !ok {
		return nil, false
	}
	return hub.Snapshot(tail), true
}

// SubscribeConnectionLogs replays recent logs, then streams live logs for a saved MCP connection.
func (m *Manager) SubscribeConnectionLogs(id string, tail int) (<-chan LogEntry, func(), bool) {
	hub, ok := m.connectionLogHub(id)
	if !ok {
		return nil, nil, false
	}
	ch, cancel := hub.Subscribe(tail)
	return ch, cancel, true
}

func (m *Manager) connectionLogHub(id string) (*ConnectionLogHub, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.connectionLocked(id); !ok {
		return nil, false
	}
	return m.ensureLogHubLocked(id), true
}

func (m *Manager) ensureLogHub(id string) *ConnectionLogHub {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureLogHubLocked(id)
}

func (m *Manager) ensureLogHubLocked(id string) *ConnectionLogHub {
	if m.logs == nil {
		m.logs = make(map[string]*ConnectionLogHub)
	}
	hub := m.logs[id]
	if hub == nil {
		hub = NewConnectionLogHub(id)
		m.logs[id] = hub
	}
	return hub
}

func (m *Manager) appendConnectionLog(id, stream, level, format string, args ...any) {
	if id == "" {
		return
	}
	m.ensureLogHub(id).Append(stream, level, fmt.Sprintf(format, args...))
}

func (m *Manager) appendConnectionLogLocked(id, stream, level, format string, args ...any) {
	if id == "" {
		return
	}
	m.ensureLogHubLocked(id).Append(stream, level, fmt.Sprintf(format, args...))
}

// FindByContainer returns the connection bound to a specific container, or nil if none exists.
func (m *Manager) FindByContainer(nodeletID, containerID string) *ConnectionWithStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.config.Connections {
		cfg := &m.config.Connections[i]
		if cfg.ContainerID == containerID && cfg.NodeletID == nodeletID {
			item := m.connectionStatusLocked(*cfg)
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
	for connID, tools := range m.connectionToolInfosLocked() {
		result[connID] = tools
	}
	return result
}

// GetConnectionToolEntries returns running MCP tools with connection metadata.
func (m *Manager) GetConnectionToolEntries() []ConnectionTool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.collectToolsLocked()
}

// Add persists a new connection and starts it asynchronously if enabled.
func (m *Manager) Add(cfg ConnectionConfig) error {
	m.mu.Lock()
	cfg = normalizeConnectionConfig(cfg)

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
	m.appendConnectionLogLocked(cfg.ID, "system", "info", "connection %q saved", cfg.Name)

	var startToken int64
	if cfg.Enabled {
		startToken = m.scheduleStartLocked(cfg.ID)
		m.appendConnectionLogLocked(cfg.ID, "system", "info", "start scheduled")
	}

	m.notifyChangeLocked()
	m.mu.Unlock()
	if cfg.Enabled {
		go m.startAsync(cfg, startToken)
	}
	return nil
}

// Update persists changes to an existing connection, stops the old subprocess,
// and starts a new one asynchronously if enabled.
func (m *Manager) Update(cfg ConnectionConfig) error {
	m.mu.Lock()
	cfg = normalizeConnectionConfig(cfg)

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
	m.appendConnectionLogLocked(cfg.ID, "system", "info", "connection %q updated", cfg.Name)

	var startToken int64
	if cfg.Enabled {
		startToken = m.scheduleStartLocked(cfg.ID)
		m.appendConnectionLogLocked(cfg.ID, "system", "info", "start scheduled")
	}

	m.notifyChangeLocked()
	m.mu.Unlock()
	if cfg.Enabled {
		go m.startAsync(cfg, startToken)
	}
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
	delete(m.starting, id)
	m.appendConnectionLogLocked(id, "system", "info", "connection removed")
	delete(m.logs, id)

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
	m.appendConnectionLog(connID, "system", "info", "testing tool %q", toolName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = callToolArguments(args)

	result, err := session.CallTool(ctx, req)
	if err != nil {
		logutil.Error("mcp: test tool call error",
			zap.String("conn", cfgName),
			zap.String("tool", toolName),
			zap.Error(err),
		)
		console.Feed("mcp error: test tool %q on %q: %v", toolName, cfgName, err)
		m.appendConnectionLog(connID, "system", "error", "tool %q call error: %v", toolName, err)
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
		m.appendConnectionLog(connID, "system", "error", "tool %q returned error: %s", toolName, errMsg)
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
	m.appendConnectionLog(connID, "system", "info", "tool %q test passed", toolName)
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

func callToolArguments(args map[string]any) any {
	if len(args) == 0 {
		return json.RawMessage(`{}`)
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
// When the tested config matches a saved connection, the runtime status is
// updated so the UI reflects current MCP availability.
func (m *Manager) Test(cfg ConnectionConfig) error {
	// 为什么要在后端清洗
	// 不是应该随着用户填写参数然后实时将参数处理好在参数页面实时渲染？
	cfg = normalizeConnectionConfig(cfg)
	// 执行测试，返回结果
	var hub *ConnectionLogHub
	if cfg.ID != "" {
		if existingHub, ok := m.connectionLogHub(cfg.ID); ok {
			hub = existingHub
			hub.Append("system", "info", fmt.Sprintf("testing connection %q", cfg.Name))
		}
	}
	err := m.testConnection(cfg, hub)
	if hub != nil {
		if err != nil {
			hub.Append("system", "error", fmt.Sprintf("connection test error: %v", err))
		} else {
			hub.Append("system", "info", "connection test passed")
		}
	}
	m.applyTestResult(cfg, err)
	return err
}

func (m *Manager) testConnection(cfg ConnectionConfig, hub *ConnectionLogHub) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var logWriter io.Writer
	if hub != nil {
		logWriter = hub.LineWriter("stderr")
	}
	session, tools, closer, err := ConnectWithLog(ctx, mcpCfg, logWriter)
	if err != nil {
		// stderr 已由 Connect/connectStdio 附在 error 中，此处不再重复拼接。
		return fmt.Errorf("%w: test connect: %w", ErrTestConnectFailed, err)
	}
	defer closer()
	if err := verifyBackend(ctx, cfg, session, tools); err != nil {
		return fmt.Errorf("%w: test backend: %w", ErrTestConnectFailed, err)
	}
	return nil
}

func (m *Manager) applyTestResult(cfg ConnectionConfig, testErr error) {
	if cfg.ID == "" {
		return
	}

	m.mu.Lock()
	if _, ok := m.connectionLocked(cfg.ID); !ok {
		m.mu.Unlock()
		return
	}

	if testErr == nil {
		if _, ok := m.processes[cfg.ID]; ok {
			delete(m.errors, cfg.ID)
		}
		m.notifyChangeLocked()
		m.mu.Unlock()
		return
	}

	proc, running := m.processes[cfg.ID]
	if !running {
		delete(m.starting, cfg.ID)
		m.errors[cfg.ID] = testErr.Error()
		m.notifyChangeLocked()
		m.mu.Unlock()
		return
	}
	if sameRuntimeConfig(proc.cfg, cfg) {
		m.markProcessErrorLocked(cfg.ID, testErr)
		m.mu.Unlock()
		return
	}

	currentCfg := proc.cfg
	session := proc.session
	tools := proc.tools
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	currentErr := verifyBackend(ctx, currentCfg, session, tools)
	cancel()
	if currentErr == nil {
		return
	}

	m.mu.Lock()
	if currentProc, stillRunning := m.processes[cfg.ID]; stillRunning && currentProc == proc {
		m.markProcessErrorLocked(cfg.ID, currentErr)
	}
	m.mu.Unlock()
}

func (m *Manager) connectionLocked(id string) (ConnectionConfig, bool) {
	for _, cfg := range m.config.Connections {
		if cfg.ID == id {
			return cfg, true
		}
	}
	return ConnectionConfig{}, false
}

func sameRuntimeConfig(a, b ConnectionConfig) bool {
	return a.Type == b.Type &&
		normalizedTransport(a.Transport) == normalizedTransport(b.Transport) &&
		a.Command == b.Command &&
		a.URL == b.URL &&
		slices.Equal(a.Args, b.Args) &&
		slices.Equal(a.Env, b.Env)
}

func normalizedTransport(transport string) string {
	if transport == "" {
		return "stdio"
	}
	return transport
}

func (m *Manager) markProcessErrorLocked(id string, err error) {
	delete(m.starting, id)
	m.errors[id] = err.Error()
	m.appendConnectionLogLocked(id, "system", "error", "connection error: %v", err)
	if proc, ok := m.processes[id]; ok {
		if proc.healthCancel != nil {
			proc.healthCancel()
		}
		proc.closer()
		delete(m.processes, id)
	}
	m.notifyChangeLocked()
}

type backendProbe struct {
	name string
	args map[string]any
}

func verifyBackend(ctx context.Context, cfg ConnectionConfig, session MCPSession, tools []tool.BaseTool) error {
	for _, probe := range backendProbes(cfg) {
		if !hasTool(ctx, tools, probe.name) {
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

func hasTool(ctx context.Context, tools []tool.BaseTool, name string) bool {
	for _, bt := range tools {
		info, err := bt.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		if info.Name == name {
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
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	for id := range m.starting {
		delete(m.starting, id)
	}
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

func normalizeConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	switch strings.ToLower(cfg.Type) {
	case "mysql":
		return normalizeEnvConfig(cfg, []string{
			"MYSQL_DSN",
		})
	case "redis":
		return normalizeRedisConnectionConfig(cfg)
	case "postgres":
		return normalizeEnvConfig(cfg, []string{
			"DATABASE_URL",
		})
	case "etcd":
		return normalizeEnvConfig(cfg, []string{
			"ETCD_ENDPOINTS",
			"ETCD_USERNAME",
			"ETCD_PASSWORD",
		})
	case "elasticsearch":
		return normalizeElasticsearchConnectionConfig(cfg)
	case "kafka":
		return normalizeKafkaConnectionConfig(cfg)
	case "nacos":
		return normalizeNacosConnectionConfig(cfg)
	default:
		return cfg
	}
}

func normalizeEnvConfig(cfg ConnectionConfig, keys []string) ConnectionConfig {
	generated := make([]string, 0, len(keys))
	for _, key := range keys {
		value := envValue(cfg.Env, key)
		if value != "" {
			generated = append(generated, key+"="+value)
		}
	}
	return withGeneratedEnv(cfg, generated, keys)
}

func normalizeRedisConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	generated := make([]string, 0, 5)
	if host := envValue(cfg.Env, "REDIS_HOST"); host != "" {
		generated = append(generated, "REDIS_HOST="+host)
	}
	if port := envValue(cfg.Env, "REDIS_PORT"); port != "" {
		generated = append(generated, "REDIS_PORT="+port)
	}
	if username := envValue(cfg.Env, "REDIS_USERNAME"); username != "" {
		generated = append(generated, "REDIS_USERNAME="+username)
	}
	if database := envValue(cfg.Env, "REDIS_DB"); database != "" {
		generated = append(generated, "REDIS_DB="+database)
	}
	if password := envValueAny(cfg.Env, "REDIS_PWD", "REDIS_PASSWORD"); password != "" {
		generated = append(generated, "REDIS_PWD="+password)
	}
	return withGeneratedEnv(cfg, generated, []string{
		"REDIS_HOST",
		"REDIS_PORT",
		"REDIS_USERNAME",
		"REDIS_DB",
		"REDIS_PWD",
		"REDIS_PASSWORD",
	})
}

func normalizeElasticsearchConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	hosts := envValueAny(cfg.Env, "ELASTICSEARCH_HOSTS", "ELASTICSEARCH_URL")
	generated := make([]string, 0, 3)
	if hosts != "" {
		generated = append(generated, "ELASTICSEARCH_HOSTS="+hosts)
	}
	if username := envValue(cfg.Env, "ELASTICSEARCH_USERNAME"); username != "" {
		generated = append(generated, "ELASTICSEARCH_USERNAME="+username)
	}
	if password := envValue(cfg.Env, "ELASTICSEARCH_PASSWORD"); password != "" {
		generated = append(generated, "ELASTICSEARCH_PASSWORD="+password)
	}
	return withGeneratedEnv(cfg, generated, []string{
		"ELASTICSEARCH_HOSTS",
		"ELASTICSEARCH_URL",
		"ELASTICSEARCH_USERNAME",
		"ELASTICSEARCH_PASSWORD",
	})
}

func normalizeKafkaConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	bootstrap := envValueAny(cfg.Env, "BOOTSTRAP_SERVERS", "KAFKA_BOOTSTRAP_SERVERS")
	username := envValueAny(cfg.Env, "KAFKA_API_KEY", "KAFKA_SASL_USERNAME")
	password := envValueAny(cfg.Env, "KAFKA_API_SECRET", "KAFKA_SASL_PASSWORD")
	securityProtocol := envValue(cfg.Env, "KAFKA_SECURITY_PROTOCOL")
	saslMechanism := envValueAny(cfg.Env, "KAFKA_SASL_MECHANISM", "KAFKA_SASL_MECHANISMS")

	if username != "" && password != "" {
		if securityProtocol == "" {
			securityProtocol = "sasl_plaintext"
		}
		if saslMechanism == "" {
			saslMechanism = "PLAIN"
		}
	}

	generated := make([]string, 0, 5)
	if bootstrap != "" {
		generated = append(generated, "BOOTSTRAP_SERVERS="+bootstrap)
	}
	if username != "" && password != "" {
		generated = append(generated, "KAFKA_API_KEY="+username)
		generated = append(generated, "KAFKA_API_SECRET="+password)
	}
	if securityProtocol != "" {
		generated = append(generated, "KAFKA_SECURITY_PROTOCOL="+securityProtocol)
	}
	if saslMechanism != "" {
		generated = append(generated, "KAFKA_SASL_MECHANISM="+saslMechanism)
	}
	return withGeneratedEnv(cfg, generated, []string{
		"BOOTSTRAP_SERVERS",
		"KAFKA_BOOTSTRAP_SERVERS",
		"KAFKA_API_KEY",
		"KAFKA_API_SECRET",
		"KAFKA_SASL_USERNAME",
		"KAFKA_SASL_PASSWORD",
		"KAFKA_SECURITY_PROTOCOL",
		"KAFKA_SASL_MECHANISM",
		"KAFKA_SASL_MECHANISMS",
	})
}

func normalizeNacosConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	addr := envValue(cfg.Env, "NACOS_ADDR")
	if addr == "" {
		addr = joinHostPort(argValue(cfg.Args, "--host"), argValue(cfg.Args, "--port"))
	}

	generated := make([]string, 0, 4)
	if addr != "" {
		generated = append(generated, "NACOS_ADDR="+addr)
	}
	if username := envValue(cfg.Env, "NACOS_USERNAME"); username != "" {
		generated = append(generated, "NACOS_USERNAME="+username)
	}
	if password := envValue(cfg.Env, "NACOS_PASSWORD"); password != "" {
		generated = append(generated, "NACOS_PASSWORD="+password)
	}
	if namespace := envValue(cfg.Env, "NACOS_NAMESPACE"); namespace != "" {
		generated = append(generated, "NACOS_NAMESPACE="+namespace)
	}

	cfg.Args = stripArgsWithValues(cfg.Args, []string{"--host", "--port", "--access_token"})
	return withGeneratedEnv(cfg, generated, []string{
		"NACOS_ADDR",
		"NACOS_USERNAME",
		"NACOS_PASSWORD",
		"NACOS_NAMESPACE",
	})
}

func withGeneratedEnv(cfg ConnectionConfig, generated []string, keys []string) ConnectionConfig {
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}
	extra := make([]string, 0, len(cfg.Env))
	for _, item := range cfg.Env {
		if _, ok := keySet[envKey(item)]; ok {
			continue
		}
		extra = append(extra, item)
	}
	next := append(append([]string{}, generated...), extra...)
	if slices.Equal(cfg.Env, next) {
		return cfg
	}
	cfg.Env = next
	return cfg
}

func envKey(env string) string {
	key, _, _ := strings.Cut(env, "=")
	return strings.TrimSpace(key)
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(item, prefix))
		}
	}
	return ""
}

func envValueAny(env []string, keys ...string) string {
	for _, key := range keys {
		if value := envValue(env, key); value != "" {
			return value
		}
	}
	return ""
}

func argValue(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func joinHostPort(host, port string) string {
	if host == "" {
		return ""
	}
	if port == "" || strings.Contains(host, ":") || strings.Contains(host, ",") || strings.Contains(host, "://") {
		return host
	}
	return host + ":" + port
}

func stripArgsWithValues(args []string, flags []string) []string {
	flagSet := make(map[string]struct{}, len(flags))
	for _, flag := range flags {
		flagSet[flag] = struct{}{}
	}
	kept := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if _, ok := flagSet[args[i]]; ok {
			i++
			continue
		}
		kept = append(kept, args[i])
	}
	return kept
}

func (m *Manager) load() error {
	ctx := context.Background()
	records, err := m.runtime.ListMCPConnections(ctx)
	if err != nil {
		return err
	}
	m.config.Connections = mcpConnectionsFromRuntime(records)
	if m.config.Connections == nil {
		m.config.Connections = []ConnectionConfig{}
	}
	normalized := false
	for i := range m.config.Connections {
		next := normalizeConnectionConfig(m.config.Connections[i])
		if !sameRuntimeConfig(m.config.Connections[i], next) {
			m.config.Connections[i] = next
			normalized = true
		}
	}
	if normalized {
		return m.saveLocked()
	}
	return nil
}

func (m *Manager) saveLocked() error {
	return m.runtime.ReplaceMCPConnections(context.Background(), mcpConnectionsToRuntime(m.config.Connections))
}

func mcpConnectionsToRuntime(connections []ConnectionConfig) []runtimestore.MCPConnectionRecord {
	records := make([]runtimestore.MCPConnectionRecord, len(connections))
	for i, c := range connections {
		c = normalizeConnectionConfig(c)
		records[i] = runtimestore.MCPConnectionRecord{
			ID:          c.ID,
			Name:        c.Name,
			Type:        c.Type,
			Transport:   c.Transport,
			Command:     c.Command,
			Args:        append([]string{}, c.Args...),
			Env:         append([]string{}, c.Env...),
			URL:         c.URL,
			Enabled:     c.Enabled,
			ContainerID: c.ContainerID,
			NodeletID:   c.NodeletID,
		}
	}
	return records
}

func mcpConnectionsFromRuntime(records []runtimestore.MCPConnectionRecord) []ConnectionConfig {
	connections := make([]ConnectionConfig, len(records))
	for i, r := range records {
		connections[i] = ConnectionConfig{
			ID:          r.ID,
			Name:        r.Name,
			Type:        r.Type,
			Transport:   r.Transport,
			Command:     r.Command,
			Args:        append([]string{}, r.Args...),
			Env:         append([]string{}, r.Env...),
			URL:         r.URL,
			Enabled:     r.Enabled,
			ContainerID: r.ContainerID,
			NodeletID:   r.NodeletID,
		}
	}
	return connections
}

func (m *Manager) scheduleStartLocked(id string) int64 {
	m.nextStart++
	token := m.nextStart
	m.starting[id] = token
	delete(m.errors, id)
	return token
}

func (m *Manager) startAsync(cfg ConnectionConfig, token int64) {
	hub := m.ensureLogHub(cfg.ID)
	hub.Append("system", "info", fmt.Sprintf("starting connection %q", cfg.Name))
	proc, err := startProcess(cfg, hub)

	m.mu.Lock()
	defer m.mu.Unlock()

	currentToken, isStarting := m.starting[cfg.ID]
	if !isStarting || currentToken != token {
		if proc != nil {
			proc.closer()
		}
		hub.Append("system", "warn", "start cancelled by newer change")
		return
	}

	currentCfg, exists := m.connectionLocked(cfg.ID)
	if m.closed || !exists || !currentCfg.Enabled || !sameRuntimeConfig(currentCfg, cfg) {
		delete(m.starting, cfg.ID)
		if proc != nil {
			proc.closer()
		}
		hub.Append("system", "warn", "start cancelled because connection config changed")
		m.notifyChangeLocked()
		return
	}

	delete(m.starting, cfg.ID)
	if err != nil {
		logutil.Error("mcp: start", zap.String("id", cfg.ID), zap.Error(err))
		m.errors[cfg.ID] = err.Error()
		hub.Append("system", "error", fmt.Sprintf("connection start error: %v", err))
		m.notifyChangeLocked()
		return
	}
	if proc == nil {
		logutil.Error("mcp: start", zap.String("id", cfg.ID), zap.Error(errors.New("start returned nil process")))
		m.errors[cfg.ID] = "start returned nil process"
		hub.Append("system", "error", "connection start error: start returned nil process")
		m.notifyChangeLocked()
		return
	}

	if oldProc, ok := m.processes[cfg.ID]; ok {
		if oldProc.healthCancel != nil {
			oldProc.healthCancel()
		}
		oldProc.closer()
	}

	proc.cfg = currentCfg
	healthCtx, healthCancel := context.WithCancel(context.Background())
	proc.healthCancel = healthCancel
	m.processes[cfg.ID] = proc
	delete(m.errors, cfg.ID)
	logutil.Info("mcp: started",
		zap.String("id", cfg.ID),
		zap.Int("tools", len(proc.tools)),
	)
	hub.Append("system", "info", fmt.Sprintf("connection started with %d tools", len(proc.tools)))
	go m.runHealthCheck(healthCtx, cfg.ID)
	m.notifyChangeLocked()
}

func startProcess(cfg ConnectionConfig, hub *ConnectionLogHub) (*managedProcess, error) {
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
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		if hub != nil {
			hub.Append("system", "info", fmt.Sprintf("connect attempt %d/%d via %s", attempt, maxRetries, transport))
		}

		var logWriter io.Writer
		if hub != nil {
			logWriter = hub.LineWriter("stderr")
		}
		session, tools, closer, err := ConnectWithLog(ctx, mcpCfg, logWriter)
		if err == nil {
			if verifyErr := verifyBackend(ctx, cfg, session, tools); verifyErr != nil {
				closer()
				err = fmt.Errorf("backend verify: %w", verifyErr)
			}
		}
		cancel()

		if err == nil {
			if hub != nil {
				hub.Append("system", "info", fmt.Sprintf("connect attempt %d/%d passed", attempt, maxRetries))
			}
			return &managedProcess{
				cfg:     cfg,
				session: session,
				closer:  closer,
				tools:   tools,
			}, nil
		}

		lastErr = err
		if hub != nil {
			level := "warn"
			if attempt == maxRetries {
				level = "error"
			}
			hub.Append("system", level, fmt.Sprintf("connect attempt %d/%d error: %v", attempt, maxRetries, err))
		}
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

	return nil, fmt.Errorf("connect (%d attempts): %w", maxRetries, lastErr)
}

// runHealthCheck 在后台周期性对已启动的连接执行 verifyBackend。
// 检查到后端不可达时标记为 error 并移除进程；恢复时清除错误。
// 使用轻量 Verify (MCP Ping) 避免阻塞，耗时通常 <1s。
func (m *Manager) runHealthCheck(ctx context.Context, id string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		m.mu.Lock()
		proc, ok := m.processes[id]
		if !ok {
			m.mu.Unlock()
			return
		}
		cfg := proc.cfg
		session := proc.session
		tools := proc.tools
		m.mu.Unlock()

		verifyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		verifyErr := verifyBackend(verifyCtx, cfg, session, tools)
		cancel()

		m.mu.Lock()
		if currentProc, stillThere := m.processes[id]; !stillThere || currentProc != proc {
			m.mu.Unlock()
			return
		}

		if verifyErr != nil {
			logutil.Error("mcp: health check failed",
				zap.String("id", id),
				zap.String("name", cfg.Name),
				zap.Error(verifyErr),
			)
			console.Feed("mcp error: connection %q health check failed: %v", cfg.Name, verifyErr)
			m.markProcessErrorLocked(id, verifyErr)
		} else {
			if _, hadError := m.errors[id]; hadError {
				delete(m.errors, id)
				logutil.Info("mcp: health check recovered", zap.String("id", id), zap.String("name", cfg.Name))
				console.Feed("mcp info: connection %q recovered", cfg.Name)
				m.appendConnectionLogLocked(id, "system", "info", "health check recovered")
				m.notifyChangeLocked()
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) stopLocked(id string) {
	delete(m.starting, id)
	proc, ok := m.processes[id]
	if !ok {
		return
	}
	if proc.healthCancel != nil {
		proc.healthCancel()
	}
	proc.closer()
	delete(m.processes, id)
	logutil.Info("mcp: stopped", zap.String("id", id))
	m.appendConnectionLogLocked(id, "system", "info", "connection stopped")
}

func (m *Manager) toolInfosForConnectionLocked(connID string) []ToolInfo {
	infos := m.connectionToolInfosLocked()
	return infos[connID]
}

func (m *Manager) connectionToolInfosLocked() map[string][]ToolInfo {
	result := make(map[string][]ToolInfo, len(m.processes))
	for _, entry := range m.collectToolsLocked() {
		result[entry.ConnectionID] = append(result[entry.ConnectionID], ToolInfo{
			Name:           entry.OriginalName,
			Description:    entry.Description,
			OriginalName:   entry.OriginalName,
			ModelName:      entry.ModelName,
			ConnectionType: entry.ConnectionType,
		})
	}
	return result
}

func (m *Manager) collectToolsLocked() []ConnectionTool {
	ids := make([]string, 0, len(m.processes))
	for id := range m.processes {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var all []ConnectionTool
	for _, id := range ids {
		proc := m.processes[id]
		for _, bt := range proc.tools {
			info, err := bt.Info(context.Background())
			if err != nil {
				continue
			}
			all = append(all, ConnectionTool{
				ConnectionID:   proc.cfg.ID,
				ConnectionName: proc.cfg.Name,
				ConnectionType: proc.cfg.Type,
				NodeletID:      proc.cfg.NodeletID,
				OriginalName:   info.Name,
				Description:    info.Desc,
				Tool:           bt,
			})
		}
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
