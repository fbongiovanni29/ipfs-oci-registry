package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerish/ipfs-oci-registry/internal/types"
)

func TestNewStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Verify file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}

func TestStoreLocking(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Open first store
	store1, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create first store: %v", err)
	}
	defer store1.Close()

	// Try to open second store - should fail with timeout
	_, err = NewStore(dbPath)
	if err == nil {
		t.Error("expected error when opening locked database, got nil")
	}
}

func TestBlobMapping(t *testing.T) {
	store := createTestStore(t)
	defer store.Close()

	mapping := &types.BlobMapping{
		Digest:    "sha256:abc123def456",
		CID:       "QmTestCID123",
		Size:      12345,
		MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
		Source:    "push",
		CreatedAt: time.Now(),
	}

	// Test Put
	if err := store.PutMapping(mapping); err != nil {
		t.Fatalf("failed to put mapping: %v", err)
	}

	// Test Get
	retrieved, err := store.GetMapping(mapping.Digest)
	if err != nil {
		t.Fatalf("failed to get mapping: %v", err)
	}

	if retrieved.Digest != mapping.Digest {
		t.Errorf("digest mismatch: got %s, want %s", retrieved.Digest, mapping.Digest)
	}
	if retrieved.CID != mapping.CID {
		t.Errorf("CID mismatch: got %s, want %s", retrieved.CID, mapping.CID)
	}
	if retrieved.Size != mapping.Size {
		t.Errorf("size mismatch: got %d, want %d", retrieved.Size, mapping.Size)
	}

	// Test HasBlob
	if !store.HasBlob(mapping.Digest) {
		t.Error("HasBlob returned false for existing blob")
	}
	if store.HasBlob("sha256:nonexistent") {
		t.Error("HasBlob returned true for non-existent blob")
	}

	// Test Delete
	if err := store.DeleteMapping(mapping.Digest); err != nil {
		t.Fatalf("failed to delete mapping: %v", err)
	}

	// Verify deleted
	_, err = store.GetMapping(mapping.Digest)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestTagReference(t *testing.T) {
	store := createTestStore(t)
	defer store.Close()

	ref := &types.TagReference{
		Repository: "docker.io/library/nginx",
		Tag:        "latest",
		Digest:     "sha256:abc123",
		UpdatedAt:  time.Now(),
	}

	// Test Put
	if err := store.PutTag(ref); err != nil {
		t.Fatalf("failed to put tag: %v", err)
	}

	// Test Get
	retrieved, err := store.GetTag(ref.Repository, ref.Tag)
	if err != nil {
		t.Fatalf("failed to get tag: %v", err)
	}

	if retrieved.Digest != ref.Digest {
		t.Errorf("digest mismatch: got %s, want %s", retrieved.Digest, ref.Digest)
	}

	// Test ListTags
	tags, err := store.ListTags(ref.Repository)
	if err != nil {
		t.Fatalf("failed to list tags: %v", err)
	}

	if len(tags) != 1 || tags[0] != "latest" {
		t.Errorf("unexpected tags: %v", tags)
	}

	// Add another tag
	ref2 := &types.TagReference{
		Repository: "docker.io/library/nginx",
		Tag:        "1.25",
		Digest:     "sha256:def456",
		UpdatedAt:  time.Now(),
	}
	store.PutTag(ref2)

	tags, _ = store.ListTags(ref.Repository)
	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(tags))
	}
}

func TestRepository(t *testing.T) {
	store := createTestStore(t)
	defer store.Close()

	repoName := "myapp"

	// Test AddRepositoryManifest creates repo
	if err := store.AddRepositoryManifest(repoName, "sha256:manifest1"); err != nil {
		t.Fatalf("failed to add manifest: %v", err)
	}

	// Add another manifest
	if err := store.AddRepositoryManifest(repoName, "sha256:manifest2"); err != nil {
		t.Fatalf("failed to add second manifest: %v", err)
	}

	// Get repository
	repo, err := store.GetRepository(repoName)
	if err != nil {
		t.Fatalf("failed to get repository: %v", err)
	}

	if len(repo.Manifests) != 2 {
		t.Errorf("expected 2 manifests, got %d", len(repo.Manifests))
	}

	// Adding duplicate manifest should not create duplicate
	store.AddRepositoryManifest(repoName, "sha256:manifest1")
	repo, _ = store.GetRepository(repoName)
	if len(repo.Manifests) != 2 {
		t.Errorf("duplicate manifest was added, got %d manifests", len(repo.Manifests))
	}

	// Test ListRepositories
	repos, err := store.ListRepositories()
	if err != nil {
		t.Fatalf("failed to list repositories: %v", err)
	}

	if len(repos) != 1 || repos[0] != repoName {
		t.Errorf("unexpected repositories: %v", repos)
	}
}

func TestUploadSession(t *testing.T) {
	store := createTestStore(t)
	defer store.Close()

	session := &types.UploadSession{
		ID:           "test-uuid-123",
		Repository:   "myapp",
		StartedAt:    time.Now(),
		BytesWritten: 0,
		TempPath:     "/tmp/upload-123",
	}

	// Test Put
	if err := store.PutUpload(session); err != nil {
		t.Fatalf("failed to put upload: %v", err)
	}

	// Test Get
	retrieved, err := store.GetUpload(session.ID)
	if err != nil {
		t.Fatalf("failed to get upload: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("ID mismatch: got %s, want %s", retrieved.ID, session.ID)
	}
	if retrieved.TempPath != session.TempPath {
		t.Errorf("TempPath mismatch: got %s, want %s", retrieved.TempPath, session.TempPath)
	}

	// Test update
	session.BytesWritten = 1024
	store.PutUpload(session)
	retrieved, _ = store.GetUpload(session.ID)
	if retrieved.BytesWritten != 1024 {
		t.Errorf("BytesWritten not updated: got %d", retrieved.BytesWritten)
	}

	// Test Delete
	if err := store.DeleteUpload(session.ID); err != nil {
		t.Fatalf("failed to delete upload: %v", err)
	}

	_, err = store.GetUpload(session.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestGetStats(t *testing.T) {
	store := createTestStore(t)
	defer store.Close()

	// Add some data
	store.PutMapping(&types.BlobMapping{Digest: "sha256:1", CID: "Qm1"})
	store.PutMapping(&types.BlobMapping{Digest: "sha256:2", CID: "Qm2"})
	store.PutTag(&types.TagReference{Repository: "repo", Tag: "v1", Digest: "sha256:1"})
	store.AddRepositoryManifest("repo", "sha256:1")

	stats, err := store.GetStats()
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats.BlobCount != 2 {
		t.Errorf("expected 2 blobs, got %d", stats.BlobCount)
	}
	if stats.TagCount != 1 {
		t.Errorf("expected 1 tag, got %d", stats.TagCount)
	}
	if stats.RepositoryCount != 1 {
		t.Errorf("expected 1 repository, got %d", stats.RepositoryCount)
	}
}

func TestGetNonExistent(t *testing.T) {
	store := createTestStore(t)
	defer store.Close()

	_, err := store.GetMapping("sha256:nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}

	_, err = store.GetTag("repo", "nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}

	_, err = store.GetRepository("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}

	_, err = store.GetUpload("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// createTestStore creates a temporary store for testing
func createTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}

	return store
}
