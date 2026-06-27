package config

import (
	"fmt"
	"regexp"

	"oops/internal/connection"
)

var mysqlTCPAddrPattern = regexp.MustCompile(`@tcp\(([^)]+)\)`)

// Connections 把组件配置转换成 GUI Connections 面板使用的连接列表。
func (c Config) Connections() []connection.Connection {
	connections := make([]connection.Connection, 0, 8)
	connections = append(connections, c.ExtraConnections...)

	if c.MySQL.DSN != "" {
		connections = append(connections, connection.Connection{
			ID:      "mysql",
			Name:    "MySQL",
			Type:    "mysql",
			Address: mysqlAddress(c.MySQL.DSN),
		})
	}

	if c.Redis.Addr != "" {
		connections = append(connections, connection.Connection{
			ID:      "redis",
			Name:    "Redis",
			Type:    "redis",
			Address: c.Redis.Addr,
		})
	}

	for index, endpoint := range c.Etcd.Endpoints {
		connections = append(connections, connection.Connection{
			ID:      numberedID("etcd", index),
			Name:    numberedName("Etcd", index, len(c.Etcd.Endpoints)),
			Type:    "etcd",
			Address: endpoint,
		})
	}

	for index, addr := range c.Kafka.Addrs {
		connections = append(connections, connection.Connection{
			ID:      numberedID("kafka", index),
			Name:    numberedName("Kafka", index, len(c.Kafka.Addrs)),
			Type:    "kafka",
			Address: addr,
		})
	}

	if c.OTel.Endpoint != "" {
		connections = append(connections, connection.Connection{
			ID:      "otel",
			Name:    "OpenTelemetry",
			Type:    "otel",
			Address: c.OTel.Endpoint,
		})
	}

	return connections
}

// mysqlAddress 从 MySQL DSN 中提取 host:port。
func mysqlAddress(dsn string) string {
	matches := mysqlTCPAddrPattern.FindStringSubmatch(dsn)
	if len(matches) != 2 {
		return dsn
	}
	return matches[1]
}

// numberedID 为多实例连接生成稳定 ID。
func numberedID(prefix string, index int) string {
	if index == 0 {
		return prefix
	}
	return fmt.Sprintf("%s-%d", prefix, index+1)
}

// numberedName 为多实例连接生成展示名称。
func numberedName(prefix string, index int, total int) string {
	if total <= 1 {
		return prefix
	}
	return fmt.Sprintf("%s %d", prefix, index+1)
}
