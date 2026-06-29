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
	handler := MountStatic(mux, staticDir, nil, nil)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))

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
	handler := MountStatic(mux, staticDir, nil, nil)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "<main>app</main>") {
		t.Fatalf("body does not contain expected content: %q", response.Body.String())
	}
}

func TestMountStaticMissingBuild(t *testing.T) {
	mux := http.NewServeMux()
	handler := MountStatic(mux, t.TempDir(), nil, nil)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestMountStaticConfigInjection(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html><head></head><body>ok</body></html>"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	mux := http.NewServeMux()
	handler := MountStatic(mux, staticDir, nil, nil)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `<script id="config__json"`) {
		t.Fatalf("body missing config__json script: %q", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"authProvider":"none"`) {
		t.Fatalf("body missing authProvider: %q", response.Body.String())
	}
}

func TestMountStaticAPIPassthrough(t *testing.T) {
	// API 路由应该被透传到 mux，不经过 SPA handler。
	mux := http.NewServeMux()
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html>spa</html>"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	handler := MountStatic(mux, staticDir, nil, nil)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/test", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if strings.TrimSpace(response.Body.String()) != `{"ok":true}` {
		t.Fatalf("body = %q, want API response", response.Body.String())
	}
}
