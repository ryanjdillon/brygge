# OpenTelemetry

Brygge exports metrics, traces, and logs via OTLP. The SDK lives in the Go API; on the production VM a local OpenTelemetry Collector receives that data, enriches it with host metrics + journald logs, and forwards everything to an upstream collector over Tailscale.

## Pages in this section

| Page | Read when you need to know… |
|------|-----------------------------|
| [instrumentation.md](instrumentation.md) | What signals the Brygge API emits (HTTP, DB pool, outbound traces, resource attributes) |
| [app-config.md](app-config.md) | How to point the Brygge API at a collector via env vars (`OTEL_EXPORTER_OTLP_*`) |
| [local-collector.md](local-collector.md) | The OTel Collector running on the production VM — what it scrapes, how it's wired in NixOS |
| [upstream-contract.md](upstream-contract.md) | Contract the VM expects from the upstream collector (transport, auth, signals). Hand to a cluster-side operator |
| [docker-collector.md](docker-collector.md) | Self-contained docker-compose example collector with Prometheus + Grafana (for dev/standalone deploys) |

## Quick orientation

- **Local dev:** spin up the docker-compose collector from [docker-collector.md](docker-collector.md), point `OTEL_EXPORTER_OTLP_ENDPOINT` at it.
- **Production:** the NixOS VM already runs a collector on `127.0.0.1:4317` (see [local-collector.md](local-collector.md)). The Brygge API is wired to it via `services.brygge.extraEnvironment` in `nix/host.nix`. Forwarding to the home cluster requires `/etc/otel/env` and Tailscale (see [upstream-contract.md](upstream-contract.md)).

See also: [../developer/configuration.md](../developer/configuration.md) · [../developer/deploy.md](../developer/deploy.md)
