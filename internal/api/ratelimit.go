package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// rateLimiter 基于 IP 的令牌桶限流器。
type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newRateLimiter 创建限流器并启动定期清理。
func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
	}
	go rl.cleanup(5 * time.Minute)
	return rl
}

// allow 检查指定 IP 是否允许请求。reqPerSec 为每秒允许的请求数，burst 为突发容量。
func (rl *rateLimiter) allow(ip string, reqPerSec int, burst int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, ok := rl.visitors[ip]
	if !ok {
		limiter := rate.NewLimiter(rate.Limit(reqPerSec), burst)
		rl.visitors[ip] = &visitor{limiter: limiter, lastSeen: time.Now()}
		return limiter.Allow()
	}

	v.lastSeen = time.Now()
	return v.limiter.Allow()
}

// cleanup 定期清理超过 ttl 未访问的 IP 条目。
func (rl *rateLimiter) cleanup(ttl time.Duration) {
	ticker := time.NewTicker(ttl)
	for range ticker.C {
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > ttl {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// defaultLimiter 全局默认限流器。
var defaultLimiter = newRateLimiter()

// extractIP 从请求中提取客户端 IP，优先 X-Forwarded-For（反代场景）。
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}

	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// rateLimit 全局默认限流中间件（200 req/s，突发 400）。
func rateLimit(next http.HandlerFunc) http.HandlerFunc {
	return rateLimitWith(next, 200, 400)
}

// rateLimitChat chat 端点限流（5 req/s，突发 10）。
func rateLimitChat(next http.HandlerFunc) http.HandlerFunc {
	return rateLimitWith(next, 5, 10)
}

// rateLimitWith 创建指定参数的限流中间件。
func rateLimitWith(next http.HandlerFunc, reqPerSec int, burst int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !defaultLimiter.allow(ip, reqPerSec, burst) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
