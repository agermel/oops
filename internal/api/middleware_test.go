package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersAllowPatchForCORSPreflight(t *testing.T) {
	t.Setenv("OOPS_CORS_ORIGIN", "https://ops.example.test")

	handler := securityHeaders(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	request := httptest.NewRequest(http.MethodOptions, "/api/sessions/session-1", nil)
	response := httptest.NewRecorder()

	handler(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, PATCH, DELETE" {
		t.Fatalf("Access-Control-Allow-Methods = %q, want PATCH included", got)
	}
}
