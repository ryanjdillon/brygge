package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

var meter = otel.Meter("brygge-api")

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func Metrics(next http.Handler) http.Handler {
	requestCount, _ := meter.Int64Counter("http.server.request.count",
		otelmetric.WithDescription("Total HTTP requests"),
	)
	requestDuration, _ := meter.Float64Histogram("http.server.request.duration",
		otelmetric.WithDescription("HTTP request duration in seconds"),
		otelmetric.WithUnit("s"),
	)
	requestsInFlight, _ := meter.Int64UpDownCounter("http.server.active_requests",
		otelmetric.WithDescription("Number of in-flight requests"),
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestsInFlight.Add(r.Context(), 1)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		requestsInFlight.Add(r.Context(), -1)
		duration := time.Since(start).Seconds()

		routePattern := chi.RouteContext(r.Context()).RoutePattern()
		if routePattern == "" {
			routePattern = "unmatched"
		}

		attrs := otelmetric.WithAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.route", routePattern),
			attribute.String("http.status_code", strconv.Itoa(rec.status)),
		)
		attrsNoStatus := otelmetric.WithAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.route", routePattern),
		)

		requestCount.Add(r.Context(), 1, attrs)
		requestDuration.Record(r.Context(), duration, attrsNoStatus)
	})
}
