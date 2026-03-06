# Kubernetes Portability Notes

Brygge is designed for Docker Compose on a single VPS, but the architecture has no Docker-specific dependencies. This document outlines how to migrate to Kubernetes if your deployment outgrows a single server.

---

## Traefik labels to IngressRoute CRDs

The Docker Compose setup uses Traefik labels for routing. These map directly to Traefik IngressRoute custom resources on Kubernetes. Traefik is also an official Kubernetes Ingress Controller, so the mental model carries over.

### Docker Compose label (current)

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.api.rule=Host(`example.com`) && PathPrefix(`/api`)
  - traefik.http.routers.api.entrypoints=websecure
  - traefik.http.routers.api.tls.certresolver=letsencrypt
  - traefik.http.services.api.loadbalancer.server.port=8080
```

### Equivalent Kubernetes IngressRoute

```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: brygge-api
spec:
  entryPoints:
    - websecure
  routes:
    - match: Host(`example.com`) && PathPrefix(`/api`)
      kind: Rule
      services:
        - name: brygge-api
          port: 8080
  tls:
    certResolver: letsencrypt
```

The `match` rule syntax is identical. Each set of Docker labels on a service becomes one IngressRoute manifest. Repeat for the frontend catch-all, Dendrite (`matrix.example.com`), Element (`element.example.com`), and Uptime Kuma (`status.example.com`).

TLS can alternatively be handled by cert-manager with Let's Encrypt ClusterIssuers instead of Traefik's built-in ACME resolver.

---

## Disabling go:embed for separate frontend deployment

By default, the Go binary embeds the Vue production build and serves it directly. This is ideal for single-binary deployment but may not suit Kubernetes architectures where the frontend is served by a CDN or separate Nginx pod.

The embed is controlled by a build tag:

| Build command                                     | Behavior                                |
|---------------------------------------------------|-----------------------------------------|
| `go build ./cmd/api`                              | Embeds frontend (default)               |
| `go build -tags noembed ./cmd/api`                | Empty filesystem; API only              |

With `noembed`, the API binary serves only `/api/*` routes. Deploy the Vue `dist/` directory separately using:

- An Nginx container or pod
- A CDN (Cloudflare Pages, Netlify, etc.)
- An S3 bucket with static website hosting

The relevant source files:

- `backend/embed.go` -- `//go:build !noembed` -- includes the embedded filesystem
- `backend/embed_noembed.go` -- `//go:build noembed` -- returns an empty filesystem

---

## Service container equivalents

| Docker Compose service | Kubernetes resource     | Notes                                              |
|------------------------|-------------------------|----------------------------------------------------|
| `api`                  | Deployment + Service    | Stateless; scale horizontally                      |
| `db` (PostgreSQL)      | StatefulSet + PVC       | Or use a managed database (e.g. Hetzner Managed PostgreSQL, CloudNativePG operator) |
| `redis`                | Deployment + Service    | Stateless in this use case (sessions, locks). A managed Redis or Dragonfly also works. |
| `dendrite`             | StatefulSet + PVC       | Requires persistent storage for media and state    |
| `element`              | Deployment + Service    | Static files; can also be served from a CDN        |
| `uptime-kuma`          | StatefulSet + PVC       | SQLite-based; needs persistent volume              |
| `traefik`              | DaemonSet or Deployment | Installed via the official Traefik Helm chart, or use any Ingress Controller |

### Persistent volumes

Services that need stable storage (PostgreSQL, Dendrite, Uptime Kuma) should use PersistentVolumeClaims backed by your cluster's storage class. On Hetzner Cloud, the `hcloud-csi` driver provides block storage volumes.

### Secrets

The `.env` file maps to Kubernetes Secrets. Reference them in pod specs via `envFrom` or individual `env` entries with `secretKeyRef`.

---

## Helm chart structure (suggested)

A Helm chart is not currently implemented. The following is guidance for someone building one.

```
charts/brygge/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── api-deployment.yaml
│   ├── api-service.yaml
│   ├── api-ingressroute.yaml
│   ├── db-statefulset.yaml
│   ├── db-service.yaml
│   ├── redis-deployment.yaml
│   ├── redis-service.yaml
│   ├── dendrite-statefulset.yaml
│   ├── dendrite-service.yaml
│   ├── dendrite-ingressroute.yaml
│   ├── element-deployment.yaml
│   ├── element-service.yaml
│   ├── element-ingressroute.yaml
│   ├── uptime-statefulset.yaml
│   ├── uptime-service.yaml
│   ├── uptime-ingressroute.yaml
│   ├── secrets.yaml
│   └── configmap.yaml
└── README.md
```

### values.yaml (key fields)

```yaml
domain: example.com

api:
  image: ghcr.io/your-org/brygge:latest
  replicas: 2
  embedFrontend: false   # Use noembed tag; serve frontend separately

db:
  enabled: true          # Set false if using managed PostgreSQL
  image: postgres:16-alpine
  storage: 10Gi

redis:
  enabled: true
  image: redis:7-alpine

dendrite:
  image: matrixdotorg/dendrite-monolith:latest
  storage: 5Gi

element:
  image: vectorim/element-web:latest

uptimeKuma:
  enabled: true
  storage: 1Gi

tls:
  certResolver: letsencrypt
  # Or use cert-manager:
  # clusterIssuer: letsencrypt-prod
```

### Considerations

- **Database**: For production Kubernetes deployments, a managed PostgreSQL service or the CloudNativePG operator is preferable to running PostgreSQL in a StatefulSet. It simplifies backups, failover, and upgrades.
- **Migrations**: Run as a Kubernetes Job that executes before the API Deployment rolls out. Use an init container or a Helm pre-upgrade hook.
- **Health checks**: The API exposes `GET /api/health` which returns service status for PostgreSQL and Redis. Use this as a readiness and liveness probe for the API pod.
- **Horizontal scaling**: The API is stateless (JWT auth, Redis for sessions). Multiple replicas behind a Service work without session affinity.
