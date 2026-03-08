package upstream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/config"
	"github.com/rs/zerolog"
)

// Client handles communication with upstream registries.
type Client struct {
	configs    map[string]config.UpstreamConfig
	httpClient *http.Client
	tokens     map[string]*tokenCache
	tokenMu    sync.RWMutex
	logger     zerolog.Logger
}

// tokenCache stores cached authentication tokens.
type tokenCache struct {
	Token     string
	ExpiresAt time.Time
}

// NewClient creates a new upstream client.
func NewClient(configs map[string]config.UpstreamConfig, logger zerolog.Logger) *Client {
	return &Client{
		configs: configs,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Don't follow redirects automatically - we want to handle them
				return http.ErrUseLastResponse
			},
		},
		tokens: make(map[string]*tokenCache),
		logger: logger,
	}
}

// GetConfig returns the configuration for a registry.
func (c *Client) GetConfig(registry string) (config.UpstreamConfig, bool) {
	cfg, ok := c.configs[registry]
	return cfg, ok
}

// HasUpstream checks if a registry is configured as an upstream.
func (c *Client) HasUpstream(registry string) bool {
	_, ok := c.configs[registry]
	return ok
}

// FetchManifest fetches a manifest from an upstream registry.
func (c *Client) FetchManifest(ctx context.Context, registry, repository, reference string) (*http.Response, error) {
	cfg, ok := c.configs[registry]
	if !ok {
		return nil, fmt.Errorf("no upstream configured for registry: %s", registry)
	}

	path := fmt.Sprintf("/v2/%s/manifests/%s", repository, reference)

	return c.doRequestWithAuth(ctx, cfg, registry, http.MethodGet, path,
		[]string{
			"application/vnd.docker.distribution.manifest.v2+json",
			"application/vnd.docker.distribution.manifest.list.v2+json",
			"application/vnd.oci.image.manifest.v1+json",
			"application/vnd.oci.image.index.v1+json",
		}, nil)
}

// HeadManifest checks if a manifest exists in an upstream registry.
func (c *Client) HeadManifest(ctx context.Context, registry, repository, reference string) (*http.Response, error) {
	cfg, ok := c.configs[registry]
	if !ok {
		return nil, fmt.Errorf("no upstream configured for registry: %s", registry)
	}

	path := fmt.Sprintf("/v2/%s/manifests/%s", repository, reference)

	return c.doRequestWithAuth(ctx, cfg, registry, http.MethodHead, path,
		[]string{
			"application/vnd.docker.distribution.manifest.v2+json",
			"application/vnd.docker.distribution.manifest.list.v2+json",
			"application/vnd.oci.image.manifest.v1+json",
			"application/vnd.oci.image.index.v1+json",
		}, nil)
}

// FetchBlob fetches a blob from an upstream registry.
func (c *Client) FetchBlob(ctx context.Context, registry, repository, digest string) (*http.Response, error) {
	cfg, ok := c.configs[registry]
	if !ok {
		return nil, fmt.Errorf("no upstream configured for registry: %s", registry)
	}

	path := fmt.Sprintf("/v2/%s/blobs/%s", repository, digest)

	return c.doRequestWithAuth(ctx, cfg, registry, http.MethodGet, path, nil, nil)
}

// HeadBlob checks if a blob exists in an upstream registry.
func (c *Client) HeadBlob(ctx context.Context, registry, repository, digest string) (*http.Response, error) {
	cfg, ok := c.configs[registry]
	if !ok {
		return nil, fmt.Errorf("no upstream configured for registry: %s", registry)
	}

	path := fmt.Sprintf("/v2/%s/blobs/%s", repository, digest)

	return c.doRequestWithAuth(ctx, cfg, registry, http.MethodHead, path, nil, nil)
}

// doRequestWithAuth performs a request with authentication.
func (c *Client) doRequestWithAuth(ctx context.Context, cfg config.UpstreamConfig, registry, method, path string, accept []string, body io.Reader) (*http.Response, error) {
	urlStr := cfg.URL + path

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Accept headers
	if len(accept) > 0 {
		req.Header.Set("Accept", strings.Join(accept, ", "))
	}

	// Try with cached token first
	token := c.getCachedToken(registry)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if cfg.Auth.Type == "basic" && cfg.Auth.Username != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth(cfg.Auth.Username, cfg.Auth.Password))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Handle authentication challenge
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()

		// Parse WWW-Authenticate header
		authHeader := resp.Header.Get("WWW-Authenticate")
		newToken, err := c.authenticate(ctx, cfg, registry, authHeader, path)
		if err != nil {
			return nil, fmt.Errorf("authentication failed: %w", err)
		}

		// Retry with new token
		req, err = http.NewRequestWithContext(ctx, method, urlStr, body)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		if len(accept) > 0 {
			req.Header.Set("Accept", strings.Join(accept, ", "))
		}
		req.Header.Set("Authorization", "Bearer "+newToken)

		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("retry request failed: %w", err)
		}
	}

	// Handle rate limiting
	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		return nil, &RateLimitError{
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}

	// Handle redirects (e.g., Docker Hub/GCR redirect blob downloads to CDN)
	if resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusSeeOther ||
		resp.StatusCode == http.StatusTemporaryRedirect ||
		resp.StatusCode == http.StatusPermanentRedirect {
		location := resp.Header.Get("Location")
		resp.Body.Close()

		if location == "" {
			return nil, fmt.Errorf("redirect without Location header")
		}

		// Resolve relative redirect URLs against the original request URL
		locationURL, err := url.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("failed to parse redirect location: %w", err)
		}
		if !locationURL.IsAbs() {
			baseURL, _ := url.Parse(cfg.URL)
			locationURL = baseURL.ResolveReference(locationURL)
			location = locationURL.String()
		}

		// Follow the redirect
		redirectReq, err := http.NewRequestWithContext(ctx, method, location, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create redirect request: %w", err)
		}

		// Don't send auth header to external CDN
		resp, err = c.httpClient.Do(redirectReq)
		if err != nil {
			return nil, fmt.Errorf("redirect request failed: %w", err)
		}
	}

	return resp, nil
}

// authenticate handles Bearer token authentication.
func (c *Client) authenticate(ctx context.Context, cfg config.UpstreamConfig, registry, wwwAuthenticate, scope string) (string, error) {
	// Parse WWW-Authenticate header: Bearer realm="...",service="...",scope="..."
	params := parseWWWAuthenticate(wwwAuthenticate)

	realm := params["realm"]
	if realm == "" && cfg.Auth.TokenURL != "" {
		realm = cfg.Auth.TokenURL
	}
	if realm == "" {
		return "", fmt.Errorf("no authentication realm found")
	}

	service := params["service"]
	if service == "" && cfg.Auth.Service != "" {
		service = cfg.Auth.Service
	}

	scopeParam := params["scope"]
	if scopeParam == "" {
		// Construct scope from path
		parts := strings.Split(strings.TrimPrefix(scope, "/v2/"), "/")
		if len(parts) >= 2 {
			repo := strings.Join(parts[:len(parts)-1], "/")
			scopeParam = fmt.Sprintf("repository:%s:pull", repo)
		}
	}

	// Build token request URL
	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("invalid realm URL: %w", err)
	}

	query := tokenURL.Query()
	if service != "" {
		query.Set("service", service)
	}
	if scopeParam != "" {
		query.Set("scope", scopeParam)
	}
	tokenURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	// Add basic auth if configured
	if cfg.Auth.Username != "" && cfg.Auth.Password != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth(cfg.Auth.Username, cfg.Auth.Password))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	token := tokenResp.Token
	if token == "" {
		token = tokenResp.AccessToken
	}

	// Cache the token
	expiresIn := tokenResp.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 300 // Default 5 minutes
	}

	c.tokenMu.Lock()
	c.tokens[registry] = &tokenCache{
		Token:     token,
		ExpiresAt: time.Now().Add(time.Duration(expiresIn-30) * time.Second), // Expire 30s early
	}
	c.tokenMu.Unlock()

	c.logger.Debug().
		Str("registry", registry).
		Int("expires_in", expiresIn).
		Msg("authenticated with upstream registry")

	return token, nil
}

// getCachedToken returns a cached token if valid.
func (c *Client) getCachedToken(registry string) string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()

	cache, ok := c.tokens[registry]
	if !ok {
		return ""
	}

	if time.Now().After(cache.ExpiresAt) {
		return ""
	}

	return cache.Token
}

// parseWWWAuthenticate parses the WWW-Authenticate header.
func parseWWWAuthenticate(header string) map[string]string {
	params := make(map[string]string)

	// Strip "Bearer " prefix
	header = strings.TrimPrefix(header, "Bearer ")

	// Parse key="value" pairs
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		idx := strings.Index(part, "=")
		if idx == -1 {
			continue
		}

		key := strings.TrimSpace(part[:idx])
		value := strings.Trim(strings.TrimSpace(part[idx+1:]), "\"")
		params[key] = value
	}

	return params
}

// basicAuth returns base64-encoded basic auth credentials.
func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

// parseRetryAfter parses the Retry-After header.
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 30 * time.Second
	}

	// Try parsing as integer (seconds)
	var seconds int
	if _, err := fmt.Sscanf(header, "%d", &seconds); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Could also parse HTTP date format, but integer is most common
	return 30 * time.Second
}

// RateLimitError is returned when upstream rate limiting is hit.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited, retry after %s", e.RetryAfter)
}
