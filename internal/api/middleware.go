package api

import (
	"crypto/subtle"
	"net/http"
	"os"

	"oops/internal/logutil"
	"go.uber.org/zap"
)

const (
	// MaxRequestBodySize 限制 POST/PUT 请求体大小为 1 MiB。
	MaxRequestBodySize = 1 << 20
)

// APIToken 从环境变量读取 ops-plane 的 Bearer Token。
// 为空时跳过鉴权（开发环境向后兼容）。
func APIToken() string {
	return os.Getenv("OOPS_TOKEN")
}

// authorize 是所有 API 路由的 Bearer Token 鉴权中间件。
// 使用 subtle.ConstantTimeCompare 防时序攻击。
// 同时支持 ?token= 查询参数（供 EventSource/WebSocket 等无法自定义 header 的场景）。
func authorize(next http.HandlerFunc) http.HandlerFunc {
	token := APIToken()
	if token == "" {
		return next
	}

	expected := "Bearer " + token

	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")

		// SSE / WebSocket 等客户端无法设置 Authorization header，
		// 允许通过 ?token= 查询参数传入。
		if auth == "" {
			if qt := r.URL.Query().Get("token"); qt != "" {
				auth = "Bearer " + qt
			}
		}

		if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSONError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// limitBody 限制 POST/PUT 请求体大小，防止内存耗尽。
func limitBody(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
		}
		next(w, r)
	}
}

// securityHeaders 为所有 HTTP 响应添加安全相关头。
func securityHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next(w, r)
	}
}

// sanitizedError 记录真实错误到日志，返回脱敏后的 HTTP 错误响应。
func sanitizedError(w http.ResponseWriter, context string, err error, status int) {
	logutil.Error("api: "+context, zap.Error(err))
	msg := http.StatusText(status)
	if msg == "" {
		msg = "internal error"
	}
	http.Error(w, msg, status)
}
