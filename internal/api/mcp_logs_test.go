package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"oops/internal/mcp"
	runtimestore "oops/internal/store/runtime"
)

func TestHandleMCPLogs(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)

	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	manager, err := mcp.NewManagerWithRuntimeAndConsole(runtime, nil, nil)
	if err != nil {
		t.Fatalf("NewManagerWithRuntime: %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Add(mcp.ConnectionConfig{
		ID:        "conn-1",
		Name:      "Redis",
		Type:      "redis",
		Transport: "stdio",
		Enabled:   false,
		NodeletID: "node-1",
	}); err != nil {
		t.Fatalf("add connection: %v", err)
	}

	server := New(Options{
		UserStore:    userStore,
		TokenService: tokenService,
	})
	server.mcpManager = manager

	request := httptest.NewRequest(http.MethodGet, "/api/mcp/connections/conn-1/logs?tail=20", nil)
	request.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
	response := httptest.NewRecorder()

	server.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var logs []mcp.LogEntry
	if err := json.NewDecoder(response.Body).Decode(&logs); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if len(logs) == 0 {
		t.Fatal("logs are empty")
	}
	if logs[0].ConnectionID != "conn-1" {
		t.Fatalf("connection id = %q, want conn-1", logs[0].ConnectionID)
	}
}

func TestWriteMCPToolTestErrorMapsDrainingToRetryableServiceUnavailable(t *testing.T) {
	response := httptest.NewRecorder()
	writeMCPRuntimeError(response, fmt.Errorf("%w: conn-1", mcp.ErrConnectionDraining))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := response.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}
