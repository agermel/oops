package agent

import (
	"encoding/json"
	"testing"
	"time"
)

// TestContainerJSON 验证容器模型的 JSON 字段稳定。
func TestContainerJSON(t *testing.T) {
	created := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	container := Container{
		ID:        "abc123",
		Name:      "ccnubox-bff",
		Image:     "ccnubox/bff:latest",
		State:     "running",
		HostID:    "prod-api-01",
		Created:   created,
		StartedAt: created,
	}

	data, err := json.Marshal(container)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["hostId"] != "prod-api-01" {
		t.Fatalf("hostId = %v, want %v", decoded["hostId"], "prod-api-01")
	}
	if _, ok := decoded["health"]; ok {
		t.Fatal("health should be omitted when empty")
	}
}

// TestContainerLogsPath 验证容器日志路径会转义容器 ID。
func TestContainerLogsPath(t *testing.T) {
	path := ContainerLogsPath("name/with slash")
	if path != "/containers/name%2Fwith%20slash/logs" {
		t.Fatalf("path = %q", path)
	}
}
