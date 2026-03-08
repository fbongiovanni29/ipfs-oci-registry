package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the registry.
type Config struct {
	Server     ServerConfig              `yaml:"server"`
	Storage    StorageConfig             `yaml:"storage"`
	IPFS       IPFSConfig                `yaml:"ipfs"`
	Federation FederationConfig          `yaml:"federation"`
	Auth       AuthConfig                `yaml:"auth"`
	RateLimit  RateLimitConfig           `yaml:"rate_limit"`
	GC         GCConfig                  `yaml:"gc"`
	Metrics    MetricsConfig             `yaml:"metrics"`
	Upstreams  map[string]UpstreamConfig `yaml:"upstreams"`
	Logging    LoggingConfig             `yaml:"logging"`
}

// ServerConfig configures the HTTP server.
type ServerConfig struct {
	Address string    `yaml:"address"`
	TLS     TLSConfig `yaml:"tls"`
}

// TLSConfig configures TLS for the HTTP server.
type TLSConfig struct {
	Enabled bool   `yaml:"enabled"`
	Cert    string `yaml:"cert"`
	Key     string `yaml:"key"`
}

// StorageConfig configures local storage.
type StorageConfig struct {
	Database string `yaml:"database"`
	TempDir  string `yaml:"temp_dir"`
}

// IPFSConfig configures the IPFS client.
type IPFSConfig struct {
	APIURL     string        `yaml:"api_url"`
	Timeout    time.Duration `yaml:"timeout"`
	PinContent bool          `yaml:"pin_content"`
}

// FederationConfig configures the federation layer.
type FederationConfig struct {
	Enabled              bool          `yaml:"enabled"`
	Topic                string        `yaml:"topic"`
	QueryTimeout         time.Duration `yaml:"query_timeout"`
	AnnounceNewContent   bool          `yaml:"announce_new_content"`
	SharePushedImages    bool          `yaml:"share_pushed_images"`
	ShareUpstreamImages  bool          `yaml:"share_upstream_images"`
	PublicNamespace      string        `yaml:"public_namespace"`
	TagTTL               time.Duration `yaml:"tag_ttl"`
}

// AuthConfig configures authentication for registry endpoints.
type AuthConfig struct {
	Enabled bool              `yaml:"enabled"`
	Realm   string            `yaml:"realm"`
	Users   map[string]string `yaml:"users"` // username -> password (use env vars: ${PASSWORD})
}

// RateLimitConfig configures per-IP rate limiting.
type RateLimitConfig struct {
	Enabled    bool `yaml:"enabled"`
	MaxPerMin  int  `yaml:"max_per_minute"`
	BurstSize  int  `yaml:"burst_size"`
}

// GCConfig configures garbage collection of old content.
type GCConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
	MaxAge   time.Duration `yaml:"max_age"`
	DryRun   bool          `yaml:"dry_run"`
}

// MetricsConfig configures Prometheus metrics.
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

// UpstreamConfig configures an upstream registry.
type UpstreamConfig struct {
	URL  string             `yaml:"url"`
	Auth UpstreamAuthConfig `yaml:"auth"`
}

// UpstreamAuthConfig configures authentication for an upstream registry.
type UpstreamAuthConfig struct {
	Type     string `yaml:"type"` // "bearer" or "basic"
	TokenURL string `yaml:"token_url"`
	Service  string `yaml:"service"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// LoggingConfig configures logging.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Address: ":5000",
			TLS:     TLSConfig{Enabled: false},
		},
		Storage: StorageConfig{
			Database: "/var/lib/oci-ipfs/registry.db",
			TempDir:  "/var/lib/oci-ipfs/uploads",
		},
		IPFS: IPFSConfig{
			APIURL:     "http://localhost:5001",
			Timeout:    30 * time.Second,
			PinContent: true,
		},
		Federation: FederationConfig{
			Enabled:             true,
			Topic:               "/oci-registry/v1/mappings",
			QueryTimeout:        500 * time.Millisecond,
			AnnounceNewContent:  true,
			SharePushedImages:   false,
			ShareUpstreamImages: true,
			PublicNamespace:     "public",
			TagTTL:              5 * time.Minute,
		},
		Auth: AuthConfig{
			Enabled: false,
			Realm:   "OCI Registry",
		},
		RateLimit: RateLimitConfig{
			Enabled:   false,
			MaxPerMin: 600,
			BurstSize: 50,
		},
		GC: GCConfig{
			Enabled:  false,
			Interval: 1 * time.Hour,
			MaxAge:   7 * 24 * time.Hour, // 7 days
			DryRun:   false,
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
		Upstreams: map[string]UpstreamConfig{
			"docker.io": {
				URL: "https://registry-1.docker.io",
				Auth: UpstreamAuthConfig{
					Type:     "bearer",
					TokenURL: "https://auth.docker.io/token",
					Service:  "registry.docker.io",
				},
			},
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Load loads configuration from a YAML file.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Expand environment variables in sensitive fields
	for name, upstream := range cfg.Upstreams {
		upstream.Auth.Username = os.ExpandEnv(upstream.Auth.Username)
		upstream.Auth.Password = os.ExpandEnv(upstream.Auth.Password)
		cfg.Upstreams[name] = upstream
	}

	for user, pass := range cfg.Auth.Users {
		cfg.Auth.Users[user] = os.ExpandEnv(pass)
	}

	return cfg, nil
}
