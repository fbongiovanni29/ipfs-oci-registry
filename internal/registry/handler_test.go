package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/config"
	"github.com/fbongiovanni29/ipfs-oci-registry/internal/storage"
	"github.com/fbongiovanni29/ipfs-oci-registry/internal/types"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

// mockIPFSClient implements a mock IPFS client for testing
type mockIPFSClient struct {
	blobs map[string][]byte
}

func newMockIPFSClient() *mockIPFSClient {
	return &mockIPFSClient{
		blobs: make(map[string][]byte),
	}
}

func (m *mockIPFSClient) Add(ctx context.Context, r io.Reader) (*mockAddResponse, error) {
	data, _ := io.ReadAll(r)
	hash := sha256.Sum256(data)
	cid := "Qm" + hex.EncodeToString(hash[:])[:32]
	m.blobs[cid] = data
	return &mockAddResponse{Hash: cid}, nil
}

func (m *mockIPFSClient) Cat(ctx context.Context, cid string) (io.ReadCloser, error) {
	data, ok := m.blobs[cid]
	if !ok {
		return nil, io.EOF
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockIPFSClient) Stat(ctx context.Context, cid string) (int64, error) {
	data, ok := m.blobs[cid]
	if !ok {
		return 0, io.EOF
	}
	return int64(len(data)), nil
}

type mockAddResponse struct {
	Hash string
}

// mockFederation implements a mock federation for testing
type mockFederation struct{}

func (m *mockFederation) QueryDigest(ctx context.Context, digest string) (*types.BlobMapping, error) {
	return nil, nil
}

func (m *mockFederation) Announce(mapping *types.BlobMapping) error {
	return nil
}

// testHandler creates a handler for testing with mocked dependencies
type testHandler struct {
	*Handler
	store      *storage.Store
	ipfsMock   *mockIPFSClient
	router     *mux.Router
}

func setupTestHandler(t *testing.T) *testHandler {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := storage.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ipfsMock := newMockIPFSClient()
	logger := zerolog.Nop()

	cfg := config.DefaultConfig()
	cfg.Storage.TempDir = filepath.Join(dir, "uploads")

	// Create a minimal handler struct for testing
	h := &Handler{
		store:      store,
		config:     cfg,
		logger:     logger,
		tempDir:    cfg.Storage.TempDir,
		federation: &mockFederation{},
	}

	router := mux.NewRouter()
	h.RegisterRoutes(router)

	return &testHandler{
		Handler:  h,
		store:    store,
		ipfsMock: ipfsMock,
		router:   router,
	}
}

func (th *testHandler) Close() {
	th.store.Close()
}

func TestAPIVersion(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	apiVersion := rec.Header().Get("Docker-Distribution-API-Version")
	if apiVersion != "registry/2.0" {
		t.Errorf("unexpected API version header: %s", apiVersion)
	}
}

func TestCatalogEmpty(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	req := httptest.NewRequest(http.MethodGet, "/v2/_catalog", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should be empty or null
	if result.Repositories != nil && len(result.Repositories) > 0 {
		t.Errorf("expected empty repositories, got %v", result.Repositories)
	}
}

func TestCatalogWithRepos(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	// Add some repositories
	th.store.AddRepositoryManifest("nginx", "sha256:abc")
	th.store.AddRepositoryManifest("myapp", "sha256:def")

	req := httptest.NewRequest(http.MethodGet, "/v2/_catalog", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(result.Repositories) != 2 {
		t.Errorf("expected 2 repositories, got %d", len(result.Repositories))
	}
}

func TestTagsListEmpty(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	req := httptest.NewRequest(http.MethodGet, "/v2/myapp/tags/list", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Name != "myapp" {
		t.Errorf("expected name myapp, got %s", result.Name)
	}
}

func TestTagsListWithTags(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	// Add some tags
	th.store.PutTag(&types.TagReference{Repository: "myapp", Tag: "latest", Digest: "sha256:abc"})
	th.store.PutTag(&types.TagReference{Repository: "myapp", Tag: "v1.0", Digest: "sha256:def"})

	req := httptest.NewRequest(http.MethodGet, "/v2/myapp/tags/list", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	var result struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	json.NewDecoder(rec.Body).Decode(&result)

	if len(result.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(result.Tags))
	}
}

func TestBlobHeadNotFound(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	req := httptest.NewRequest(http.MethodHead, "/v2/myapp/blobs/sha256:nonexistent", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestBlobHeadExists(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	// Add a blob mapping
	digest := "sha256:abc123def456"
	th.store.PutMapping(&types.BlobMapping{
		Digest: digest,
		CID:    "QmTest123",
		Size:   12345,
	})

	req := httptest.NewRequest(http.MethodHead, "/v2/myapp/blobs/"+digest, nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	if rec.Header().Get("Docker-Content-Digest") != digest {
		t.Errorf("unexpected digest header: %s", rec.Header().Get("Docker-Content-Digest"))
	}

	if rec.Header().Get("Content-Length") != "12345" {
		t.Errorf("unexpected content length: %s", rec.Header().Get("Content-Length"))
	}
}

func TestManifestNotFound(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	req := httptest.NewRequest(http.MethodGet, "/v2/myapp/manifests/latest", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}

	var errResp types.OCIErrorResponse
	json.NewDecoder(rec.Body).Decode(&errResp)

	if len(errResp.Errors) == 0 {
		t.Error("expected error response")
	}
	if errResp.Errors[0].Code != types.ErrorCodeManifestUnknown {
		t.Errorf("unexpected error code: %s", errResp.Errors[0].Code)
	}
}

func TestBlobDelete(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	digest := "sha256:todelete"
	th.store.PutMapping(&types.BlobMapping{Digest: digest, CID: "Qm123"})

	req := httptest.NewRequest(http.MethodDelete, "/v2/myapp/blobs/"+digest, nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("expected status 202, got %d", rec.Code)
	}

	// Verify deleted
	if th.store.HasBlob(digest) {
		t.Error("blob should have been deleted")
	}
}

func TestParseImageName(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantRegistry string
		wantRepo     string
	}{
		{
			name:         "local image",
			input:        "myapp",
			wantRegistry: "",
			wantRepo:     "myapp",
		},
		{
			name:         "local with namespace",
			input:        "myorg/myapp",
			wantRegistry: "",
			wantRepo:     "myorg/myapp",
		},
		{
			name:         "docker hub",
			input:        "docker.io/library/nginx",
			wantRegistry: "docker.io",
			wantRepo:     "library/nginx",
		},
		{
			name:         "ghcr",
			input:        "ghcr.io/org/app",
			wantRegistry: "ghcr.io",
			wantRepo:     "org/app",
		},
		{
			name:         "private registry with port",
			input:        "registry.local:5000/myapp",
			wantRegistry: "registry.local:5000",
			wantRepo:     "myapp",
		},
		{
			name:         "nested path",
			input:        "gcr.io/project/subdir/image",
			wantRegistry: "gcr.io",
			wantRepo:     "project/subdir/image",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRegistry, gotRepo := parseImageName(tt.input)
			if gotRegistry != tt.wantRegistry {
				t.Errorf("registry: got %q, want %q", gotRegistry, tt.wantRegistry)
			}
			if gotRepo != tt.wantRepo {
				t.Errorf("repo: got %q, want %q", gotRepo, tt.wantRepo)
			}
		})
	}
}

func TestComputeDigest(t *testing.T) {
	content := []byte("hello world")
	expected := "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	got := computeDigest(content)
	if got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestOCIErrorResponse(t *testing.T) {
	errResp := types.NewOCIError(types.ErrorCodeBlobUnknown, "blob not found", nil)

	if len(errResp.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errResp.Errors))
	}

	if errResp.Errors[0].Code != types.ErrorCodeBlobUnknown {
		t.Errorf("unexpected code: %s", errResp.Errors[0].Code)
	}

	if errResp.Errors[0].Message != "blob not found" {
		t.Errorf("unexpected message: %s", errResp.Errors[0].Message)
	}
}

func TestCORSHeaders(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	// The handler itself doesn't set CORS - that's done in middleware
	// This test verifies the API responds correctly
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

// Test that nested repository names work
func TestNestedRepositoryNames(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	// Add tags for nested repos
	th.store.PutTag(&types.TagReference{
		Repository: "docker.io/library/nginx",
		Tag:        "latest",
		Digest:     "sha256:abc",
	})

	req := httptest.NewRequest(http.MethodGet, "/v2/docker.io/library/nginx/tags/list", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	json.NewDecoder(rec.Body).Decode(&result)

	if result.Name != "docker.io/library/nginx" {
		t.Errorf("unexpected name: %s", result.Name)
	}

	if len(result.Tags) != 1 || result.Tags[0] != "latest" {
		t.Errorf("unexpected tags: %v", result.Tags)
	}
}

func TestUploadInitialization(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	req := httptest.NewRequest(http.MethodPost, "/v2/myapp/blobs/uploads/", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("expected status 202, got %d", rec.Code)
	}

	location := rec.Header().Get("Location")
	if location == "" {
		t.Error("expected Location header")
	}

	if !strings.Contains(location, "/v2/myapp/blobs/uploads/") {
		t.Errorf("unexpected location: %s", location)
	}

	uuid := rec.Header().Get("Docker-Upload-UUID")
	if uuid == "" {
		t.Error("expected Docker-Upload-UUID header")
	}
}

func TestCrossRepoMount(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	// Add a blob that can be mounted
	digest := "sha256:mountable123"
	th.store.PutMapping(&types.BlobMapping{
		Digest: digest,
		CID:    "QmMountable",
		Size:   1000,
	})

	// Try to mount it in another repo
	req := httptest.NewRequest(http.MethodPost,
		"/v2/newrepo/blobs/uploads/?mount="+digest+"&from=oldrepo", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201 for successful mount, got %d", rec.Code)
	}

	if rec.Header().Get("Docker-Content-Digest") != digest {
		t.Errorf("unexpected digest: %s", rec.Header().Get("Docker-Content-Digest"))
	}
}

func TestHealthLive(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestHealthReady(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	// Readiness requires IPFS — our test handler has no real IPFS client,
	// so ipfsClient is nil. We just verify the endpoint exists and returns a response.
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	th.router.ServeHTTP(rec, req)

	// Will be 503 because ipfsClient is nil in test
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusOK {
		t.Errorf("expected 200 or 503, got %d", rec.Code)
	}
}

func TestIsTagStale(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	// Set TTL to 5 minutes
	th.config.Federation.TagTTL = 5 * time.Minute

	fresh := &types.TagReference{
		Repository: "myapp",
		Tag:        "latest",
		Digest:     "sha256:abc",
		UpdatedAt:  time.Now(),
	}
	if th.isTagStale(fresh) {
		t.Error("fresh tag should not be stale")
	}

	stale := &types.TagReference{
		Repository: "myapp",
		Tag:        "latest",
		Digest:     "sha256:abc",
		UpdatedAt:  time.Now().Add(-10 * time.Minute),
	}
	if !th.isTagStale(stale) {
		t.Error("old tag should be stale")
	}

	// TTL disabled
	th.config.Federation.TagTTL = 0
	if th.isTagStale(stale) {
		t.Error("tag should never be stale when TTL is 0")
	}
}

func TestShouldAnnounce(t *testing.T) {
	th := setupTestHandler(t)
	defer th.Close()

	tests := []struct {
		name                string
		sharePushed         bool
		shareUpstream       bool
		publicNamespace     string
		mapping             *types.BlobMapping
		want                bool
	}{
		{
			name:          "upstream image with sharing enabled",
			shareUpstream: true,
			mapping:       &types.BlobMapping{Source: "upstream:docker.io", Repository: "docker.io/library/alpine"},
			want:          true,
		},
		{
			name:          "upstream image with sharing disabled",
			shareUpstream: false,
			mapping:       &types.BlobMapping{Source: "upstream:docker.io", Repository: "docker.io/library/alpine"},
			want:          false,
		},
		{
			name:        "pushed image with sharing disabled",
			sharePushed: false,
			mapping:     &types.BlobMapping{Source: "push", Repository: "internal/myapp"},
			want:        false,
		},
		{
			name:        "pushed image with sharing enabled",
			sharePushed: true,
			mapping:     &types.BlobMapping{Source: "push", Repository: "internal/myapp"},
			want:        true,
		},
		{
			name:            "pushed image in public namespace",
			sharePushed:     false,
			publicNamespace: "public",
			mapping:         &types.BlobMapping{Source: "push", Repository: "public/mytools"},
			want:            true,
		},
		{
			name:            "pushed image NOT in public namespace",
			sharePushed:     false,
			publicNamespace: "public",
			mapping:         &types.BlobMapping{Source: "push", Repository: "private/myapp"},
			want:            false,
		},
		{
			name:            "pushed image with name starting with public but not in namespace",
			sharePushed:     false,
			publicNamespace: "public",
			mapping:         &types.BlobMapping{Source: "push", Repository: "publicdata"},
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th.config.Federation.AnnounceNewContent = true
			th.config.Federation.SharePushedImages = tt.sharePushed
			th.config.Federation.ShareUpstreamImages = tt.shareUpstream
			th.config.Federation.PublicNamespace = tt.publicNamespace

			got := th.shouldAnnounce(tt.mapping)
			if got != tt.want {
				t.Errorf("shouldAnnounce() = %v, want %v", got, tt.want)
			}
		})
	}

	// Test with AnnounceNewContent disabled — nothing should be announced
	t.Run("announce disabled globally", func(t *testing.T) {
		th.config.Federation.AnnounceNewContent = false
		th.config.Federation.SharePushedImages = true
		th.config.Federation.ShareUpstreamImages = true

		got := th.shouldAnnounce(&types.BlobMapping{Source: "push", Repository: "public/anything"})
		if got != false {
			t.Errorf("shouldAnnounce() = %v, want false when announce disabled", got)
		}
	})
}
