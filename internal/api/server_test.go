package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"oops/internal/config"
	"oops/internal/nodelet"
)

type fakeNodeletClient struct {
	streamTail string
}

// Host 返回测试用 Nodelet 主机信息。
func (f *fakeNodeletClient) Host(context.Context, string, string) (nodelet.Host, error) {
	return nodelet.Host{}, nil
}

// Containers 返回测试用容器列表。
func (f *fakeNodeletClient) Containers(context.Context, string, string) ([]nodelet.Container, error) {
	return nil, nil
}

// ContainerLogs 返回测试用历史日志。
func (f *fakeNodeletClient) ContainerLogs(context.Context, string, string, string, string) ([]nodelet.LogEntry, error) {
	return nil, nil
}

// ContainerLogsStream 返回测试用实时日志 SSE。
func (f *fakeNodeletClient) ContainerLogsStream(_ context.Context, _ string, _ string, _ string, tail string) (io.ReadCloser, error) {
	f.streamTail = tail
	return io.NopCloser(strings.NewReader("data: {\"message\":\"started\"}\n\n")), nil
}

// TestSplitNodeletResourcePathContainers 验证中心端容器列表路径解析。
func TestSplitNodeletResourcePathContainers(t *testing.T) {
	nodeletID, containerID, action, ok := splitNodeletResourcePath("/api/nodelets/local/containers")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if nodeletID != "local" {
		t.Fatalf("nodeletID = %q, want %q", nodeletID, "local")
	}
	if containerID != "" {
		t.Fatalf("containerID = %q, want empty", containerID)
	}
	if action != "containers" {
		t.Fatalf("action = %q, want %q", action, "containers")
	}
}

// TestSplitNodeletResourcePathLogs 验证中心端容器日志路径解析。
func TestSplitNodeletResourcePathLogs(t *testing.T) {
	nodeletID, containerID, action, ok := splitNodeletResourcePath("/api/nodelets/local/containers/container-1/logs")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if nodeletID != "local" {
		t.Fatalf("nodeletID = %q, want %q", nodeletID, "local")
	}
	if containerID != "container-1" {
		t.Fatalf("containerID = %q, want %q", containerID, "container-1")
	}
	if action != "logs" {
		t.Fatalf("action = %q, want %q", action, "logs")
	}
}

// TestSplitNodeletResourcePathLogsStream 验证中心端容器日志流路径解析。
func TestSplitNodeletResourcePathLogsStream(t *testing.T) {
	nodeletID, containerID, action, ok := splitNodeletResourcePath("/api/nodelets/local/containers/container-1/logs/stream")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if nodeletID != "local" {
		t.Fatalf("nodeletID = %q, want %q", nodeletID, "local")
	}
	if containerID != "container-1" {
		t.Fatalf("containerID = %q, want %q", containerID, "container-1")
	}
	if action != "logs/stream" {
		t.Fatalf("action = %q, want %q", action, "logs/stream")
	}
}

// TestHandleNodeletLogsStream 验证中心端透传 Nodelet SSE。
func TestHandleNodeletLogsStream(t *testing.T) {
	client := &fakeNodeletClient{}
	server := New(Options{
		Nodelets: []config.NodeletConfig{{
			ID:      "local",
			Address: "http://nodelet",
			Token:   "secret",
		}},
		NodeletClient: client,
		StaticDir:     t.TempDir(),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodelets/local/containers/container-1/logs/stream?tail=20", nil)
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want %q", response.Header().Get("Content-Type"), "text/event-stream")
	}
	if response.Body.String() != "data: {\"message\":\"started\"}\n\n" {
		t.Fatalf("body = %q", response.Body.String())
	}
	if client.streamTail != "20" {
		t.Fatalf("tail = %q, want %q", client.streamTail, "20")
	}
}
