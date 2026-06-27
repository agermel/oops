package config

import (
	"testing"

	"oops/internal/connection"
)

// TestConnections 验证组件配置能转换成 GUI 连接列表。
func TestConnections(t *testing.T) {
	cfg := Config{
		ExtraConnections: []connection.Connection{{
			ID:      "es",
			Name:    "Elasticsearch",
			Type:    "elasticsearch",
			Address: "http://es.example.com:9200",
		}},
		MySQL: MySQLConfig{DSN: "user:pass@tcp(mysql.example.com:3306)/app"},
		Redis: RedisConfig{Addr: "redis.example.com:6379"},
		Etcd:  EtcdConfig{Endpoints: []string{"etcd.example.com:2379"}},
		Kafka: KafkaConfig{Addrs: []string{"kafka.example.com:9092"}},
		OTel:  OTelConfig{Endpoint: "otel.example.com:4318"},
	}

	connections := cfg.Connections()
	if len(connections) != 6 {
		t.Fatalf("connections length = %d, want 6", len(connections))
	}
	if connections[0].ID != "es" {
		t.Fatalf("first connection ID = %q, want %q", connections[0].ID, "es")
	}
	if connections[1].Address != "mysql.example.com:3306" {
		t.Fatalf("mysql address = %q, want %q", connections[1].Address, "mysql.example.com:3306")
	}
}
