package config

import "testing"

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
