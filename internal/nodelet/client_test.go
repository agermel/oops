package nodelet

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientContainers 验证中心端能调用远端 Nodelet 容器列表接口。
func TestClientContainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ContainersPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, ContainersPath)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer secret")
		}
		_, _ = w.Write([]byte(`[{"id":"container-1","name":"api","image":"api:latest","state":"running","hostId":"host-1","created":"2026-06-27T08:00:00Z","startedAt":"2026-06-27T08:00:00Z"}]`))
	}))
	defer server.Close()

	client := NewClient(server.Client())
	containers, err := client.Containers(context.Background(), server.URL, "secret")
	if err != nil {
		t.Fatalf("Containers() error = %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("len(containers) = %d, want %d", len(containers), 1)
	}
	if containers[0].Name != "api" {
		t.Fatalf("Name = %q, want %q", containers[0].Name, "api")
	}
}

// TestClientContainerLogs 验证中心端能调用远端 Nodelet 容器日志接口。
func TestClientContainerLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ContainerLogsPath("container-1") {
			t.Fatalf("path = %q, want %q", r.URL.Path, ContainerLogsPath("container-1"))
		}
		if r.URL.Query().Get("tail") != "20" {
			t.Fatalf("tail = %q, want %q", r.URL.Query().Get("tail"), "20")
		}
		_, _ = w.Write([]byte(`[{"timestamp":"2026-06-27T08:00:00Z","containerId":"container-1","stream":"stdout","message":"started"}]`))
	}))
	defer server.Close()

	client := NewClient(server.Client())
	logs, err := client.ContainerLogs(context.Background(), server.URL, "", "container-1", "20")
	if err != nil {
		t.Fatalf("ContainerLogs() error = %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("len(logs) = %d, want %d", len(logs), 1)
	}
	if logs[0].Message != "started" {
		t.Fatalf("Message = %q, want %q", logs[0].Message, "started")
	}
}

// TestClientContainerLogsStream 验证中心端能调用远端 Nodelet 容器日志 SSE 接口。
func TestClientContainerLogsStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ContainerLogsStreamPath("container-1") {
			t.Fatalf("path = %q, want %q", r.URL.Path, ContainerLogsStreamPath("container-1"))
		}
		if r.URL.Query().Get("tail") != "20" {
			t.Fatalf("tail = %q, want %q", r.URL.Query().Get("tail"), "20")
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer secret")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"message\":\"started\"}\n\n"))
	}))
	defer server.Close()

	client := NewClient(server.Client())
	stream, err := client.ContainerLogsStream(context.Background(), server.URL, "secret", "container-1", "20")
	if err != nil {
		t.Fatalf("ContainerLogsStream() error = %v", err)
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "data: {\"message\":\"started\"}\n\n" {
		t.Fatalf("body = %q", string(data))
	}
}
