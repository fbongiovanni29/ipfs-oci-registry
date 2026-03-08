package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/config"
)

func TestRateLimiterAllows(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:   true,
		MaxPerMin: 10,
		BurstSize: 5,
	}
	rl := NewRateLimiter(cfg)

	handler := rl.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request should succeed
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRateLimiterBlocks(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:   true,
		MaxPerMin: 5,
		BurstSize: 0,
	}
	rl := NewRateLimiter(cfg)

	handler := rl.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust the limit
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i < 5 && rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
		}
		if i == 5 && rec.Code != http.StatusTooManyRequests {
			t.Errorf("request %d: expected 429, got %d", i, rec.Code)
		}
	}
}

func TestRateLimiterPerIP(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:   true,
		MaxPerMin: 2,
		BurstSize: 0,
	}
	rl := NewRateLimiter(cfg)

	handler := rl.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust limit for IP 1
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// IP 2 should still work
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.RemoteAddr = "192.168.1.2:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("different IP should not be rate limited, got %d", rec.Code)
	}
}

func TestRateLimiterSkipsHealth(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:   true,
		MaxPerMin: 1,
		BurstSize: 0,
	}
	rl := NewRateLimiter(cfg)

	handler := rl.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust the limit
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Now rate limited for /v2/ but health should still work
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("healthz should not be rate limited, got %d", rec.Code)
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	cfg := config.RateLimitConfig{Enabled: false}
	rl := NewRateLimiter(cfg)

	handler := rl.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when disabled, got %d", rec.Code)
	}
}
