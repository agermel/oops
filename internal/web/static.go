package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"oops/internal/auth"
)

// pageConfig 注入到 HTML 的页面配置。
type pageConfig struct {
	AuthProvider string     `json:"authProvider"`
	User         *pageUser  `json:"user,omitempty"`
	NeedsSetup   bool       `json:"needsSetup,omitempty"`
}

type pageUser struct {
	Name string `json:"name"`
}

// spaHandler 处理 SPA 请求：文件优先 → index.html fallback + config 注入 + auth 重定向。
type spaHandler struct {
	staticDir    string
	tokenService *auth.TokenService
	userStore    *auth.Store
	indexHTML    []byte // 缓存的 index.html 原始内容
}

// extractUser 从 JWT Cookie 提取用户。
func (h *spaHandler) extractUser(r *http.Request) *pageUser {
	if h.tokenService == nil {
		return nil
	}
	cookie, err := r.Cookie("jwt")
	if err != nil {
		return nil
	}
	claims, err := h.tokenService.VerifyToken(cookie.Value)
	if err != nil {
		return nil
	}
	return &pageUser{Name: claims.Name}
}

// ServeHTTP 实现 http.Handler。
func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. 尝试 serve 真实文件（JS/CSS/图片等静态资源）。
	// 去掉前缀 / 避免 filepath.Join 将路径当作绝对路径而丢弃 staticDir。
	rel := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
	absPath := filepath.Join(h.staticDir, rel)

	// 安全检查：确保路径在 staticDir 下。
	resolved, err := filepath.Abs(absPath)
	if err == nil {
		sep := string(os.PathSeparator)
		if strings.HasPrefix(resolved, h.staticDir+sep) || resolved == h.staticDir {
			if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
				http.ServeFile(w, r, resolved)
				return
			}
		}
	}

	// 2. 确保 index.html 存在（build 产物必须有）。
	if err := h.ensureBuild(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// 3. Auth 重定向：未登录时跳转到 /login 或 /setup。
	needsSetup := h.userStore == nil || !h.userStore.IsSetup()
	user := h.extractUser(r)
	if user == nil {
		if needsSetup {
			// 尚未创建用户 → 重定向到 /setup（排除 /login 和自身避免死循环）。
			if r.URL.Path != "/setup" && r.URL.Path != "/login" {
				http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
				return
			}
		} else {
			// 已设置用户但未登录 → 重定向到 /login。
			if r.URL.Path != "/login" {
				redirectURL := "/login?redirectUrl=" + r.URL.String()
				http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
				return
			}
		}
	}

	// 4. 注入 config__json 到 index.html。
	h.serveIndexWithConfig(w, "simple", user, needsSetup)
}

// ensureBuild 确保前端构建产物存在，首次调用时缓存 index.html。
func (h *spaHandler) ensureBuild() error {
	if h.indexHTML != nil {
		return nil
	}
	indexPath := filepath.Join(h.staticDir, "index.html")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("React build is missing. Run: npm --prefix web install && npm --prefix web run build")
	}
	h.indexHTML = data
	return nil
}

// serveIndexWithConfig 在 index.html 中注入 config__json 后返回。
func (h *spaHandler) serveIndexWithConfig(w http.ResponseWriter, provider string, user *pageUser, needsSetup bool) {
	cfg := pageConfig{AuthProvider: provider, User: user, NeedsSetup: needsSetup}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 在 </head> 前注入 <script id="config__json">。
	scriptTag := fmt.Sprintf("<script id=\"config__json\" type=\"application/json\">%s</script>\n</head>", string(cfgJSON))
	injected := strings.Replace(string(h.indexHTML), "</head>", scriptTag, 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(injected))
}

// MountStatic 返回一个组合 handler：API 路径走 mux，其他路径走 SPA handler。
// 必须在 API 路由已注册到 mux 之后调用。
func MountStatic(mux *http.ServeMux, staticDir string, tokenService *auth.TokenService, userStore *auth.Store) http.Handler {
	absDir, err := filepath.Abs(staticDir)
	if err != nil {
		absDir = staticDir
	}

	spa := &spaHandler{
		staticDir:    absDir,
		tokenService: tokenService,
		userStore:    userStore,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API 路径交给 mux（已注册的具体 API 路由）。
		if strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		// 其他所有路径走 SPA handler（静态文件 + index.html fallback）。
		spa.ServeHTTP(w, r)
	})
}
