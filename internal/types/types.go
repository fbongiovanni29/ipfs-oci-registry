package types

import (
	"time"
)

// BlobMapping represents the mapping between an OCI digest and an IPFS CID.
type BlobMapping struct {
	Digest     string    `json:"digest"`
	CID        string    `json:"cid"`
	Size       int64     `json:"size"`
	MediaType  string    `json:"media_type,omitempty"`
	Repository string    `json:"repository,omitempty"`
	Source     string    `json:"source"` // "push", "upstream:<registry>", "federation:<peerID>"
	CreatedAt  time.Time `json:"created_at"`
}

// TagReference represents a tag pointing to a manifest digest.
type TagReference struct {
	Repository string    `json:"repository"`
	Tag        string    `json:"tag"`
	Digest     string    `json:"digest"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Repository represents metadata about a repository.
type Repository struct {
	Name      string   `json:"name"`
	Tags      []string `json:"tags"`
	Manifests []string `json:"manifests"` // list of manifest digests
}

// UploadSession tracks an in-progress blob upload.
type UploadSession struct {
	ID         string    `json:"id"`
	Repository string    `json:"repository"`
	StartedAt  time.Time `json:"started_at"`
	BytesWritten int64   `json:"bytes_written"`
	TempPath   string    `json:"temp_path"`
}

// FederationMessage is the pubsub message format for announcing mappings and tags.
type FederationMessage struct {
	Type      string    `json:"type"` // "mapping", "query", "response", "tag"
	Digest    string    `json:"digest,omitempty"`
	CID       string    `json:"cid,omitempty"`
	Size      int64     `json:"size,omitempty"`
	MediaType string    `json:"media_type,omitempty"`
	PeerID    string    `json:"peer_id"`
	RequestID string    `json:"request_id,omitempty"` // for query/response correlation
	Timestamp time.Time `json:"timestamp"`

	// Tag-related fields (for type="tag")
	Repository string `json:"repository,omitempty"`
	Tag        string `json:"tag,omitempty"`
}

// OCIError represents an OCI-compliant error response.
type OCIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail,omitempty"`
}

// OCIErrorResponse is the wrapper for OCI error responses.
type OCIErrorResponse struct {
	Errors []OCIError `json:"errors"`
}

// Common OCI error codes
const (
	ErrorCodeBlobUnknown         = "BLOB_UNKNOWN"
	ErrorCodeBlobUploadInvalid   = "BLOB_UPLOAD_INVALID"
	ErrorCodeBlobUploadUnknown   = "BLOB_UPLOAD_UNKNOWN"
	ErrorCodeDigestInvalid       = "DIGEST_INVALID"
	ErrorCodeManifestBlobUnknown = "MANIFEST_BLOB_UNKNOWN"
	ErrorCodeManifestInvalid     = "MANIFEST_INVALID"
	ErrorCodeManifestUnknown     = "MANIFEST_UNKNOWN"
	ErrorCodeNameInvalid         = "NAME_INVALID"
	ErrorCodeNameUnknown         = "NAME_UNKNOWN"
	ErrorCodeSizeInvalid         = "SIZE_INVALID"
	ErrorCodeUnauthorized        = "UNAUTHORIZED"
	ErrorCodeDenied              = "DENIED"
	ErrorCodeUnsupported         = "UNSUPPORTED"
)

// NewOCIError creates a new OCI error response.
func NewOCIError(code, message string, detail any) OCIErrorResponse {
	return OCIErrorResponse{
		Errors: []OCIError{
			{
				Code:    code,
				Message: message,
				Detail:  detail,
			},
		},
	}
}
