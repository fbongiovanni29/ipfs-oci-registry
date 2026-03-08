package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/config"
)

type ipCounter struct {
	count   int
	resetAt time.Time
}

// RateLimiter tracks request counts per IP within a sliding window.
type RateLimiter struct {
	mu       sync.Mutex
	counters map[string]*ipCounter
	limit    int
	burst    int
	window   time.Duration
}

// NewRateLimiter creates a rate limiter from config.
func NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		counters: make(map[string]*ipCounter),
		limit:    cfg.MaxPerMin,
		burst:    cfg.BurstSize,
		window:   time.Minute,
	}

	// Background cleanup of expired entries
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rl.cleanup()
		}
	}()

	return rl
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, c := range rl.counters {
		if now.After(c.resetAt) {
			delete(rl.counters, ip)
		}
	}
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	c, ok := rl.counters[ip]
	if !ok || now.After(c.resetAt) {
		rl.counters[ip] = &ipCounter{count: 1, resetAt: now.Add(rl.window)}
		return true
	}

	c.count++
	// Allow burst above the per-minute limit for short spikes
	if c.count > rl.limit+rl.burst {
		return false
	}
	return true
}

// Middleware returns an HTTP middleware that enforces rate limits.
// Health endpoints are excluded from rate limiting.
func (rl *RateLimiter) Middleware(cfg config.RateLimitConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Skip rate limiting for health endpoints
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}

			ip := extractIP(r)
			if !rl.allow(ip) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, `{"errors":[{"code":"TOOMANYREQUESTS","message":"rate limit exceeded"}]}`, http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func extractIP(r *http.Request) string {
	// Check X-Forwarded-For first (for proxied requests)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (client IP)
		if ip, _, err := net.SplitHostPort(xff); err == nil {
			return ip
		}
		return xff
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
