package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oops/internal/auth"
	"oops/internal/nodelet"
	"oops/internal/project"
	runtimestore "oops/internal/store/runtime"

)

type fakeNodeletClient struct {
	streamTail string
}

func testNodeletManager(t *testing.T) *nodelet.NodeletManager {
	t.Helper()
	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
	})
	nm, err := nodelet.NewNodeletManagerWithRuntime(runtime)
	if err != nil {
		t.Fatalf("NewNodeletManagerWithRuntime: %v", err)
	}
	return nm
}

func testProjectStore(t *testing.T) *project.Store {
	t.Helper()
	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
	})
	store, err := project.NewStore(runtime)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
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

// ContainerExec 返回测试用容器 exec 结果。
func (f *fakeNodeletClient) ContainerExec(context.Context, string, string, string, []string) (nodelet.ExecResult, error) {
	return nodelet.ExecResult{ExitCode: 0}, nil
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
	nm := testNodeletManager(t)
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
	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
	})
	store, err := auth.NewStoreFromRuntime(runtime)
	if err != nil {
		t.Fatalf("new auth store: %v", err)
	}
	if err := store.Setup("admin", "Admin", "test"); err != nil {
		t.Fatalf("setup auth store: %v", err)
	}
	ts := auth.NewTokenService(store.User.Password, 24*time.Hour)
	token, err := ts.CreateToken("admin", "Admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return store, ts, token
}

// TestHandleNodeletLogsStream 验证中心端透传 Nodelet SSE。
func TestHandleNodeletLogsStream(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)

	client := &fakeNodeletClient{}
	nm := testNodeletManager(t)
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

func TestHandleProjectCreateGeneratesID(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)

	projectStore := testProjectStore(t)
	server := New(Options{
		UserStore:    userStore,
		TokenService: tokenService,
	})
	server.projectStore = projectStore

	request := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(`{"name":"CCNU Box"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	var project project.Project
	if err := json.NewDecoder(response.Body).Decode(&project); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if project.ID == "" {
		t.Fatal("generated id is empty")
	}
	if project.Name != "CCNU Box" {
		t.Fatalf("name = %q, want %q", project.Name, "CCNU Box")
	}
	if got := projectStore.Get(project.ID); got == nil {
		t.Fatalf("project %q missing from store", project.ID)
	}
}

func TestHandleProjectCreateRejectsDuplicateName(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)

	projectStore := testProjectStore(t)
	if err := projectStore.Add(project.Project{ID: "project-1", Name: "CCNU Box"}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	server := New(Options{
		UserStore:    userStore,
		TokenService: tokenService,
	})
	server.projectStore = projectStore

	request := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(`{"name":"CCNU Box"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "already exists") {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestHandleProjectUpdateCollectionPresence(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)
	projectStore := testProjectStore(t)
	if err := projectStore.Add(project.Project{
		ID:                    "project-1",
		Name:                  "Project",
		NodeletIDs:            []string{"nodelet-1", "nodelet-2"},
		ExcludedContainerRefs: []string{"nodelet-1/container-1"},
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	original := projectStore.Get("project-1")
	server := New(Options{UserStore: userStore, TokenService: tokenService})
	server.projectStore = projectStore

	update := func(body string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, "/api/projects/project-1", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
		response := httptest.NewRecorder()
		server.Routes().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("PUT body=%s: status=%d body=%s", body, response.Code, response.Body.String())
		}
	}

	update(`{"name":"Omitted"}`)
	if got := projectStore.Get("project-1"); len(got.NodeletIDs) != 2 || len(got.ExcludedContainerRefs) != 1 {
		t.Fatalf("omitted collections changed: %+v", got)
	}

	update(`{"name":"Null","nodeletIds":null,"excludedContainerRefs":null}`)
	if got := projectStore.Get("project-1"); len(got.NodeletIDs) != 2 || len(got.ExcludedContainerRefs) != 1 {
		t.Fatalf("null collections changed: %+v", got)
	}

	update(`{"name":"Empty","nodeletIds":[],"excludedContainerRefs":[]}`)
	if got := projectStore.Get("project-1"); len(got.NodeletIDs) != 0 || len(got.ExcludedContainerRefs) != 0 {
		t.Fatalf("empty collections were not applied: %+v", got)
	}

	update(`{"name":"Arrays","nodeletIds":["nodelet-3"],"excludedContainerRefs":["nodelet-3/container-3"],"createdAt":"1970-01-01T00:00:00Z","updatedAt":"1970-01-01T00:00:00Z"}`)
	got := projectStore.Get("project-1")
	if len(got.NodeletIDs) != 1 || got.NodeletIDs[0] != "nodelet-3" || len(got.ExcludedContainerRefs) != 1 || got.ExcludedContainerRefs[0] != "nodelet-3/container-3" {
		t.Fatalf("arrays were not applied: %+v", got)
	}
	if !got.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("client createdAt replaced server time: %s -> %s", original.CreatedAt, got.CreatedAt)
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
