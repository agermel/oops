package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadExample 验证 Viper 能读取 config 目录下的示例配置。
func TestLoadExample(t *testing.T) {
	cfg, err := Load("../../config/config.example.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Env != "prod" {
		t.Fatalf("Env = %q, want %q", cfg.Env, "prod")
	}
	if len(cfg.ExtraConnections) != 3 {
		t.Fatalf("ExtraConnections length = %d, want 3", len(cfg.ExtraConnections))
	}
	if len(cfg.Nodelets) != 1 {
		t.Fatalf("Nodelets length = %d, want 1", len(cfg.Nodelets))
	}
	if cfg.Nodelets[0].Address != "http://127.0.0.1:8686" {
		t.Fatalf("Nodelets[0].Address = %q, want %q", cfg.Nodelets[0].Address, "http://127.0.0.1:8686")
	}
	if cfg.Nodelets[0].Token != "change-me" {
		t.Fatalf("Nodelets[0].Token = %q, want %q", cfg.Nodelets[0].Token, "change-me")
	}
	data, err := json.Marshal(cfg.Nodelets[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"id":"local","name":"本机","address":"http://127.0.0.1:8686"}` {
		t.Fatalf("nodelet JSON = %s", data)
	}
	if cfg.ExtraConnections[0].ID != "elasticsearch" {
		t.Fatalf("ExtraConnections[0].ID = %q, want %q", cfg.ExtraConnections[0].ID, "elasticsearch")
	}
	if cfg.MySQL.DSN == "" {
		t.Fatal("MySQL.DSN is empty")
	}
	if cfg.Redis.Addr != "127.0.0.1:6379" {
		t.Fatalf("Redis.Addr = %q, want %q", cfg.Redis.Addr, "127.0.0.1:6379")
	}
	if len(cfg.Etcd.Endpoints) != 1 {
		t.Fatalf("Etcd.Endpoints length = %d, want 1", len(cfg.Etcd.Endpoints))
	}
	if len(cfg.Kafka.Addrs) != 1 {
		t.Fatalf("Kafka.Addrs length = %d, want 1", len(cfg.Kafka.Addrs))
	}
	if cfg.OTel.Endpoint != "127.0.0.1:4318" {
		t.Fatalf("OTel.Endpoint = %q, want %q", cfg.OTel.Endpoint, "127.0.0.1:4318")
	}
	if !cfg.LLM.Enabled {
		t.Fatal("LLM.Enabled = false, want true")
	}
	if cfg.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("LLM.Model = %q, want %q", cfg.LLM.Model, "gpt-4o-mini")
	}
	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("LLM.BaseURL = %q, want %q", cfg.LLM.BaseURL, "https://api.openai.com/v1")
	}
}

// TestLoadRuntimeEnvPath 验证运行时配置优先读取 OOPS_CONFIG。
func TestLoadRuntimeEnvPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`env: "test"
nodelets:
  - id: "custom"
    name: "自定义"
    address: "http://127.0.0.1:9999"
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv(EnvPath, path)

	cfg, err := LoadRuntime()
	if err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	if cfg.Env != "test" {
		t.Fatalf("Env = %q, want %q", cfg.Env, "test")
	}
	if len(cfg.Nodelets) != 1 || cfg.Nodelets[0].ID != "custom" {
		t.Fatalf("Nodelets = %#v", cfg.Nodelets)
	}
}
