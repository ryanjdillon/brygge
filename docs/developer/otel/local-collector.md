# Local Collector (production VM)

The production NixOS VM runs an OpenTelemetry Collector (contrib distribution) as a system service. It has four jobs:

1. **Scrape VM-level signals** — host metrics (CPU, memory, disk, network, processes), certificate expiry, journal file sizes, and journald logs from key services.
2. **Count Stalwart journal events** that alerts need (remote rejections, DKIM failures, bounces) into metrics.
3. **Receive OTLP from the Brygge API** on `127.0.0.1:4317` (gRPC) and `127.0.0.1:4318` (HTTP).
4. **Forward everything** to an upstream collector (typically a home/central cluster) over the tailnet (a Headscale-controlled Tailscale network).

Configured in `nix/host.nix` via `services.opentelemetry-collector`.

## Receivers

| Receiver | Source |
|----------|--------|
| `hostmetrics` | CPU, memory, disk, filesystem, load, network, paging, processes — 30s interval. The filesystem scraper skips `tmpfs`/`devtmpfs`/`overlay`/`squashfs`/`ramfs` and also emits `system.filesystem.utilization` (off by default upstream) |
| `tls_check` | Certificate time left on `<domain>:443`, `mail.<domain>:443`, `mail.<domain>:465` — 10m interval. Caddy (443) and Stalwart load the certificate separately, so both are checked; Stalwart's listeners share one certificate, so 465 stands in for 993 (each probe also logs a Stalwart journal line) |
| `file_stats` | Size and mtime of `/var/log/journal/*/*.journal` — 5m interval |
| `otlp` | gRPC on `127.0.0.1:4317`, HTTP on `127.0.0.1:4318` (from Brygge API) |
| `journald` | Units: `brygge.service`, `stalwart.service`, `caddy.service`, `postgresql.service`, `podman-bulwark.service` |

## Processors

- `resourcedetection` — adds host-level attributes (`host.name`, OS, etc.) from `system` + `env` detectors.
- `resource` — adds:
  - `host.name` = `clubConfig.hostname`
  - `service.namespace` = `clubConfig.domain`
- `resource/host-identity` — inserts `service.name = brygge-host` on host metrics. The upstream Prometheus path builds `job` from `service.namespace/service.name` and leaves it unset without `service.name`.
- `transform/host-attributes` — copies the resource attributes `tlscheck.target` and `file.name` onto each data point (upstream, other resource attributes only reach `target_info`, so series would collide), and sets `file.size`'s unit to `By` (`b` would become a `_b` suffix).
- `filter/stalwart-signals`, `transform/stalwart-signals`, `transform/stalwart-gauges`, `resource/stalwart-identity` — the Stalwart signal chain, see [Stalwart journal signals](#stalwart-journal-signals).
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
metrics:                  [otlp]                              → [resourcedetection, resource, batch] → [otlp/upstream]
metrics/host:             [hostmetrics, tls_check, file_stats] → [resourcedetection, resource, resource/host-identity, transform/host-attributes, batch] → [otlp/upstream]
traces:                   [otlp]                              → [resourcedetection, resource, batch] → [otlp/upstream]
logs:                     [otlp, journald]                    → [resourcedetection, resource, batch] → [otlp/upstream]
logs/stalwart-signals:    [journald]                          → [filter/stalwart-signals, transform/stalwart-signals] → [count/stalwart]
metrics/stalwart-signals: [count/stalwart]                    → [resourcedetection, resource, resource/stalwart-identity, transform/stalwart-gauges, batch] → [otlp/upstream]
```

`journald` fans out: `logs` ships every record unchanged, and `logs/stalwart-signals` sees its own copy. Host metrics are kept out of `metrics` so `brygge-host` never touches the app's OTLP metrics.

## Stalwart journal signals

Stalwart's `journal` tracer writes each event as a journal record: `MESSAGE` is the event's description string, and each event key becomes an upper-cased field (`CODE`, `HOSTNAME`, `DKIMFAIL`, …). Those records are the only source for per-provider rejections and for DKIM results in DMARC reports, so the collector counts them:

1. `filter/stalwart-signals` drops everything except `stalwart.service` records whose `MESSAGE` is one of the pinned strings below. It drops non-map bodies first.
2. `transform/stalwart-signals` copies the body fields the counts are keyed on into attributes, because the `count` connector reads attributes only: `HOSTNAME` → `server.address` and `String(CODE)` → `smtp.code` on rejections; `kind` = `dsn_permfail` / `double_bounce` on bounces.
3. `count/stalwart` emits one delta sum per metric per batch.
4. `transform/stalwart-gauges` turns those delta sums into gauges (the upstream Prometheus path drops delta sums), and `resource/stalwart-identity` sets `service.name = stalwart`. Each point is "events in this batch": alert on `sum_over_time`, never `rate`.

| Metric (OTLP) | Prometheus name upstream | Attributes → labels | Counts records where |
|---|---|---|---|
| `stalwart.journal.remote_rejections` | `stalwart_journal_remote_rejections` | `server_address`, `smtp_code` | `MESSAGE == "Message rejected by remote server"`. Rejections from a failed connection or handshake have no `CODE` and count as `smtp_code="none"` |
| `stalwart.journal.dmarc_dkim_failures` | `stalwart_journal_dmarc_dkim_failures` | — | `MESSAGE == "DMARC report received with warnings"` and `Int(DKIMFAIL) > 0` (one per report, not per failure) |
| `stalwart.journal.dkim_signer_missing` | `stalwart_journal_dkim_signer_missing` | — | `MESSAGE == "DKIM signer not found"` |
| `stalwart.journal.bounces` | `stalwart_journal_bounces` | `kind` | `MESSAGE == "DSN permanent failure notification"` (`dsn_permfail`) or `"Discarding message after double bounce"` (`double_bounce`) |

All carry `job="<club domain>/stalwart"`. A series exists only after its first event; a quiet host sends nothing for these metrics.

The DMARC count needs Stalwart to parse incoming aggregate reports: `report.analysis.addresses = [ "postmaster@*" ]` in `services.stalwart.settings` (forwarding stays on). The `MESSAGE` strings are Stalwart 0.15.5's — a contract re-checked on every Stalwart upgrade, see [stalwart-internals.md § Journal events the collector counts](../mail/stalwart-internals.md#journal-events-the-collector-counts).

## Host metrics upstream

Host signals the alert rules use, as named in Prometheus by an exporter with default unit/type suffixing (`job="<club domain>/brygge-host"`; dots in attribute names become underscores):

| Metric (OTLP) | Prometheus name | Labels of note |
|---|---|---|
| `tlscheck.time_left` (s) | `tlscheck_time_left_seconds` | `tlscheck_target` (`host:port`) |
| `file.size` (By) | `file_size_bytes` | `file_name` |
| `file.mtime` (s) | `file_mtime_seconds` | `file_name` |
| `system.filesystem.utilization` (1) | `system_filesystem_utilization_ratio` | `mountpoint`, `device`, `type` |

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
tailscale up --login-server=<headscale-url> --advertise-tags=tag:downstream --accept-dns=false --accept-routes=true
systemctl restart tailscaled-set
```

Login and tag flags are `tailscale up`-only, so they aren't in the Nix config. `tailscale up` rejects a command that omits any non-default pref (and `--reset` would revert them), so it repeats the `extraSetFlags` values. Those are applied by `tailscaled-set` at boot and whenever `extraSetFlags` changes; prefs persist in tailscaled state. Restart `tailscaled-set` after any manual `tailscale up` so the Nix-declared prefs win. The Headscale ACL for `tag:downstream` should allow exactly one outbound flow from this node — to the upstream collector's OTLP port — so a compromised public VM can't pivot inward.

## Common misses

- **The local resolver overrides tailnet DNS.** `services.unbound.resolveLocalQueries` sets `networking.resolvconf.useLocalResolver`, so openresolv *prepends* `127.0.0.1` / `::1` to `/etc/resolv.conf`. The tailnet client can only *append* `100.100.100.100`, and glibc never asks the next nameserver after `unbound` answers NXDOMAIN. The collector then gets zero addresses for the endpoint and logs `no children to pick from` on every export. The forward-zone is the fix; `--accept-dns=true` can't be.
- **`services.tailscale.extraUpFlags` is dead config without `authKeyFile`.** Only `tailscaled-autoconnect` applies it, and that unit exists only when an auth key file is set. Use `extraSetFlags` for flags `tailscale set` accepts, and run `up`-only flags by hand.
- **Short endpoint names don't resolve.** With `--accept-dns=false` there is no tailnet `search` domain; `OTLP_ENDPOINT` must be the FQDN.
- **The collector name must be a MagicDNS record, not a split-DNS route.** With `--accept-dns=false`, `100.100.100.100` still answers MagicDNS peer names and Headscale `extra_records`, but not split-DNS routes. If the collector name is only served by a split-DNS nameserver it won't resolve here; make it an `extra_records` entry. Check with `tailscale dns status` — "Split DNS Routes" must not be what provides it.
- **An empty `OTLP_ENDPOINT=` line crash-loops the collector.** The `${env:OTLP_ENDPOINT:-…}` build-time default applies only when the variable is unset, not when it is set to empty.
- **A `count` condition that errors loses the batch.** `count/stalwart` returns the error and emits none of that batch's counts, and a `filter` error with `error_mode: ignore` *keeps* the record. Every condition in the Stalwart chain must evaluate cleanly on any record that reaches it: missing body keys compare as nil, `Int()` of a non-number is nil, and the filter drops non-map bodies before any field lookup.
- **A second Stalwart tracer can double the counts.** Only the module's default `tracer.journal` writes structured events to the journal. A `stdout` tracer lands there too, as whole formatted lines that don't equal the pinned `MESSAGE` strings; any tracer that writes the bare description would be counted twice.
- **`stalwart.journal.*` gauges are per-batch counts.** Use `sum_over_time(…[window])`; `rate`/`increase` assume a counter.
- **A changed tailnet suffix** (Headscale `base_domain`) needs the matching `tailnet_domain` in tfvars and a redeploy, or the endpoint stops resolving.
