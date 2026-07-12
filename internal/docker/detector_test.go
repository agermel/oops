package docker

import (
	"testing"
)

func TestDetectServiceType(t *testing.T) {
	tests := []struct {
		image string
		want  ServiceType
	}{
		{"mysql:8.0", ServiceMySQL},
		{"mysql:5.7", ServiceMySQL},
		{"mariadb:10.11", ServiceMySQL},
		{"registry.example.com/mysql:8.0", ServiceMySQL},
		{"redis:7-alpine", ServiceRedis},
		{"redis:latest", ServiceRedis},
		{"postgres:15", ServicePostgres},
		{"postgres:14-alpine", ServicePostgres},
		{"mongo:7", ServiceMongo},
		{"mongo:6.0", ServiceMongo},
		{"nginx:1.25", ServiceNginx},
		{"nginx:alpine", ServiceNginx},
		{"elasticsearch:8.11.0", ServiceElasticsearch},
		{"kibana:8.11.0", ServiceElasticsearch},
		{"confluentinc/cp-kafka:7.6.0", ServiceKafka},
		{"bitnami/kafka:3.6", ServiceKafka},
		{"quay.io/coreos/etcd:v3.5", ServiceEtcd},
		{"jaegertracing/all-in-one:1.52", ServiceJaeger},
		{"nacos/nacos-server:v2.3.0", ServiceNacos},
		{"rabbitmq:3.12-management", ServiceRabbitMQ},
		{"ubuntu:22.04", ServiceUnknown},
		{"alpine:latest", ServiceUnknown},
		{"golang:1.22", ServiceUnknown},
		{"my-custom-app:v1", ServiceUnknown},
		{"", ServiceUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := DetectServiceType(tt.image)
			if got != tt.want {
				t.Errorf("DetectServiceType(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}

func TestServiceTypeIsDatabase(t *testing.T) {
	if !ServiceMySQL.IsDatabase() {
		t.Error("MySQL should be a database")
	}
	if !ServiceRedis.IsDatabase() {
		t.Error("Redis should be a database")
	}
	if ServiceNginx.IsDatabase() {
		t.Error("Nginx should not be a database")
	}
}

func TestServiceTypeIsMiddleware(t *testing.T) {
	if !ServiceKafka.IsMiddleware() {
		t.Error("Kafka should be middleware")
	}
	if !ServiceEtcd.IsMiddleware() {
		t.Error("Etcd should be middleware")
	}
	if ServiceMySQL.IsMiddleware() {
		t.Error("MySQL should not be middleware")
	}
}
