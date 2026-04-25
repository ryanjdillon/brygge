package telemetry

import (
	"context"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Shutdown func(ctx context.Context) error

type Options struct {
	ServiceName    string
	ServiceVersion string
	// ClubSlug tags every signal so the upstream collector can filter
	// per-club (resource attribute `club.slug`, e.g. "kbl").
	ClubSlug string
	// ClubDomain is the public domain for the club (resource attribute
	// `club.domain`, e.g. "klokkarvikbaatlag.no"). Useful when slugs
	// collide across tenants but domains don't.
	ClubDomain string
	// TraceSampleRatio is the fraction of root traces to sample
	// (0.0–1.0). Child spans inherit the parent decision. Overridden
	// when OTEL_TRACES_SAMPLER_ARG is set in env.
	TraceSampleRatio float64
}

func Setup(ctx context.Context, opts Options) (Shutdown, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(opts.ServiceName),
		semconv.ServiceVersion(opts.ServiceVersion),
	}
	if opts.ClubSlug != "" {
		attrs = append(attrs, attribute.String("club.slug", opts.ClubSlug))
	}
	if opts.ClubDomain != "" {
		attrs = append(attrs, attribute.String("club.domain", opts.ClubDomain))
	}

	res, err := resource.New(ctx, resource.WithAttributes(attrs...))
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter, trace.WithBatchTimeout(5*time.Second)),
		trace.WithResource(res),
		trace.WithSampler(trace.ParentBased(trace.TraceIDRatioBased(traceSampleRatio(opts.TraceSampleRatio)))),
	)
	otel.SetTracerProvider(tp)

	metricExporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter, metric.WithInterval(15*time.Second))),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return func(ctx context.Context) error {
		tpErr := tp.Shutdown(ctx)
		mpErr := mp.Shutdown(ctx)
		if tpErr != nil {
			return tpErr
		}
		return mpErr
	}, nil
}

// traceSampleRatio resolves the effective sampling ratio. Env var
// OTEL_TRACES_SAMPLER_ARG (a 0.0–1.0 float) wins over the code-side
// default; if the env value can't be parsed or is out of range, the
// caller-provided default applies. A ratio <= 0 is clamped to 0
// (no sampling); >= 1 to 1.0 (always sample).
func traceSampleRatio(defaultRatio float64) float64 {
	r := defaultRatio
	if v := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			r = parsed
		}
	}
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return r
}
