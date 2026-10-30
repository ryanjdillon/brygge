package payments

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// eventTypeOther counts a 5xx for an event type outside the provider's
// allowlist, so the label set stays closed whatever a provider sends.
const eventTypeOther = "other"

// WebhookFailures counts the 5xx answers a payment webhook gives, by
// provider and event type. A 5xx is how a webhook asks the provider to
// deliver the event again (PAY-14), so a count that keeps growing is an event
// the provider keeps retrying and Brygge keeps failing to settle.
//
// Attributes are limited to provider and event_type, and event_type is
// checked against the allowlist given at construction: event ids, payment
// references and error text would explode cardinality. A nil
// *WebhookFailures records nothing, so handlers built without telemetry (and
// tests) need no guard.
type WebhookFailures struct {
	counter  metric.Int64Counter
	provider string
	known    map[string]bool
}

// NewWebhookFailures returns the counter for provider, with eventTypes as the
// event types it reports under their own name. Call after the global
// MeterProvider is installed (telemetry.Setup).
func NewWebhookFailures(provider string, eventTypes []string) *WebhookFailures {
	return NewWebhookFailuresWithMeter(otel.Meter("brygge-api"), provider, eventTypes)
}

// NewWebhookFailuresWithMeter is NewWebhookFailures on a given meter, for tests.
func NewWebhookFailuresWithMeter(meter metric.Meter, provider string, eventTypes []string) *WebhookFailures {
	counter, err := meter.Int64Counter("brygge.payment.webhook.failure",
		metric.WithDescription("Payment webhook deliveries answered 5xx, by provider and event type"),
		metric.WithUnit("{response}"),
	)
	if err != nil {
		otel.Handle(err)
		return nil
	}
	w := &WebhookFailures{counter: counter, provider: provider, known: make(map[string]bool, len(eventTypes))}
	// Create every series at 0 so an increase over the first failure is
	// computable; a counter that appears on its first failure has no prior
	// sample to diff against.
	for _, t := range append(append([]string(nil), eventTypes...), eventTypeOther) {
		w.known[t] = true
		counter.Add(context.Background(), 0, metric.WithAttributes(w.attrs(t)...))
	}
	return w
}

func (w *WebhookFailures) attrs(eventType string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("provider", w.provider),
		attribute.String("event_type", eventType),
	}
}

// Record counts one 5xx answer for an event of eventType.
func (w *WebhookFailures) Record(ctx context.Context, eventType string) {
	if w == nil {
		return
	}
	if !w.known[eventType] {
		eventType = eventTypeOther
	}
	// The request's context may already be cancelled; the count must not be.
	w.counter.Add(context.WithoutCancel(ctx), 1, metric.WithAttributes(w.attrs(eventType)...))
}

// attemptAgeQuerier is the part of a pgx pool the authorized-attempt gauge
// reads with.
type attemptAgeQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const (
	attemptAgeTimeout = 5 * time.Second
	// attemptAgeLogEvery limits the warning for a failing read to one per
	// interval, as the mail queue monitor does.
	attemptAgeLogEvery = 15 * time.Minute
)

// oldestAuthorizedAgeSQL is the age in seconds of the longest-authorized
// attempt, 0 when none is. updated_at is when the attempt last changed
// status; an authorized attempt keeps it until it is captured, released or
// cancelled (a redelivered decision that leaves the status as it is does not
// touch it), so it is when the attempt became authorized.
const oldestAuthorizedAgeSQL = `SELECT COALESCE(EXTRACT(EPOCH FROM (now() - MIN(updated_at))), 0)::float8
	  FROM payment_attempts WHERE status = 'authorized'`

// AuthorizedAttemptAge reports the age of the oldest payment attempt that is
// authorized and not yet settled. An attempt is authorized only between the
// decision to capture it and the write that records the capture, which is
// seconds; one that stays authorized is a payment the provider holds or has
// taken and Brygge has not booked (its settlement keeps failing, or its event
// never arrived again). It spans every club in the database.
type AuthorizedAttemptAge struct {
	db  attemptAgeQuerier
	log zerolog.Logger

	mu         sync.Mutex
	lastErrLog time.Time
}

// NewAuthorizedAttemptAge builds the gauge over db. Call RegisterMetrics once.
func NewAuthorizedAttemptAge(db attemptAgeQuerier, log zerolog.Logger) *AuthorizedAttemptAge {
	return &AuthorizedAttemptAge{db: db, log: log.With().Str("component", "payment-attempt-age").Logger()}
}

// RegisterMetrics registers the gauge on the global MeterProvider.
func (a *AuthorizedAttemptAge) RegisterMetrics() error {
	_, err := a.registerMetrics(otel.Meter("brygge-api"))
	return err
}

func (a *AuthorizedAttemptAge) registerMetrics(meter metric.Meter) (metric.Registration, error) {
	age, err := meter.Float64ObservableGauge("brygge.payment.attempt.oldest_authorized_age",
		metric.WithDescription("Age of the oldest payment attempt that is authorized and not yet settled; 0 when there is none"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	return meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, attemptAgeTimeout)
		defer cancel()
		var seconds float64
		if err := a.db.QueryRow(ctx, oldestAuthorizedAgeSQL).Scan(&seconds); err != nil {
			// Observe nothing rather than a 0 that reads as "all settled".
			a.warn(err)
			return nil
		}
		o.ObserveFloat64(age, seconds)
		return nil
	}, age)
}

func (a *AuthorizedAttemptAge) warn(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.lastErrLog) < attemptAgeLogEvery {
		return
	}
	a.lastErrLog = time.Now()
	a.log.Warn().Err(err).Msg("reading the oldest authorized payment attempt failed")
}
