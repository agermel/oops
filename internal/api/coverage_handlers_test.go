package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"oops/internal/auth"
	"oops/internal/nodelet"
	"oops/internal/project"
	runtimestore "oops/internal/store/runtime"
)

type coverageNodeletClient struct {
	*fakeNodeletClient
	containers []nodelet.Container
}

func (c *coverageNodeletClient) Containers(context.Context, string, string) ([]nodelet.Container, error) {
	return c.containers, nil
}

func TestAuthRoutesSetupLoginAndLogout(t *testing.T) {
	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	userStore, err := auth.NewStoreFromRuntime(runtime)
	if err != nil {
		t.Fatalf("open auth store: %v", err)
	}
	server := New(Options{RuntimeStore: runtime, UserStore: userStore, TokenTTL: time.Hour})
	routes := server.Routes()

	status := httptest.NewRecorder()
	routes.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"setup":false`)) {
		t.Fatalf("initial auth status = %d %s", status.Code, status.Body.String())
	}

	setupForm := url.Values{"username": {"admin"}, "name": {"Admin"}, "password": {"test"}}
	setupReq := httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewBufferString(setupForm.Encode()))
	setupReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setupResp := httptest.NewRecorder()
	routes.ServeHTTP(setupResp, setupReq)
	if setupResp.Code != http.StatusOK {
		t.Fatalf("setup status = %d, body = %s", setupResp.Code, setupResp.Body.String())
	}

	badLoginForm := url.Values{"username": {"admin"}, "password": {"wrong"}}
	badLoginReq := httptest.NewRequest(http.MethodPost, "/api/token", bytes.NewBufferString(badLoginForm.Encode()))
	badLoginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badLoginResp := httptest.NewRecorder()
	routes.ServeHTTP(badLoginResp, badLoginReq)
	if badLoginResp.Code != http.StatusUnauthorized {
		t.Fatalf("invalid login status = %d, body = %s", badLoginResp.Code, badLoginResp.Body.String())
	}

	loginForm := url.Values{"username": {"admin"}, "password": {"test"}}
	loginReq := httptest.NewRequest(http.MethodPost, "/api/token", bytes.NewBufferString(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("X-Forwarded-Proto", "https")
	loginResp := httptest.NewRecorder()
	routes.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginResp.Code, loginResp.Body.String())
	}
	loginCookie := responseCookie(t, loginResp, "jwt")
	if !loginCookie.Secure {
		t.Fatal("HTTPS login cookie is not secure")
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(loginCookie)
	meResp := httptest.NewRecorder()
	routes.ServeHTTP(meResp, meReq)
	if meResp.Code != http.StatusOK || !bytes.Contains(meResp.Body.Bytes(), []byte(`"name":"Admin"`)) {
		t.Fatalf("auth me = %d %s", meResp.Code, meResp.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodDelete, "/api/token", nil)
	logoutResp := httptest.NewRecorder()
	routes.ServeHTTP(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("logout status = %d", logoutResp.Code)
	}
	logoutCookie := responseCookie(t, logoutResp, "jwt")
	if logoutCookie.MaxAge >= 0 || logoutCookie.Secure {
		t.Fatalf("logout cookie = %+v", logoutCookie)
	}
}

func TestProjectRoutesManageServersContainersAndExclusions(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)
	projectStore := testProjectStore(t)
	if err := projectStore.Add(project.Project{ID: "project-1", Name: "Project"}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	nodeletManager := testNodeletManager(t)
	if err := nodeletManager.Add(&nodelet.NodeletConfig{
		ID: "nodelet-1", Name: "Nodelet", Address: "http://nodelet", Token: "secret",
	}); err != nil {
		t.Fatalf("seed nodelet: %v", err)
	}
	client := &coverageNodeletClient{
		fakeNodeletClient: &fakeNodeletClient{},
		containers: []nodelet.Container{{
			ID: "container-1", Name: "database", Image: "mysql:8", State: "running",
		}},
	}
	server := New(Options{
		NodeletManager: nodeletManager,
		NodeletClient:  client,
		UserStore:      userStore,
		TokenService:   tokenService,
	})
	server.projectStore = projectStore

	request := func(method, target, body string) *httptest.ResponseRecorder {
		t.Helper()
		var input *bytes.Buffer
		if body != "" {
			input = bytes.NewBufferString(body)
		} else {
			input = bytes.NewBuffer(nil)
		}
		req := httptest.NewRequest(method, target, input)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
		resp := httptest.NewRecorder()
		server.Routes().ServeHTTP(resp, req)
		return resp
	}

	projects := request(http.MethodGet, "/api/projects", "")
	if projects.Code != http.StatusOK || !bytes.Contains(projects.Body.Bytes(), []byte(`"project-1"`)) {
		t.Fatalf("project list = %d %s", projects.Code, projects.Body.String())
	}
	projectGet := request(http.MethodGet, "/api/projects/project-1", "")
	if projectGet.Code != http.StatusOK || !bytes.Contains(projectGet.Body.Bytes(), []byte(`"name":"Project"`)) {
		t.Fatalf("project get = %d %s", projectGet.Code, projectGet.Body.String())
	}

	addServer := request(http.MethodPost, "/api/projects/project-1/servers", `{"nodeletId":"nodelet-1"}`)
	if addServer.Code != http.StatusOK {
		t.Fatalf("add server = %d %s", addServer.Code, addServer.Body.String())
	}
	servers := request(http.MethodGet, "/api/projects/project-1/servers", "")
	var serverList []serverWithNodelet
	if err := json.NewDecoder(servers.Body).Decode(&serverList); err != nil {
		t.Fatalf("decode project servers: %v", err)
	}
	if servers.Code != http.StatusOK || len(serverList) != 1 || serverList[0].Nodelet.ID != "nodelet-1" {
		t.Fatalf("project servers = %d %+v", servers.Code, serverList)
	}

	containers := request(http.MethodGet, "/api/projects/project-1/servers/nodelet-1/containers", "")
	var containerList []containerWithType
	if err := json.NewDecoder(containers.Body).Decode(&containerList); err != nil {
		t.Fatalf("decode project containers: %v", err)
	}
	if containers.Code != http.StatusOK || len(containerList) != 1 || containerList[0].ServiceType != "mysql" {
		t.Fatalf("project containers = %d %+v", containers.Code, containerList)
	}

	exclude := request(http.MethodPost, "/api/projects/project-1/excluded-containers", `{"nodeletId":"nodelet-1","containerId":"container-1"}`)
	if exclude.Code != http.StatusOK {
		t.Fatalf("exclude container = %d %s", exclude.Code, exclude.Body.String())
	}
	include := request(http.MethodDelete, "/api/projects/project-1/excluded-containers?nodeletId=nodelet-1&containerId=container-1", "")
	if include.Code != http.StatusOK {
		t.Fatalf("include container = %d %s", include.Code, include.Body.String())
	}

	removeServer := request(http.MethodDelete, "/api/projects/project-1/servers/nodelet-1", "")
	if removeServer.Code != http.StatusOK {
		t.Fatalf("remove server = %d %s", removeServer.Code, removeServer.Body.String())
	}
	deleteProject := request(http.MethodDelete, "/api/projects/project-1", "")
	if deleteProject.Code != http.StatusOK || projectStore.Get("project-1") != nil {
		t.Fatalf("delete project = %d %s", deleteProject.Code, deleteProject.Body.String())
	}
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response did not set cookie %q", name)
	return nil
}
