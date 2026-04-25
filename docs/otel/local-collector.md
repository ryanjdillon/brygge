# Local Collector (production VM)

The production NixOS VM runs an OpenTelemetry Collector (contrib distribution) as a system service. It has two jobs:

1. **Scrape VM-level signals** — host metrics (CPU, memory, disk, network, processes) and journald logs from key services.
2. **Receive OTLP from the Brygge API** on `127.0.0.1:4317` (gRPC) and `127.0.0.1:4318` (HTTP).
3. **Forward everything** to an upstream collector (typically a home/central cluster) over Tailscale.

Configured in `nix/host.nix` via `services.opentelemetry-collector`.

## Receivers

| Receiver | Source |
|----------|--------|
| `hostmetrics` | CPU, memory, disk, filesystem, load, network, paging, processes — 30s interval |
| `otlp` | gRPC on `127.0.0.1:4317`, HTTP on `127.0.0.1:4318` (from Brygge API) |
| `journald` | Units: `brygge.service`, `stalwart.service`, `caddy.service`, `postgresql.service`, `podman-bulwark.service` |

## Processors

- `resourcedetection` — adds host-level attributes (`host.name`, OS, etc.) from `system` + `env` detectors.
- `resource` — adds:
  - `host.name` = `clubConfig.hostname`
  - `service.namespace` = `clubConfig.domain`
- `batch` — 10s timeout, 1024-record batches.

## Exporter

A single `otlp/upstream` exporter, gRPC, configured from `/etc/otel/env`:

```
OTLP_ENDPOINT=<host>:4317
OTLP_AUTH_HEADER=Bearer <token>
```

`tls.insecure = true` because the path runs over Tailscale's WireGuard tunnel. The bearer token rides in gRPC metadata regardless.

For the upstream's side of the contract — what cluster operators need to provide — see [upstream-contract.md](upstream-contract.md).

## Pipelines

```
metrics: [hostmetrics, otlp] → [resourcedetection, resource, batch] → [otlp/upstream]
traces:  [otlp]               → [resourcedetection, resource, batch] → [otlp/upstream]
logs:    [otlp, journald]     → [resourcedetection, resource, batch] → [otlp/upstream]
```

The collector's own internal-metrics endpoint is disabled (`telemetry.metrics.level = "none"`) to avoid an exporter loop.

## Systemd integration

```nix
systemd.services.opentelemetry-collector.serviceConfig = {
  EnvironmentFile = "/etc/otel/env";
  SupplementaryGroups = [ "systemd-journal" ];
};
```

`SupplementaryGroups` is required for the journald receiver to read the system journal.

## Operating

| Action | Command |
|--------|---------|
| Restart | `systemctl restart opentelemetry-collector` |
| Logs | `journalctl -u opentelemetry-collector -f` |
| Rotate upstream token | Edit `/etc/otel/env`, then restart the unit |
| Verify receivers | `ss -tlnp \| grep -E '4317\|4318'` |

The collector requires Tailscale to be up on the host (`services.tailscale.enable = true`). Run `tailscale up` once after first deploy to authenticate.
