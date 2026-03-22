package telemetry

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// RegisterPoolMetrics registers OTEL observable gauges that report pgx
// connection pool statistics on each metric collection interval.
func RegisterPoolMetrics(pool *pgxpool.Pool) error {
	meter := otel.Meter("brygge-api")

	_, err := meter.Int64ObservableGauge("db.pool.connections.active",
		otelmetric.WithDescription("Number of acquired (in-use) connections"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			o.Observe(int64(pool.Stat().AcquiredConns()))
			return nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = meter.Int64ObservableGauge("db.pool.connections.idle",
		otelmetric.WithDescription("Number of idle connections"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			o.Observe(int64(pool.Stat().IdleConns()))
			return nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = meter.Int64ObservableGauge("db.pool.connections.total",
		otelmetric.WithDescription("Total number of connections in the pool"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			o.Observe(int64(pool.Stat().TotalConns()))
			return nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = meter.Int64ObservableGauge("db.pool.connections.max",
		otelmetric.WithDescription("Maximum pool size"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			o.Observe(int64(pool.Stat().MaxConns()))
			return nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = meter.Int64ObservableCounter("db.pool.acquire.count",
		otelmetric.WithDescription("Cumulative count of connection acquisitions"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			o.Observe(pool.Stat().AcquireCount())
			return nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = meter.Float64ObservableGauge("db.pool.acquire.duration_seconds",
		otelmetric.WithDescription("Cumulative time spent acquiring connections"),
		otelmetric.WithUnit("s"),
		otelmetric.WithFloat64Callback(func(_ context.Context, o otelmetric.Float64Observer) error {
			o.Observe(pool.Stat().AcquireDuration().Seconds())
			return nil
		}),
	)
	if err != nil {
		return err
	}

	_, err = meter.Int64ObservableCounter("db.pool.empty_acquire.count",
		otelmetric.WithDescription("Cumulative count of acquisitions when pool was empty"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			o.Observe(pool.Stat().EmptyAcquireCount())
			return nil
		}),
	)
	return err
}
