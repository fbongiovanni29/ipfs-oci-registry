# OCI IPFS Registry Helm Chart

A Helm chart for deploying the OCI IPFS Registry - a decentralized container registry with IPFS backend.

## Prerequisites

- Kubernetes 1.23+
- Helm 3.0+
- IPFS node (Kubo) - can be deployed by this chart or external

## Installation

### Basic Installation (with bundled IPFS)

```bash
# Deploy registry + IPFS together
helm install registry ./charts/oci-ipfs-registry \
  --set ipfs.deploy.enabled=true
```

### P2P Mode (IPFS DaemonSet)

For true P2P within the cluster, deploy IPFS on every node:

```bash
helm install registry ./charts/oci-ipfs-registry \
  --set ipfs.deploy.enabled=true \
  --set ipfs.deploy.mode=daemonset
```

This creates:
- IPFS DaemonSet (one pod per node)
- Headless service for peer discovery
- Automatic cluster peering

### External IPFS

```bash
helm install registry ./charts/oci-ipfs-registry \
  --set ipfs.apiUrl=http://my-ipfs:5001
```

### Cloud Provider Configurations

Pre-configured values files are provided for major cloud providers:

**AWS EKS:**
```bash
helm install registry ./charts/oci-ipfs-registry \
  -f values-eks.yaml \
  --set ingress.hosts[0].host=registry.internal.yourcompany.com
```

**Google GKE:**
```bash
helm install registry ./charts/oci-ipfs-registry \
  -f values-gke.yaml \
  --set ingress.hosts[0].host=registry.internal.yourcompany.com
```

**Azure AKS:**
```bash
helm install registry ./charts/oci-ipfs-registry \
  -f values-aks.yaml \
  --set ingress.hosts[0].host=registry.internal.yourcompany.com
```

## Configuration

### Registry Settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of registry replicas | `1` |
| `image.repository` | Registry image repository | `oci-ipfs-registry` |
| `image.tag` | Registry image tag | `appVersion` |
| `service.type` | Kubernetes service type | `ClusterIP` |
| `service.port` | Service port | `5000` |
| `ingress.enabled` | Enable ingress | `false` |
| `persistence.enabled` | Enable persistent storage | `true` |
| `persistence.size` | PVC size | `10Gi` |

### IPFS Settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `ipfs.apiUrl` | External IPFS API endpoint | `http://ipfs:5001` |
| `ipfs.pinContent` | Pin content in IPFS | `true` |
| `ipfs.deploy.enabled` | Deploy IPFS with this chart | `false` |
| `ipfs.deploy.mode` | `deployment` or `daemonset` | `deployment` |
| `ipfs.deploy.image.tag` | Kubo version | `v0.27.0` |
| `ipfs.deploy.persistence.enabled` | Persist IPFS data | `true` |
| `ipfs.deploy.persistence.size` | Storage size (deployment mode) | `20Gi` |
| `ipfs.deploy.persistence.hostPath` | Host path (daemonset mode) | `/var/lib/ipfs` |

### Federation Settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `federation.enabled` | Enable IPFS pubsub federation | `true` |
| `federation.topic` | Pubsub topic | `/oci-registry/v1/mappings` |

### Advanced / Production Settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `extraEnv` | Extra environment variables | `[]` |
| `extraVolumes` | Extra volumes to add | `[]` |
| `extraVolumeMounts` | Extra volume mounts | `[]` |
| `podLabels` | Extra labels for pods | `{}` |
| `priorityClassName` | Pod priority class | `""` |
| `topologySpreadConstraints` | Topology spread for HA | `[]` |
| `livenessProbe.*` | Liveness probe settings | see values.yaml |
| `readinessProbe.*` | Readiness probe settings | see values.yaml |
| `networkPolicy.enabled` | Enable NetworkPolicy | `false` |
| `upstreamCredentialsSecret` | Secret name for upstream creds | `""` |

### Example: Production Configuration

```yaml
# values-production.yaml
replicaCount: 3

priorityClassName: high-priority

podLabels:
  sidecar.istio.io/inject: "true"

topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: topology.kubernetes.io/zone
    whenUnsatisfiable: DoNotSchedule
    labelSelector:
      matchLabels:
        app.kubernetes.io/name: oci-ipfs-registry

networkPolicy:
  enabled: true
  ingressNamespaceSelector:
    kubernetes.io/metadata.name: production

extraEnv:
  - name: LOG_LEVEL
    value: "info"

# Reference credentials from K8s Secret
upstreamCredentialsSecret: upstream-registry-creds
```

### Example: Mount Custom CA Certificates

```yaml
extraVolumes:
  - name: ca-certs
    secret:
      secretName: custom-ca-bundle

extraVolumeMounts:
  - name: ca-certs
    mountPath: /etc/ssl/certs/custom-ca.crt
    subPath: ca.crt
    readOnly: true
```

See `values.yaml` for full configuration options.

## Architecture

### Standard Mode (Registry Deployment + IPFS Deployment)
```
┌──────────────────────────────────────────────────────────┐
│                      Kubernetes                          │
│  ┌─────────────────┐      ┌─────────────────────────┐   │
│  │  OCI Registry   │◄────►│  IPFS (Deployment)      │   │
│  │  (Deployment)   │      │                         │   │
│  └────────┬────────┘      └─────────────────────────┘   │
│           │                                              │
│           ▼                                              │
│  ┌─────────────────┐                                    │
│  │  Ingress/LB     │                                    │
│  └─────────────────┘                                    │
└──────────────────────────────────────────────────────────┘
```

### P2P Mode (Registry Deployment + IPFS DaemonSet)
```
┌──────────────────────────────────────────────────────────┐
│                      Kubernetes                          │
│                                                          │
│  ┌─────────────────┐                                    │
│  │  OCI Registry   │◄─────────┐                         │
│  │  (Deployment)   │          │                         │
│  └─────────────────┘          ▼                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │              IPFS DaemonSet                      │   │
│  │  ┌─────────┐   ┌─────────┐   ┌─────────┐        │   │
│  │  │ Node A  │◄─►│ Node B  │◄─►│ Node C  │        │   │
│  │  │  IPFS   │   │  IPFS   │   │  IPFS   │        │   │
│  │  └─────────┘   └─────────┘   └─────────┘        │   │
│  │         Auto-peering via headless service        │   │
│  └─────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────┘
```

### Full Decentralized Mode (Registry DaemonSet + IPFS DaemonSet)
```
┌──────────────────────────────────────────────────────────┐
│                      Kubernetes                          │
│                                                          │
│  ┌─────────────────────────────────────────────────┐   │
│  │           Registry + IPFS DaemonSets             │   │
│  │                                                   │   │
│  │  ┌────────────────┐  ┌────────────────┐          │   │
│  │  │    Node A      │  │    Node B      │          │   │
│  │  │  ┌──────────┐  │  │  ┌──────────┐  │          │   │
│  │  │  │ Registry │  │  │  │ Registry │  │          │   │
│  │  │  └────┬─────┘  │  │  └────┬─────┘  │   ...    │   │
│  │  │       │        │  │       │        │          │   │
│  │  │  ┌────▼─────┐  │  │  ┌────▼─────┐  │          │   │
│  │  │  │   IPFS   │◄─┼──┼─►│   IPFS   │  │          │   │
│  │  │  └──────────┘  │  │  └──────────┘  │          │   │
│  │  └────────────────┘  └────────────────┘          │   │
│  │                                                   │   │
│  │  Tags synced via IPFS pubsub (eventual consistency) │
│  │  Blobs shared via IPFS content addressing           │
│  └─────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────┘
```

## Using the Registry

Once deployed with proper DNS/TLS:

```bash
# Pull through proxy
docker pull registry.yourcompany.com/docker.io/library/nginx:latest

# Push local images
docker tag myapp:v1 registry.yourcompany.com/myapp:v1
docker push registry.yourcompany.com/myapp:v1

# In Kubernetes pod specs
spec:
  containers:
  - name: app
    image: registry.yourcompany.com/docker.io/library/nginx:latest
```

## Deployment Modes

### Registry Mode

The registry itself can run as a Deployment (default) or DaemonSet:

| Mode | Command | Use Case |
|------|---------|----------|
| Deployment | `--set registry.mode=deployment` | Standard HA setup, shared storage |
| DaemonSet | `--set registry.mode=daemonset` | True decentralization, no central server |

**Registry DaemonSet mode** provides true decentralization:
- Each node runs its own registry instance
- Tag updates are synchronized via IPFS pubsub (eventual consistency)
- No single point of failure
- Best combined with IPFS DaemonSet mode

```bash
# Full P2P mode: registry + IPFS on every node
helm install registry ./charts/oci-ipfs-registry \
  --set registry.mode=daemonset \
  --set ipfs.deploy.enabled=true \
  --set ipfs.deploy.mode=daemonset
```

### IPFS Deployment Modes

Set `ipfs.deploy.enabled=true` to deploy IPFS with the chart:

| Mode | Command | Use Case |
|------|---------|----------|
| Single instance | `--set ipfs.deploy.mode=deployment` | Dev/test, small clusters |
| DaemonSet (P2P) | `--set ipfs.deploy.mode=daemonset` | Production, edge, large clusters |

**IPFS DaemonSet mode** provides true P2P within the cluster:
- Each node runs its own IPFS instance
- Nodes automatically discover and peer with each other
- Images cached on one node are available to all nodes via IPFS

### External IPFS

For existing IPFS infrastructure, just set `ipfs.apiUrl`:

```bash
helm install registry ./charts/oci-ipfs-registry \
  --set ipfs.apiUrl=http://your-ipfs:5001
```

## Uninstallation

```bash
helm uninstall registry
```

## License

Apache 2.0
