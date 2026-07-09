package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	runtimestore "oops/internal/store/runtime"
)

func testRuntimeStore(t *testing.T) *runtimestore.Store {
	t.Helper()
	store, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestAgentSettingsHandlersGetAndUpdate(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)
	server := New(Options{
		RuntimeStore: testRuntimeStore(t),
		UserStore:    userStore,
		TokenService: tokenService,
	})

	getResp := serveAgentSettingsRequest(t, server, jwtToken, http.MethodGet, "/api/agent-settings", "")
	if getResp.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d, body = %s", getResp.Code, http.StatusOK, getResp.Body.String())
	}
	var got agentSettingsResponse
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.MaxTurns != runtimestore.DefaultAgentMaxTurns {
		t.Fatalf("default maxTurns = %d, want %d", got.MaxTurns, runtimestore.DefaultAgentMaxTurns)
	}

	putResp := serveAgentSettingsRequest(t, server, jwtToken, http.MethodPut, "/api/agent-settings", `{"maxTurns":7}`)
	if putResp.Code != http.StatusOK {
		t.Fatalf("put status = %d, want %d, body = %s", putResp.Code, http.StatusOK, putResp.Body.String())
	}

	getResp = serveAgentSettingsRequest(t, server, jwtToken, http.MethodGet, "/api/agent-settings", "")
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decode get after update: %v", err)
	}
	if got.MaxTurns != 7 {
		t.Fatalf("maxTurns = %d, want 7", got.MaxTurns)
	}
}

func TestAgentSettingsHandlersRejectInvalidMaxTurns(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)
	server := New(Options{
		RuntimeStore: testRuntimeStore(t),
		UserStore:    userStore,
		TokenService: tokenService,
	})

	for _, body := range []string{
		`{"maxTurns":0}`,
		`{"maxTurns":-1}`,
		`{"maxTurns":101}`,
		`{"maxTurns":1.5}`,
	} {
		resp := serveAgentSettingsRequest(t, server, jwtToken, http.MethodPut, "/api/agent-settings", body)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want %d, response = %s", body, resp.Code, http.StatusBadRequest, resp.Body.String())
		}
	}
}

func TestAgentSettingsHandlersRequireRuntimeStore(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)
	server := New(Options{
		UserStore:    userStore,
		TokenService: tokenService,
	})

	resp := serveAgentSettingsRequest(t, server, jwtToken, http.MethodGet, "/api/agent-settings", "")
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body = %s", resp.Code, http.StatusInternalServerError, resp.Body.String())
	}
}

func serveAgentSettingsRequest(t *testing.T, server *Server, jwtToken, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
	resp := httptest.NewRecorder()
	server.Routes().ServeHTTP(resp, req)
	return resp
}
