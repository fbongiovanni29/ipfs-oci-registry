package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/config"
)

// Auth returns middleware that enforces basic authentication.
// Health endpoints (/healthz, /readyz) are excluded from auth.
func Auth(cfg config.AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Skip auth for health endpoints
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}

			user, pass, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+cfg.Realm+`"`)
				http.Error(w, `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`, http.StatusUnauthorized)
				return
			}

			expected, exists := cfg.Users[user]
			if !exists || subtle.ConstantTimeCompare([]byte(pass), []byte(expected)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+cfg.Realm+`"`)
				http.Error(w, `{"errors":[{"code":"UNAUTHORIZED","message":"invalid credentials"}]}`, http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
