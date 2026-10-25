# OTel Instrumentation

What the Brygge API emits over OTLP. SDK setup (resource, exporters, sampler) lives in `backend/internal/telemetry/`; the instruments live next to the code they measure (`internal/middleware/metrics.go`, `internal/telemetry/pgx.go`, `internal/email/instrument.go`). Every instrument uses the meter `otel.Meter("brygge-api")`.

Prometheus names below assume the upstream `prometheusremotewrite` exporter's default suffixing (unit suffix added, `_total` on counters, `{…}` annotation units dropped). The series carry `job` = `<service.namespace>/<service.name>`, e.g. `klokkarvikbaatlag.no/brygge-api`.

## HTTP server metrics (middleware)

| Metric | Type | Attributes |
|--------|------|------------|
| `http.server.request.count` | Counter | method, route, status_code |
| `http.server.request.duration` | Histogram (seconds) | method, route |
| `http.server.active_requests` | UpDownCounter | — |

## Database pool metrics (pgx)

| Metric | Type | Description |
|--------|------|-------------|
| `db.pool.connections.active` | Gauge | In-use connections |
| `db.pool.connections.idle` | Gauge | Idle connections |
| `db.pool.connections.total` | Gauge | Total connections |
| `db.pool.connections.max` | Gauge | Pool max size |
| `db.pool.acquire.count` | Counter | Cumulative acquisitions |
| `db.pool.acquire.duration_seconds` | Gauge | Cumulative acquire time |
| `db.pool.empty_acquire.count` | Counter | Acquisitions when pool was empty |

## App email sends

| Metric | Prometheus name | Type | Attributes |
|--------|-----------------|------|------------|
| `brygge.email.send` (`{message}`) | `brygge_email_send_total` | Counter (cumulative) | `kind` = `magic_link` \| `invoice` \| `invoice_reminder` \| `other`; `result` = `ok` \| `error` |

Counted by the `email.Instrument` decorator around the SMTP sender, once per hand-off to the SMTP relay (Stalwart). `kind` comes from `email.WithKind(ctx, …)` at the call site; untagged sends count as `other`. All eight `kind` × `result` series are created at 0 on startup so `increase()` sees the first error. Recipients, subjects and error text are never attributes — the error itself is in the `brygge.service` journal.

What is **not** counted: broadcast mail (inbox compose and bulk send) goes out through JMAP as the sending mailbox, not through `email.Sender`. A `result="ok"` only means the relay accepted the message; remote deferrals and bounces show up in Stalwart's queue and journal, not here.

## Outbound HTTP traces

These external HTTP clients are wrapped with `otelhttp.NewTransport`, creating child spans:

- **Dendrite** (Matrix forum proxy) — 15s timeout
- **Yr.no** (weather API) — 10s timeout
- **Anthropic** (AI document processing) — default timeout

Each span includes `http.method`, `http.url`, `http.status_code`, and propagates W3C trace context headers. The Vipps client, the Stalwart admin/JMAP clients and SMTP are not traced.

## Default resource attributes

`telemetry.Setup` sets:

| Attribute | Value |
|-----------|-------|
| `service.name` | `brygge-api` — the `ServiceName` passed in `cmd/api/main.go` |
| `service.version` | `1.0.0` |
| `club.slug` | from `CLUB_SLUG` env (e.g. `kbl`) — short identifier for upstream filtering |
| `club.domain` | from `DOMAIN` env (e.g. `klokkarvikbaatlag.no`) — full domain when slugs might collide across tenants |

The SDK merges `OTEL_RESOURCE_ATTRIBUTES` (and `OTEL_SERVICE_NAME`) underneath these, so the code's values win on conflict: changing `OTEL_SERVICE_NAME` has no effect, while other keys from `OTEL_RESOURCE_ATTRIBUTES` are added — see [app-config.md](app-config.md). On the production VM the local collector adds `service.namespace` = club domain ([local-collector.md](local-collector.md)).

## Trace sampling

Traces use a parent-based ratio sampler (`ParentBased(TraceIDRatioBased(ratio))`). The default ratio is **0.1** (10% of root traces sampled; child spans inherit the parent decision). Override at runtime by setting `OTEL_TRACES_SAMPLER_ARG` to any value in `[0.0, 1.0]`. Metrics are pre-aggregated and not sampled.

## Graceful degradation

If the collector is unreachable at startup, Brygge logs a warning and continues without telemetry:

```
WRN failed to initialize OpenTelemetry — metrics and tracing disabled
```

The app functions normally — no metrics or traces are exported, but no errors are raised on requests. On shutdown, the SDK flushes any buffered telemetry with a 5-second timeout.
