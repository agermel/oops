package api

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"strings"
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

// MCPAllowedCommands 返回 MCP stdio 子进程允许的命令列表。
// 可通过环境变量 OOPS_MCP_ALLOWED_COMMANDS 扩展，逗号分隔。
// 默认空列表表示不允许任何命令。
func MCPAllowedCommands() []string {
	base := []string{}
	if extra := os.Getenv("OOPS_MCP_ALLOWED_COMMANDS"); extra != "" {
		for _, cmd := range strings.Split(extra, ",") {
			cmd = strings.TrimSpace(cmd)
			if cmd != "" {
				base = append(base, cmd)
			}
		}
	}
	return base
}

// authorize 是所有 API 路由的 Bearer Token 鉴权中间件。
// 使用 subtle.ConstantTimeCompare 防时序攻击。
func authorize(next http.HandlerFunc) http.HandlerFunc {
	token := APIToken()
	if token == "" {
		return next
	}

	return func(w http.ResponseWriter, r *http.Request) {
		expected := "Bearer " + token
		actual := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
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
	log.Printf("api: %s: %v", context, err)
	msg := http.StatusText(status)
	if msg == "" {
		msg = "internal error"
	}
	http.Error(w, msg, status)
}
