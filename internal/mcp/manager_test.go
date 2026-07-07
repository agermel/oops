package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"
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
	if err := manager.Test(ConnectionConfig{ID: "global", Name: "global", Type: "mysql"}); err == nil {
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
	manager := &Manager{
		config:    managerState{Connections: []ConnectionConfig{cfg}},
		processes: map[string]*managedProcess{},
		starting:  map[string]int64{},
		errors:    map[string]string{cfg.ID: "old error"},
		logs:      map[string]*ConnectionLogHub{},
		startProc: func(ConnectionConfig, *ConnectionLogHub) (*managedProcess, error) {
			close(started)
			return &managedProcess{closer: func() {}}, nil
		},
	}

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

	entries := manager.collectToolsLocked()
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
