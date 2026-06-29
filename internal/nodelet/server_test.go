package nodelet

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeHostProvider struct {
	host       Host
	containers []Container
	inspect    ContainerInspect
	logs       []LogEntry
	streamLogs []LogEntry
	err        error
}

// Host 返回测试用机器信息。
func (f fakeHostProvider) Host(_ *http.Request) (Host, error) {
	return f.host, f.err
}

// Containers 返回测试用容器列表。
func (f fakeHostProvider) Containers(_ *http.Request) ([]Container, error) {
	return f.containers, f.err
}

// ContainerInspect 返回测试用容器详细信息。
func (f fakeHostProvider) ContainerInspect(_ *http.Request, _ string) (ContainerInspect, error) {
	return f.inspect, f.err
}

// ContainerLogs 返回测试用容器日志。
func (f fakeHostProvider) ContainerLogs(_ *http.Request, _ string) ([]LogEntry, error) {
	return f.logs, f.err
}

// ContainerLogsStream 返回测试用容器日志流。
func (f fakeHostProvider) ContainerLogsStream(_ *http.Request, _ string) (<-chan LogEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	logs := make(chan LogEntry, len(f.streamLogs))
	for _, entry := range f.streamLogs {
		logs <- entry
	}
	close(logs)
	return logs, nil
}

// TestServerHealth 验证 Nodelet 存活接口。
func TestServerHealth(t *testing.T) {
	server := NewServer(fakeHostProvider{})
	request := httptest.NewRequest(http.MethodGet, HealthPath, nil)
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body = %q", response.Body.String())
	}
}

// TestServerHost 验证 Nodelet 机器信息接口需要鉴权。
func TestServerHost(t *testing.T) {
	server := NewServerWithToken(fakeHostProvider{host: Host{
		ID:        "host-1",
		Name:      "prod-api-01",
		Available: true,
	}}, "secret")
	request := httptest.NewRequest(http.MethodGet, HostPath, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

// TestServerHostWithToken 验证 Nodelet 数据接口接受正确 Token。
func TestServerHostWithToken(t *testing.T) {
	server := NewServerWithToken(fakeHostProvider{host: Host{
		ID:        "host-1",
		Name:      "prod-api-01",
		Available: true,
	}}, "secret")
	request := httptest.NewRequest(http.MethodGet, HostPath, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

// TestServerHostUnauthorized 验证 Nodelet 数据接口拒绝错误 Token。
func TestServerHostUnauthorized(t *testing.T) {
	server := NewServerWithToken(fakeHostProvider{}, "secret")
	request := httptest.NewRequest(http.MethodGet, HostPath, nil)
	request.Header.Set("Authorization", "Bearer wrong")
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

// TestServerHostNoTokenConfigured 验证未配置 Token 时受保护接口返回 503。
func TestServerHostNoTokenConfigured(t *testing.T) {
	server := NewServer(fakeHostProvider{host: Host{
		ID:   "host-1",
		Name: "prod-api-01",
	}})
	request := httptest.NewRequest(http.MethodGet, HostPath, nil)
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

// TestServerHealthWithoutToken 验证 Nodelet 存活接口无需 Token。
func TestServerHealthWithoutToken(t *testing.T) {
	server := NewServerWithToken(fakeHostProvider{}, "secret")
	request := httptest.NewRequest(http.MethodGet, HealthPath, nil)
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

// TestServerContainers 验证 Nodelet 容器列表接口。
func TestServerContainers(t *testing.T) {
	server := NewServerWithToken(fakeHostProvider{containers: []Container{
		{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "api"},
	}}, "secret")
	request := httptest.NewRequest(http.MethodGet, ContainersPath, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

// TestServerContainersUnavailable 验证容器列表读取失败时返回 503。
func TestServerContainersUnavailable(t *testing.T) {
	server := NewServerWithToken(fakeHostProvider{err: errors.New("docker unavailable")}, "secret")
	request := httptest.NewRequest(http.MethodGet, ContainersPath, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

// TestServerContainerLogs 验证 Nodelet 容器日志接口。
func TestServerContainerLogs(t *testing.T) {
	server := NewServerWithToken(fakeHostProvider{logs: []LogEntry{
		{ContainerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Stream: "stdout", Message: "started"},
	}}, "secret")
	request := httptest.NewRequest(http.MethodGet, ContainerLogsPath("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

// TestServerContainerLogsStream 验证 Nodelet 容器日志 SSE 接口。
func TestServerContainerLogsStream(t *testing.T) {
	server := NewServerWithToken(fakeHostProvider{streamLogs: []LogEntry{
		{ContainerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Stream: "stdout", Message: "started"},
	}}, "secret")
	request := httptest.NewRequest(http.MethodGet, ContainerLogsStreamPath("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want %q", response.Header().Get("Content-Type"), "text/event-stream")
	}
	if !strings.Contains(response.Body.String(), `data: {"timestamp":"0001-01-01T00:00:00Z","containerId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stream":"stdout","message":"started"}`) {
		t.Fatalf("body = %q", response.Body.String())
	}
}

// TestServerHostUnavailable 验证机器信息读取失败时返回 503。
func TestServerHostUnavailable(t *testing.T) {
	server := NewServerWithToken(fakeHostProvider{err: errors.New("docker unavailable")}, "secret")
	request := httptest.NewRequest(http.MethodGet, HostPath, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

// TestPathTraversalRejection 验证路径穿越 payload 被拒绝。
func TestPathTraversalRejection(t *testing.T) {
	payloads := []struct {
		name string
		path string
	}{
		{"double dot", "/containers/../../etc/passwd/logs"},
		{"percent-encoded double dot", "/containers/%2e%2e/%2e%2e/etc%2fpasswd/logs"},
		{"mixed encoding", "/containers/foo%2f..%2fbar/logs"},
		{"non-hex container ID", "/containers/../../../root/logs"},
	}

	for _, tc := range payloads {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ok := splitContainerPath(tc.path)
			if ok {
				t.Fatalf("path %q should be rejected", tc.path)
			}
		})
	}
}
