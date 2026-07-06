package mcp

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
