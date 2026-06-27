package web

import (
	"net/http"
	"os"
	"path/filepath"
)

// MountStatic 在生产模式下提供 React 构建产物。
func MountStatic(mux *http.ServeMux, staticDir string) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(staticDir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			http.ServeFile(w, r, path)
			return
		}

		indexPath := filepath.Join(staticDir, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			http.Error(w, "React build is missing. Run: npm --prefix web install && npm --prefix web run build", http.StatusServiceUnavailable)
			return
		}
		http.ServeFile(w, r, indexPath)
	})
}
