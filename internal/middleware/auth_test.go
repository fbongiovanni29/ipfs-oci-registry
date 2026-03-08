package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/config"
)

func TestAuthDisabled(t *testing.T) {
	cfg := config.AuthConfig{Enabled: false}
	handler := Auth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthRequired(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Realm:   "test",
		Users:   map[string]string{"admin": "secret"},
	}
	handler := Auth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No credentials
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header")
	}
}

func TestAuthValidCredentials(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Realm:   "test",
		Users:   map[string]string{"admin": "secret"},
	}
	handler := Auth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with valid credentials, got %d", rec.Code)
	}
}

func TestAuthInvalidCredentials(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Realm:   "test",
		Users:   map[string]string{"admin": "secret"},
	}
	handler := Auth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.SetBasicAuth("admin", "wrong")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with bad credentials, got %d", rec.Code)
	}
}

func TestAuthSkipsHealthEndpoints(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Realm:   "test",
		Users:   map[string]string{"admin": "secret"},
	}
	handler := Auth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for %s without credentials, got %d", path, rec.Code)
		}
	}
}
