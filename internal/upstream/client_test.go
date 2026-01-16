package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/containerish/ipfs-oci-registry/internal/config"
	"github.com/rs/zerolog"
)

func TestNewClient(t *testing.T) {
	configs := map[string]config.UpstreamConfig{
		"docker.io": {
			URL: "https://registry-1.docker.io",
		},
	}

	client := NewClient(configs, zerolog.Nop())

	if client == nil {
		t.Fatal("expected non-nil client")
	}

	if !client.HasUpstream("docker.io") {
		t.Error("expected docker.io upstream to exist")
	}

	if client.HasUpstream("nonexistent.io") {
		t.Error("unexpected upstream exists")
	}
}

func TestGetConfig(t *testing.T) {
	configs := map[string]config.UpstreamConfig{
		"docker.io": {
			URL: "https://registry-1.docker.io",
			Auth: config.UpstreamAuthConfig{
				Type:     "bearer",
				TokenURL: "https://auth.docker.io/token",
			},
		},
	}

	client := NewClient(configs, zerolog.Nop())

	cfg, ok := client.GetConfig("docker.io")
	if !ok {
		t.Fatal("expected to get docker.io config")
	}

	if cfg.URL != "https://registry-1.docker.io" {
		t.Errorf("unexpected URL: %s", cfg.URL)
	}

	if cfg.Auth.Type != "bearer" {
		t.Errorf("unexpected auth type: %s", cfg.Auth.Type)
	}

	_, ok = client.GetConfig("nonexistent.io")
	if ok {
		t.Error("expected not to get nonexistent config")
	}
}

func TestParseWWWAuthenticate(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   map[string]string
	}{
		{
			name:   "docker hub style",
			header: `Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/nginx:pull"`,
			want: map[string]string{
				"realm":   "https://auth.docker.io/token",
				"service": "registry.docker.io",
				"scope":   "repository:library/nginx:pull",
			},
		},
		{
			name:   "minimal",
			header: `Bearer realm="https://auth.example.com/token"`,
			want: map[string]string{
				"realm": "https://auth.example.com/token",
			},
		},
		{
			name:   "with spaces",
			header: `Bearer realm="https://auth.example.com/token", service="myservice"`,
			want: map[string]string{
				"realm":   "https://auth.example.com/token",
				"service": "myservice",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWWWAuthenticate(tt.header)

			for key, wantVal := range tt.want {
				gotVal, ok := got[key]
				if !ok {
					t.Errorf("missing key %s", key)
					continue
				}
				if gotVal != wantVal {
					t.Errorf("key %s: got %q, want %q", key, gotVal, wantVal)
				}
			}
		})
	}
}

func TestBasicAuth(t *testing.T) {
	result := basicAuth("user", "pass")
	// base64("user:pass") = "dXNlcjpwYXNz"
	expected := "dXNlcjpwYXNz"

	if result != expected {
		t.Errorf("got %s, want %s", result, expected)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", 30 * time.Second},
		{"60", 60 * time.Second},
		{"120", 120 * time.Second},
		{"invalid", 30 * time.Second},
	}

	for _, tt := range tests {
		got := parseRetryAfter(tt.header)
		if got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestRateLimitError(t *testing.T) {
	err := &RateLimitError{RetryAfter: 60 * time.Second}

	msg := err.Error()
	if msg != "rate limited, retry after 1m0s" {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestFetchManifestWithMockServer(t *testing.T) {
	// Create a mock registry server
	manifest := map[string]interface{}{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.docker.distribution.manifest.v2+json",
		"config": map[string]string{
			"mediaType": "application/vnd.docker.container.image.v1+json",
			"digest":    "sha256:config123",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/library/nginx/manifests/latest":
			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
			w.Header().Set("Docker-Content-Digest", "sha256:manifest123")
			json.NewEncoder(w).Encode(manifest)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	configs := map[string]config.UpstreamConfig{
		"test.registry": {
			URL: server.URL,
		},
	}

	client := NewClient(configs, zerolog.Nop())

	resp, err := client.FetchManifest(context.Background(), "test.registry", "library/nginx", "latest")
	if err != nil {
		t.Fatalf("failed to fetch manifest: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	if resp.Header.Get("Docker-Content-Digest") != "sha256:manifest123" {
		t.Errorf("unexpected digest header: %s", resp.Header.Get("Docker-Content-Digest"))
	}
}

func TestFetchBlobWithMockServer(t *testing.T) {
	blobContent := []byte("fake blob content")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/library/nginx/blobs/sha256:blob123":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", "sha256:blob123")
			w.Write(blobContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	configs := map[string]config.UpstreamConfig{
		"test.registry": {
			URL: server.URL,
		},
	}

	client := NewClient(configs, zerolog.Nop())

	resp, err := client.FetchBlob(context.Background(), "test.registry", "library/nginx", "sha256:blob123")
	if err != nil {
		t.Fatalf("failed to fetch blob: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestAuthenticationFlow(t *testing.T) {
	tokenIssued := false
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			// Token endpoint
			tokenIssued = true
			json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "test-token-123",
				"expires_in": 300,
			})
		case "/v2/library/nginx/manifests/latest":
			// Check for auth
			auth := r.Header.Get("Authorization")
			if auth == "" {
				// No auth, return 401 with challenge
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+serverURL+`/token",service="test"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if auth != "Bearer test-token-123" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			// Auth OK
			w.Header().Set("Docker-Content-Digest", "sha256:abc")
			w.Write([]byte(`{"schemaVersion": 2}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configs := map[string]config.UpstreamConfig{
		"test.registry": {
			URL: server.URL,
			Auth: config.UpstreamAuthConfig{
				Type: "bearer",
			},
		},
	}

	client := NewClient(configs, zerolog.Nop())

	resp, err := client.FetchManifest(context.Background(), "test.registry", "library/nginx", "latest")
	if err != nil {
		t.Fatalf("failed to fetch manifest: %v", err)
	}
	defer resp.Body.Close()

	if !tokenIssued {
		t.Error("expected token to be requested")
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestRedirectHandling(t *testing.T) {
	blobContent := []byte("blob from CDN")

	// CDN server (redirect target)
	cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(blobContent)
	}))
	defer cdnServer.Close()

	// Registry server (redirects to CDN)
	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/library/nginx/blobs/sha256:blob123" {
			w.Header().Set("Location", cdnServer.URL+"/blob")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer registryServer.Close()

	configs := map[string]config.UpstreamConfig{
		"test.registry": {
			URL: registryServer.URL,
		},
	}

	client := NewClient(configs, zerolog.Nop())

	resp, err := client.FetchBlob(context.Background(), "test.registry", "library/nginx", "sha256:blob123")
	if err != nil {
		t.Fatalf("failed to fetch blob: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestNoUpstreamConfigured(t *testing.T) {
	client := NewClient(map[string]config.UpstreamConfig{}, zerolog.Nop())

	_, err := client.FetchManifest(context.Background(), "nonexistent.io", "repo", "tag")
	if err == nil {
		t.Error("expected error for unconfigured upstream")
	}
}
