package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerish/ipfs-oci-registry/internal/config"
	"github.com/containerish/ipfs-oci-registry/internal/ipfs"
	"github.com/containerish/ipfs-oci-registry/internal/storage"
	"github.com/containerish/ipfs-oci-registry/internal/types"
	"github.com/containerish/ipfs-oci-registry/internal/upstream"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

// Handler implements the OCI Distribution API.
type Handler struct {
	store      *storage.Store
	ipfsClient *ipfs.Client
	upstream   *upstream.Client
	federation Federation
	config     *config.Config
	logger     zerolog.Logger
	tempDir    string
}

// Federation interface for the federation layer.
type Federation interface {
	QueryDigest(ctx context.Context, digest string) (*types.BlobMapping, error)
	Announce(mapping *types.BlobMapping) error
}

// NewHandler creates a new registry handler.
func NewHandler(
	store *storage.Store,
	ipfsClient *ipfs.Client,
	upstreamClient *upstream.Client,
	federation Federation,
	cfg *config.Config,
	logger zerolog.Logger,
) (*Handler, error) {
	// Ensure temp directory exists
	if err := os.MkdirAll(cfg.Storage.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	return &Handler{
		store:      store,
		ipfsClient: ipfsClient,
		upstream:   upstreamClient,
		federation: federation,
		config:     cfg,
		logger:     logger,
		tempDir:    cfg.Storage.TempDir,
	}, nil
}

// RegisterRoutes registers the OCI Distribution API routes.
func (h *Handler) RegisterRoutes(r *mux.Router) {
	// API version check
	r.HandleFunc("/v2/", h.handleAPIVersion).Methods(http.MethodGet, http.MethodHead)
	r.HandleFunc("/v2", h.handleAPIVersion).Methods(http.MethodGet, http.MethodHead)

	// Catalog
	r.HandleFunc("/v2/_catalog", h.handleCatalog).Methods(http.MethodGet)

	// Tags list
	r.HandleFunc("/v2/{name:.*}/tags/list", h.handleTagsList).Methods(http.MethodGet)

	// Manifests
	r.HandleFunc("/v2/{name:.*}/manifests/{reference}", h.handleManifestHead).Methods(http.MethodHead)
	r.HandleFunc("/v2/{name:.*}/manifests/{reference}", h.handleManifestGet).Methods(http.MethodGet)
	r.HandleFunc("/v2/{name:.*}/manifests/{reference}", h.handleManifestPut).Methods(http.MethodPut)
	r.HandleFunc("/v2/{name:.*}/manifests/{reference}", h.handleManifestDelete).Methods(http.MethodDelete)

	// Blobs
	r.HandleFunc("/v2/{name:.*}/blobs/{digest}", h.handleBlobHead).Methods(http.MethodHead)
	r.HandleFunc("/v2/{name:.*}/blobs/{digest}", h.handleBlobGet).Methods(http.MethodGet)
	r.HandleFunc("/v2/{name:.*}/blobs/{digest}", h.handleBlobDelete).Methods(http.MethodDelete)

	// Blob uploads
	r.HandleFunc("/v2/{name:.*}/blobs/uploads/", h.handleBlobUploadInit).Methods(http.MethodPost)
	r.HandleFunc("/v2/{name:.*}/blobs/uploads/{uuid}", h.handleBlobUploadPatch).Methods(http.MethodPatch)
	r.HandleFunc("/v2/{name:.*}/blobs/uploads/{uuid}", h.handleBlobUploadPut).Methods(http.MethodPut)
}

// handleAPIVersion handles GET /v2/
func (h *Handler) handleAPIVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.WriteHeader(http.StatusOK)
}

// handleCatalog handles GET /v2/_catalog
func (h *Handler) handleCatalog(w http.ResponseWriter, r *http.Request) {
	repos, err := h.store.ListRepositories()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeNameUnknown, "failed to list repositories")
		return
	}

	response := struct {
		Repositories []string `json:"repositories"`
	}{
		Repositories: repos,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleTagsList handles GET /v2/{name}/tags/list
func (h *Handler) handleTagsList(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	tags, err := h.store.ListTags(name)
	if err != nil && err != storage.ErrNotFound {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeNameUnknown, "failed to list tags")
		return
	}

	if tags == nil {
		tags = []string{}
	}

	response := struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}{
		Name: name,
		Tags: tags,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleManifestHead handles HEAD /v2/{name}/manifests/{reference}
func (h *Handler) handleManifestHead(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	reference := vars["reference"]

	digest, content, mediaType, err := h.getManifest(r.Context(), name, reference)
	if err != nil {
		h.logger.Debug().Err(err).Str("name", name).Str("ref", reference).Msg("manifest not found")
		h.writeError(w, http.StatusNotFound, types.ErrorCodeManifestUnknown, "manifest unknown")
		return
	}

	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	w.WriteHeader(http.StatusOK)
}

// handleManifestGet handles GET /v2/{name}/manifests/{reference}
func (h *Handler) handleManifestGet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	reference := vars["reference"]

	digest, content, mediaType, err := h.getManifest(r.Context(), name, reference)
	if err != nil {
		h.logger.Debug().Err(err).Str("name", name).Str("ref", reference).Msg("manifest not found")
		h.writeError(w, http.StatusNotFound, types.ErrorCodeManifestUnknown, "manifest unknown")
		return
	}

	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	w.Write(content)
}

// handleManifestPut handles PUT /v2/{name}/manifests/{reference}
func (h *Handler) handleManifestPut(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	reference := vars["reference"]

	content, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, types.ErrorCodeManifestInvalid, "failed to read manifest")
		return
	}

	// Calculate digest
	hash := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(hash[:])

	mediaType := r.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/vnd.oci.image.manifest.v1+json"
	}

	// Store in IPFS
	addResp, err := h.ipfsClient.Add(r.Context(), strings.NewReader(string(content)))
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to add manifest to IPFS")
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeManifestInvalid, "failed to store manifest")
		return
	}

	// Store mapping
	mapping := &types.BlobMapping{
		Digest:    digest,
		CID:       addResp.Hash,
		Size:      int64(len(content)),
		MediaType: mediaType,
		Source:    "push",
		CreatedAt: time.Now(),
	}

	if err := h.store.PutMapping(mapping); err != nil {
		h.logger.Error().Err(err).Msg("failed to store mapping")
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeManifestInvalid, "failed to store manifest mapping")
		return
	}

	// If reference is a tag (not a digest), store the tag
	if !strings.HasPrefix(reference, "sha256:") {
		tagRef := &types.TagReference{
			Repository: name,
			Tag:        reference,
			Digest:     digest,
			UpdatedAt:  time.Now(),
		}
		if err := h.store.PutTag(tagRef); err != nil {
			h.logger.Error().Err(err).Msg("failed to store tag")
		}
	}

	// Update repository metadata
	if err := h.store.AddRepositoryManifest(name, digest); err != nil {
		h.logger.Error().Err(err).Msg("failed to update repository")
	}

	// Announce to federation
	if h.federation != nil {
		if err := h.federation.Announce(mapping); err != nil {
			h.logger.Warn().Err(err).Msg("failed to announce manifest to federation")
		}
	}

	h.logger.Info().
		Str("name", name).
		Str("reference", reference).
		Str("digest", digest).
		Str("cid", addResp.Hash).
		Msg("manifest pushed")

	w.Header().Set("Location", fmt.Sprintf("/v2/%s/manifests/%s", name, digest))
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusCreated)
}

// handleManifestDelete handles DELETE /v2/{name}/manifests/{reference}
func (h *Handler) handleManifestDelete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	reference := vars["reference"]

	// Resolve tag to digest if needed
	digest := reference
	if !strings.HasPrefix(reference, "sha256:") {
		tagRef, err := h.store.GetTag(name, reference)
		if err != nil {
			h.writeError(w, http.StatusNotFound, types.ErrorCodeManifestUnknown, "manifest unknown")
			return
		}
		digest = tagRef.Digest
	}

	// Delete mapping
	if err := h.store.DeleteMapping(digest); err != nil {
		h.logger.Error().Err(err).Msg("failed to delete mapping")
	}

	w.WriteHeader(http.StatusAccepted)
}

// handleBlobHead handles HEAD /v2/{name}/blobs/{digest}
func (h *Handler) handleBlobHead(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	digest := vars["digest"]

	mapping, err := h.resolveBlob(r.Context(), name, digest)
	if err != nil {
		h.writeError(w, http.StatusNotFound, types.ErrorCodeBlobUnknown, "blob unknown")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", mapping.Size))
	w.WriteHeader(http.StatusOK)
}

// handleBlobGet handles GET /v2/{name}/blobs/{digest}
func (h *Handler) handleBlobGet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	digest := vars["digest"]

	mapping, err := h.resolveBlob(r.Context(), name, digest)
	if err != nil {
		h.writeError(w, http.StatusNotFound, types.ErrorCodeBlobUnknown, "blob unknown")
		return
	}

	// Fetch from IPFS
	reader, err := h.ipfsClient.Cat(r.Context(), mapping.CID)
	if err != nil {
		h.logger.Error().Err(err).Str("cid", mapping.CID).Msg("failed to fetch from IPFS")
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUnknown, "failed to fetch blob")
		return
	}
	defer reader.Close()

	// Verify digest as we stream
	verifier := newVerifyingWriter(w, digest)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", mapping.Size))

	if _, err := io.Copy(verifier, reader); err != nil {
		h.logger.Error().Err(err).Msg("failed to stream blob")
		return
	}

	if err := verifier.Verify(); err != nil {
		h.logger.Error().Err(err).Str("digest", digest).Msg("digest verification failed")
		// Content already sent, can't return error to client
	}
}

// handleBlobDelete handles DELETE /v2/{name}/blobs/{digest}
func (h *Handler) handleBlobDelete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	digest := vars["digest"]

	if err := h.store.DeleteMapping(digest); err != nil {
		h.logger.Error().Err(err).Msg("failed to delete blob mapping")
	}

	w.WriteHeader(http.StatusAccepted)
}

// handleBlobUploadInit handles POST /v2/{name}/blobs/uploads/
func (h *Handler) handleBlobUploadInit(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Check for cross-repo mount
	mount := r.URL.Query().Get("mount")
	from := r.URL.Query().Get("from")

	if mount != "" && from != "" {
		// Try to mount from another repository
		if h.store.HasBlob(mount) {
			w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", name, mount))
			w.Header().Set("Docker-Content-Digest", mount)
			w.WriteHeader(http.StatusCreated)
			return
		}
	}

	// Check for single POST upload (digest in query)
	digest := r.URL.Query().Get("digest")
	if digest != "" {
		h.handleMonolithicUpload(w, r, name, digest)
		return
	}

	// Create upload session
	uploadID := uuid.New().String()
	tempPath := filepath.Join(h.tempDir, uploadID)

	session := &types.UploadSession{
		ID:           uploadID,
		Repository:   name,
		StartedAt:    time.Now(),
		BytesWritten: 0,
		TempPath:     tempPath,
	}

	if err := h.store.PutUpload(session); err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to create upload session")
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", name, uploadID))
	w.Header().Set("Docker-Upload-UUID", uploadID)
	w.Header().Set("Range", "0-0")
	w.WriteHeader(http.StatusAccepted)
}

// handleBlobUploadPatch handles PATCH /v2/{name}/blobs/uploads/{uuid}
func (h *Handler) handleBlobUploadPatch(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	uploadID := vars["uuid"]

	session, err := h.store.GetUpload(uploadID)
	if err != nil {
		h.writeError(w, http.StatusNotFound, types.ErrorCodeBlobUploadUnknown, "upload unknown")
		return
	}

	// Open or create temp file
	f, err := os.OpenFile(session.TempPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to open temp file")
		return
	}
	defer f.Close()

	// Write chunk
	n, err := io.Copy(f, r.Body)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to write chunk")
		return
	}

	session.BytesWritten += n
	if err := h.store.PutUpload(session); err != nil {
		h.logger.Error().Err(err).Msg("failed to update upload session")
	}

	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", name, uploadID))
	w.Header().Set("Docker-Upload-UUID", uploadID)
	w.Header().Set("Range", fmt.Sprintf("0-%d", session.BytesWritten-1))
	w.WriteHeader(http.StatusAccepted)
}

// handleBlobUploadPut handles PUT /v2/{name}/blobs/uploads/{uuid}
func (h *Handler) handleBlobUploadPut(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	uploadID := vars["uuid"]
	digest := r.URL.Query().Get("digest")

	if digest == "" {
		h.writeError(w, http.StatusBadRequest, types.ErrorCodeDigestInvalid, "digest required")
		return
	}

	session, err := h.store.GetUpload(uploadID)
	if err != nil {
		h.writeError(w, http.StatusNotFound, types.ErrorCodeBlobUploadUnknown, "upload unknown")
		return
	}

	// Write any remaining data
	if r.ContentLength > 0 {
		f, err := os.OpenFile(session.TempPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to open temp file")
			return
		}
		if _, err := io.Copy(f, r.Body); err != nil {
			f.Close()
			h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to write data")
			return
		}
		f.Close()
	}

	// Verify digest
	f, err := os.Open(session.TempPath)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to read temp file")
		return
	}

	hash := sha256.New()
	size, err := io.Copy(hash, f)
	f.Close()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to compute digest")
		return
	}

	computed := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if computed != digest {
		os.Remove(session.TempPath)
		h.store.DeleteUpload(uploadID)
		h.writeError(w, http.StatusBadRequest, types.ErrorCodeDigestInvalid,
			fmt.Sprintf("digest mismatch: computed %s, expected %s", computed, digest))
		return
	}

	// Add to IPFS
	f, _ = os.Open(session.TempPath)
	defer f.Close()

	addResp, err := h.ipfsClient.Add(r.Context(), f)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to add to IPFS")
		return
	}

	// Store mapping
	mapping := &types.BlobMapping{
		Digest:    digest,
		CID:       addResp.Hash,
		Size:      size,
		Source:    "push",
		CreatedAt: time.Now(),
	}

	if err := h.store.PutMapping(mapping); err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to store mapping")
		return
	}

	// Cleanup
	os.Remove(session.TempPath)
	h.store.DeleteUpload(uploadID)

	// Announce to federation
	if h.federation != nil {
		if err := h.federation.Announce(mapping); err != nil {
			h.logger.Warn().Err(err).Msg("failed to announce blob to federation")
		}
	}

	h.logger.Info().
		Str("digest", digest).
		Str("cid", addResp.Hash).
		Int64("size", size).
		Msg("blob pushed")

	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", name, digest))
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusCreated)
}

// handleMonolithicUpload handles single-request blob uploads.
func (h *Handler) handleMonolithicUpload(w http.ResponseWriter, r *http.Request, name, digest string) {
	// Read body to temp file while computing hash
	tempPath := filepath.Join(h.tempDir, uuid.New().String())
	f, err := os.Create(tempPath)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to create temp file")
		return
	}
	defer os.Remove(tempPath)
	defer f.Close()

	hash := sha256.New()
	mw := io.MultiWriter(f, hash)

	size, err := io.Copy(mw, r.Body)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to read body")
		return
	}
	f.Close()

	computed := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if computed != digest {
		h.writeError(w, http.StatusBadRequest, types.ErrorCodeDigestInvalid,
			fmt.Sprintf("digest mismatch: computed %s, expected %s", computed, digest))
		return
	}

	// Add to IPFS
	f, _ = os.Open(tempPath)
	defer f.Close()

	addResp, err := h.ipfsClient.Add(r.Context(), f)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to add to IPFS")
		return
	}

	// Store mapping
	mapping := &types.BlobMapping{
		Digest:    digest,
		CID:       addResp.Hash,
		Size:      size,
		Source:    "push",
		CreatedAt: time.Now(),
	}

	if err := h.store.PutMapping(mapping); err != nil {
		h.writeError(w, http.StatusInternalServerError, types.ErrorCodeBlobUploadInvalid, "failed to store mapping")
		return
	}

	// Announce to federation
	if h.federation != nil {
		if err := h.federation.Announce(mapping); err != nil {
			h.logger.Warn().Err(err).Msg("failed to announce blob to federation")
		}
	}

	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", name, digest))
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusCreated)
}

// writeError writes an OCI-compliant error response.
func (h *Handler) writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(types.NewOCIError(code, message, nil))
}

// verifyingWriter verifies content digest as it's written.
type verifyingWriter struct {
	w        http.ResponseWriter
	hasher   hash.Hash
	expected string
}

func newVerifyingWriter(w http.ResponseWriter, digest string) *verifyingWriter {
	return &verifyingWriter{
		w:        w,
		hasher:   sha256.New(),
		expected: digest,
	}
}

func (v *verifyingWriter) Write(p []byte) (int, error) {
	v.hasher.Write(p)
	return v.w.Write(p)
}

func (v *verifyingWriter) Verify() error {
	computed := "sha256:" + hex.EncodeToString(v.hasher.Sum(nil))
	if computed != v.expected {
		return fmt.Errorf("digest mismatch: computed %s, expected %s", computed, v.expected)
	}
	return nil
}
