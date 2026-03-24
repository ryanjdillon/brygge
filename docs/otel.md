# OpenTelemetry

Brygge exports metrics and traces via the OpenTelemetry SDK using OTLP gRPC. This gives visibility into request latency, error rates, database pool health, and outbound API calls.

---

## What's instrumented

### HTTP metrics (middleware)

| Metric | Type | Attributes |
|--------|------|------------|
| `http.server.request.count` | Counter | method, route, status_code |
| `http.server.request.duration` | Histogram (seconds) | method, route |
| `http.server.active_requests` | UpDownCounter | — |

### Database pool metrics (pgx)

| Metric | Type | Description |
|--------|------|-------------|
| `db.pool.connections.active` | Gauge | In-use connections |
| `db.pool.connections.idle` | Gauge | Idle connections |
| `db.pool.connections.total` | Gauge | Total connections |
| `db.pool.connections.max` | Gauge | Pool max size |
| `db.pool.acquire.count` | Counter | Cumulative acquisitions |
| `db.pool.acquire.duration_seconds` | Gauge | Cumulative acquire time |
| `db.pool.empty_acquire.count` | Counter | Acquisitions when pool was empty |

### Outbound HTTP traces

All external HTTP clients are wrapped with `otelhttp.NewTransport`, creating child spans for:

- **Dendrite** (Matrix forum proxy) — 15s timeout
- **Yr.no** (weather API) — 10s timeout
- **Vipps** (OAuth/payment) — 15s timeout
- **Anthropic** (AI document processing) — default timeout
- **Resend** (email delivery) — 10s timeout

Each span includes `http.method`, `http.url`, `http.status_code`, and propagates W3C trace context headers.

---

## Configuration

The OTEL SDK reads standard environment variables — no Brygge-specific config needed:

```bash
# Required: point to your OTEL collector
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317

# Optional: transport protocol (default: grpc)
OTEL_EXPORTER_OTLP_PROTOCOL=grpc

# Optional: add resource attributes
OTEL_RESOURCE_ATTRIBUTES=deployment.environment=production,service.namespace=brygge

# Optional: configure trace sampling (default: always_on)
OTEL_TRACES_SAMPLER=parentbased_traceidratio
OTEL_TRACES_SAMPLER_ARG=0.1
```

Add these to `deploy/.env` or set them in `deploy/docker-compose.yml` under the `api` service's `environment` block.

---

## Collector setup

Brygge sends telemetry to an OTEL collector via gRPC on port 4317. The collector can then forward to any backend (Prometheus, Grafana Cloud, Jaeger, etc.).

### Example: collector with Prometheus + Grafana

Add to your `docker-compose.yml`:

```yaml
otel-collector:
  image: otel/opentelemetry-collector-contrib:latest
  ports:
    - "4317:4317"   # OTLP gRPC
    - "8889:8889"   # Prometheus metrics endpoint
  volumes:
    - ./otel-collector-config.yaml:/etc/otelcol-contrib/config.yaml

prometheus:
  image: prom/prometheus:latest
  volumes:
    - ./prometheus.yml:/etc/prometheus/prometheus.yml
  ports:
    - "9090:9090"

grafana:
  image: grafana/grafana:latest
  ports:
    - "3000:3000"
  environment:
    - GF_AUTH_ANONYMOUS_ENABLED=true
```

**otel-collector-config.yaml:**

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

exporters:
  prometheus:
    endpoint: 0.0.0.0:8889
  debug:
    verbosity: basic

processors:
  batch:
    timeout: 5s

service:
  pipelines:
    metrics:
      receivers: [otlp]
      processors: [batch]
      exporters: [prometheus]
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug]
```

**prometheus.yml:**

```yaml
scrape_configs:
  - job_name: otel-collector
    scrape_interval: 15s
    static_configs:
      - targets: ['otel-collector:8889']
```

Then set on the Brygge API service:

```yaml
api:
  environment:
    OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-collector:4317
```

---

## Graceful degradation

If the collector is unreachable at startup, Brygge logs a warning and continues without telemetry:

```
WRN failed to initialize OpenTelemetry — metrics and tracing disabled
```

The app functions normally — no metrics or traces are exported, but no errors are raised on requests.

On shutdown, the SDK flushes any buffered telemetry with a 5-second timeout.

---

## Resource attributes

The SDK automatically sets:

| Attribute | Value |
|-----------|-------|
| `service.name` | `brygge-api` |
| `service.version` | `1.0.0` |

Add more via `OTEL_RESOURCE_ATTRIBUTES` (comma-separated key=value pairs).

---

See also: [configuration.md](configuration.md) | [deploy.md](deploy.md)
