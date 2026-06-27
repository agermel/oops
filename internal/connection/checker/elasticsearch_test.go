package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"oops/internal/connection"
)

// TestElasticsearchCheckerCheckAlive 验证 ES health 接口返回 2xx 时状态为 alive。
func TestElasticsearchCheckerCheckAlive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_cluster/health" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/_cluster/health")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewElasticsearchChecker()
	result := checker.Check(context.Background(), connection.Connection{
		ID:      "es-1",
		Type:    ElasticsearchType,
		Address: server.URL,
	})

	if result.Status != connection.StatusAlive {
		t.Fatalf("Status = %q, want %q", result.Status, connection.StatusAlive)
	}
	if result.ConnectionID != "es-1" {
		t.Fatalf("ConnectionID = %q, want %q", result.ConnectionID, "es-1")
	}
}

// TestElasticsearchCheckerCheckDead 验证 ES health 接口返回非 2xx 时状态为 dead。
func TestElasticsearchCheckerCheckDead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	checker := NewElasticsearchChecker()
	result := checker.Check(context.Background(), connection.Connection{
		ID:      "es-1",
		Type:    ElasticsearchType,
		Address: server.URL,
	})

	if result.Status != connection.StatusDead {
		t.Fatalf("Status = %q, want %q", result.Status, connection.StatusDead)
	}
}
