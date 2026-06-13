# OTel App Configuration

How to point the Brygge API at a collector. The OTel SDK reads standard environment variables — no Brygge-specific config is needed.

## Environment variables

```bash
# Required: point to your OTel collector
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317

# Optional: transport protocol (default: grpc)
OTEL_EXPORTER_OTLP_PROTOCOL=grpc

# Optional: extra resource attributes (comma-separated key=value pairs)
OTEL_RESOURCE_ATTRIBUTES=deployment.environment=production,service.namespace=brygge

# Optional: trace sampling — code default is 0.1 (10%) with parent-based
# inheritance. Bump to 1.0 during incident debugging if needed.
OTEL_TRACES_SAMPLER_ARG=0.1
```

## Where to set them

| Deployment | Where |
|------------|-------|
| Local dev / docker-compose | `deploy/.env` or `environment:` block of the `api` service in `deploy/docker-compose.yml` |
| NixOS VM | `services.brygge.extraEnvironment` in `nix/host.nix` (already wired to the local collector at `http://127.0.0.1:4317`) |

## NixOS production defaults

The VM's `nix/host.nix` sets:

```nix
services.brygge.extraEnvironment = {
  OTEL_EXPORTER_OTLP_ENDPOINT = "http://127.0.0.1:4317";
  OTEL_EXPORTER_OTLP_PROTOCOL = "grpc";
  OTEL_SERVICE_NAME = "brygge-api";
  OTEL_TRACES_SAMPLER_ARG = "0.1";
};
```

This ships telemetry to the local collector — see [local-collector.md](local-collector.md) for what happens next.
