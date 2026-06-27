package config

import (
	"encoding/json"
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
	if len(cfg.Agents) != 1 {
		t.Fatalf("Agents length = %d, want 1", len(cfg.Agents))
	}
	if cfg.Agents[0].Address != "http://127.0.0.1:8686" {
		t.Fatalf("Agents[0].Address = %q, want %q", cfg.Agents[0].Address, "http://127.0.0.1:8686")
	}
	if cfg.Agents[0].Token != "change-me" {
		t.Fatalf("Agents[0].Token = %q, want %q", cfg.Agents[0].Token, "change-me")
	}
	data, err := json.Marshal(cfg.Agents[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"id":"local","name":"本机","address":"http://127.0.0.1:8686"}` {
		t.Fatalf("agent JSON = %s", data)
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
}
