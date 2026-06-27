package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMountStaticServesAsset(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "app.js"), []byte("console.log('ok')"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	mux := http.NewServeMux()
	MountStatic(mux, staticDir)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if strings.TrimSpace(response.Body.String()) != "console.log('ok')" {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestMountStaticFallsBackToIndex(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<main>app</main>"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	mux := http.NewServeMux()
	MountStatic(mux, staticDir)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if strings.TrimSpace(response.Body.String()) != "<main>app</main>" {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestMountStaticMissingBuild(t *testing.T) {
	mux := http.NewServeMux()
	MountStatic(mux, t.TempDir())

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
