package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MountStatic 在生产模式下提供 React 构建产物。
func MountStatic(mux *http.ServeMux, staticDir string) {
	// 解析为绝对路径，防止 filepath.Join 在 Clean 返回绝对路径时丢弃 staticDir。
	absDir, err := filepath.Abs(staticDir)
	if err != nil {
		absDir = staticDir
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 只使用 Clean 后的相对路径片段，与根目录拼接后再一次 Clean 确保结果在根下。
		rel := filepath.Clean(r.URL.Path)
		path := filepath.Join(absDir, rel)

		// 二次验证：确保最终路径确实在静态资源目录之下。
		resolved, err := filepath.Abs(path)
		if err != nil || !strings.HasPrefix(resolved, absDir+string(os.PathSeparator)) && resolved != absDir {
			http.NotFound(w, r)
			return
		}

		if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
			http.ServeFile(w, r, resolved)
			return
		}

		indexPath := filepath.Join(absDir, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			http.Error(w, "React build is missing. Run: npm --prefix web install && npm --prefix web run build", http.StatusServiceUnavailable)
			return
		}
		http.ServeFile(w, r, indexPath)
	})
}
