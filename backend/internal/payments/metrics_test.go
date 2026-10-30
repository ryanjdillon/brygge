package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

func collect(t *testing.T, reader *sdkmetric.ManualReader, name string) (metricdata.Metrics, bool) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

func failureCounts(t *testing.T, reader *sdkmetric.ManualReader) map[[2]string]int64 {
	t.Helper()
	m, ok := collect(t, reader, "brygge.payment.webhook.failure")
	if !ok {
		t.Fatal("brygge.payment.webhook.failure not exported")
	}
	if m.Unit != "{response}" {
		t.Errorf("unit = %q, want {response}", m.Unit)
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok || !sum.IsMonotonic || sum.Temporality != metricdata.CumulativeTemporality {
		t.Fatalf("want a monotonic cumulative Sum[int64], got %T", m.Data)
	}
	out := map[[2]string]int64{}
	for _, dp := range sum.DataPoints {
		if dp.Attributes.Len() != 2 {
			t.Errorf("attributes = %v, want only provider and event_type", dp.Attributes.ToSlice())
		}
		p, _ := dp.Attributes.Value(attribute.Key("provider"))
		e, _ := dp.Attributes.Value(attribute.Key("event_type"))
		out[[2]string{p.AsString(), e.AsString()}] = dp.Value
	}
	return out
}

func newTestFailures(provider string, types []string) (*WebhookFailures, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return NewWebhookFailuresWithMeter(mp.Meter("brygge-api"), provider, types), reader
}

func TestWebhookFailures_PreCreatesEverySeriesAtZero(t *testing.T) {
	_, reader := newTestFailures("stripe", []string{"a.b", "c.d"})
	got := failureCounts(t, reader)
	want := [][2]string{{"stripe", "a.b"}, {"stripe", "c.d"}, {"stripe", "other"}}
	if len(got) != len(want) {
		t.Fatalf("series = %v, want %v", got, want)
	}
	for _, k := range want {
		if v, ok := got[k]; !ok || v != 0 {
			t.Errorf("series %v = %d (present %v), want 0", k, v, ok)
		}
	}
}

// An event type outside the allowlist, including an empty one, is counted as
// "other": a provider cannot grow the label set.
func TestWebhookFailures_UnknownTypesShareOneSeries(t *testing.T) {
	w, reader := newTestFailures("vipps", []string{"AUTHORIZED", "CAPTURED"})
	ctx := context.Background()
	w.Record(ctx, "AUTHORIZED")
	w.Record(ctx, "AUTHORIZED")
	w.Record(ctx, "CAPTURED")
	w.Record(ctx, "SOMETHING_NEW")
	w.Record(ctx, "evt_0123456789")
	w.Record(ctx, "")

	got := failureCounts(t, reader)
	if len(got) != 3 {
		t.Errorf("series = %v, want AUTHORIZED, CAPTURED and other only", got)
	}
	for k, want := range map[[2]string]int64{
		{"vipps", "AUTHORIZED"}: 2, {"vipps", "CAPTURED"}: 1, {"vipps", "other"}: 3,
	} {
		if got[k] != want {
			t.Errorf("series %v = %d, want %d", k, got[k], want)
		}
	}
}

func TestWebhookFailures_NilRecordsNothing(t *testing.T) {
	var w *WebhookFailures
	w.Record(context.Background(), "anything")
}

// A cancelled request still counts: the failure is what the metric is for.
func TestWebhookFailures_CountsAfterTheRequestEnded(t *testing.T) {
	w, reader := newTestFailures("stripe", []string{"payment_intent.succeeded"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Record(ctx, "payment_intent.succeeded")
	if got := failureCounts(t, reader)[[2]string{"stripe", "payment_intent.succeeded"}]; got != 1 {
		t.Errorf("count = %d, want 1", got)
	}
}

type fakeRow struct {
	seconds float64
	err     error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*float64)) = r.seconds
	return nil
}

type fakeAgeDB struct{ row fakeRow }

func (f *fakeAgeDB) QueryRow(context.Context, string, ...any) pgx.Row { return f.row }

func ageGauge(t *testing.T, reader *sdkmetric.ManualReader) (float64, bool) {
	t.Helper()
	m, ok := collect(t, reader, "brygge.payment.attempt.oldest_authorized_age")
	if !ok {
		return 0, false
	}
	if m.Unit != "s" {
		t.Errorf("unit = %q, want s", m.Unit)
	}
	dps := m.Data.(metricdata.Gauge[float64]).DataPoints
	if len(dps) != 1 || dps[0].Attributes.Len() != 0 {
		t.Fatalf("data points = %v, want one without attributes", dps)
	}
	return dps[0].Value, true
}

func TestAuthorizedAttemptAge_ObservesTheQueryResult(t *testing.T) {
	db := &fakeAgeDB{row: fakeRow{seconds: 5400}}
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if _, err := NewAuthorizedAttemptAge(db, zerolog.Nop()).registerMetrics(mp.Meter("brygge-api")); err != nil {
		t.Fatal(err)
	}
	if v, ok := ageGauge(t, reader); !ok || v != 5400 {
		t.Errorf("gauge = %v (present %v), want 5400", v, ok)
	}
	db.row = fakeRow{seconds: 0}
	if v, ok := ageGauge(t, reader); !ok || v != 0 {
		t.Errorf("gauge with nothing authorized = %v (present %v), want 0", v, ok)
	}
}

// A read that fails reports nothing: a 0 would read as "everything settled".
func TestAuthorizedAttemptAge_FailedReadObservesNothing(t *testing.T) {
	db := &fakeAgeDB{row: fakeRow{err: errors.New("connection refused")}}
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if _, err := NewAuthorizedAttemptAge(db, zerolog.Nop()).registerMetrics(mp.Meter("brygge-api")); err != nil {
		t.Fatal(err)
	}
	if v, ok := ageGauge(t, reader); ok {
		t.Errorf("gauge = %v, want absent", v)
	}
}

// The query counts only authorized attempts and measures from the moment the
// attempt became authorized.
func TestAuthorizedAttemptAge_Query(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, token) VALUES ($1, 450, $2) RETURNING id`,
		clubID, "tok-"+testutil.RandomHex(8)).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	insert := func(status, ref, age string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, updated_at)
			 VALUES ($1, $2, 'stripe', $3, $4, 450, now() - $5::interval)`,
			clubID, orderID, ref, status, age); err != nil {
			t.Fatal(err)
		}
	}
	read := func() float64 {
		t.Helper()
		var s float64
		if err := pool.QueryRow(ctx, oldestAuthorizedAgeSQL).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	if got := read(); got != 0 {
		t.Errorf("no attempts = %v, want 0", got)
	}
	insert("captured", "cs_old_captured", "3 days")
	insert("created", "cs_old_created", "3 days")
	insert("failed", "cs_old_failed", "3 days")
	if got := read(); got != 0 {
		t.Errorf("only settled or unauthorized attempts = %v, want 0", got)
	}
	insert("authorized", "cs_young", "2 minutes")
	insert("authorized", "cs_old", "90 minutes")
	if got := read(); got < 90*60 || got > 91*60 {
		t.Errorf("oldest authorized = %v s, want about 5400", got)
	}
}
