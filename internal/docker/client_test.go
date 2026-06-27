package docker

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

type fakeAPI struct {
	info       system.Info
	version    client.ServerVersionResult
	containers []containertypes.Summary
	logs       string
	err        error
}

// ContainerList 返回测试用容器列表。
func (f fakeAPI) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	return client.ContainerListResult{Items: f.containers}, f.err
}

// ContainerLogs 返回测试用容器日志流。
func (f fakeAPI) ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
	return io.NopCloser(strings.NewReader(f.logs)), f.err
}

// Info 返回测试用 Docker daemon 信息。
func (f fakeAPI) Info(context.Context, client.InfoOptions) (client.SystemInfoResult, error) {
	return client.SystemInfoResult{Info: f.info}, f.err
}

// Ping 返回测试用 Docker daemon 连通性。
func (f fakeAPI) Ping(context.Context, client.PingOptions) (client.PingResult, error) {
	return client.PingResult{}, f.err
}

// ServerVersion 返回测试用 Docker daemon 版本信息。
func (f fakeAPI) ServerVersion(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error) {
	return f.version, f.err
}

// TestHost 验证 Docker 信息能转换成 Agent Host。
func TestHost(t *testing.T) {
	api := fakeAPI{info: system.Info{
		ID:            "docker-host-id",
		Name:          "prod-api-01",
		NCPU:          4,
		MemTotal:      8 << 30,
		ServerVersion: "28.0.0",
	}}
	client := NewClientWithAPI(api, "http://127.0.0.1:8686")

	host, err := client.Host(httptest.NewRequest("GET", "/host", nil))
	if err != nil {
		t.Fatalf("Host() error = %v", err)
	}
	if host.ID != "docker-host-id" {
		t.Fatalf("ID = %q, want %q", host.ID, "docker-host-id")
	}
	if host.Runtime != "docker" {
		t.Fatalf("Runtime = %q, want %q", host.Runtime, "docker")
	}
	if !host.Available {
		t.Fatal("Available = false, want true")
	}
}

// TestContainers 验证 Docker 容器列表能转换成 Agent Container。
func TestContainers(t *testing.T) {
	api := fakeAPI{
		info: system.Info{
			ID:   "docker-host-id",
			Name: "prod-api-01",
		},
		containers: []containertypes.Summary{
			{
				ID:      "container-1",
				Names:   []string{"/api"},
				Image:   "ccnubox/api:latest",
				State:   containertypes.StateRunning,
				Created: 1710000000,
				Health:  &containertypes.HealthSummary{Status: containertypes.Healthy},
			},
		},
	}
	client := NewClientWithAPI(api, "http://127.0.0.1:8686")

	containers, err := client.Containers(httptest.NewRequest("GET", "/containers", nil))
	if err != nil {
		t.Fatalf("Containers() error = %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("len(containers) = %d, want %d", len(containers), 1)
	}
	if containers[0].Name != "api" {
		t.Fatalf("Name = %q, want %q", containers[0].Name, "api")
	}
	if containers[0].HostID != "docker-host-id" {
		t.Fatalf("HostID = %q, want %q", containers[0].HostID, "docker-host-id")
	}
	if containers[0].Health != string(containertypes.Healthy) {
		t.Fatalf("Health = %q, want %q", containers[0].Health, containertypes.Healthy)
	}
	if !containers[0].Created.Equal(time.Unix(1710000000, 0)) {
		t.Fatalf("Created = %s, want %s", containers[0].Created, time.Unix(1710000000, 0))
	}
}

// TestContainerLogs 验证 Docker 容器日志能转换成 Agent LogEntry。
func TestContainerLogs(t *testing.T) {
	api := fakeAPI{
		info: system.Info{ID: "docker-host-id"},
		logs: "2026-06-27T08:00:00Z app started\n",
	}
	client := NewClientWithAPI(api, "http://127.0.0.1:8686")

	logs, err := client.ContainerLogs(httptest.NewRequest("GET", "/containers/container-1/logs?tail=20", nil), "container-1")
	if err != nil {
		t.Fatalf("ContainerLogs() error = %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("len(logs) = %d, want %d", len(logs), 1)
	}
	if logs[0].Message != "app started" {
		t.Fatalf("Message = %q, want %q", logs[0].Message, "app started")
	}
	if logs[0].Stream != "stdout" {
		t.Fatalf("Stream = %q, want %q", logs[0].Stream, "stdout")
	}
}

// TestHostPingError 验证 Docker daemon 不可达时返回错误。
func TestHostPingError(t *testing.T) {
	client := NewClientWithAPI(fakeAPI{err: errors.New("docker unavailable")}, "http://127.0.0.1:8686")

	_, err := client.Host(httptest.NewRequest("GET", "/host", nil))
	if err == nil {
		t.Fatal("Host() error is nil, want error")
	}
}
