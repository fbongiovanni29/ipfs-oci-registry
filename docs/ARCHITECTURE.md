# IPFS OCI Registry Proxy - Architecture

## Overview

An OCI Distribution API-compliant registry that uses IPFS as its storage and distribution backend. It functions as:

1. **A full OCI registry** - push and pull images directly
2. **A pull-through cache** - mirror upstream registries (Docker Hub, GHCR, etc.)
3. **A federated distribution network** - share images across IPFS peers

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              IPFS Network                                   │
│                                                                             │
│    ┌─────────────┐         ┌─────────────┐         ┌─────────────┐         │
│    │   Node A    │◄───────►│   Node B    │◄───────►│   Node C    │         │
│    │ (Registry)  │  pubsub │ (Registry)  │  pubsub │ (Registry)  │         │
│    └──────┬──────┘         └──────┬──────┘         └──────┬──────┘         │
│           │                       │                       │                 │
└───────────┼───────────────────────┼───────────────────────┼─────────────────┘
            │                       │                       │
            ▼                       ▼                       ▼
     ┌──────────────┐        ┌──────────────┐        ┌──────────────┐
     │  containerd  │        │  containerd  │        │  containerd  │
     │  (Cluster A) │        │  (Cluster B) │        │  (Cluster C) │
     └──────────────┘        └──────────────┘        └──────────────┘
```

## Design Principles

1. **OCI digests are canonical** - `sha256:...` is the source of truth
2. **IPFS CIDs are internal** - users never see or use CIDs directly
3. **Cache-first, fail-safe** - IPFS/federation failures fall back to upstream
4. **Verify everything** - always validate content matches digest
5. **No critical path dependencies** - federation is opportunistic, not required

## Components

```
┌─────────────────────────────────────────────────────────────────┐
│                     IPFS OCI Registry Proxy                     │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                    HTTP Server                           │   │
│  │              (OCI Distribution API v2)                   │   │
│  └─────────────────────────┬───────────────────────────────┘   │
│                            │                                    │
│  ┌─────────────────────────▼───────────────────────────────┐   │
│  │                   Registry Handler                       │   │
│  │  - Route requests by image name                          │   │
│  │  - Handle authentication (optional)                      │   │
│  │  - Manage upload sessions                                │   │
│  └───────┬─────────────────┬─────────────────┬─────────────┘   │
│          │                 │                 │                  │
│          ▼                 ▼                 ▼                  │
│  ┌───────────────┐ ┌───────────────┐ ┌───────────────┐         │
│  │    Storage    │ │   Upstream    │ │  Federation   │         │
│  │    Manager    │ │    Client     │ │    Layer      │         │
│  │               │ │               │ │               │         │
│  │ - IPFS Client │ │ - Docker Hub  │ │ - Pubsub      │         │
│  │ - Mapping DB  │ │ - GHCR        │ │ - Peer Disco  │         │
│  │ - Digest→CID  │ │ - Private Reg │ │ - Announce    │         │
│  └───────┬───────┘ └───────┬───────┘ └───────┬───────┘         │
│          │                 │                 │                  │
└──────────┼─────────────────┼─────────────────┼──────────────────┘
           │                 │                 │
           ▼                 ▼                 ▼
    ┌─────────────┐   ┌─────────────┐   ┌─────────────┐
    │    Kubo     │   │  Upstream   │   │    IPFS     │
    │  (go-ipfs)  │   │  Registries │   │   Network   │
    └─────────────┘   └─────────────┘   └─────────────┘
```

### Component Details

#### HTTP Server
- Implements OCI Distribution Spec v2
- Handles TLS termination (optional, can run behind ingress)
- Request logging and metrics

#### Registry Handler
- Routes requests based on image repository name
- Determines if request is for local content or upstream mirror
- Manages blob upload sessions (UUID-based)
- Optional Bearer token authentication

#### Storage Manager
- **IPFS Client**: Communicates with Kubo via HTTP API
- **Mapping Store**: BoltDB database for digest→CID mappings
- **Content Verification**: Always validates SHA256 before serving

#### Upstream Client
- Fetches from configured upstream registries
- Handles authentication (Bearer tokens, basic auth)
- Respects rate limits (429 backoff)
- Streams large blobs (no memory buffering)

#### Federation Layer
- **Pubsub Publisher**: Announces new digest→CID mappings
- **Pubsub Subscriber**: Listens for mappings from peers
- **Mapping Cache**: Short-term cache of remote mappings
- **Timeout/Circuit Breaker**: Never blocks on federation
- **Federation Policy**: Controls what gets announced based on source and namespace

## Data Flow

### Pull (Cache Hit - Local)

```
Client                  Registry                IPFS
  │                        │                      │
  │ GET /v2/nginx/blobs/   │                      │
  │     sha256:abc123      │                      │
  │───────────────────────►│                      │
  │                        │                      │
  │                        │ lookup(sha256:abc)   │
  │                        │─────────┐            │
  │                        │         │ BoltDB    │
  │                        │◄────────┘            │
  │                        │ found: QmXYZ         │
  │                        │                      │
  │                        │ GET /api/v0/cat      │
  │                        │     ?arg=QmXYZ       │
  │                        │─────────────────────►│
  │                        │                      │
  │                        │◄─────────────────────│
  │                        │      blob data       │
  │                        │                      │
  │                        │ verify sha256        │
  │                        │─────────┐            │
  │                        │◄────────┘            │
  │                        │                      │
  │◄───────────────────────│                      │
  │      blob data         │                      │
```

### Pull (Cache Miss - Upstream + Federation)

```
Client          Registry            Federation        Upstream         IPFS
  │                │                     │               │               │
  │ GET manifest   │                     │               │               │
  │───────────────►│                     │               │               │
  │                │                     │               │               │
  │                │ lookup(sha256:abc)  │               │               │
  │                │──────┐              │               │               │
  │                │◄─────┘ not found    │               │               │
  │                │                     │               │               │
  │                │ query(sha256:abc)   │               │               │
  │                │ (timeout: 500ms)    │               │               │
  │                │────────────────────►│               │               │
  │                │                     │               │               │
  │                │◄────────────────────│               │               │
  │                │  not found / timeout│               │               │
  │                │                     │               │               │
  │                │ GET manifest        │               │               │
  │                │─────────────────────────────────────►               │
  │                │                     │               │               │
  │                │◄────────────────────────────────────│               │
  │                │     manifest data   │               │               │
  │                │                     │               │               │
  │                │ verify digest       │               │               │
  │                │──────┐              │               │               │
  │                │◄─────┘              │               │               │
  │                │                     │               │               │
  │                │ POST /api/v0/add    │               │               │
  │                │────────────────────────────────────────────────────►│
  │                │                     │               │               │
  │                │◄───────────────────────────────────────────────────│
  │                │     CID: QmXYZ      │               │               │
  │                │                     │               │               │
  │                │ store(sha256→QmXYZ) │               │               │
  │                │──────┐              │               │               │
  │                │◄─────┘              │               │               │
  │                │                     │               │               │
  │                │ announce(sha256,CID)│               │               │
  │                │────────────────────►│               │               │
  │                │                     │ pubsub        │               │
  │                │                     │──────────────────────────────►│
  │                │                     │               │               │
  │◄───────────────│                     │               │               │
  │  manifest data │                     │               │               │
```

### Pull (Federation Hit)

```
Client          Registry            Federation                    IPFS
  │                │                     │                          │
  │ GET blob       │                     │                          │
  │───────────────►│                     │                          │
  │                │                     │                          │
  │                │ lookup(sha256:abc)  │                          │
  │                │──────┐              │                          │
  │                │◄─────┘ not found    │                          │
  │                │                     │                          │
  │                │ query(sha256:abc)   │                          │
  │                │────────────────────►│                          │
  │                │                     │                          │
  │                │◄────────────────────│                          │
  │                │  found: QmXYZ       │                          │
  │                │  from peer: NodeB   │                          │
  │                │                     │                          │
  │                │ GET /api/v0/cat?arg=QmXYZ                      │
  │                │────────────────────────────────────────────────►│
  │                │                     │                          │
  │                │◄───────────────────────────────────────────────│
  │                │     blob data (from NodeB via IPFS)            │
  │                │                     │                          │
  │                │ verify sha256       │                          │
  │                │──────┐              │                          │
  │                │◄─────┘ ✓ matches    │                          │
  │                │                     │                          │
  │                │ store(sha256→QmXYZ) │ (cache locally)          │
  │                │──────┐              │                          │
  │                │◄─────┘              │                          │
  │                │                     │                          │
  │◄───────────────│                     │                          │
  │    blob data   │                     │                          │
```

### Push

```
Client                  Registry                           IPFS
  │                        │                                 │
  │ POST /v2/myapp/blobs/  │                                 │
  │      uploads/          │                                 │
  │───────────────────────►│                                 │
  │                        │                                 │
  │◄───────────────────────│                                 │
  │ 202 Accepted           │                                 │
  │ Location: /uploads/uuid│                                 │
  │                        │                                 │
  │ PATCH /uploads/uuid    │                                 │
  │ [blob chunks...]       │                                 │
  │───────────────────────►│                                 │
  │                        │ stream to temp file             │
  │◄───────────────────────│                                 │
  │ 202 Accepted           │                                 │
  │                        │                                 │
  │ PUT /uploads/uuid      │                                 │
  │    ?digest=sha256:abc  │                                 │
  │───────────────────────►│                                 │
  │                        │                                 │
  │                        │ verify digest                   │
  │                        │──────┐                          │
  │                        │◄─────┘                          │
  │                        │                                 │
  │                        │ POST /api/v0/add                │
  │                        │────────────────────────────────►│
  │                        │                                 │
  │                        │◄───────────────────────────────│
  │                        │      CID: QmXYZ                 │
  │                        │                                 │
  │                        │ store mapping                   │
  │                        │──────┐                          │
  │                        │◄─────┘                          │
  │                        │                                 │
  │                        │ announce to federation          │
  │                        │─────────────────────────────────►
  │                        │                                 │
  │◄───────────────────────│                                 │
  │ 201 Created            │                                 │
  │ Location: /blobs/sha256│                                 │
```

## OCI Distribution API Endpoints

### Required for Pull (containerd/Docker compatibility)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/v2/` | API version check (must return 200) |
| HEAD | `/v2/<name>/manifests/<reference>` | Check manifest exists |
| GET | `/v2/<name>/manifests/<reference>` | Fetch manifest (by tag or digest) |
| HEAD | `/v2/<name>/blobs/<digest>` | Check blob exists |
| GET | `/v2/<name>/blobs/<digest>` | Fetch blob |

### Required for Push

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/v2/<name>/blobs/uploads/` | Initiate upload |
| PATCH | `/v2/<name>/blobs/uploads/<uuid>` | Upload chunks |
| PUT | `/v2/<name>/blobs/uploads/<uuid>?digest=` | Complete upload |
| PUT | `/v2/<name>/manifests/<reference>` | Push manifest |
| POST | `/v2/<name>/blobs/uploads/?mount=<digest>&from=<repo>` | Cross-repo mount |

### Optional (nice to have)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/v2/<name>/tags/list` | List tags |
| DELETE | `/v2/<name>/manifests/<reference>` | Delete manifest |
| DELETE | `/v2/<name>/blobs/<digest>` | Delete blob |
| GET | `/v2/_catalog` | List repositories |

## Blob Storage & Digest→CID Mapping

### Storage Strategy

1. **All blobs stored in IPFS** - both pushed and pulled content
2. **Digest is the key** - `sha256:abc123...` uniquely identifies content
3. **CID is the IPFS address** - internal, never exposed to clients
4. **Mapping stored in BoltDB** - fast, embedded, single-file database

### BoltDB Schema

```
Bucket: "digests"
  Key:   "sha256:abc123..."
  Value: {
    "cid": "QmXYZ...",
    "size": 12345678,
    "created": "2024-01-15T10:30:00Z",
    "source": "upstream:docker.io" | "push" | "federation:peerID"
  }

Bucket: "tags"
  Key:   "docker.io/library/nginx:latest"
  Value: {
    "digest": "sha256:abc123...",
    "updated": "2024-01-15T10:30:00Z"
  }

Bucket: "repositories"
  Key:   "docker.io/library/nginx"
  Value: {
    "tags": ["latest", "1.25", "1.25.3"],
    "manifests": ["sha256:abc...", "sha256:def..."]
  }
```

### Verification Flow

```go
func (s *Storage) GetBlob(digest string) (io.ReadCloser, error) {
    // 1. Look up CID from digest
    mapping, err := s.db.GetMapping(digest)
    if err != nil {
        return nil, ErrNotFound
    }

    // 2. Fetch from IPFS
    reader, err := s.ipfs.Cat(mapping.CID)
    if err != nil {
        return nil, err
    }

    // 3. Wrap with verification
    return NewVerifyingReader(reader, digest), nil
}

// VerifyingReader computes SHA256 as it reads
// Returns error on Close() if digest doesn't match
type VerifyingReader struct {
    reader io.ReadCloser
    hasher hash.Hash
    expected string
}
```

## IPFS Integration

### Kubo HTTP API Usage

```go
type IPFSClient struct {
    apiURL string  // e.g., "http://localhost:5001"
    client *http.Client
}

// Add content to IPFS, returns CID
func (c *IPFSClient) Add(r io.Reader) (string, error)

// Get content by CID
func (c *IPFSClient) Cat(cid string) (io.ReadCloser, error)

// Pin content to prevent garbage collection
func (c *IPFSClient) Pin(cid string) error

// Subscribe to pubsub topic
func (c *IPFSClient) PubsubSubscribe(topic string) (<-chan Message, error)

// Publish to pubsub topic
func (c *IPFSClient) PubsubPublish(topic string, data []byte) error
```

### Pubsub Topics

```
Topic: "/oci-registry/v1/mappings"

Message format (JSON):
{
  "type": "mapping",
  "digest": "sha256:abc123...",
  "cid": "QmXYZ...",
  "size": 12345678,
  "mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
  "peer": "12D3KooW...",  // IPFS peer ID
  "timestamp": "2024-01-15T10:30:00Z"
}
```

### Federation Query Flow

```go
func (f *Federation) QueryDigest(ctx context.Context, digest string) (*Mapping, error) {
    // Create a context with short timeout - federation is opportunistic
    ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
    defer cancel()

    // Check local cache of recently announced mappings
    if mapping, ok := f.cache.Get(digest); ok {
        return mapping, nil
    }

    // Query via pubsub request/response
    // (Implementation detail: could use libp2p request/response or pubsub)
    return f.queryPeers(ctx, digest)
}
```

## Upstream Registry Integration

### Multi-Registry Support

```yaml
# Configuration
upstreams:
  docker.io:
    url: https://registry-1.docker.io
    auth:
      type: bearer
      token_url: https://auth.docker.io/token
      service: registry.docker.io

  ghcr.io:
    url: https://ghcr.io
    auth:
      type: bearer
      token_url: https://ghcr.io/token

  my-private.registry.com:
    url: https://my-private.registry.com
    auth:
      type: basic
      username: ${REGISTRY_USERNAME}
      password: ${REGISTRY_PASSWORD}
```

### URL Routing

```
Request: GET /v2/docker.io/library/nginx/manifests/latest
         ────────┬───────────────────────────────────────
                 │
                 ▼
    Parse: registry="docker.io", repo="library/nginx", ref="latest"
                 │
                 ▼
    Route to upstream: https://registry-1.docker.io/v2/library/nginx/manifests/latest
```

### Rate Limit Handling

```go
func (u *UpstreamClient) Fetch(ctx context.Context, url string) (*http.Response, error) {
    for attempt := 0; attempt < maxRetries; attempt++ {
        resp, err := u.doRequest(ctx, url)
        if err != nil {
            return nil, err
        }

        if resp.StatusCode == http.StatusTooManyRequests {
            // Respect Retry-After header
            retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
            select {
            case <-time.After(retryAfter):
                continue
            case <-ctx.Done():
                return nil, ctx.Err()
            }
        }

        return resp, nil
    }
    return nil, ErrRateLimited
}
```

## Edge Cases & Container Runtime Quirks

### containerd Mirror Behavior

1. **Mirrors are tried first** - containerd tries mirrors before upstream
2. **Expects standard error codes** - 404 means "try next mirror", 5xx means "retry"
3. **Follows redirects** - can return 307 to redirect to IPFS gateway (optional)
4. **Requires Docker-Content-Digest header** - must return digest in response

### Manifest List (Multi-arch) Handling

```
GET /v2/nginx/manifests/latest
    │
    ▼
Returns: manifest list (fat manifest)
    │
    ├─► linux/amd64 → sha256:aaa...
    ├─► linux/arm64 → sha256:bbb...
    └─► linux/arm/v7 → sha256:ccc...

containerd then requests the specific platform manifest
```

### Important Headers

```http
# Response headers for manifests
Docker-Content-Digest: sha256:abc123...
Content-Type: application/vnd.docker.distribution.manifest.v2+json
# or: application/vnd.oci.image.manifest.v1+json
# or: application/vnd.oci.image.index.v1+json

# Response headers for blobs
Docker-Content-Digest: sha256:abc123...
Content-Type: application/octet-stream
Content-Length: 12345678
Accept-Ranges: bytes  # for resumable downloads
```

### Chunked Upload Handling

```
# Client may upload in multiple chunks
POST   /v2/repo/blobs/uploads/           → 202, Location: /uploads/uuid
PATCH  /v2/repo/blobs/uploads/uuid       → 202 (chunk 1)
PATCH  /v2/repo/blobs/uploads/uuid       → 202 (chunk 2)
PUT    /v2/repo/blobs/uploads/uuid?digest=sha256:... → 201

# Or single monolithic upload
POST   /v2/repo/blobs/uploads/?digest=sha256:...
       [entire blob in body]             → 201
```

## Security Considerations

### Content Verification

- **Always verify SHA256** before serving content to clients
- **Never trust federation blindly** - verify digest matches
- **Validate manifest schema** before storing

### Authentication (Optional)

```yaml
auth:
  enabled: true
  type: bearer
  issuer: "https://auth.myregistry.com"
  # or integrate with external OAuth/OIDC
```

### Network Security

- Run IPFS in a controlled network or with connection filters
- Consider IPFS private network mode for sensitive deployments
- TLS between registry and Kubo if not co-located

## Configuration

```yaml
# config.yaml
server:
  address: ":5000"
  tls:
    enabled: false
    cert: /path/to/cert.pem
    key: /path/to/key.pem

storage:
  database: /var/lib/oci-ipfs/registry.db
  temp_dir: /var/lib/oci-ipfs/uploads

ipfs:
  api_url: http://localhost:5001
  timeout: 30s
  pin_content: true

federation:
  enabled: true
  topic: /oci-registry/v1/mappings
  query_timeout: 500ms
  announce_new_content: true
  share_pushed_images: false      # proprietary images stay local
  share_upstream_images: true     # public upstream pulls are shared
  public_namespace: "public"      # push to public/ to opt in to sharing

upstreams:
  docker.io:
    url: https://registry-1.docker.io
    auth:
      token_url: https://auth.docker.io/token
      service: registry.docker.io
  ghcr.io:
    url: https://ghcr.io
  gcr.io:
    url: https://gcr.io
  us-docker.pkg.dev:
    url: https://us-docker.pkg.dev
  public.ecr.aws:
    url: https://public.ecr.aws
  mcr.microsoft.com:
    url: https://mcr.microsoft.com
  quay.io:
    url: https://quay.io

logging:
  level: info
  format: json
```

## Federation Policy

The federation policy controls which images are announced to peers via IPFS pubsub. This allows organizations to share public images while keeping proprietary images private.

### Policy Decision Flow

```
Image pushed/pulled
        │
        ▼
  announce_new_content: false? ──► Don't announce
        │ true
        ▼
  Source is "upstream:*"? ──► Check share_upstream_images
        │ no
        ▼
  Repository starts with public_namespace? ──► Announce
        │ no
        ▼
  Check share_pushed_images
```

### Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `share_pushed_images` | `false` | Announce images pushed directly to the registry |
| `share_upstream_images` | `true` | Announce images pulled from upstream registries |
| `public_namespace` | `"public"` | Namespace prefix that opts pushed images in to federation |

### Deployment Scenarios

**Public sharing (default):** Share upstream pulls, keep pushed images private.
```yaml
share_pushed_images: false
share_upstream_images: true
public_namespace: "public"
```

**Internal federation (private swarm):** Share everything within your own infrastructure.
```yaml
share_pushed_images: true
share_upstream_images: true
```

**No federation:** Local cache only, no peer communication.
```yaml
enabled: false
```

### Network Isolation

Federation policy controls what gets *announced*. Network isolation controls who you *peer with*:

- **Private IPFS swarm**: Configure all nodes with a shared swarm key. Only nodes with the key can connect.
- **Unique topic**: Use a company-specific pubsub topic for additional isolation.
- Both can be combined for defense in depth.

## Future Enhancements

1. **Garbage Collection** - unpin unused content from IPFS
2. **Replication Policies** - pin important images to multiple nodes
3. **Signature Verification** - cosign/notation integration
4. **Metrics & Tracing** - Prometheus metrics, OpenTelemetry
5. **Web UI** - browse cached images, view federation status
6. **IPFS Cluster Integration** - for high availability
