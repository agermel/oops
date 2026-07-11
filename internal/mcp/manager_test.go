package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/goleak"
	runtimestore "oops/internal/store/runtime"
)

func TestManagerRuntimeLoadsSQLiteConnections(t *testing.T) {
	dir := t.TempDir()

	runtime, err := runtimestore.Open(filepath.Join(dir, "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()
	if err := runtime.ReplaceMCPConnections(t.Context(), []runtimestore.MCPConnectionRecord{
		{
			ID:          "mysql-1",
			Name:        "mysql",
			Type:        "mysql",
			Transport:   "stdio",
			Command:     "mysql-mcp-server",
			Args:        []string{"--flag"},
			Env:         []string{"MYSQL_DSN=x"},
			Enabled:     false,
			ContainerID: "c1",
			NodeletID:   "n1",
		},
	}); err != nil {
		t.Fatalf("seed runtime mcp connection: %v", err)
	}

	manager, err := NewManagerWithRuntime(runtime, nil)
	if err != nil {
		t.Fatalf("NewManagerWithRuntime: %v", err)
	}
	list := manager.List()
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].ID != "mysql-1" || list[0].Args[0] != "--flag" || list[0].Env[0] != "MYSQL_DSN=x" {
		t.Fatalf("imported connection = %+v", list[0])
	}
	if list[0].Status != "stopped" {
		t.Fatalf("status = %q, want stopped", list[0].Status)
	}
}

func TestManagerRuntimePreservesAndDisablesGlobalConnections(t *testing.T) {
	dir := t.TempDir()

	runtime, err := runtimestore.Open(filepath.Join(dir, "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()
	if err := runtime.ReplaceMCPConnections(t.Context(), []runtimestore.MCPConnectionRecord{
		{
			ID:      "global",
			Name:    "global mysql",
			Type:    "mysql",
			Enabled: true,
		},
		{
			ID:        "server",
			Name:      "server mysql",
			Type:      "mysql",
			Enabled:   false,
			NodeletID: "n1",
		},
	}); err != nil {
		t.Fatalf("seed runtime mcp connections: %v", err)
	}

	manager, err := NewManagerWithRuntime(runtime, nil)
	if err != nil {
		t.Fatalf("NewManagerWithRuntime: %v", err)
	}
	list := manager.List()
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2: %+v", len(list), list)
	}
	if list[0].ID != "global" || list[0].Enabled || list[0].Status != "stopped" {
		t.Fatalf("global connection = %+v, want preserved disabled stopped", list[0])
	}
	if list[1].ID != "server" {
		t.Fatalf("server connection = %q, want server", list[1].ID)
	}

	records, err := runtime.ListMCPConnections(t.Context())
	if err != nil {
		t.Fatalf("ListMCPConnections: %v", err)
	}
	if len(records) != 2 || records[0].ID != "global" || records[0].Enabled || records[1].ID != "server" {
		t.Fatalf("persisted records = %+v, want preserved disabled global and server", records)
	}
}

func TestManagerRejectsGlobalConnection(t *testing.T) {
	dir := t.TempDir()

	runtime, err := runtimestore.Open(filepath.Join(dir, "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()

	manager, err := NewManagerWithRuntime(runtime, nil)
	if err != nil {
		t.Fatalf("NewManagerWithRuntime: %v", err)
	}

	if err := manager.Add(ConnectionConfig{ID: "global", Name: "global", Type: "mysql"}); err == nil {
		t.Fatal("Add accepted global connection")
	}
	if err := manager.Test(context.Background(), ConnectionConfig{ID: "global", Name: "global", Type: "mysql"}); err == nil {
		t.Fatal("Test accepted global connection")
	}
}

func TestApplySuccessfulTestRestartsEnabledConnection(t *testing.T) {
	cfg := ConnectionConfig{
		ID:        "kafka-1",
		Name:      "kafka",
		Type:      "kafka",
		Transport: "stdio",
		Command:   "./mcp-servers/kafka/kafka-mcp",
		Enabled:   true,
		NodeletID: "n1",
	}
	started := make(chan struct{})
	lifecycle, cancel := context.WithCancel(t.Context())
	manager := &Manager{
		config:    managerState{Connections: []ConnectionConfig{cfg}},
		processes: map[string]*managedProcess{},
		draining:  map[*managedProcess]struct{}{},
		starting:  map[string]scheduledStart{},
		errors:    map[string]string{cfg.ID: "old error"},
		logs:      map[string]*ConnectionLogHub{},
		lifecycle: lifecycle,
		cancel:    cancel,
		startProc: func(context.Context, ConnectionConfig, *ConnectionLogHub) (*managedProcess, error) {
			close(started)
			return &managedProcess{closer: func() {}}, nil
		},
	}
	t.Cleanup(manager.Close)

	manager.applyTestResult(cfg, nil)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("connection restart was not scheduled")
	}
}

func TestCallToolArgumentsKeepsEmptyObject(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Name = "list-topics"
	req.Params.Arguments = callToolArguments(nil)

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if got := string(data); !strings.Contains(got, `"arguments":{}`) {
		t.Fatalf("request json = %s", got)
	}
}

func TestCollectToolsLockedIncludesConnectionMetadata(t *testing.T) {
	manager := &Manager{
		processes: map[string]*managedProcess{
			"cache-a": {
				cfg: ConnectionConfig{
					ID:        "cache-a",
					Name:      "Redis",
					Type:      "redis",
					NodeletID: "node-a",
				},
				tools: []tool.BaseTool{
					toolBaseForTest{name: "info", desc: "redis info"},
					toolBaseForTest{name: "get", desc: "redis get"},
				},
			},
			"cache-b": {
				cfg: ConnectionConfig{
					ID:        "cache-b",
					Name:      "Cache",
					Type:      "redis",
					NodeletID: "node-b",
				},
				tools: []tool.BaseTool{
					toolBaseForTest{name: "info", desc: "redis info duplicate"},
				},
			},
			"etcd-main": {
				cfg: ConnectionConfig{
					ID:   "etcd-main",
					Name: "Etcd",
					Type: "etcd",
				},
				tools: []tool.BaseTool{
					toolBaseForTest{name: "etcd_get", desc: "etcd get"},
				},
			},
		},
	}
	for _, proc := range manager.processes {
		if err := proc.prepare(t.Context(), t.Context(), proc.cfg); err != nil {
			t.Fatalf("prepare process: %v", err)
		}
	}
	manager.refreshVisibleToolsLocked()

	entries := manager.GetConnectionToolEntries()
	got := make(map[string]string, len(entries))
	for _, entry := range entries {
		got[entry.ConnectionID+":"+entry.OriginalName] = entry.ConnectionType + ":" + entry.NodeletID + ":" + entry.Description
	}

	want := map[string]string{
		"cache-a:info":       "redis:node-a:redis info",
		"cache-a:get":        "redis:node-a:redis get",
		"cache-b:info":       "redis:node-b:redis info duplicate",
		"etcd-main:etcd_get": "etcd::etcd get",
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Fatalf("metadata for %s = %q, want %q (all: %#v)", key, got[key], wantValue, got)
		}
	}
}

func TestNormalizeConnectionConfigBackfillsKafkaAuthDefaults(t *testing.T) {
	cfg := normalizeConnectionConfig(ConnectionConfig{
		Type: "kafka",
		Env: []string{
			"BOOTSTRAP_SERVERS=kafka.example.com:9094",
			"KAFKA_API_KEY=root",
			"KAFKA_API_SECRET=secret",
		},
	})

	if envValue(cfg.Env, "KAFKA_SECURITY_PROTOCOL") != "sasl_plaintext" {
		t.Fatalf("KAFKA_SECURITY_PROTOCOL = %q", envValue(cfg.Env, "KAFKA_SECURITY_PROTOCOL"))
	}
	if envValue(cfg.Env, "KAFKA_SASL_MECHANISM") != "PLAIN" {
		t.Fatalf("KAFKA_SASL_MECHANISM = %q", envValue(cfg.Env, "KAFKA_SASL_MECHANISM"))
	}
}

type toolBaseForTest struct {
	name string
	desc string
}

func (t toolBaseForTest) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: t.desc}, nil
}

func TestNormalizeConnectionConfigKeepsExplicitKafkaProtocol(t *testing.T) {
	cfg := normalizeConnectionConfig(ConnectionConfig{
		Type: "kafka",
		Env: []string{
			"BOOTSTRAP_SERVERS=kafka.example.com:9094",
			"KAFKA_API_KEY=root",
			"KAFKA_API_SECRET=secret",
			"KAFKA_SECURITY_PROTOCOL=sasl_ssl",
			"KAFKA_SASL_MECHANISM=SCRAM-SHA-512",
		},
	})

	if envValue(cfg.Env, "KAFKA_SECURITY_PROTOCOL") != "sasl_ssl" {
		t.Fatalf("KAFKA_SECURITY_PROTOCOL = %q", envValue(cfg.Env, "KAFKA_SECURITY_PROTOCOL"))
	}
	if envValue(cfg.Env, "KAFKA_SASL_MECHANISM") != "SCRAM-SHA-512" {
		t.Fatalf("KAFKA_SASL_MECHANISM = %q", envValue(cfg.Env, "KAFKA_SASL_MECHANISM"))
	}
}

func TestNormalizeConnectionConfigRemovesBlankRedisGeneratedEnv(t *testing.T) {
	cfg := normalizeConnectionConfig(ConnectionConfig{
		Type: "redis",
		Env: []string{
			"REDIS_HOST=redis.example.com",
			"REDIS_PORT=6379",
			"REDIS_USERNAME=",
			"REDIS_DB=0",
			"REDIS_PASSWORD=secret",
			"EXTRA=1",
		},
	})

	want := []string{
		"REDIS_HOST=redis.example.com",
		"REDIS_PORT=6379",
		"REDIS_DB=0",
		"REDIS_PWD=secret",
		"EXTRA=1",
	}
	if !slices.Equal(cfg.Env, want) {
		t.Fatalf("env = %#v, want %#v", cfg.Env, want)
	}
}

func TestNormalizeConnectionConfigMovesNacosHostArgsToEnv(t *testing.T) {
	cfg := normalizeConnectionConfig(ConnectionConfig{
		Type: "nacos",
		Args: []string{"--host", "nacos.example.com", "--port", "8848", "--debug"},
		Env:  []string{"NACOS_USERNAME=nacos", "NACOS_PASSWORD=secret", "EXTRA=1"},
	})

	wantEnv := []string{
		"NACOS_ADDR=nacos.example.com:8848",
		"NACOS_USERNAME=nacos",
		"NACOS_PASSWORD=secret",
		"EXTRA=1",
	}
	if !slices.Equal(cfg.Env, wantEnv) {
		t.Fatalf("env = %#v, want %#v", cfg.Env, wantEnv)
	}
	if !slices.Equal(cfg.Args, []string{"--debug"}) {
		t.Fatalf("args = %#v, want --debug", cfg.Args)
	}
}

func newMCPManagerForTest(t *testing.T, onChange func([]ConnectionTool)) (*Manager, *runtimestore.Store) {
	t.Helper()
	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	manager, err := NewManagerWithRuntime(runtime, onChange)
	if err != nil {
		t.Fatalf("NewManagerWithRuntime: %v", err)
	}
	t.Cleanup(manager.Close)
	return manager, runtime
}

func testMCPConnectionConfig(id string, enabled bool) ConnectionConfig {
	return ConnectionConfig{
		ID:        id,
		Name:      id,
		Type:      "redis",
		Transport: "stdio",
		Enabled:   enabled,
		NodeletID: "node-1",
	}
}

func waitForMCP(t *testing.T, condition func() bool) {
	t.Helper()
	if condition() {
		return
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			if condition() {
				return
			}
		case <-timeout.C:
			t.Fatal("condition did not become true")
		}
	}
}

type blockingInvokableTool struct {
	started     chan struct{}
	release     <-chan struct{}
	startedOnce sync.Once
}

func (t *blockingInvokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "inspect", Desc: "inspect state"}, nil
}

func (t *blockingInvokableTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	t.startedOnce.Do(func() { close(t.started) })
	select {
	case <-t.release:
		return "ok", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type blockingMCPSession struct {
	started     chan struct{}
	release     <-chan struct{}
	startedOnce sync.Once
}

func (s *blockingMCPSession) CallTool(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.startedOnce.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return mcp.NewToolResultText("ok"), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*blockingMCPSession) Ping(context.Context) error {
	return nil
}

type statusCodeError struct {
	status int
}

func (e statusCodeError) Error() string {
	return "status error"
}

func (e statusCodeError) StatusCode() int {
	return e.status
}

func TestManagerNotificationDeliversEveryCommittedSnapshot(t *testing.T) {
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(releaseFirst) })
	}()

	var callsMu sync.Mutex
	var calls [][]ConnectionTool
	manager, _ := newMCPManagerForTest(t, func(tools []ConnectionTool) {
		callsMu.Lock()
		calls = append(calls, slices.Clone(tools))
		callCount := len(calls)
		callsMu.Unlock()
		if callCount == 1 {
			close(firstEntered)
			<-releaseFirst
		}
	})
	manager.startProc = func(context.Context, ConnectionConfig, *ConnectionLogHub) (*managedProcess, error) {
		return &managedProcess{
			tools:  []tool.BaseTool{toolBaseForTest{name: "inspect", desc: "inspect state"}},
			closer: func() {},
		}, nil
	}

	cfg := testMCPConnectionConfig("notify", true)
	if err := manager.Add(cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first tool notification did not arrive")
	}

	disabled := cfg
	disabled.Enabled = false
	if err := manager.Update(disabled); err != nil {
		t.Fatalf("disable connection: %v", err)
	}
	if err := manager.Update(cfg); err != nil {
		t.Fatalf("enable connection: %v", err)
	}
	waitForMCP(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return manager.toolRevision == 3
	})

	releaseOnce.Do(func() { close(releaseFirst) })
	waitForMCP(t, func() bool {
		callsMu.Lock()
		defer callsMu.Unlock()
		return len(calls) == 3
	})

	callsMu.Lock()
	defer callsMu.Unlock()
	if got := []int{len(calls[0]), len(calls[1]), len(calls[2])}; !slices.Equal(got, []int{1, 0, 1}) {
		t.Fatalf("notification snapshot sizes = %v, want [1 0 1]", got)
	}
}

func TestManagerNotificationCallbackCanReadAndCannotMutateSnapshot(t *testing.T) {
	callbackDone := make(chan struct{})
	var callbackOnce sync.Once
	var manager *Manager
	manager, _ = newMCPManagerForTest(t, func(tools []ConnectionTool) {
		_ = manager.List()
		_ = manager.GetConnectionToolEntries()
		if len(tools) > 0 {
			tools[0].Description = "mutated by callback"
		}
		callbackOnce.Do(func() { close(callbackDone) })
	})
	manager.startProc = func(context.Context, ConnectionConfig, *ConnectionLogHub) (*managedProcess, error) {
		return &managedProcess{
			tools:  []tool.BaseTool{toolBaseForTest{name: "inspect", desc: "original description"}},
			closer: func() {},
		}, nil
	}

	if err := manager.Add(testMCPConnectionConfig("reentrant", true)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("callback did not finish")
	}

	entries := manager.GetConnectionToolEntries()
	if len(entries) != 1 {
		t.Fatalf("tool entries = %d, want 1", len(entries))
	}
	if entries[0].Description != "original description" {
		t.Fatalf("snapshot description = %q, want original description", entries[0].Description)
	}
}

func TestLeasedToolDrainsInFlightCallsBeforeClose(t *testing.T) {
	releaseCall := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseCall) })
	})
	base := &blockingInvokableTool{started: make(chan struct{}), release: releaseCall}
	closed := make(chan struct{})
	proc := &managedProcess{
		tools:  []tool.BaseTool{base},
		closer: func() { close(closed) },
	}
	cfg := testMCPConnectionConfig("lease", true)
	if err := proc.prepare(t.Context(), t.Context(), cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	entries := proc.connectionTools()
	if len(entries) != 1 {
		t.Fatalf("tool entries = %d, want 1", len(entries))
	}
	invokable, ok := entries[0].Tool.(tool.InvokableTool)
	if !ok {
		t.Fatal("leased tool is not invokable")
	}

	callDone := make(chan error, 1)
	go func() {
		_, err := invokable.InvokableRun(t.Context(), "{}")
		callDone <- err
	}()
	select {
	case <-base.started:
	case <-time.After(time.Second):
		t.Fatal("tool call did not start")
	}

	proc.beginDrain()
	if _, err := invokable.InvokableRun(t.Context(), "{}"); !errors.Is(err, ErrConnectionDraining) {
		t.Fatalf("new call error = %v, want ErrConnectionDraining", err)
	}
	closeDone := make(chan struct{})
	go func() {
		proc.close()
		close(closeDone)
	}()
	select {
	case <-closed:
		t.Fatal("process closed before leased call completed")
	case <-time.After(30 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(releaseCall) })
	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("leased call: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("leased call did not finish")
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("process close did not finish")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("process closer was not called")
	}
}

func TestManagerRemoveWaitsForTestToolLease(t *testing.T) {
	releaseCall := make(chan struct{})
	var releaseOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(releaseCall) })
	}()
	session := &blockingMCPSession{started: make(chan struct{}), release: releaseCall}
	closed := make(chan struct{})
	manager, _ := newMCPManagerForTest(t, nil)
	manager.startProc = func(context.Context, ConnectionConfig, *ConnectionLogHub) (*managedProcess, error) {
		return &managedProcess{
			session: session,
			tools:   []tool.BaseTool{toolBaseForTest{name: "inspect", desc: "inspect state"}},
			closer:  func() { close(closed) },
		}, nil
	}
	cfg := testMCPConnectionConfig("test-tool", true)
	if err := manager.Add(cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitForMCP(t, func() bool {
		return len(manager.GetConnectionToolEntries()) == 1
	})

	callDone := make(chan error, 1)
	go func() {
		_, err := manager.TestTool(t.Context(), cfg.ID, "inspect")
		callDone <- err
	}()
	select {
	case <-session.started:
	case <-time.After(time.Second):
		t.Fatal("TestTool call did not start")
	}

	removeDone := make(chan error, 1)
	go func() {
		removeDone <- manager.Remove(cfg.ID)
	}()
	waitForMCP(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return len(manager.processes) == 0 && len(manager.draining) == 1
	})
	if _, err := manager.TestTool(t.Context(), cfg.ID, "inspect"); !errors.Is(err, ErrConnectionDraining) {
		t.Fatalf("TestTool during drain error = %v, want ErrConnectionDraining", err)
	}
	select {
	case <-closed:
		t.Fatal("process closed before TestTool lease completed")
	case <-time.After(30 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(releaseCall) })
	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("TestTool: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("TestTool call did not finish")
	}
	select {
	case err := <-removeDone:
		if err != nil {
			t.Fatalf("Remove: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Remove did not finish")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("process closer was not called")
	}
	manager.mu.Lock()
	_, logHubExists := manager.logs[cfg.ID]
	manager.mu.Unlock()
	if logHubExists {
		t.Fatal("completed TestTool recreated the removed connection log hub")
	}
}

func TestManagerApplyTestCancellationKeepsRunningConnection(t *testing.T) {
	manager, _ := newMCPManagerForTest(t, nil)
	cfg := testMCPConnectionConfig("test-cancel", true)
	proc := &managedProcess{cfg: cfg, closer: func() {}}
	manager.mu.Lock()
	manager.config = managerState{Connections: []ConnectionConfig{cfg}}
	manager.processes[cfg.ID] = proc
	manager.errors[cfg.ID] = "previous error"
	manager.mu.Unlock()

	for _, testErr := range []error{
		fmt.Errorf("%w: %w", ErrTestConnectFailed, context.Canceled),
		fmt.Errorf("%w: %w", ErrTestConnectFailed, context.DeadlineExceeded),
	} {
		manager.applyTestResult(cfg, testErr)
		manager.mu.Lock()
		current := manager.processes[cfg.ID]
		errText := manager.errors[cfg.ID]
		manager.mu.Unlock()
		if current != proc {
			t.Fatalf("canceled test detached running process: got %p, want %p", current, proc)
		}
		if errText != "previous error" {
			t.Fatalf("canceled test changed connection error to %q", errText)
		}
	}
}

func TestManagerCloseWaitsForBlockedToolCallback(t *testing.T) {
	ignoreExisting := goleak.IgnoreCurrent()
	callbackEntered := make(chan struct{})
	releaseCallback := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseCallback) }) })
	var callbackOnce sync.Once
	manager, runtime := newMCPManagerForTest(t, func([]ConnectionTool) {
		callbackOnce.Do(func() { close(callbackEntered) })
		<-releaseCallback
	})
	manager.startProc = func(context.Context, ConnectionConfig, *ConnectionLogHub) (*managedProcess, error) {
		return &managedProcess{
			tools:  []tool.BaseTool{toolBaseForTest{name: "inspect", desc: "inspect state"}},
			closer: func() {},
		}, nil
	}
	if err := manager.Add(testMCPConnectionConfig("close-callback", true)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case <-callbackEntered:
	case <-time.After(time.Second):
		t.Fatal("tool callback did not start")
	}

	closed := make(chan struct{})
	go func() {
		manager.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned before in-flight callback completed")
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseCallback) })
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after callback completed")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	goleak.VerifyNone(t, ignoreExisting)
}

func TestManagerCloseCancelsBlockedStart(t *testing.T) {
	ignoreExisting := goleak.IgnoreCurrent()
	startEntered := make(chan struct{})
	startCanceled := make(chan struct{})
	manager, runtime := newMCPManagerForTest(t, nil)
	manager.startProc = func(ctx context.Context, _ ConnectionConfig, _ *ConnectionLogHub) (*managedProcess, error) {
		close(startEntered)
		<-ctx.Done()
		close(startCanceled)
		return nil, ctx.Err()
	}
	if err := manager.Add(testMCPConnectionConfig("close-start", true)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case <-startEntered:
	case <-time.After(time.Second):
		t.Fatal("start did not begin")
	}

	closed := make(chan struct{})
	go func() {
		manager.Close()
		close(closed)
	}()
	select {
	case <-startCanceled:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel active start")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for canceled start")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	goleak.VerifyNone(t, ignoreExisting)
}

func TestManagerDiscardsStaleStartWithoutNotification(t *testing.T) {
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	var releaseOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(releaseStart) })
	}()
	changes := make(chan []ConnectionTool, 1)
	closed := make(chan struct{})
	manager, _ := newMCPManagerForTest(t, func(tools []ConnectionTool) {
		changes <- tools
	})
	manager.startProc = func(context.Context, ConnectionConfig, *ConnectionLogHub) (*managedProcess, error) {
		close(startEntered)
		<-releaseStart
		return &managedProcess{
			tools:  []tool.BaseTool{toolBaseForTest{name: "inspect", desc: "inspect state"}},
			closer: func() { close(closed) },
		}, nil
	}
	cfg := testMCPConnectionConfig("stale", true)
	if err := manager.Add(cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case <-startEntered:
	case <-time.After(time.Second):
		t.Fatal("start did not begin")
	}

	disabled := cfg
	disabled.Enabled = false
	if err := manager.Update(disabled); err != nil {
		t.Fatalf("Update: %v", err)
	}
	releaseOnce.Do(func() { close(releaseStart) })
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("stale candidate was not closed")
	}

	manager.mu.Lock()
	configRevision := manager.configRevision
	toolRevision := manager.toolRevision
	connections := cloneManagerState(manager.config).Connections
	visible := slices.Clone(manager.visibleTools)
	manager.mu.Unlock()
	if configRevision != 2 || toolRevision != 0 || len(visible) != 0 {
		t.Fatalf("revisions/tools = %d/%d/%d, want 2/0/0", configRevision, toolRevision, len(visible))
	}
	if len(connections) != 1 || connections[0].Enabled {
		t.Fatalf("saved config = %+v, want disabled connection", connections)
	}
	select {
	case change := <-changes:
		t.Fatalf("stale start emitted notification: %#v", change)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestManagerKeepsCommittedStateWhenDurableWriteFails(t *testing.T) {
	changes := make(chan []ConnectionTool, 2)
	manager, runtime := newMCPManagerForTest(t, func(tools []ConnectionTool) {
		changes <- tools
	})
	manager.startProc = func(context.Context, ConnectionConfig, *ConnectionLogHub) (*managedProcess, error) {
		return &managedProcess{
			tools:  []tool.BaseTool{toolBaseForTest{name: "inspect", desc: "inspect state"}},
			closer: func() {},
		}, nil
	}
	cfg := testMCPConnectionConfig("durable", true)
	if err := manager.Add(cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("initial notification did not arrive")
	}
	waitForMCP(t, func() bool {
		return len(manager.GetConnectionToolEntries()) == 1
	})

	manager.mu.Lock()
	beforeConfigRevision := manager.configRevision
	beforeToolRevision := manager.toolRevision
	beforeProcess := manager.processes[cfg.ID]
	manager.mu.Unlock()
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	changed := cfg
	changed.Enabled = false
	changed.Name = "new name"
	if err := manager.Update(changed); err == nil {
		t.Fatal("Update succeeded after durable store close")
	}

	manager.mu.Lock()
	afterConfigRevision := manager.configRevision
	afterToolRevision := manager.toolRevision
	afterProcess := manager.processes[cfg.ID]
	connections := cloneManagerState(manager.config).Connections
	visible := slices.Clone(manager.visibleTools)
	manager.mu.Unlock()
	if afterConfigRevision != beforeConfigRevision || afterToolRevision != beforeToolRevision {
		t.Fatalf("revisions changed from %d/%d to %d/%d", beforeConfigRevision, beforeToolRevision, afterConfigRevision, afterToolRevision)
	}
	if afterProcess != beforeProcess || len(visible) != 1 {
		t.Fatalf("committed process/tools changed after durable failure: process=%p want %p tools=%d", afterProcess, beforeProcess, len(visible))
	}
	if len(connections) != 1 || connections[0].Name != cfg.Name || !connections[0].Enabled {
		t.Fatalf("committed config changed after durable failure: %+v", connections)
	}
	select {
	case change := <-changes:
		t.Fatalf("durable failure emitted notification: %#v", change)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestIsPermanentStartError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "canceled", err: context.Canceled, want: true},
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "http 401", err: statusCodeError{status: 401}, want: true},
		{name: "http 404", err: statusCodeError{status: 404}, want: true},
		{name: "http 500", err: statusCodeError{status: 500}, want: false},
		{name: "error text 403", err: errors.New("HTTP status 403"), want: true},
		{name: "error text 500", err: errors.New("HTTP status 500"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isPermanentStartError(test.err); got != test.want {
				t.Fatalf("isPermanentStartError(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}
