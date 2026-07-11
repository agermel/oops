package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"oops/internal/httprate"

	"golang.org/x/time/rate"
)

func TestServerRatePoliciesStayIsolatedForOneClient(t *testing.T) {
	limiter, err := httprate.New(httprate.Options{})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer limiter.Close()
	server := &Server{httpRateLimiter: limiter}
	policyA := httprate.Policy{Key: "api-test", Rate: rate.Limit(1), Burst: 1}
	policyB := httprate.Policy{Key: "run-test", Rate: rate.Limit(1), Burst: 1}
	handlerA := server.rateLimitWith(func(http.ResponseWriter, *http.Request) {}, policyA)
	handlerB := server.rateLimitWith(func(http.ResponseWriter, *http.Request) {}, policyB)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.8:4444"

	first := httptest.NewRecorder()
	handlerA(first, request)
	if first.Code != http.StatusOK {
		t.Fatalf("first policy A status = %d, want 200", first.Code)
	}
	second := httptest.NewRecorder()
	handlerA(second, request)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second policy A status = %d, want 429", second.Code)
	}
	other := httptest.NewRecorder()
	handlerB(other, request)
	if other.Code != http.StatusOK {
		t.Fatalf("policy B status = %d, want 200", other.Code)
	}
}

func TestServerClientIPUsesForwardedAddressOnlyForTrustedProxy(t *testing.T) {
	limiter, err := httprate.New(httprate.Options{TrustedProxyCIDRs: []string{"10.0.0.0/8"}})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer limiter.Close()
	server := &Server{httpRateLimiter: limiter}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Forwarded-For", "203.0.113.3")
	request.RemoteAddr = "198.51.100.7:443"
	if got := server.clientIP(request); got != "198.51.100.7" {
		t.Fatalf("untrusted client IP = %q", got)
	}
	request.RemoteAddr = "10.0.0.7:443"
	if got := server.clientIP(request); got != "203.0.113.3" {
		t.Fatalf("trusted client IP = %q", got)
	}
}

func TestLoginLimiterExpiresWithInjectedClock(t *testing.T) {
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	limiter := newLoginLimiter(func() time.Time { return now })
	for range 5 {
		limiter.recordFail("admin:203.0.113.8")
	}
	if limiter.allow("admin:203.0.113.8") {
		t.Fatal("blocked login is allowed")
	}
	now = now.Add(15 * time.Minute)
	if !limiter.allow("admin:203.0.113.8") {
		t.Fatal("expired login block remains active")
	}
	limiter.Close()
}
