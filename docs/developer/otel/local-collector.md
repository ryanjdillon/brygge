# Local Collector (production VM)

The production NixOS VM runs an OpenTelemetry Collector (contrib distribution) as a system service. It has three jobs:

1. **Scrape VM-level signals** — host metrics (CPU, memory, disk, network, processes) and journald logs from key services.
2. **Receive OTLP from the Brygge API** on `127.0.0.1:4317` (gRPC) and `127.0.0.1:4318` (HTTP).
3. **Forward everything** to an upstream collector (typically a home/central cluster) over the tailnet (a Headscale-controlled Tailscale network).

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
OTLP_ENDPOINT=<collector>.<tailnet_domain>:4317
OTLP_AUTH_HEADER=Bearer <token>
```

The endpoint host must be a **fully qualified** tailnet (MagicDNS) name under `tailnet_domain` — see [Name resolution](#name-resolution). `tls.insecure = true` because the path runs over the tailnet's WireGuard tunnel. The bearer token rides in gRPC metadata regardless.

Retry and queueing are bounded and memory-only:

| Setting | Value |
|---------|-------|
| `retry_on_failure` | 5s initial, 60s max interval, give up after 10m |
| `sending_queue` | 2 consumers, 1000 batches (the default, set explicitly) |

There is no persistent queue: an upstream outage longer than the retry window, or one that fills the queue, **drops data**. A multi-hour outage leaves a gap upstream.

For the upstream's side of the contract — what cluster operators need to provide — see [upstream-contract.md](upstream-contract.md).

## Pipelines

```
metrics: [hostmetrics, otlp] → [resourcedetection, resource, batch] → [otlp/upstream]
traces:  [otlp]               → [resourcedetection, resource, batch] → [otlp/upstream]
logs:    [otlp, journald]     → [resourcedetection, resource, batch] → [otlp/upstream]
```

## Name resolution

The host's only nameserver is a local `unbound` that recurses from the root (Stalwart's Spamhaus DNSBL lookups must come from the host's own IP). A recursive resolver can't see tailnet names, so `unbound` forwards **only** the tailnet zone to the tailnet client's MagicDNS stub at `100.100.100.100`; every other name still recurses.

- The zone is `clubConfig.tailnetDomain`, from `tailnet_domain` in `terraform/terraform.tfvars.json` (see [deploy.md](../deploy.md#1-configure-the-club)). Unset or empty → no forward-zone, and the upstream endpoint won't resolve.
- The zone is `domain-insecure`: MagicDNS answers are unsigned, and DNSSEC validation would otherwise reject them.
- `infra-keep-probing` keeps `unbound` retrying `100.100.100.100`. Queries at boot can arrive before `tailscaled` is up; without it `unbound` marks the address down for ~15 minutes. Expect a brief SERVFAIL window at boot until `tailscaled` answers — the exporter's retry covers it.
- The tailnet client runs with `--accept-dns=false` (via `services.tailscale.extraSetFlags`), so `/etc/resolv.conf` lists only `127.0.0.1` / `::1` and has no tailnet `search` line.

## Self-telemetry and log volume

The collector's own logs go to journald, so they compete with every other unit for the journal's `SystemMaxUse` (500M) cap.

| Setting | Value | Why |
|---------|-------|-----|
| `telemetry.logs.level` | `warn` | Info-level startup/shutdown chatter isn't useful on the host |
| `telemetry.logs.disable_stacktrace` | `true` | **Load-bearing.** Each export error otherwise carries a ~20-frame stack trace, one journal entry per frame |
| `telemetry.logs.sampling` | 5 per 60s, then 1 in 500 | Explicit; the collector samples by default |
| `LogRateLimitIntervalSec` / `LogRateLimitBurst` (unit) | 60s / 600 | journald backstop independent of the collector's settings |
| `telemetry.metrics` | `basic`, Prometheus pull reader on `127.0.0.1:8888` | Exporter health on the host. Nothing scrapes it into a pipeline, so it can't loop |

## Systemd integration

```nix
systemd.services.opentelemetry-collector = {
  after = [ "tailscaled.service" "nss-lookup.target" ];
  serviceConfig = {
    EnvironmentFile = "/etc/otel/env";
    LogRateLimitIntervalSec = "60s";
    LogRateLimitBurst = 600;
  };
};
```

`after` keeps the first export from failing on DNS before the tailnet client and resolver are up. The NixOS module already adds the `systemd-journal` supplementary group the journald receiver needs, and validates the generated config with `otelcol validate` at build time.

## Operating

| Action | Command |
|--------|---------|
| Restart | `systemctl restart opentelemetry-collector` |
| Logs | `journalctl -u opentelemetry-collector -f` |
| Rotate upstream token | Edit `/etc/otel/env`, then restart the unit |
| Verify receivers | `ss -tlnp \| grep -E '4317\|4318\|8888'` |
| Endpoint resolves | `dig @127.0.0.1 <endpoint FQDN>` |
| Export health | `curl -s 127.0.0.1:8888/metrics \| grep otelcol_exporter_` — `sent_*` rising, `send_failed_*` flat |
| Log volume | `journalctl -u opentelemetry-collector --since -1h \| wc -l` — near 0 when healthy |

The host must be a tailnet node. Join it once after first deploy:

```bash
tailscale up --login-server=<headscale-url> --advertise-tags=tag:downstream
```

Login and tag flags are `tailscale up`-only, so they aren't in the Nix config; `--accept-dns=false --accept-routes=true` are reapplied by `tailscaled-set` on every activation. The Headscale ACL for `tag:downstream` should allow exactly one outbound flow from this node — to the upstream collector's OTLP port — so a compromised public VM can't pivot inward.

## Common misses

- **The local resolver overrides tailnet DNS.** `services.unbound.resolveLocalQueries` sets `networking.resolvconf.useLocalResolver`, so openresolv *prepends* `127.0.0.1` / `::1` to `/etc/resolv.conf`. The tailnet client can only *append* `100.100.100.100`, and glibc never asks the next nameserver after `unbound` answers NXDOMAIN. The collector then gets zero addresses for the endpoint and logs `no children to pick from` on every export. The forward-zone is the fix; `--accept-dns=true` can't be.
- **`services.tailscale.extraUpFlags` is dead config without `authKeyFile`.** Only `tailscaled-autoconnect` applies it, and that unit exists only when an auth key file is set. Use `extraSetFlags` for flags `tailscale set` accepts, and run `up`-only flags by hand.
- **Short endpoint names don't resolve.** With `--accept-dns=false` there is no tailnet `search` domain; `OTLP_ENDPOINT` must be the FQDN.
- **A changed tailnet suffix** (Headscale `base_domain`) needs the matching `tailnet_domain` in tfvars and a redeploy, or the endpoint stops resolving.
