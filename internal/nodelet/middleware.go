package nodelet

import (
	"net/http"
	"time"

	"oops/internal/logutil"

	"go.uber.org/zap"
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

// statusRecorder wraps http.ResponseWriter to capture the response status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush 将底层 ResponseWriter 的 Flush 方法透传出去（SSE 需要）。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// requestLogger logs every HTTP request with method, path, status, latency, and client IP.
func (s *Server) requestLogger(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ip := s.clientIP(r)
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

func (s *Server) clientIP(r *http.Request) string {
	if s.limiter == nil {
		return r.RemoteAddr
	}
	return s.limiter.ClientIP(r)
}
