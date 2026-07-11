package api

import (
	"net/http"
	"sync"
	"time"

	"oops/internal/httprate"

	"golang.org/x/time/rate"
)

var (
	apiRatePolicy = httprate.Policy{Key: "api", Rate: rate.Limit(200), Burst: 400}
	runRatePolicy = httprate.Policy{Key: "run", Rate: rate.Limit(5), Burst: 10}
)

func (s *Server) rateLimit(next http.HandlerFunc) http.HandlerFunc {
	return s.rateLimitWith(next, apiRatePolicy)
}

func (s *Server) rateLimitRun(next http.HandlerFunc) http.HandlerFunc {
	return s.rateLimitWith(next, runRatePolicy)
}

func (s *Server) rateLimitWith(next http.HandlerFunc, policy httprate.Policy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.httpRateLimiter != nil && !s.httpRateLimiter.Allow(r, policy) {
			w.Header().Set("Retry-After", "1")
			writeJSONError(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func (s *Server) clientIP(r *http.Request) string {
	if s.httpRateLimiter == nil {
		return r.RemoteAddr
	}
	return s.httpRateLimiter.ClientIP(r)
}

// loginLimiter keeps failed-login windows local to one API server. Its entries
// are pruned on access, so it has no independent goroutine to own.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginEntry
	now      func() time.Time
}

type loginEntry struct {
	failures  int
	blockedAt time.Time
	lastFail  time.Time
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{attempts: make(map[string]*loginEntry), now: now}
}

func (l *loginLimiter) allow(key string) bool {
	if l == nil {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	entry := l.attempts[key]
	if entry == nil || entry.blockedAt.IsZero() {
		return true
	}
	return now.Sub(entry.blockedAt) >= 15*time.Minute
}

func (l *loginLimiter) recordFail(key string) {
	if l == nil {
		return
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	entry := l.attempts[key]
	if entry == nil || now.Sub(entry.lastFail) > time.Minute {
		entry = &loginEntry{}
		l.attempts[key] = entry
	}
	entry.failures++
	entry.lastFail = now
	if entry.failures >= 5 {
		entry.blockedAt = now
	}
}

func (l *loginLimiter) recordSuccess(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

func (l *loginLimiter) pruneLocked(now time.Time) {
	for key, entry := range l.attempts {
		if !entry.blockedAt.IsZero() && now.Sub(entry.blockedAt) >= 15*time.Minute {
			delete(l.attempts, key)
			continue
		}
		if entry.blockedAt.IsZero() && now.Sub(entry.lastFail) > time.Minute {
			delete(l.attempts, key)
		}
	}
}

func (l *loginLimiter) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.attempts = nil
	l.mu.Unlock()
}

func (s *Server) loginLimitKey(username string, r *http.Request) string {
	return s.clientIP(r) + ":" + username
}
