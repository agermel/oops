package checker

import "oops/internal/connection"

// NewDefaultRegistry 创建 GUI 默认使用的连接检查器注册表。
func NewDefaultRegistry() *connection.Registry {
	registry := connection.NewRegistry()

	_ = registry.Register(NewElasticsearchChecker())
	_ = registry.Register(NewHTTPChecker("jaeger", "/"))
	_ = registry.Register(NewHTTPChecker("nacos", "/nacos/v1/console/health/readiness"))
	_ = registry.Register(NewHTTPChecker("http", ""))
	_ = registry.Register(NewTCPChecker("mysql"))
	_ = registry.Register(NewTCPChecker("redis"))
	_ = registry.Register(NewTCPChecker("etcd"))
	_ = registry.Register(NewTCPChecker("kafka"))
	_ = registry.Register(NewTCPChecker("otel"))

	return registry
}
