# Upstream Collector Contract

The Brygge VM ships telemetry to an upstream OTel collector. This page documents the contract that upstream is expected to satisfy. Hand it to whoever runs that collector (typically a home/central Kubernetes cluster).

## Transport

- **Protocol:** OTLP over **gRPC** (not HTTP).
- **Network path:** a tailnet controlled by Headscale — the VM joins via `tailscale up --login-server=<headscale-url> --advertise-tags=tag:downstream`; the cluster collector is reached at its tailnet (MagicDNS) name, directly or through a subnet route another node advertises.
- **Name resolution:** the VM resolves the endpoint by forwarding the tailnet zone (`tailnet_domain` in the VM's tfvars) from its local `unbound` to MagicDNS at `100.100.100.100`. The VM does not accept tailnet DNS settings, so it has no `search` domain — the endpoint must be a **fully qualified** name inside that zone. If the Headscale `base_domain` changes, the VM operator must update `tailnet_domain`. See [local-collector.md § Name resolution](local-collector.md#name-resolution).
- **ACL:** `tag:downstream` should be allowed exactly one outbound flow: the collector's IP on port 4317. No other ports, subnet hosts, or tailnet nodes.
- **TLS:** Sender uses `tls.insecure = true` and relies on WireGuard for transport encryption. The cluster receiver does **not** need to terminate TLS on this listener; a plaintext gRPC OTLP receiver bound to the tailnet interface is fine.
- **Port:** `4317` (standard OTLP gRPC).
- **Endpoint format expected:** `host:port` with no scheme, where `host` is the FQDN, e.g. `otel.tail.example.net:4317`.
- **Delivery:** the sender retries for up to 10 minutes with a 1000-batch in-memory queue. A longer outage drops data; there is no on-disk buffer.

## Authentication

- **Mechanism:** Bearer token passed in gRPC metadata header `Authorization`.
- **Header value format:** `Bearer <token>` (the literal string, sent verbatim).
- The cluster collector should require this header (e.g. via the `bearertokenauth` extension or equivalent) and reject unauthenticated streams.

## Signals sent

Three pipelines, all routed to the same upstream exporter:

| Signal | Sources |
|--------|---------|
| **metrics** | `hostmetrics` (CPU, memory, disk, filesystem, load, network, paging, processes — 30s interval) + OTLP from `brygge.service` (app-level metrics) |
| **traces** | OTLP from `brygge.service` |
| **logs** | OTLP from `brygge.service` + `journald` for `brygge`, `stalwart`, `caddy`, `postgresql`, `podman-bulwark` |

Resource attributes added before export:

- `host.name` = VM hostname (e.g. `brygge`)
- `service.namespace` = club domain (e.g. `klokkarvikbaatlag.no`)
- Plus standard `system` / `env` resourcedetection attributes.

Batched at 10s / 1024 records.

## What the cluster needs to provide

1. A gRPC OTLP receiver reachable at a fully qualified tailnet DNS name on port 4317, plus the tailnet suffix for the VM's `tailnet_domain`.
2. Bearer-token auth on that receiver, with a token issued for the Brygge VM.
3. Routing/storage downstream for all three signals (metrics, traces, logs).

## What gets handed to the VM operator

Two values, written to `/etc/otel/env` on the VM (root-only, 0400):

```
OTLP_ENDPOINT=<collector>.<tailnet-domain>:4317
OTLP_AUTH_HEADER=Bearer <token>
```

And one non-secret value for the VM's tfvars: `tailnet_domain` = the tailnet suffix.

That's the entire contract. Rotating the token is just rewriting that file and `systemctl restart opentelemetry-collector`.
