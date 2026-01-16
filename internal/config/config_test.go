package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Address != ":5000" {
		t.Errorf("expected address :5000, got %s", cfg.Server.Address)
	}

	if cfg.IPFS.APIURL != "http://localhost:5001" {
		t.Errorf("expected IPFS API URL http://localhost:5001, got %s", cfg.IPFS.APIURL)
	}

	if cfg.IPFS.Timeout != 30*time.Second {
		t.Errorf("expected IPFS timeout 30s, got %s", cfg.IPFS.Timeout)
	}

	if !cfg.Federation.Enabled {
		t.Error("expected federation to be enabled by default")
	}

	if cfg.Federation.QueryTimeout != 500*time.Millisecond {
		t.Errorf("expected query timeout 500ms, got %s", cfg.Federation.QueryTimeout)
	}

	// Check docker.io upstream is configured
	dockerHub, ok := cfg.Upstreams["docker.io"]
	if !ok {
		t.Error("expected docker.io upstream to be configured")
	}
	if dockerHub.URL != "https://registry-1.docker.io" {
		t.Errorf("unexpected docker.io URL: %s", dockerHub.URL)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	configContent := `
server:
  address: ":8080"
  tls:
    enabled: true
    cert: /path/to/cert.pem
    key: /path/to/key.pem

storage:
  database: /custom/path/registry.db
  temp_dir: /custom/path/uploads

ipfs:
  api_url: http://ipfs.local:5001
  timeout: 60s
  pin_content: false

federation:
  enabled: false
  topic: /custom/topic
  query_timeout: 1s
  announce_new_content: false

upstreams:
  ghcr.io:
    url: https://ghcr.io
    auth:
      type: bearer
      token_url: https://ghcr.io/token

logging:
  level: debug
  format: json
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify loaded values
	if cfg.Server.Address != ":8080" {
		t.Errorf("expected address :8080, got %s", cfg.Server.Address)
	}

	if !cfg.Server.TLS.Enabled {
		t.Error("expected TLS to be enabled")
	}

	if cfg.Storage.Database != "/custom/path/registry.db" {
		t.Errorf("unexpected database path: %s", cfg.Storage.Database)
	}

	if cfg.IPFS.APIURL != "http://ipfs.local:5001" {
		t.Errorf("unexpected IPFS API URL: %s", cfg.IPFS.APIURL)
	}

	if cfg.IPFS.Timeout != 60*time.Second {
		t.Errorf("expected timeout 60s, got %s", cfg.IPFS.Timeout)
	}

	if cfg.IPFS.PinContent {
		t.Error("expected pin_content to be false")
	}

	if cfg.Federation.Enabled {
		t.Error("expected federation to be disabled")
	}

	if cfg.Federation.QueryTimeout != 1*time.Second {
		t.Errorf("expected query timeout 1s, got %s", cfg.Federation.QueryTimeout)
	}

	// Check ghcr.io upstream was added
	ghcr, ok := cfg.Upstreams["ghcr.io"]
	if !ok {
		t.Error("expected ghcr.io upstream to be configured")
	}
	if ghcr.URL != "https://ghcr.io" {
		t.Errorf("unexpected ghcr.io URL: %s", ghcr.URL)
	}

	if cfg.Logging.Level != "debug" {
		t.Errorf("expected log level debug, got %s", cfg.Logging.Level)
	}
}

func TestLoadConfigMergesDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Minimal config - should merge with defaults
	configContent := `
server:
  address: ":9000"
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Custom value should be set
	if cfg.Server.Address != ":9000" {
		t.Errorf("expected address :9000, got %s", cfg.Server.Address)
	}

	// Defaults should still be present
	if cfg.IPFS.APIURL != "http://localhost:5001" {
		t.Errorf("expected default IPFS API URL, got %s", cfg.IPFS.APIURL)
	}

	// Docker Hub default should still be present
	if _, ok := cfg.Upstreams["docker.io"]; !ok {
		t.Error("expected docker.io upstream default to be preserved")
	}
}

func TestLoadConfigEnvExpansion(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Set environment variables
	os.Setenv("TEST_REGISTRY_USER", "testuser")
	os.Setenv("TEST_REGISTRY_PASS", "testpass")
	defer os.Unsetenv("TEST_REGISTRY_USER")
	defer os.Unsetenv("TEST_REGISTRY_PASS")

	configContent := `
upstreams:
  private.registry.com:
    url: https://private.registry.com
    auth:
      type: basic
      username: ${TEST_REGISTRY_USER}
      password: ${TEST_REGISTRY_PASS}
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	upstream := cfg.Upstreams["private.registry.com"]
	if upstream.Auth.Username != "testuser" {
		t.Errorf("expected username testuser, got %s", upstream.Auth.Username)
	}
	if upstream.Auth.Password != "testpass" {
		t.Errorf("expected password testpass, got %s", upstream.Auth.Password)
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Error("expected error for non-existent config file")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	invalidYAML := `
server:
  address: ":5000"
  invalid yaml here
    - not valid
`

	if err := os.WriteFile(configPath, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}
