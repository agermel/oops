package api

import (
	"net/http"
	"os"

	"go.uber.org/zap"
	"oops/internal/logutil"
)

const (
	// MaxRequestBodySize 限制 POST/PUT 请求体大小为 1 MiB。
	MaxRequestBodySize = 1 << 20
)

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
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
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
	writeJSONError(w, msg, status)
}
