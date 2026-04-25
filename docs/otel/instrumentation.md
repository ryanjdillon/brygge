# OTel Instrumentation

What the Brygge API emits over OTLP. Implementation lives in `backend/internal/observability/`.

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

## Outbound HTTP traces

All external HTTP clients are wrapped with `otelhttp.NewTransport`, creating child spans for:

- **Dendrite** (Matrix forum proxy) — 15s timeout
- **Yr.no** (weather API) — 10s timeout
- **Vipps** (OAuth/payment) — 15s timeout
- **Anthropic** (AI document processing) — default timeout
- **SMTP** (email delivery to Stalwart on localhost) — 10s timeout

Each span includes `http.method`, `http.url`, `http.status_code`, and propagates W3C trace context headers.

## Default resource attributes

The SDK automatically sets:

| Attribute | Value |
|-----------|-------|
| `service.name` | `brygge-api` |
| `service.version` | `1.0.0` |

Additional attributes can be added via `OTEL_RESOURCE_ATTRIBUTES` — see [app-config.md](app-config.md).

## Graceful degradation

If the collector is unreachable at startup, Brygge logs a warning and continues without telemetry:

```
WRN failed to initialize OpenTelemetry — metrics and tracing disabled
```

The app functions normally — no metrics or traces are exported, but no errors are raised on requests. On shutdown, the SDK flushes any buffered telemetry with a 5-second timeout.
