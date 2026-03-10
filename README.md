![IPFS OCI Registry](assets/oci-ipfs-registry.png)

# IPFS OCI Registry

**Pull Once, Share Everywhere** — A decentralized, federated container registry powered by IPFS.

[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev)
[![OCI Compliant](https://img.shields.io/badge/OCI-Distribution%20Spec-blue?style=flat)](https://github.com/opencontainers/distribution-spec)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat)](LICENSE)

> **Archived** — This project is no longer under active development. It works, it's tested, and it taught us a lot — but IPFS turned out to be the wrong transport layer for container images at scale. See [Lessons Learned](#lessons-learned) below.

---

## Try It

```bash
docker compose up -d
docker pull localhost:5000/docker.io/library/alpine:latest
```

That's it. First pull fetches from Docker Hub and caches in IPFS. Every pull after that comes from IPFS — by you or anyone in the federation.

---

## Why?

Container registries are centralized bottlenecks. Docker Hub rate limits you. Your cloud provider locks you in. Air-gapped environments require complex mirroring. Multi-region deployments mean paying for the same bytes over and over.

**What if container images distributed themselves?**

| Feature | Docker Hub | ECR/GCR/ACR | Harbor | Spegel | **IPFS Registry** |
|---------|------------|-------------|--------|--------|-------------------|
| Pull-through cache | - | Yes | Yes | - | Yes |
| P2P within cluster | - | - | - | Yes | Yes |
| **P2P across internet** | - | - | - | - | **Yes** |
| **Cross-org sharing** | - | - | - | - | **Yes** |
| No central server | - | - | - | Yes | Yes * |
| Works fully offline | - | - | Yes | Yes | Yes |
| **Global CDN effect** | - | - | - | - | **Yes** |
| Content-addressed | - | - | - | - | Yes |
| Rate limit free | - | Yes | Yes | Yes | Yes |

\* *Set `registry.mode=daemonset` + `ipfs.deploy.mode=daemonset` for full decentralization*

---

## How It Works

```
Company A pulls nginx:latest → stored in IPFS → CID: Qm123...
Company B pulls nginx:latest → fetched from Company A via IPFS
                               ↳ No coordination. No trust. Just math.
```

Content is verified by SHA256 digest. If the bytes don't match, they're rejected. You don't need to trust the source — **you trust the hash**.

### Request Flow

1. **Client Request** → Image pull by tag or digest
2. **Local Lookup** → Check BoltDB for existing digest→CID mapping
3. **Federation Query** → Ask IPFS peers if they have it (pubsub)
4. **Upstream Fetch** → Pull from Docker Hub/GHCR/etc. if not found
5. **IPFS Storage** → Add blobs to IPFS, get CID back
6. **Announce** → Broadcast new mapping to federation
7. **Serve** → Stream content to client with digest verification

---

## Supported Registries

Pull-through proxy with automatic IPFS caching:

| Registry | Example |
|----------|---------|
| Docker Hub | `docker pull localhost:5000/docker.io/library/alpine:latest` |
| GitHub (GHCR) | `docker pull localhost:5000/ghcr.io/aquasecurity/trivy:latest` |
| Google (GCR) | `docker pull localhost:5000/gcr.io/google-containers/pause:latest` |
| Google (GAR) | `docker pull localhost:5000/us-docker.pkg.dev/google-samples/containers/gke/hello-app:1.0` |
| AWS ECR Public | `docker pull localhost:5000/public.ecr.aws/docker/library/alpine:latest` |
| Microsoft (MCR) | `docker pull localhost:5000/mcr.microsoft.com/hello-world:latest` |
| Quay.io | `docker pull localhost:5000/quay.io/prometheus/node-exporter:latest` |

---

## Federation Policy

Control what gets shared. Proprietary images stay private by default.

| Image Source | Default | Override |
|---|---|---|
| Pulled from upstream (Docker Hub, etc.) | **Shared** via federation | `share_upstream_images: false` |
| Pushed to registry | **Private** | `share_pushed_images: true` |
| Pushed to `public/` namespace | **Shared** via federation | Change `public_namespace` |

### Private Swarm (Enterprise)

Run a private IPFS swarm so your nodes only peer with each other. Images replicate across regions and clouds without touching the public internet:

```yaml
federation:
  enabled: true
  topic: /mycompany-internal/oci-registry/v1
  share_pushed_images: true       # Safe — private swarm only
  share_upstream_images: true
```

Works across AWS, GCP, Azure, on-prem — doesn't matter. See [ARCHITECTURE.md](docs/ARCHITECTURE.md) for the gateway pattern and deployment diagrams.

---

## Push Your Own Images

```bash
# Private by default
docker tag myapp:v1 localhost:5000/myapp:v1
docker push localhost:5000/myapp:v1

# Opt in to sharing via federation
docker tag mytools:v1 localhost:5000/public/mytools:v1
docker push localhost:5000/public/mytools:v1
```

---

## Deploy to Kubernetes

```bash
# Basic
helm install registry ./charts/oci-ipfs-registry \
  --set ipfs.deploy.enabled=true

# Full decentralization (registry + IPFS on every node)
helm install registry ./charts/oci-ipfs-registry \
  --set registry.mode=daemonset \
  --set ipfs.deploy.enabled=true \
  --set ipfs.deploy.mode=daemonset
```

| Mode | Registry | IPFS | Use Case |
|------|----------|------|----------|
| Standard | Deployment | External | Existing IPFS infrastructure |
| P2P Storage | Deployment | DaemonSet | P2P distribution within cluster |
| Full P2P | DaemonSet | DaemonSet | No central server, maximum resilience |

Cloud-specific values files for AWS EKS, Google GKE, and Azure AKS included.

---

## Production Ready

- **Health checks** — `/healthz` (liveness) + `/readyz` (checks BoltDB + IPFS)
- **Auth** — Optional basic auth with env var expansion for secrets
- **Rate limiting** — Per-IP with configurable burst
- **Tag TTL** — Stale-while-revalidate; serves cache if upstream is down
- **GC** — Background cleanup with IPFS unpin, dry-run mode
- **Upload safety** — Per-upload mutex for concurrent chunk uploads
- **Prometheus metrics** — 18 custom metrics (HTTP, IPFS ops, federation, cache hit/miss, upstream, GC, storage gauges)

See [`config.example.yaml`](config.example.yaml) for all options.

---

## Testing

```bash
go test ./... -v
```

60+ tests covering storage, handlers, health, auth, rate limiting, tag TTL, metrics, config, and upstream client.

---

## Roadmap

- [x] Pull-through proxy (7 registries)
- [x] Federation via IPFS pubsub
- [x] Federation policy (private by default)
- [x] Private swarm support
- [x] Prometheus metrics
- [x] Health endpoints, auth, rate limiting, GC
- [x] Tag TTL with stale-while-revalidate
- [x] Helm chart (Deployment + DaemonSet modes)
- [ ] Web UI for browsing images
- [ ] Signature verification (cosign/notation)
- [ ] S3-compatible storage backend
- [ ] Cluster mode with shared state (etcd)
- [ ] Transparent proxy mode (containerd mirror config generator)

---

## Contributing

Contributions welcome! See [ARCHITECTURE.md](docs/ARCHITECTURE.md) for design details.

```bash
git clone https://github.com/fbongiovanni29/ipfs-oci-registry
cd ipfs-oci-registry
go mod download
go build ./cmd/registry
```

---

## License

MIT — see [LICENSE](LICENSE).

---

## Lessons Learned

We built a fully functional OCI registry backed by IPFS — federation, pull-through proxy for 7 registries, Prometheus metrics, the works. Then we benchmarked it.

### IPFS is slow for large blobs

| Image | Size | IPFS Cache | Docker Hub Direct |
|-------|------|-----------|-------------------|
| alpine | 3.5MB | **249ms** | 1985ms |
| nginx | 70MB | 2354ms | **677ms** |
| golang | 300MB | 9348ms | **771ms** |

IPFS wins for tiny images where network round-trip dominates. For anything real-world, it's 3-12x slower than a CDN. IPFS Bitswap exchanges 256KB blocks with per-block negotiation overhead — it was designed for DAG traversal, not streaming large sequential files.

### The speed story requires scale that doesn't exist yet

The pitch was "images get faster as more people use them." That's true in theory — Bitswap can pull blocks from multiple peers simultaneously. But with 1 seeder, you have 1 pipe, same as HTTP. The network effect only kicks in with many seeders, and building that network is a chicken-and-egg problem.

### The value prop didn't survive contact with reality

- **"Eliminates rate limits"** — So does any caching proxy. Harbor, registry mirrors, even a simple nginx cache.
- **"P2P across organizations"** — Technically unique, but nobody's asking for it. Companies share public images by pulling from Docker Hub. It works.
- **"No central server"** — Most teams are fine with one. The operational overhead of running IPFS nodes outweighs the resilience benefit.
- **"Works offline"** — True, but so does any cache. The differentiator is P2P offline without a central server — a real but very niche use case (edge/field deployments).

### What would actually work

**BitTorrent with DHT** is likely the right transport for decentralized container image distribution. It's designed for large file transfer, proven at internet scale, truly decentralized (no tracker needed with DHT), and has mature Go libraries. Kraken (Uber) already proved BitTorrent works for container images — they just bolted a centralized tracker on top. Remove the tracker, use DHT, and you'd get Kraken's speed without the central dependency.

### What we'd keep

The architecture is sound — the OCI handler, federation policy, digest→content-ID mapping, GitOps deployment model. If someone wanted to build a BitTorrent-backed registry, most of this codebase would carry over. The IPFS transport layer is the part to replace.

### Was it worth building?

Yes. The code works. The tests pass. The architecture docs are solid. And we now know, with benchmarks to prove it, exactly why IPFS isn't the right tool for this job — which is more useful than speculating about it.

---

**Stop depending on centralized registries. Start distributing containers the way the internet was meant to work.**
