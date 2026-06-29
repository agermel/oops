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
// 当 OOPS_REQUIRE_AUTH=true 时，若 OOPS_TOKEN 为空则拒绝所有请求。
// 同时支持 ?token= 查询参数（供 EventSource 等无法自定义 header 的场景）。
func authorize(next http.HandlerFunc) http.HandlerFunc {
	token := APIToken()
	if token == "" {
		if os.Getenv("OOPS_REQUIRE_AUTH") == "true" {
			return func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "auth required but OOPS_TOKEN not set", http.StatusServiceUnavailable)
			}
		}
		return next
	}

	expected := "Bearer " + token

	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")

		// 允许通过 ?token= 查询参数传递 token（供 EventSource 等无法自定义 header 的场景）。
		if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) != 1 {
			if subtle.ConstantTimeCompare([]byte("Bearer "+r.URL.Query().Get("token")), []byte(expected)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeJSONError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
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
	behindProxy := os.Getenv("OOPS_BEHIND_PROXY") == "true"
	corsOrigin := os.Getenv("OOPS_CORS_ORIGIN")

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'")

		if behindProxy {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		if corsOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
		}

		// CORS 预检请求直接返回
		if r.Method == http.MethodOptions && corsOrigin != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

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
