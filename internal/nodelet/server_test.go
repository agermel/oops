package nodelet

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeHostProvider struct {
	host       Host
	containers []Container
	logs       []LogEntry
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

// ContainerLogs 返回测试用容器日志。
func (f fakeHostProvider) ContainerLogs(_ *http.Request, _ string) ([]LogEntry, error) {
	return f.logs, f.err
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

// TestServerHost 验证 Nodelet 机器信息接口。
func TestServerHost(t *testing.T) {
	server := NewServer(fakeHostProvider{host: Host{
		ID:        "host-1",
		Name:      "prod-api-01",
		Available: true,
	}})
	request := httptest.NewRequest(http.MethodGet, HostPath, nil)
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
	server := NewServer(fakeHostProvider{containers: []Container{
		{ID: "container-1", Name: "api"},
	}})
	request := httptest.NewRequest(http.MethodGet, ContainersPath, nil)
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

// TestServerContainersUnavailable 验证容器列表读取失败时返回 503。
func TestServerContainersUnavailable(t *testing.T) {
	server := NewServer(fakeHostProvider{err: errors.New("docker unavailable")})
	request := httptest.NewRequest(http.MethodGet, ContainersPath, nil)
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

// TestServerContainerLogs 验证 Nodelet 容器日志接口。
func TestServerContainerLogs(t *testing.T) {
	server := NewServer(fakeHostProvider{logs: []LogEntry{
		{ContainerID: "container-1", Stream: "stdout", Message: "started"},
	}})
	request := httptest.NewRequest(http.MethodGet, ContainerLogsPath("container-1"), nil)
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

// TestServerHostUnavailable 验证机器信息读取失败时返回 503。
func TestServerHostUnavailable(t *testing.T) {
	server := NewServer(fakeHostProvider{err: errors.New("docker unavailable")})
	request := httptest.NewRequest(http.MethodGet, HostPath, nil)
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
