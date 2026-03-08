![IPFS OCI Registry](assets/oci-ipfs-registry.png)

# IPFS OCI Registry

> **Note:** This project was vibecoded as a proof of concept. It is not production-ready and should be used for experimentation and learning purposes only. It has been validated locally and to some extent with minikube

**Pull Once, Share Everywhere** — A decentralized, federated container registry powered by IPFS.

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev)
[![OCI Compliant](https://img.shields.io/badge/OCI-Distribution%20Spec-blue?style=flat)](https://github.com/opencontainers/distribution-spec)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat)](LICENSE)

---

## Why Another Registry?

Container registries are centralized bottlenecks. Docker Hub rate limits you. Your cloud provider locks you in. Air-gapped environments require complex mirroring. Multi-region deployments mean paying for the same bytes over and over.

**What if container images distributed themselves?**

- Pull once, share forever
- No central server required
- Images get *faster* as more people use them
- Survives upstream outages

That's what this project does.

---

## How It's Different

| Feature | Docker Hub | ECR/GCR/ACR | Harbor | Spegel | **IPFS Registry** |
|---------|------------|-------------|--------|--------|-------------------|
| Pull-through cache | ❌ | ✅ | ✅ | ❌ | ✅ |
| P2P within cluster | ❌ | ❌ | ❌ | ✅ | ✅ |
| **P2P across internet** | ❌ | ❌ | ❌ | ❌ | ✅ |
| **Cross-org sharing** | ❌ | ❌ | ❌ | ❌ | ✅ |
| No central server | ❌ | ❌ | ❌ | ✅ | ✅ * |
| Works fully offline | ❌ | ❌ | ✅ | ✅ | ✅ |
| Vendor neutral | ❌ | ❌ | ✅ | ✅ | ✅ |
| **Global CDN effect** | ❌ | ❌ | ❌ | ❌ | ✅ |
| Content-addressed | ❌ | ❌ | ❌ | ❌ | ✅ |
| Rate limit free | ❌ | ✅ | ✅ | ✅ | ✅ |

\* *No central server: set `registry.mode=daemonset` and `ipfs.deploy.mode=daemonset` for full decentralization*

---

## The Magic: Federation Without Trust

```
┌─────────────────┐                           ┌─────────────────┐
│   Company A     │                           │   Company B     │
│                 │         IPFS Network      │                 │
│  ┌───────────┐  │    ┌─────────────────┐    │  ┌───────────┐  │
│  │ Registry  │◄─┼───►│  Shared Content │◄───┼─►│ Registry  │  │
│  └───────────┘  │    │  (by CID hash)  │    │  └───────────┘  │
│       │         │    └─────────────────┘    │       │         │
│       ▼         │                           │       ▼         │
│  ┌───────────┐  │                           │  ┌───────────┐  │
│  │ IPFS Node │◄─┼───────────────────────────┼─►│ IPFS Node │  │
│  └───────────┘  │                           │  └───────────┘  │
└─────────────────┘                           └─────────────────┘

Company A pulls nginx:latest → stored in IPFS → CID: Qm123...
Company B pulls nginx:latest → fetched from Company A via IPFS
                               ↳ No coordination. No trust. Just math.
```

Content is verified by SHA256 digest. If the bytes don't match, they're rejected. You don't need to trust the source — **you trust the hash**.

---

## Use Cases

### 🌍 Multi-Region / Edge Deployments
Deploy 1000 edge devices. The first one pulls from Docker Hub. The other 999 pull from each other via IPFS mesh. **One upstream fetch, infinite local distribution.**

### 🔒 Air-Gapped Environments
Pin images to local IPFS. Internet goes down? Registry keeps working. Upstream disappears? You still have your images.

### 🏢 Cross-Organization Collaboration
Share container images between organizations by adding their registry as an upstream — same pattern as Docker Hub. DNS is the namespace:
```
docker pull localhost:5000/registry.partner.com/shared-tools:v2
```
No VPNs, no peering agreements, no shared infrastructure. First pull comes from the source, subsequent pulls come from IPFS peers.

### 💰 Eliminate Rate Limits & Egress Costs
- Docker Hub: 100 pulls/6 hours (anonymous)
- This registry: **Unlimited** (pull from IPFS peers)
- Cloud egress: Pay once, share via IPFS forever

### 🛡️ Supply Chain Security
Content-addressed storage means **immutable images**. The CID *is* the content. Tamper with a byte and the hash changes. Built-in integrity verification.

---

## Operational Benefits

### High Availability

Traditional registries are single points of failure. IPFS Registry eliminates this:

| Scenario | Traditional Registry | IPFS Registry |
|----------|---------------------|---------------|
| Registry pod dies | ❌ All pulls fail | ✅ Other nodes serve content |
| Node failure | ❌ Need failover | ✅ Content on remaining nodes |
| Network partition | ❌ Cluster split = outage | ✅ Each partition self-sufficient |
| Rolling updates | ⚠️ Brief interruption | ✅ Zero downtime |

**With DaemonSet mode:** Every node is a fully functional registry. No leader election, no quorum, no split-brain. If a node can reach any other node, it can pull images.

### Disaster Recovery

Content-addressed storage fundamentally changes DR:

- **No backup jobs needed** — Content is replicated by design
- **No restore procedures** — Just re-pin the CIDs you need
- **Geographic redundancy built-in** — IPFS naturally spreads content
- **Instant recovery** — No waiting for backup restoration

```
Traditional DR:                    IPFS DR:
┌─────────────┐                   ┌─────────────┐
│  Primary    │──backup──►S3      │  Region A   │◄──┐
└─────────────┘                   └─────────────┘   │
      │                                  ▲          │
   disaster                              │        IPFS
      ▼                                  │          │
┌─────────────┐                   ┌─────────────┐   │
│  Restore    │◄──hours───S3      │  Region B   │◄──┘
└─────────────┘                   └─────────────┘
   (hours)                           (instant)
```

### Bandwidth & Cost Optimization

| Metric | Traditional | IPFS Registry |
|--------|-------------|---------------|
| Upstream pulls | Every node, every time | Once per cluster |
| Cross-region transfer | Pay per GB | Free via IPFS |
| Docker Hub rate limits | 100/6hr (anonymous) | Unlimited (cached) |
| Egress costs | Linear with nodes | Constant |

**Example:** 100-node cluster pulling 10GB image
- Traditional: 1TB egress ($90 on AWS)
- IPFS Registry: 10GB egress ($0.90) + free P2P distribution

### Resilience to External Failures

Your deployments survive when external services don't:

| External Failure | Impact |
|-----------------|--------|
| Docker Hub outage | ✅ Serve from IPFS cache |
| Cloud region failure | ✅ Other regions have content |
| DNS issues | ✅ IPFS uses content addressing |
| Certificate expiry upstream | ✅ Already cached locally |
| Upstream registry sunset | ✅ Your images persist |

### Deduplication

Content-addressed storage means automatic deduplication:

```
Without IPFS:                      With IPFS:
nginx:1.24  ─── 50MB              nginx:1.24  ───┐
nginx:1.25  ─── 50MB (48MB same)  nginx:1.25  ───┼─► 52MB total
nginx:1.26  ─── 50MB (48MB same)  nginx:1.26  ───┘   (shared layers)
            ─────────
Total: 150MB
```

Base layers shared across images are stored once. The more images you have, the more you save.

### Offline Operation

Once images are cached, no internet required:

- **Air-gapped deployments** — Pin images, disconnect, deploy forever
- **Intermittent connectivity** — Works when connected, serves cache when not
- **Field deployments** — Edge devices share images locally via IPFS mesh

### Auditability

Every image pull is verifiable:

```
Image Request:     nginx@sha256:abc123...
IPFS Lookup:       sha256:abc123 → CID:bafybeif...
Content Served:    Verified against digest
Result:            Cryptographically guaranteed correct
```

No trust required in the delivery path. The content either matches the hash or it's rejected.

---

## Quick Start

### Option 1: Binary

```bash
# Prerequisites: Go 1.22+, IPFS daemon running

# Build
go build -o oci-ipfs-registry ./cmd/registry

# Run
./oci-ipfs-registry --config config.yaml
```

### Option 2: Docker Compose

```bash
# Starts registry + IPFS node
docker compose up -d

# Test it
curl http://localhost:5000/v2/
docker pull localhost:5000/docker.io/library/alpine:latest
```

### Option 3: Kubernetes (Helm)

```bash
# Basic: External IPFS (you provide ipfs.apiUrl)
helm install registry ./charts/oci-ipfs-registry

# With bundled IPFS (single instance)
helm install registry ./charts/oci-ipfs-registry \
  --set ipfs.deploy.enabled=true

# With P2P mode (IPFS on every node)
helm install registry ./charts/oci-ipfs-registry \
  --set ipfs.deploy.enabled=true \
  --set ipfs.deploy.mode=daemonset

# Full decentralization (registry + IPFS on every node)
helm install registry ./charts/oci-ipfs-registry \
  --set registry.mode=daemonset \
  --set ipfs.deploy.enabled=true \
  --set ipfs.deploy.mode=daemonset

# Production (AWS EKS with internal LB + TLS)
helm install registry ./charts/oci-ipfs-registry \
  -f charts/oci-ipfs-registry/values-eks.yaml \
  --set ipfs.deploy.enabled=true \
  --set ingress.hosts[0].host=registry.internal.company.com
```

**Deployment modes:**
| Mode | Registry | IPFS | Use Case |
|------|----------|------|----------|
| Standard | Deployment | External | Existing IPFS infrastructure |
| P2P Storage | Deployment | DaemonSet | P2P blob distribution within cluster |
| Full P2P | DaemonSet | DaemonSet | No central server, maximum resilience |

Cloud-specific values files included for:
- **AWS EKS** (`values-eks.yaml`) - Internal ALB + ACM certificates
- **Google GKE** (`values-gke.yaml`) - Internal LB + managed certificates
- **Azure AKS** (`values-aks.yaml`) - App Gateway + Key Vault

---

## Usage

### Pull Through Proxy (Mirror Mode)

Validated upstream registries:

| Registry | Example |
|----------|---------|
| Docker Hub | `docker pull localhost:5000/docker.io/library/alpine:latest` |
| GitHub (GHCR) | `docker pull localhost:5000/ghcr.io/aquasecurity/trivy:latest` |
| Google (GCR) | `docker pull localhost:5000/gcr.io/google-containers/pause:latest` |
| Google (GAR) | `docker pull localhost:5000/us-docker.pkg.dev/google-samples/containers/gke/hello-app:1.0` |
| AWS ECR Public | `docker pull localhost:5000/public.ecr.aws/docker/library/alpine:latest` |
| Microsoft (MCR) | `docker pull localhost:5000/mcr.microsoft.com/hello-world:latest` |
| Quay.io | `docker pull localhost:5000/quay.io/prometheus/node-exporter:latest` |

First pull fetches from upstream and caches in IPFS. Subsequent pulls (by you or anyone in the federation) come from IPFS.

### Push Your Own Images

```bash
# Tag and push (private by default — not shared via federation)
docker tag myapp:v1 localhost:5000/myapp:v1
docker push localhost:5000/myapp:v1

# Push to the public namespace to share via federation
docker tag mytools:v1 localhost:5000/public/mytools:v1
docker push localhost:5000/public/mytools:v1
```

### Cross-Organization Image Sharing

Share images between organizations using DNS as the namespace — the same pull-through pattern used for public registries. No new protocol, no coordination, no trust assumptions beyond what DNS already provides.

**Company A** publishes images at `registry.company-a.com`. **Company B** adds it as an upstream:

```yaml
upstreams:
  # Public registries
  docker.io:
    url: https://registry-1.docker.io
    auth:
      type: bearer
      token_url: https://auth.docker.io/token
      service: registry.docker.io

  # Partner registries
  registry.company-a.com:
    url: https://registry.company-a.com
    auth:
      type: bearer
      token_url: https://registry.company-a.com/v2/token
      service: registry.company-a.com
```

Then Company B pulls Company A's images the same way they pull from Docker Hub:

```bash
# From Docker Hub
docker pull localhost:5000/docker.io/library/nginx:latest

# From Company A
docker pull localhost:5000/registry.company-a.com/myapp:v1

# From Company B
docker pull localhost:5000/registry.company-b.com/tools:v2
```

The first pull fetches from the source registry. After that, it's cached in IPFS — and federation means anyone else pulling the same image gets it from the nearest peer instead of going back to the source.

DNS is the namespace. No collisions between organizations. No new concepts to learn.

### Kubernetes Pod Spec

```yaml
spec:
  containers:
  - name: app
    image: registry.internal.company.com/docker.io/library/nginx:latest
```

---

## Configuration

```yaml
server:
  address: ":5000"

ipfs:
  api_url: http://localhost:5001
  pin_content: true              # Pin content for persistence

federation:
  enabled: true                  # Share with other IPFS registries
  topic: /oci-registry/v1       # Pubsub topic for announcements
  share_pushed_images: false     # Don't share proprietary images
  share_upstream_images: true    # Share public upstream pulls
  public_namespace: "public"     # Push to public/ to opt in to sharing

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

  gcr.io:
    url: https://gcr.io
    auth:
      type: bearer
      token_url: https://gcr.io/v2/token
      service: gcr.io

  us-docker.pkg.dev:
    url: https://us-docker.pkg.dev
    auth:
      type: bearer
      token_url: https://us-docker.pkg.dev/v2/token
      service: us-docker.pkg.dev

  public.ecr.aws:
    url: https://public.ecr.aws
    auth:
      type: bearer
      token_url: https://public.ecr.aws/token
      service: public.ecr.aws

  mcr.microsoft.com:
    url: https://mcr.microsoft.com
    auth:
      type: bearer
      token_url: https://mcr.microsoft.com/v2/token
      service: mcr.microsoft.com

  quay.io:
    url: https://quay.io
    auth:
      type: bearer
      token_url: https://quay.io/v2/auth

  # Add private registries with credentials
  # private.registry.com:
  #   url: https://private.registry.com
  #   auth:
  #     type: basic
  #     username: ${REGISTRY_USER}      # Environment variable expansion
  #     password: ${REGISTRY_PASSWORD}

# Authentication (optional — disabled by default)
auth:
  enabled: false
  realm: "OCI Registry"
  users:
    admin: ${REGISTRY_PASSWORD}        # Environment variable expansion
    readonly: changeme

# Per-IP rate limiting (optional — disabled by default)
rate_limit:
  enabled: false
  max_per_minute: 600                  # Requests per IP per minute
  burst_size: 50                       # Allow short bursts above limit

# Garbage collection (optional — disabled by default)
gc:
  enabled: false
  interval: 1h                         # How often to run GC
  max_age: 168h                        # Delete content older than 7 days
  dry_run: false                       # Log what would be deleted without deleting

# Tag TTL (in federation config)
federation:
  tag_ttl: 5m                          # Re-check upstream for tag updates every 5 minutes
                                       # Serves stale cache if upstream is unreachable
```

---

## Federation Policy

Control what gets shared via federation to keep proprietary images private while still benefiting from P2P distribution of public images.

### How It Works

| Image Source | Default Behavior | How to Override |
|---|---|---|
| Pulled from upstream (Docker Hub, GHCR, etc.) | Shared via federation | Set `share_upstream_images: false` |
| Pushed to registry | **Not shared** (private) | Set `share_pushed_images: true` |
| Pushed to `public/` namespace | Shared via federation | Change `public_namespace` in config |

### Enterprise Setup: Internal Federation Only

Share everything across your own infrastructure (multi-cloud, multi-region) without leaking to the public IPFS network:

1. **Private IPFS swarm** — configure all IPFS nodes with a shared swarm key so they only peer with each other
2. **Unique topic** — use a company-specific federation topic
3. **Share everything internally** — set `share_pushed_images: true` since the network is private

```yaml
federation:
  enabled: true
  topic: /mycompany-internal/oci-registry/v1
  share_pushed_images: true       # Safe — private swarm only
  share_upstream_images: true
```

Your nodes federate with each other across AWS, GCP, Azure, on-prem — doesn't matter. No content reaches the public internet.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                        OCI IPFS Registry                         │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│   ┌─────────────┐    ┌─────────────┐    ┌─────────────────────┐ │
│   │ OCI API     │    │ Upstream    │    │ Federation          │ │
│   │ Handler     │───►│ Client      │    │ (IPFS Pubsub)       │ │
│   │             │    │             │    │                     │ │
│   │ • manifests │    │ • docker.io │    │ • Announce new CIDs │ │
│   │ • blobs     │    │ • ghcr.io   │    │ • Query peers       │ │
│   │ • uploads   │    │ • gcr.io    │    │ • Share mappings    │ │
│   │ • catalog   │    │ • private   │    │                     │ │
│   └──────┬──────┘    └──────┬──────┘    └──────────┬──────────┘ │
│          │                  │                      │            │
│          ▼                  ▼                      ▼            │
│   ┌─────────────────────────────────────────────────────────────┤
│   │                    Storage Layer                            │
│   │  ┌─────────────────┐         ┌────────────────────────────┐│
│   │  │ BoltDB          │         │ IPFS (Kubo)                ││
│   │  │                 │         │                            ││
│   │  │ • digest → CID  │◄───────►│ • Content storage          ││
│   │  │ • tag → digest  │         │ • P2P distribution         ││
│   │  │ • repositories  │         │ • Content addressing       ││
│   │  └─────────────────┘         └────────────────────────────┘│
│   └─────────────────────────────────────────────────────────────┤
└──────────────────────────────────────────────────────────────────┘
```

### Request Flow

1. **Client Request** → Image pull by tag or digest
2. **Local Lookup** → Check BoltDB for existing digest→CID mapping
3. **Federation Query** → Ask IPFS peers if they have it (pubsub)
4. **Upstream Fetch** → Pull from Docker Hub/GHCR/etc. if not found
5. **IPFS Storage** → Add blobs to IPFS, get CID back
6. **Announce** → Broadcast new mapping to federation
7. **Serve** → Stream content to client with digest verification

---

## API Endpoints

Full [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec) compliance:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/healthz` | GET | Liveness probe (always 200 if server is running) |
| `/readyz` | GET | Readiness probe (checks BoltDB + IPFS connectivity) |
| `/v2/` | GET | API version check |
| `/v2/_catalog` | GET | List repositories |
| `/v2/{name}/tags/list` | GET | List tags for repository |
| `/v2/{name}/manifests/{ref}` | GET | Get manifest by tag or digest |
| `/v2/{name}/manifests/{ref}` | PUT | Push manifest |
| `/v2/{name}/blobs/{digest}` | GET | Get blob by digest |
| `/v2/{name}/blobs/{digest}` | HEAD | Check blob existence |
| `/v2/{name}/blobs/uploads/` | POST | Initiate blob upload |
| `/v2/{name}/blobs/uploads/{uuid}` | PATCH/PUT | Upload blob chunks |
| `/metrics` | GET | Prometheus metrics (when enabled) |

---

## Production Hardening

### Health Checks

Kubernetes-ready liveness and readiness probes:

```yaml
# Pod spec
livenessProbe:
  httpGet:
    path: /healthz
    port: 5000
readinessProbe:
  httpGet:
    path: /readyz        # Checks BoltDB + IPFS connectivity
    port: 5000
```

### Authentication

Optional basic auth on all registry endpoints (health endpoints are always public):

```yaml
auth:
  enabled: true
  realm: "OCI Registry"
  users:
    admin: ${REGISTRY_PASSWORD}
```

### Rate Limiting

Per-IP rate limiting to prevent abuse. Health endpoints are excluded:

```yaml
rate_limit:
  enabled: true
  max_per_minute: 600
  burst_size: 50
```

### Tag TTL (Stale-While-Revalidate)

Cached tags (e.g., `nginx:latest`) are re-checked against upstream after the TTL expires. If upstream is unreachable, the stale cache is served — better than failing:

```yaml
federation:
  tag_ttl: 5m           # 0 = never revalidate (cache forever)
```

### Garbage Collection

Background worker that removes old content from IPFS and BoltDB:

```yaml
gc:
  enabled: true
  interval: 1h          # Run every hour
  max_age: 168h         # Remove content older than 7 days
  dry_run: true         # Preview what would be deleted (set false to actually delete)
```

Also cleans up abandoned upload sessions (older than 24 hours).

### Prometheus Metrics

Full observability with Prometheus-compatible `/metrics` endpoint:

```yaml
metrics:
  enabled: true
  path: /metrics
```

Exposed metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `registry_http_requests_total` | Counter | HTTP requests by method, route, status |
| `registry_http_request_duration_seconds` | Histogram | Request latency |
| `registry_http_response_size_bytes` | Histogram | Response payload size |
| `registry_resolve_total` | Counter | Content resolution by type (blob/manifest), source (local/federation/upstream), result (hit/miss) |
| `registry_ipfs_operations_total` | Counter | IPFS API calls by operation and result |
| `registry_ipfs_operation_duration_seconds` | Histogram | IPFS operation latency |
| `registry_federation_messages_total` | Counter | Federation pubsub messages by direction and type |
| `registry_federation_query_duration_seconds` | Histogram | Federation query latency |
| `registry_upstream_requests_total` | Counter | Upstream registry requests by registry, type, result |
| `registry_upstream_request_duration_seconds` | Histogram | Upstream request latency |
| `registry_gc_runs_total` | Counter | GC sweep count |
| `registry_gc_deleted_total` | Counter | Items deleted by GC |
| `registry_gc_duration_seconds` | Histogram | GC sweep duration |
| `registry_ratelimit_blocked_total` | Counter | Requests blocked by rate limiter |
| `registry_storage_blobs` | Gauge | Total blob mappings in store |
| `registry_storage_tags` | Gauge | Total tag references |
| `registry_storage_repositories` | Gauge | Total repositories |
| `registry_storage_db_size_bytes` | Gauge | BoltDB file size |

### Upload Safety

Concurrent PATCH/PUT requests to the same upload session are serialized with per-upload mutexes. No data corruption from parallel chunk uploads.

---

## Testing

```bash
# Run all tests
go test ./... -v

# With race detector
go test ./... -race

# With coverage
go test ./... -cover
```

58 tests covering storage, handlers, health endpoints, auth middleware, rate limiting, tag TTL, config, and upstream client.

---

## Roadmap

- [x] Garbage collection for unpinned content
- [x] Health endpoints (`/healthz`, `/readyz`)
- [x] Authentication middleware (basic auth)
- [x] Per-IP rate limiting
- [x] Tag TTL with stale-while-revalidate
- [x] Upload concurrency safety
- [x] Prometheus metrics endpoint
- [ ] Web UI for browsing images
- [ ] Signature verification (cosign/notation)
- [ ] S3-compatible storage backend option
- [ ] Cluster mode with shared state (etcd backend)
- [ ] Transparent proxy mode (containerd mirror config generator)

---

## Contributing

Contributions welcome! Please read the [architecture docs](docs/ARCHITECTURE.md) first.

```bash
# Development setup
git clone https://github.com/fbongiovanni29/ipfs-oci-registry
cd ipfs-oci-registry
go mod download
go build ./cmd/registry
```

---

## License

MIT License - see [LICENSE](LICENSE) for details.

---

---

**Stop depending on centralized registries. Start distributing containers the way the internet was meant to work.**
