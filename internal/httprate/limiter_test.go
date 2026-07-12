package httprate

import (
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/time/rate"
)

func TestLimiterSeparatesPoliciesForOneClient(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	limiter, err := New(Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer limiter.Close()

	request := httptest.NewRequest("GET", "http://example.test", nil)
	request.RemoteAddr = "203.0.113.8:1234"
	api := Policy{Key: "api", Rate: rate.Limit(1), Burst: 1}
	run := Policy{Key: "run", Rate: rate.Limit(1), Burst: 1}
	if !limiter.Allow(request, api) {
		t.Fatal("first API request rejected")
	}
	if limiter.Allow(request, api) {
		t.Fatal("second API request accepted from exhausted bucket")
	}
	if !limiter.Allow(request, run) {
		t.Fatal("Run policy shared the API bucket")
	}
}

func TestLimiterUsesForwardedClientOnlyFromTrustedProxy(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	direct, err := New(Options{})
	if err != nil {
		t.Fatalf("new direct limiter: %v", err)
	}
	defer direct.Close()
	trusted, err := New(Options{TrustedProxyCIDRs: []string{"10.0.0.0/8"}})
	if err != nil {
		t.Fatalf("new trusted limiter: %v", err)
	}
	defer trusted.Close()

	request := httptest.NewRequest("GET", "http://example.test", nil)
	request.RemoteAddr = "198.51.100.9:443"
	request.Header.Set("X-Forwarded-For", "203.0.113.20, 10.0.0.2")
	if got := direct.ClientIP(request); got != "198.51.100.9" {
		t.Fatalf("direct client IP = %q, want remote address", got)
	}
	request.RemoteAddr = "10.0.0.2:443"
	if got := trusted.ClientIP(request); got != "203.0.113.20" {
		t.Fatalf("trusted client IP = %q, want forwarded client", got)
	}
}

func TestLimiterStripsTrustedForwardedProxiesFromRight(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	limiter, err := New(Options{TrustedProxyCIDRs: []string{"10.0.0.0/8", "192.0.2.0/24"}})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer limiter.Close()

	tests := []struct {
		name      string
		forwarded string
		want      string
	}{
		{
			name:      "multiple trusted proxies",
			forwarded: "203.0.113.20, 192.0.2.4, 10.0.0.2",
			want:      "203.0.113.20",
		},
		{
			name:      "client supplied prefix",
			forwarded: "198.51.100.99, 203.0.113.20, 10.0.0.2",
			want:      "203.0.113.20",
		},
		{
			name:      "invalid nearest hop",
			forwarded: "198.51.100.99, invalid-address",
			want:      "10.0.0.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "http://example.test", nil)
			request.RemoteAddr = "10.0.0.3:443"
			request.Header.Set("X-Forwarded-For", tt.forwarded)
			if got := limiter.ClientIP(request); got != tt.want {
				t.Fatalf("client IP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLimiterPrunesWithInjectedClock(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	limiter, err := New(Options{
		EntryTTL:        time.Minute,
		CleanupInterval: time.Hour,
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer limiter.Close()

	if !limiter.AllowClient("203.0.113.8", Policy{Key: "api", Rate: rate.Limit(1), Burst: 1}) {
		t.Fatal("initial admission rejected")
	}
	now = now.Add(time.Minute)
	limiter.prune()
	limiter.mu.Lock()
	remaining := len(limiter.visitors)
	limiter.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("visitor count = %d, want 0 after TTL", remaining)
	}
}
