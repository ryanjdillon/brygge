# Standalone Collector (docker-compose)

A self-contained dev/standalone setup: OTel Collector + Prometheus + Grafana running alongside Brygge in `docker-compose.yml`. Use this if you don't have an upstream collector and just want to see telemetry locally.

## docker-compose snippet

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

## otel-collector-config.yaml

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

## prometheus.yml

```yaml
scrape_configs:
  - job_name: otel-collector
    scrape_interval: 15s
    static_configs:
      - targets: ['otel-collector:8889']
```

## Wire Brygge to it

```yaml
api:
  environment:
    OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-collector:4317
```

See [app-config.md](app-config.md) for the full list of supported env vars.
