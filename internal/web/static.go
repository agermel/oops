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
}

type pageUser struct {
	Name string `json:"name"`
}

// spaHandler 处理 SPA 请求：文件优先 → index.html fallback + config 注入 + auth 重定向。
type spaHandler struct {
	staticDir    string
	userStore    *auth.Store
	tokenService *auth.TokenService
	indexHTML    []byte // 缓存的 index.html 原始内容
}

// authProvider 判断当前鉴权模式。
func (h *spaHandler) authProvider() string {
	if h.userStore != nil && len(h.userStore.Users) > 0 {
		return "simple"
	}
	return "none"
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

	// 2. 非文件 → SPA fallback：serve index.html。
	provider := h.authProvider()
	user := h.extractUser(r)

	// 3. Auth 重定向：simple 模式且未登录时。
	if provider == "simple" && user == nil {
		// /login 自己不能重定向，否则死循环。
		if r.URL.Path != "/login" {
			redirectURL := "/login?redirectUrl=" + r.URL.String()
			http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
			return
		}
	}

	// 4. 注入 config__json 到 index.html。
	h.serveIndexWithConfig(w, provider, user)
}

// serveIndexWithConfig 在 index.html 中注入 config__json 后返回。
func (h *spaHandler) serveIndexWithConfig(w http.ResponseWriter, provider string, user *pageUser) {
	if h.indexHTML == nil {
		// 首次读取并缓存原始 index.html。
		indexPath := filepath.Join(h.staticDir, "index.html")
		data, err := os.ReadFile(indexPath)
		if err != nil {
			http.Error(w, "React build is missing. Run: npm --prefix web install && npm --prefix web run build", http.StatusServiceUnavailable)
			return
		}
		h.indexHTML = data
	}

	cfg := pageConfig{AuthProvider: provider, User: user}
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
func MountStatic(mux *http.ServeMux, staticDir string, userStore *auth.Store, tokenService *auth.TokenService) http.Handler {
	absDir, err := filepath.Abs(staticDir)
	if err != nil {
		absDir = staticDir
	}

	spa := &spaHandler{
		staticDir:    absDir,
		userStore:    userStore,
		tokenService: tokenService,
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
