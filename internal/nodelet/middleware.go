package nodelet

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"oops/internal/logutil"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// securityHeaders 为所有 Nodelet HTTP 响应添加安全相关头。
func securityHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		next(w, r)
	}
}

// ---- rate limiting ----

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	done     chan struct{}
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		visitors: make(map[string]*visitor),
		done:     make(chan struct{}),
	}
}

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

func (rl *rateLimiter) cleanup(ttl time.Duration) {
	ticker := time.NewTicker(ttl)
	defer ticker.Stop()
	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				if time.Since(v.lastSeen) > ttl {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

func (rl *rateLimiter) shutdown() {
	close(rl.done)
}

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

// statusRecorder wraps http.ResponseWriter to capture the response status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// requestLogger logs every HTTP request with method, path, status, latency, and remote IP.
func requestLogger(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ip := extractIP(r)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next(rec, r)

		latency := time.Since(start).Milliseconds()
		logutil.Info("nodelet: request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", rec.status),
			zap.Int64("latencyMs", latency),
			zap.String("ip", ip),
		)
	}
}

