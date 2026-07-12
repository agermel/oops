// Package httprate provides bounded per-client HTTP rate limiting for the API
// and Nodelet servers. It keeps trust-boundary parsing and lifecycle ownership
// beside the existing golang.org/x/time/rate token bucket.
package httprate

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const defaultEntryTTL = 5 * time.Minute

// Policy separates token buckets that share the same client address.
type Policy struct {
	Key   string
	Rate  rate.Limit
	Burst int
}

// Options controls client identity, expiry and clock injection.
type Options struct {
	TrustedProxyCIDRs []string
	EntryTTL          time.Duration
	CleanupInterval   time.Duration
	Now               func() time.Time
}

type visitorKey struct {
	policy string
	client string
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter owns rate bucket expiry and its cleanup worker.
type Limiter struct {
	mu             sync.Mutex
	visitors       map[visitorKey]*visitor
	trustedProxies []netip.Prefix
	entryTTL       time.Duration
	now            func() time.Time
	done           chan struct{}
	workers        sync.WaitGroup
	closeOnce      sync.Once
	closed         bool
}

// New constructs a limiter and validates trusted proxy CIDRs once.
func New(options Options) (*Limiter, error) {
	trustedProxies := make([]netip.Prefix, 0, len(options.TrustedProxyCIDRs))
	for _, raw := range options.TrustedProxyCIDRs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, err
		}
		trustedProxies = append(trustedProxies, prefix.Masked())
	}

	entryTTL := options.EntryTTL
	if entryTTL <= 0 {
		entryTTL = defaultEntryTTL
	}
	cleanupInterval := options.CleanupInterval
	if cleanupInterval <= 0 {
		cleanupInterval = entryTTL
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	limiter := &Limiter{
		visitors:       make(map[visitorKey]*visitor),
		trustedProxies: trustedProxies,
		entryTTL:       entryTTL,
		now:            now,
		done:           make(chan struct{}),
	}
	limiter.workers.Add(1)
	go limiter.cleanup(cleanupInterval)
	return limiter, nil
}

// Allow applies policy to the request's trusted client identity.
func (l *Limiter) Allow(r *http.Request, policy Policy) bool {
	return l.AllowClient(l.ClientIP(r), policy)
}

// AllowClient applies policy to an already-derived client identity.
func (l *Limiter) AllowClient(client string, policy Policy) bool {
	if policy.Key == "" || policy.Burst <= 0 || policy.Rate <= 0 {
		return false
	}
	now := l.now()
	key := visitorKey{policy: policy.Key, client: client}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	entry := l.visitors[key]
	if entry == nil {
		entry = &visitor{limiter: rate.NewLimiter(policy.Rate, policy.Burst)}
		l.visitors[key] = entry
	}
	entry.lastSeen = now
	return entry.limiter.AllowN(now, 1)
}

// ClientIP walks X-Forwarded-For from the trusted connection toward the client.
func (l *Limiter) ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	remote := requestRemoteAddr(r.RemoteAddr)
	if !remote.IsValid() {
		return r.RemoteAddr
	}
	remote = remote.Unmap()
	if !l.isTrustedProxy(remote) {
		return remote.String()
	}

	client := remote
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(forwarded) - 1; i >= 0 && l.isTrustedProxy(client); i-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(forwarded[i]))
		if err != nil {
			return remote.String()
		}
		client = candidate.Unmap()
	}
	return client.String()
}

func requestRemoteAddr(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}
	}
	return addr
}

func (l *Limiter) isTrustedProxy(addr netip.Addr) bool {
	for _, prefix := range l.trustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (l *Limiter) cleanup(interval time.Duration) {
	defer l.workers.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-ticker.C:
			l.prune()
		}
	}
}

func (l *Limiter) prune() {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, entry := range l.visitors {
		if now.Sub(entry.lastSeen) >= l.entryTTL {
			delete(l.visitors, key)
		}
	}
}

// Close stops the cleanup worker and rejects future admissions.
func (l *Limiter) Close() {
	if l == nil {
		return
	}
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		close(l.done)
		l.workers.Wait()
		l.mu.Lock()
		l.visitors = nil
		l.mu.Unlock()
	})
}
