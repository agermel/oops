package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"oops/internal/auth"
	"oops/internal/nodelet"

	"golang.org/x/crypto/bcrypt"
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

// InspectContainer 返回测试用容器详细信息。
func (f *fakeNodeletClient) InspectContainer(context.Context, string, string, string) (nodelet.ContainerInspect, error) {
	return nodelet.ContainerInspect{}, nil
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

// TestNodeletRoutes 验证 nodelet 子资源路由匹配（Go 1.22+ 模式匹配）。
func TestNodeletRoutes(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)

	tests := []struct {
		path       string
		wantStatus int
	}{
		{"/api/nodelets/local/containers", http.StatusOK},
		{"/api/nodelets/local/containers/container-1/logs", http.StatusOK},
		{"/api/nodelets/local/containers/container-1/logs/stream", http.StatusOK},
		{"/api/nodelets", http.StatusOK},
	}

	client := &fakeNodeletClient{}
	nm, _ := nodelet.NewNodeletManager("")
	_ = nm.Add(&nodelet.NodeletConfig{ID: "local", Name: "local", Address: "http://nodelet", Token: "secret"})
	server := New(Options{
		NodeletManager: nm,
		NodeletClient:  client,
		UserStore:      userStore,
		TokenService:   tokenService,
	})

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			request.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
			response := httptest.NewRecorder()
			server.Routes().ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Errorf("GET %s: status = %d, want %d", tt.path, response.Code, tt.wantStatus)
			}
		})
	}
}

// testAuthSetup 创建带有一个测试用户的 UserStore 和 TokenService，
// 返回一个已签发的 JWT，可直接设为请求的 Cookie。
func testAuthSetup(t *testing.T) (*auth.Store, *auth.TokenService, string) {
	t.Helper()
	user := &auth.User{Username: "admin", Name: "Admin", Password: hashPassword(t, "test")}
	store := &auth.Store{User: user}
	ts := auth.NewTokenService(user.Password, 24*time.Hour)
	token, err := ts.CreateToken("admin", "Admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return store, ts, token
}

// hashPassword 是 bcrypt 哈希的测试辅助函数。
func hashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return string(hash)
}

// TestHandleNodeletLogsStream 验证中心端透传 Nodelet SSE。
func TestHandleNodeletLogsStream(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)

	client := &fakeNodeletClient{}
	nm, _ := nodelet.NewNodeletManager("")
	_ = nm.Add(&nodelet.NodeletConfig{ID: "local", Name: "local", Address: "http://nodelet", Token: "secret"})
	server := New(Options{
		NodeletManager: nm,
		NodeletClient:  client,
		UserStore:      userStore,
		TokenService:   tokenService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodelets/local/containers/container-1/logs/stream?tail=20", nil)
	request.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
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

// TestRoutesDoesNotServeStatic 验证 API 包不接管前端静态文件。
func TestRoutesDoesNotServeStatic(t *testing.T) {
	server := New(Options{NodeletClient: &fakeNodeletClient{}})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
