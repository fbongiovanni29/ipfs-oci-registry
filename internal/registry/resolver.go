package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/containerish/ipfs-oci-registry/internal/ipfs"
	"github.com/containerish/ipfs-oci-registry/internal/storage"
	"github.com/containerish/ipfs-oci-registry/internal/types"
)

// parseImageName parses an image name into registry and repository parts.
// Examples:
//   - docker.io/library/nginx -> registry=docker.io, repo=library/nginx
//   - myapp -> registry="", repo=myapp
//   - ghcr.io/org/app -> registry=ghcr.io, repo=org/app
func parseImageName(name string) (registry, repo string) {
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 1 {
		// No slash, local repository
		return "", name
	}

	// Check if first part looks like a registry (contains . or :)
	if strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") {
		return parts[0], parts[1]
	}

	// Otherwise, local repository with namespace
	return "", name
}

// getManifest retrieves a manifest by reference (tag or digest).
// Returns: digest, content, mediaType, error
func (h *Handler) getManifest(ctx context.Context, name, reference string) (string, []byte, string, error) {
	registry, repo := parseImageName(name)

	// If reference is a digest, try to fetch directly
	if strings.HasPrefix(reference, "sha256:") {
		return h.getManifestByDigest(ctx, name, registry, repo, reference)
	}

	// Reference is a tag, resolve to digest
	return h.getManifestByTag(ctx, name, registry, repo, reference)
}

// getManifestByDigest fetches a manifest by digest.
func (h *Handler) getManifestByDigest(ctx context.Context, name, registry, repo, digest string) (string, []byte, string, error) {
	// 1. Check local store
	mapping, err := h.store.GetMapping(digest)
	if err == nil {
		content, err := h.fetchFromIPFS(ctx, mapping.CID)
		if err == nil {
			// Verify digest
			computed := computeDigest(content)
			if computed == digest {
				return digest, content, mapping.MediaType, nil
			}
			h.logger.Warn().Str("digest", digest).Msg("local content digest mismatch")
		}
	}

	// 2. Check federation (if enabled)
	if h.federation != nil && h.config.Federation.Enabled {
		fedCtx, cancel := context.WithTimeout(ctx, h.config.Federation.QueryTimeout)
		mapping, err := h.federation.QueryDigest(fedCtx, digest)
		cancel()

		if err == nil && mapping != nil {
			content, err := h.fetchFromIPFS(ctx, mapping.CID)
			if err == nil {
				computed := computeDigest(content)
				if computed == digest {
					// Cache locally
					h.store.PutMapping(mapping)
					return digest, content, mapping.MediaType, nil
				}
			}
		}
	}

	// 3. Fetch from upstream (if registry is configured)
	if registry != "" && h.upstream.HasUpstream(registry) {
		return h.fetchManifestFromUpstream(ctx, name, registry, repo, digest)
	}

	return "", nil, "", fmt.Errorf("manifest not found: %s", digest)
}

// getManifestByTag fetches a manifest by tag.
func (h *Handler) getManifestByTag(ctx context.Context, name, registry, repo, tag string) (string, []byte, string, error) {
	// 1. Check local tag reference
	tagRef, err := h.store.GetTag(name, tag)
	if err == nil {
		// Got cached tag, fetch manifest by digest
		digest, content, mediaType, err := h.getManifestByDigest(ctx, name, registry, repo, tagRef.Digest)
		if err == nil {
			return digest, content, mediaType, nil
		}
	}

	// 2. Fetch from upstream to get current tag resolution
	if registry != "" && h.upstream.HasUpstream(registry) {
		return h.fetchManifestFromUpstream(ctx, name, registry, repo, tag)
	}

	// 3. Local-only image with tag
	if tagRef != nil {
		return h.getManifestByDigest(ctx, name, registry, repo, tagRef.Digest)
	}

	return "", nil, "", fmt.Errorf("manifest not found: %s:%s", name, tag)
}

// fetchManifestFromUpstream fetches a manifest from an upstream registry and caches it.
func (h *Handler) fetchManifestFromUpstream(ctx context.Context, name, registry, repo, reference string) (string, []byte, string, error) {
	resp, err := h.upstream.FetchManifest(ctx, registry, repo, reference)
	if err != nil {
		return "", nil, "", fmt.Errorf("upstream fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", nil, "", fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to read upstream response: %w", err)
	}

	// Get digest from header or compute it
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		digest = computeDigest(content)
	}

	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/vnd.docker.distribution.manifest.v2+json"
	}

	// Store in IPFS
	addResp, err := h.ipfsClient.Add(ctx, strings.NewReader(string(content)))
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to add upstream manifest to IPFS")
		// Return content anyway, just don't cache
		return digest, content, mediaType, nil
	}

	// Store mapping
	mapping := &types.BlobMapping{
		Digest:    digest,
		CID:       addResp.Hash,
		Size:      int64(len(content)),
		MediaType: mediaType,
		Source:    fmt.Sprintf("upstream:%s", registry),
		CreatedAt: time.Now(),
	}

	if err := h.store.PutMapping(mapping); err != nil {
		h.logger.Error().Err(err).Msg("failed to store manifest mapping")
	}

	// Store tag reference if this was a tag lookup
	if !strings.HasPrefix(reference, "sha256:") {
		tagRef := &types.TagReference{
			Repository: name,
			Tag:        reference,
			Digest:     digest,
			UpdatedAt:  time.Now(),
		}
		if err := h.store.PutTag(tagRef); err != nil {
			h.logger.Error().Err(err).Msg("failed to store tag reference")
		}
	}

	// Update repository
	h.store.AddRepositoryManifest(name, digest)

	// Announce to federation
	if h.federation != nil && h.config.Federation.AnnounceNewContent {
		if err := h.federation.Announce(mapping); err != nil {
			h.logger.Warn().Err(err).Msg("failed to announce manifest to federation")
		}
	}

	h.logger.Info().
		Str("registry", registry).
		Str("repo", repo).
		Str("reference", reference).
		Str("digest", digest).
		Str("cid", addResp.Hash).
		Msg("cached manifest from upstream")

	return digest, content, mediaType, nil
}

// resolveBlob resolves a blob digest to its mapping, fetching from upstream if needed.
func (h *Handler) resolveBlob(ctx context.Context, name, digest string) (*types.BlobMapping, error) {
	registry, repo := parseImageName(name)

	// 1. Check local store
	mapping, err := h.store.GetMapping(digest)
	if err == nil {
		return mapping, nil
	}

	// 2. Check federation (if enabled)
	if h.federation != nil && h.config.Federation.Enabled {
		fedCtx, cancel := context.WithTimeout(ctx, h.config.Federation.QueryTimeout)
		mapping, err := h.federation.QueryDigest(fedCtx, digest)
		cancel()

		if err == nil && mapping != nil {
			// Verify we can actually fetch it
			_, err := h.ipfsClient.Stat(ctx, mapping.CID)
			if err == nil {
				// Cache locally
				h.store.PutMapping(mapping)
				return mapping, nil
			}
		}
	}

	// 3. Fetch from upstream
	if registry != "" && h.upstream.HasUpstream(registry) {
		return h.fetchBlobFromUpstream(ctx, name, registry, repo, digest)
	}

	return nil, storage.ErrNotFound
}

// fetchBlobFromUpstream fetches a blob from an upstream registry and caches it.
func (h *Handler) fetchBlobFromUpstream(ctx context.Context, name, registry, repo, digest string) (*types.BlobMapping, error) {
	resp, err := h.upstream.FetchBlob(ctx, registry, repo, digest)
	if err != nil {
		return nil, fmt.Errorf("upstream fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	// Stream to IPFS while computing hash
	pr, pw := io.Pipe()
	hasher := sha256.New()

	// Tee the response body
	tr := io.TeeReader(resp.Body, hasher)

	var addResp *ipfs.AddResponse
	var addErr error

	go func() {
		// Copy to pipe
		_, err := io.Copy(pw, tr)
		pw.CloseWithError(err)
	}()

	addResp, addErr = h.ipfsClient.Add(ctx, pr)
	if addErr != nil {
		return nil, fmt.Errorf("failed to add blob to IPFS: %w", addErr)
	}

	// Verify digest
	computed := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if computed != digest {
		h.logger.Warn().
			Str("expected", digest).
			Str("computed", computed).
			Msg("upstream blob digest mismatch")
		return nil, fmt.Errorf("digest mismatch")
	}

	// Store mapping
	mapping := &types.BlobMapping{
		Digest:    digest,
		CID:       addResp.Hash,
		Size:      resp.ContentLength,
		Source:    fmt.Sprintf("upstream:%s", registry),
		CreatedAt: time.Now(),
	}

	if err := h.store.PutMapping(mapping); err != nil {
		h.logger.Error().Err(err).Msg("failed to store blob mapping")
	}

	// Announce to federation
	if h.federation != nil && h.config.Federation.AnnounceNewContent {
		if err := h.federation.Announce(mapping); err != nil {
			h.logger.Warn().Err(err).Msg("failed to announce blob to federation")
		}
	}

	h.logger.Info().
		Str("registry", registry).
		Str("repo", repo).
		Str("digest", digest).
		Str("cid", addResp.Hash).
		Msg("cached blob from upstream")

	return mapping, nil
}

// fetchFromIPFS fetches content from IPFS by CID.
func (h *Handler) fetchFromIPFS(ctx context.Context, cid string) ([]byte, error) {
	reader, err := h.ipfsClient.Cat(ctx, cid)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return io.ReadAll(reader)
}

// computeDigest computes the SHA256 digest of content.
func computeDigest(content []byte) string {
	hash := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(hash[:])
}
