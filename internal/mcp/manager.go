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
	// ErrConnectionDraining indicates that a lifecycle change has stopped new
	// callers while existing callers finish.
	ErrConnectionDraining = errors.New("connection is draining")
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

type connectionStart struct {
	cfg   ConnectionConfig
	token int64
	ctx   context.Context
}

type scheduledStart struct {
	token  int64
	cancel context.CancelFunc
}

// Manager manages MCP server subprocess lifecycles and persists configuration.
type Manager struct {
	mu               sync.Mutex
	mutationMu       sync.Mutex
	runtime          *runtimestore.Store
	config           managerState
	configRevision   int64
	toolRevision     int64
	visibleTools     []ConnectionTool
	processes        map[string]*managedProcess // id → running process
	draining         map[*managedProcess]struct{}
	starting         map[string]scheduledStart // id → active start token
	nextStart        int64
	workers          sync.WaitGroup
	errors           map[string]string // id → last error
	logs             map[string]*ConnectionLogHub
	consoleHub       *console.Hub
	onChange         func([]ConnectionTool)
	startProc        func(context.Context, ConnectionConfig, *ConnectionLogHub) (*managedProcess, error)
	lifecycle        context.Context
	cancel           context.CancelFunc
	notificationCh   chan struct{}
	notificationDone chan struct{}
	notificationMu   sync.Mutex
	pendingChanges   []toolChange
	notificationStop bool
	closeOnce        sync.Once
	closed           bool
}

// NewManagerWithRuntime loads MCP connections from SQLite.
func NewManagerWithRuntime(runtime *runtimestore.Store, onChange func([]ConnectionTool)) (*Manager, error) {
	return NewManagerWithRuntimeAndConsole(runtime, nil, onChange)
}

// NewManagerWithRuntimeAndConsole loads MCP connections and writes process-level
// messages to the composition-root console hub.
func NewManagerWithRuntimeAndConsole(runtime *runtimestore.Store, consoleHub *console.Hub, onChange func([]ConnectionTool)) (*Manager, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime store is required")
	}
	lifecycle, cancel := context.WithCancel(context.Background())
	m := &Manager{
		runtime:    runtime,
		processes:  make(map[string]*managedProcess),
		draining:   make(map[*managedProcess]struct{}),
		starting:   make(map[string]scheduledStart),
		errors:     make(map[string]string),
		logs:       make(map[string]*ConnectionLogHub),
		consoleHub: consoleHub,
		onChange:   onChange,
		startProc:  startProcess,
		lifecycle:  lifecycle,
		cancel:     cancel,
	}

	if err := m.load(); err != nil {
		cancel()
		return nil, fmt.Errorf("load mcp config: %w", err)
	}
	m.startNotificationDispatcher()

	var starts []connectionStart
	m.mu.Lock()
	for i := range m.config.Connections {
		cfg := m.config.Connections[i]
		if !cfg.Enabled {
			continue
		}
		start := m.scheduleStartLocked(cfg.ID)
		start.cfg = cloneConnectionConfig(cfg)
		starts = append(starts, start)
	}
	m.mu.Unlock()
	for _, start := range starts {
		m.launchStart(start)
	}

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
	item := ConnectionWithStatus{ConnectionConfig: cloneConnectionConfig(cfg)}
	if proc, ok := m.processes[cfg.ID]; ok {
		item.Status = "running"
		item.ToolCount = len(proc.metadata)
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
	ch, cancel, err := m.SubscribeConnectionLogsWithError(id, tail)
	return ch, cancel, err == nil
}

// SubscribeConnectionLogsWithError exposes bounded-stream rejection details to
// the HTTP adapter while retaining the established Manager method contract.
func (m *Manager) SubscribeConnectionLogsWithError(id string, tail int) (<-chan LogEntry, func(), error) {
	hub, ok := m.connectionLogHub(id)
	if !ok {
		return nil, nil, fmt.Errorf("connection %q not found", id)
	}
	return hub.Subscribe(tail)
}

func (m *Manager) connectionLogHub(id string) (*ConnectionLogHub, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.connectionLocked(id); !ok {
		return nil, false
	}
	return m.ensureLogHubLocked(id), true
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
	m.mu.Lock()
	if _, exists := m.connectionLocked(id); !exists {
		m.mu.Unlock()
		return
	}
	hub := m.ensureLogHubLocked(id)
	m.mu.Unlock()
	hub.Append(stream, level, fmt.Sprintf(format, args...))
}

func (m *Manager) appendConsole(format string, args ...any) {
	if m.consoleHub != nil {
		m.consoleHub.Feed(format, args...)
	}
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
	return slices.Clone(m.visibleTools)
}

// Add persists a new connection and starts it asynchronously if enabled.
func (m *Manager) Add(cfg ConnectionConfig) error {
	cfg = normalizeConnectionConfig(cfg)
	if cfg.ID == "" {
		return fmt.Errorf("id is required")
	}

	m.mutationMu.Lock()
	defer m.mutationMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("mcp manager is closed")
	}
	candidate := cloneManagerState(m.config)
	if err := validateCandidateConnection(cfg, candidate.Connections, -1); err != nil {
		m.mu.Unlock()
		return err
	}
	candidate.Connections = append(candidate.Connections, cloneConnectionConfig(cfg))
	m.mu.Unlock()

	if err := m.save(candidate); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	var start *connectionStart
	m.mu.Lock()
	m.config = candidate
	m.configRevision++
	if cfg.Enabled {
		scheduled := m.scheduleStartLocked(cfg.ID)
		scheduled.cfg = cloneConnectionConfig(cfg)
		start = &scheduled
	}
	m.mu.Unlock()
	m.appendConnectionLog(cfg.ID, "system", "info", "connection %q saved", cfg.Name)
	if start != nil {
		m.appendConnectionLog(cfg.ID, "system", "info", "start scheduled")
		m.launchStart(*start)
	}
	return nil
}

// Update persists changes to an existing connection, stops the old subprocess,
// and starts a new one asynchronously if enabled.
func (m *Manager) Update(cfg ConnectionConfig) error {
	cfg = normalizeConnectionConfig(cfg)
	m.mutationMu.Lock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.mutationMu.Unlock()
		return fmt.Errorf("mcp manager is closed")
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
		m.mutationMu.Unlock()
		return fmt.Errorf("connection %q not found", cfg.ID)
	}
	candidate := cloneManagerState(m.config)
	if err := validateCandidateConnection(cfg, candidate.Connections, idx); err != nil {
		m.mu.Unlock()
		m.mutationMu.Unlock()
		return err
	}
	candidate.Connections[idx] = cloneConnectionConfig(cfg)
	m.mu.Unlock()

	if err := m.save(candidate); err != nil {
		m.mutationMu.Unlock()
		return fmt.Errorf("save: %w", err)
	}

	var start *connectionStart
	m.mu.Lock()
	m.config = candidate
	m.configRevision++
	old := m.detachProcessLocked(cfg.ID)
	delete(m.errors, cfg.ID)
	if cfg.Enabled {
		scheduled := m.scheduleStartLocked(cfg.ID)
		scheduled.cfg = cloneConnectionConfig(cfg)
		start = &scheduled
	}
	change := m.refreshVisibleToolsLocked()
	m.mu.Unlock()
	m.enqueueToolChange(change)
	if start != nil {
		m.launchStart(*start)
	}
	m.appendConnectionLog(cfg.ID, "system", "info", "connection %q updated", cfg.Name)
	if start != nil {
		m.appendConnectionLog(cfg.ID, "system", "info", "start scheduled")
	}
	m.mutationMu.Unlock()
	m.closeDetachedProcess(old)
	return nil
}

// Remove deletes a connection and stops its subprocess.
func (m *Manager) Remove(id string) error {
	m.mutationMu.Lock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.mutationMu.Unlock()
		return fmt.Errorf("mcp manager is closed")
	}

	idx := -1
	for i, existing := range m.config.Connections {
		if existing.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		m.mutationMu.Unlock()
		return fmt.Errorf("connection %q not found", id)
	}
	candidate := cloneManagerState(m.config)
	candidate.Connections = append(candidate.Connections[:idx], candidate.Connections[idx+1:]...)
	m.mu.Unlock()

	if err := m.save(candidate); err != nil {
		m.mutationMu.Unlock()
		return fmt.Errorf("save: %w", err)
	}

	m.mu.Lock()
	m.config = candidate
	m.configRevision++
	old := m.detachProcessLocked(id)
	delete(m.errors, id)
	logHub := m.logs[id]
	delete(m.logs, id)
	change := m.refreshVisibleToolsLocked()
	m.mu.Unlock()
	m.enqueueToolChange(change)
	m.mutationMu.Unlock()
	logHub.Close()
	m.closeDetachedProcess(old)
	return nil
}

// TestTool calls a single tool on a running connection and returns its output.
// It does not hold the manager lock during the call, so it doesn't block other operations.
// If the tool has a parameter schema, default arguments are generated for required fields.
func (m *Manager) TestTool(ctx context.Context, connID, toolName string) (string, error) {
	// Briefly hold the manager lock to acquire a process lease. The call itself
	// runs outside both locks so lifecycle changes can drain safely.
	m.mu.Lock()
	proc, ok := m.processes[connID]
	if !ok {
		for draining := range m.draining {
			if draining.cfg.ID == connID {
				m.mu.Unlock()
				return "", fmt.Errorf("%w: %q", ErrConnectionDraining, connID)
			}
		}
		m.mu.Unlock()
		return "", fmt.Errorf("%w: %q", ErrConnectionNotRunning, connID)
	}
	release, err := proc.acquire()
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("%w: %q", err, connID)
	}
	hub := m.ensureLogHubLocked(connID)
	session := proc.session
	cfgName := proc.cfg.Name

	// Metadata was loaded before this process became visible to the manager.
	var toolInfo *schema.ToolInfo
	for _, metadata := range proc.metadata {
		if metadata.info.Name == toolName {
			toolInfo, err = cloneToolInfo(metadata.info)
			if err != nil {
				release()
				m.mu.Unlock()
				return "", fmt.Errorf("copy tool metadata: %w", err)
			}
			break
		}
	}
	m.mu.Unlock()
	defer release()

	args := buildDefaultArgs(toolInfo)
	hub.Append("system", "info", fmt.Sprintf("testing tool %q", toolName))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
		m.appendConsole("mcp error: test tool %q on %q: %v", toolName, cfgName, err)
		hub.Append("system", "error", fmt.Sprintf("tool %q call error: %v", toolName, err))
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
		m.appendConsole("mcp error: test tool %q on %q failed: %s", toolName, cfgName, errMsg)
		hub.Append("system", "error", fmt.Sprintf("tool %q returned error: %s", toolName, errMsg))
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
	hub.Append("system", "info", fmt.Sprintf("tool %q test passed", toolName))
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
func (m *Manager) Test(ctx context.Context, cfg ConnectionConfig) error {
	cfg = normalizeConnectionConfig(cfg)
	if err := validateServerBoundConnection(cfg); err != nil {
		return err
	}
	var hub *ConnectionLogHub
	if cfg.ID != "" {
		if existingHub, ok := m.connectionLogHub(cfg.ID); ok {
			hub = existingHub
			hub.Append("system", "info", fmt.Sprintf("testing connection %q", cfg.Name))
		}
	}
	err := m.testConnection(ctx, cfg, hub)
	if hub != nil {
		if err != nil {
			hub.Append("system", "error", fmt.Sprintf("connection test error: %v", err))
		} else {
			hub.Append("system", "info", "connection test passed")
		}
	}
	if shouldApplyTestResult(err) {
		m.applyTestResult(cfg, err)
	}
	return err
}

// shouldApplyTestResult filters caller-controlled cancellation from persistent
// connection state. A canceled HTTP request only ends that one probe.
func shouldApplyTestResult(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func (m *Manager) testConnection(ctx context.Context, cfg ConnectionConfig, hub *ConnectionLogHub) error {
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

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
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
	metadata, err := collectManagedTools(ctx, tools)
	if err != nil {
		return fmt.Errorf("%w: test tool metadata: %w", ErrTestConnectFailed, err)
	}
	names := make([]string, 0, len(metadata))
	for _, item := range metadata {
		names = append(names, item.info.Name)
	}
	if err := verifyBackend(ctx, cfg, session, names); err != nil {
		return fmt.Errorf("%w: test backend: %w", ErrTestConnectFailed, err)
	}
	return nil
}

func (m *Manager) applyTestResult(cfg ConnectionConfig, testErr error) {
	if cfg.ID == "" || !shouldApplyTestResult(testErr) {
		return
	}

	var start *connectionStart
	m.mutationMu.Lock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.mutationMu.Unlock()
		return
	}
	savedCfg, ok := m.connectionLocked(cfg.ID)
	if !ok {
		m.mu.Unlock()
		m.mutationMu.Unlock()
		return
	}

	if testErr == nil {
		if sameRuntimeConfig(savedCfg, cfg) {
			delete(m.errors, cfg.ID)
			if savedCfg.Enabled {
				if _, running := m.processes[cfg.ID]; !running {
					if _, starting := m.starting[cfg.ID]; !starting {
						scheduled := m.scheduleStartLocked(cfg.ID)
						scheduled.cfg = cloneConnectionConfig(savedCfg)
						start = &scheduled
					}
				}
			}
		}
		m.mu.Unlock()
		if start != nil {
			m.launchStart(*start)
			m.appendConnectionLog(cfg.ID, "system", "info", "start scheduled after successful test")
		}
		m.mutationMu.Unlock()
		return
	}

	proc, running := m.processes[cfg.ID]
	if !running {
		if start, ok := m.starting[cfg.ID]; ok {
			delete(m.starting, cfg.ID)
			if start.cancel != nil {
				start.cancel()
			}
		}
		m.errors[cfg.ID] = testErr.Error()
		m.mu.Unlock()
		m.mutationMu.Unlock()
		return
	}
	if sameRuntimeConfig(proc.cfg, cfg) {
		draining := m.detachProcessLocked(cfg.ID)
		m.errors[cfg.ID] = testErr.Error()
		change := m.refreshVisibleToolsLocked()
		m.mu.Unlock()
		m.enqueueToolChange(change)
		m.appendConnectionLog(cfg.ID, "system", "error", "connection error: %v", testErr)
		m.mutationMu.Unlock()
		m.closeDetachedProcess(draining)
		return
	}

	release, err := proc.acquire()
	if err != nil {
		m.mu.Unlock()
		m.mutationMu.Unlock()
		return
	}
	currentCfg := cloneConnectionConfig(proc.cfg)
	session := proc.session
	names := proc.toolNames()
	m.mu.Unlock()
	m.mutationMu.Unlock()

	ctx, cancel := context.WithTimeout(proc.context(), 15*time.Second)
	currentErr := verifyBackend(ctx, currentCfg, session, names)
	cancel()
	release()
	if currentErr == nil {
		return
	}

	m.mutationMu.Lock()
	m.mu.Lock()
	if !m.closed {
		if currentProc, stillRunning := m.processes[cfg.ID]; stillRunning && currentProc == proc {
			draining := m.detachProcessLocked(cfg.ID)
			m.errors[cfg.ID] = currentErr.Error()
			change := m.refreshVisibleToolsLocked()
			m.mu.Unlock()
			m.enqueueToolChange(change)
			m.appendConnectionLog(cfg.ID, "system", "error", "connection error: %v", currentErr)
			m.mutationMu.Unlock()
			m.closeDetachedProcess(draining)
			return
		}
	}
	m.mu.Unlock()
	m.mutationMu.Unlock()
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

func sameStoredConnectionConfig(a, b ConnectionConfig) bool {
	return a.ID == b.ID &&
		a.Name == b.Name &&
		a.Type == b.Type &&
		normalizedTransport(a.Transport) == normalizedTransport(b.Transport) &&
		a.Command == b.Command &&
		a.URL == b.URL &&
		a.Enabled == b.Enabled &&
		a.ContainerID == b.ContainerID &&
		a.NodeletID == b.NodeletID &&
		slices.Equal(a.Args, b.Args) &&
		slices.Equal(a.Env, b.Env)
}

func normalizedTransport(transport string) string {
	if transport == "" {
		return "stdio"
	}
	return transport
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.mutationMu.Lock()
		m.mu.Lock()
		m.closed = true
		if m.cancel != nil {
			m.cancel()
		}
		for id, start := range m.starting {
			delete(m.starting, id)
			if start.cancel != nil {
				start.cancel()
			}
		}
		for id := range m.processes {
			m.detachProcessLocked(id)
		}
		processes := make([]*managedProcess, 0, len(m.draining))
		for proc := range m.draining {
			processes = append(processes, proc)
		}
		logHubs := make([]*ConnectionLogHub, 0, len(m.logs))
		for _, hub := range m.logs {
			logHubs = append(logHubs, hub)
		}
		m.logs = nil
		m.visibleTools = nil
		m.mu.Unlock()

		m.closeNotificationDispatcher()
		m.mutationMu.Unlock()
		for _, hub := range logHubs {
			hub.Close()
		}
		m.workers.Wait()
		for _, proc := range processes {
			m.closeDetachedProcess(proc)
		}
	})
}

func (m *Manager) lifecycleContext() context.Context {
	if m.lifecycle != nil {
		return m.lifecycle
	}
	return context.Background()
}

func (m *Manager) toolInfosForConnectionLocked(connID string) []ToolInfo {
	infos := m.connectionToolInfosLocked()
	return infos[connID]
}

func (m *Manager) connectionToolInfosLocked() map[string][]ToolInfo {
	result := make(map[string][]ToolInfo, len(m.processes))
	for _, entry := range m.visibleTools {
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

func (m *Manager) refreshVisibleToolsLocked() *toolChange {
	next := m.buildVisibleToolsLocked()
	if sameVisibleTools(m.visibleTools, next) {
		return nil
	}
	m.visibleTools = next
	m.toolRevision++
	return &toolChange{revision: m.toolRevision, tools: slices.Clone(next)}
}

func (m *Manager) buildVisibleToolsLocked() []ConnectionTool {
	ids := make([]string, 0, len(m.processes))
	for id := range m.processes {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	all := make([]ConnectionTool, 0)
	for _, id := range ids {
		proc := m.processes[id]
		all = append(all, proc.connectionTools()...)
	}
	return all
}

func sameVisibleTools(a, b []ConnectionTool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ConnectionID != b[i].ConnectionID ||
			a[i].ConnectionName != b[i].ConnectionName ||
			a[i].ConnectionType != b[i].ConnectionType ||
			a[i].NodeletID != b[i].NodeletID ||
			a[i].ServerName != b[i].ServerName ||
			a[i].OriginalName != b[i].OriginalName ||
			a[i].ModelName != b[i].ModelName ||
			a[i].Description != b[i].Description {
			return false
		}
	}
	return true
}
