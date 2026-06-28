package docker

import (
	"strings"
)

// ServiceType 表示从容器元数据中识别出的服务类型。
type ServiceType string

const (
	ServiceMySQL         ServiceType = "mysql"
	ServiceRedis         ServiceType = "redis"
	ServicePostgres      ServiceType = "postgres"
	ServiceMongo         ServiceType = "mongo"
	ServiceNginx         ServiceType = "nginx"
	ServiceElasticsearch ServiceType = "elasticsearch"
	ServiceKafka         ServiceType = "kafka"
	ServiceEtcd          ServiceType = "etcd"
	ServiceJaeger        ServiceType = "jaeger"
	ServiceNacos         ServiceType = "nacos"
	ServiceRabbitMQ      ServiceType = "rabbitmq"
	ServiceClickHouse    ServiceType = "clickhouse"
	ServiceMinIO         ServiceType = "minio"
	ServiceConsul        ServiceType = "consul"
	ServiceZooKeeper     ServiceType = "zookeeper"
	ServicePrometheus    ServiceType = "prometheus"
	ServiceGrafana       ServiceType = "grafana"
	ServiceInfluxDB      ServiceType = "influxdb"
	ServiceMemcached     ServiceType = "memcached"
	ServiceCassandra     ServiceType = "cassandra"
	ServiceNeo4j         ServiceType = "neo4j"
	ServiceCaddy         ServiceType = "caddy"
	ServiceUnknown       ServiceType = "unknown"
)

// servicePattern 定义镜像名片段到服务类型的映射。
type servicePattern struct {
	keywords []string // 镜像名必须同时包含这些关键词（AND）
	stype    ServiceType
}

// patterns 按优先级排序，越具体的匹配越靠前。
var patterns = []servicePattern{
	{keywords: []string{"elasticsearch"}, stype: ServiceElasticsearch},
	{keywords: []string{"kibana"}, stype: ServiceElasticsearch},
	{keywords: []string{"postgres"}, stype: ServicePostgres},
	{keywords: []string{"mysql"}, stype: ServiceMySQL},
	{keywords: []string{"mariadb"}, stype: ServiceMySQL},
	{keywords: []string{"redis"}, stype: ServiceRedis},
	{keywords: []string{"mongo"}, stype: ServiceMongo},
	{keywords: []string{"nginx"}, stype: ServiceNginx},
	{keywords: []string{"kafka"}, stype: ServiceKafka},
	{keywords: []string{"etcd"}, stype: ServiceEtcd},
	{keywords: []string{"jaeger"}, stype: ServiceJaeger},
	{keywords: []string{"nacos"}, stype: ServiceNacos},
	{keywords: []string{"rabbitmq"}, stype: ServiceRabbitMQ},
	{keywords: []string{"clickhouse"}, stype: ServiceClickHouse},
	{keywords: []string{"minio"}, stype: ServiceMinIO},
	{keywords: []string{"consul"}, stype: ServiceConsul},
	{keywords: []string{"zookeeper"}, stype: ServiceZooKeeper},
	{keywords: []string{"prometheus"}, stype: ServicePrometheus},
	{keywords: []string{"grafana"}, stype: ServiceGrafana},
	{keywords: []string{"influxdb"}, stype: ServiceInfluxDB},
	{keywords: []string{"memcached"}, stype: ServiceMemcached},
	{keywords: []string{"cassandra"}, stype: ServiceCassandra},
	{keywords: []string{"neo4j"}, stype: ServiceNeo4j},
	{keywords: []string{"caddy"}, stype: ServiceCaddy},
}

// DetectServiceType 根据容器镜像名推断服务类型。
// 镜像名通常是 "mysql:8.0" 或 "registry.example.com/postgres:15" 格式。
func DetectServiceType(image string) ServiceType {
	lower := strings.ToLower(image)

	for _, p := range patterns {
		allMatch := true
		for _, kw := range p.keywords {
			if !strings.Contains(lower, kw) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return p.stype
		}
	}

	return ServiceUnknown
}

// ServiceTypeLabel 返回服务类型的中文标签。
func (s ServiceType) Label() string {
	switch s {
	case ServiceMySQL:
		return "MySQL"
	case ServiceRedis:
		return "Redis"
	case ServicePostgres:
		return "PostgreSQL"
	case ServiceMongo:
		return "MongoDB"
	case ServiceNginx:
		return "Nginx"
	case ServiceElasticsearch:
		return "Elasticsearch"
	case ServiceKafka:
		return "Kafka"
	case ServiceEtcd:
		return "Etcd"
	case ServiceJaeger:
		return "Jaeger"
	case ServiceNacos:
		return "Nacos"
	case ServiceRabbitMQ:
		return "RabbitMQ"
	case ServiceClickHouse:
		return "ClickHouse"
	case ServiceMinIO:
		return "MinIO"
	case ServiceConsul:
		return "Consul"
	case ServiceZooKeeper:
		return "ZooKeeper"
	case ServicePrometheus:
		return "Prometheus"
	case ServiceGrafana:
		return "Grafana"
	case ServiceInfluxDB:
		return "InfluxDB"
	case ServiceMemcached:
		return "Memcached"
	case ServiceCassandra:
		return "Cassandra"
	case ServiceNeo4j:
		return "Neo4j"
	case ServiceCaddy:
		return "Caddy"
	default:
		return "未知"
	}
}

// IsDatabase 判断服务类型是否为数据库。
func (s ServiceType) IsDatabase() bool {
	switch s {
	case ServiceMySQL, ServiceRedis, ServicePostgres, ServiceMongo:
		return true
	default:
		return false
	}
}

// IsMiddleware 判断服务类型是否为中间件。
func (s ServiceType) IsMiddleware() bool {
	switch s {
	case ServiceKafka, ServiceEtcd, ServiceRabbitMQ, ServiceNacos:
		return true
	default:
		return false
	}
}
