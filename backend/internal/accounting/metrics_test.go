package accounting

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func collectMetric(t *testing.T, reader *sdkmetric.ManualReader, name string) (metricdata.Metrics, bool) {
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

// int64GaugeBy returns the gauge's points keyed by the one attribute key;
// nil when the gauge observed nothing.
func int64GaugeBy(t *testing.T, reader *sdkmetric.ManualReader, name, unit, key string) map[string]int64 {
	t.Helper()
	m, ok := collectMetric(t, reader, name)
	if !ok {
		return nil
	}
	if m.Unit != unit {
		t.Errorf("%s unit = %q, want %q", name, m.Unit, unit)
	}
	g, ok := m.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("%s: want Gauge[int64], got %T", name, m.Data)
	}
	out := map[string]int64{}
	for _, dp := range g.DataPoints {
		if dp.Attributes.Len() != 1 {
			t.Errorf("%s attributes = %v, want only %s", name, dp.Attributes.ToSlice(), key)
		}
		v, _ := dp.Attributes.Value(attribute.Key(key))
		out[v.AsString()] = dp.Value
	}
	return out
}

func newTestAutoBookingMetrics(t *testing.T) (*AutoBookingMetrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m := NewAutoBookingMetrics()
	if _, err := m.registerMetrics(mp.Meter("brygge-api")); err != nil {
		t.Fatal(err)
	}
	return m, reader
}

const (
	lastSuccessMetric   = "brygge.vipps.report_sync.last_success"
	lastRunFailedMetric = "brygge.accounting.auto_booking.last_run_failed"
)

func TestAutoBookingMetricsLastSuccessPerMSN(t *testing.T) {
	m, reader := newTestAutoBookingMetrics(t)
	first := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
	m.VippsReportSynced("100001", first)
	m.VippsReportSynced("100002", first.Add(time.Hour))
	m.VippsReportSynced("100001", first.Add(24*time.Hour))

	got := int64GaugeBy(t, reader, lastSuccessMetric, "s", "msn")
	want := map[string]int64{"100001": first.Add(24 * time.Hour).Unix(), "100002": first.Add(time.Hour).Unix()}
	if len(got) != len(want) {
		t.Fatalf("series = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("msn %s = %d, want %d", k, got[k], v)
		}
	}
}

// Before the first success nothing is observed, not 0: the alert's absent
// branch, not a 1970 timestamp, covers a restart whose first pull fails.
func TestAutoBookingMetricsNothingBeforeFirstSuccess(t *testing.T) {
	m, reader := newTestAutoBookingMetrics(t)
	if got := int64GaugeBy(t, reader, lastSuccessMetric, "s", "msn"); len(got) != 0 {
		t.Errorf("before any pull = %v, want nothing", got)
	}
	if got := int64GaugeBy(t, reader, lastRunFailedMetric, "{run}", "provider"); len(got) != 0 {
		t.Errorf("before any run = %v, want nothing", got)
	}
	// A run alone says nothing about the sync.
	m.AutoBookingRun(AutoBookingProviderVipps, false)
	if got := int64GaugeBy(t, reader, lastSuccessMetric, "s", "msn"); len(got) != 0 {
		t.Errorf("after a run without a pull = %v, want nothing", got)
	}
}

func TestAutoBookingMetricsLastRunFailedPerProvider(t *testing.T) {
	m, reader := newTestAutoBookingMetrics(t)
	read := func() map[string]int64 {
		t.Helper()
		return int64GaugeBy(t, reader, lastRunFailedMetric, "{run}", "provider")
	}

	m.AutoBookingRun(AutoBookingProviderVipps, true)
	if got := read(); len(got) != 1 || got["vipps"] != 1 {
		t.Errorf("after a failed vipps run = %v, want vipps=1 only", got)
	}
	m.AutoBookingRun("stripe", false)
	if got := read(); len(got) != 2 || got["vipps"] != 1 || got["stripe"] != 0 {
		t.Errorf("after a good stripe run = %v, want vipps=1 stripe=0", got)
	}
	// The next good run resolves it.
	m.AutoBookingRun(AutoBookingProviderVipps, false)
	if got := read(); got["vipps"] != 0 {
		t.Errorf("after a good vipps run = %v, want vipps=0", got)
	}
	// An unknown provider never grows the label set.
	m.AutoBookingRun("paypal", true)
	m.AutoBookingRun("", true)
	if got := read(); len(got) != 2 {
		t.Errorf("series = %v, want vipps and stripe only", got)
	}
}

// failingBacklogDB fails every read, as an unreachable database does.
type failingBacklogDB struct{ err error }

type failingRow struct{ err error }

func (r failingRow) Scan(...any) error { return r.err }

func (f failingBacklogDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, f.err
}

func (f failingBacklogDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return failingRow(f)
}

// A read that fails observes nothing: a 0 would read as "everything booked"
// and resolve the alert for the wrong reason.
func TestBacklogGaugesReadErrorObservesNothing(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	g := NewBacklogGauges(failingBacklogDB{err: errors.New("connection refused")}, zerolog.Nop())
	if _, err := g.registerMetrics(mp.Meter("brygge-api")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"brygge.vipps.unbooked_charge.oldest_age", "brygge.vipps.ready_payout.oldest_age"} {
		if m, ok := collectMetric(t, reader, name); ok {
			if g, isGauge := m.Data.(metricdata.Gauge[float64]); !isGauge || len(g.DataPoints) > 0 {
				t.Errorf("%s = %v, want nothing", name, m.Data)
			}
		}
	}
}

// The Postgres pattern the payout gauge parses deposits with matches, and
// captures, exactly what VippsSettlementPattern does in Go. The integration
// test runs the same table through Postgres.
func TestVippsSettlementPatternSQLMatchesGo(t *testing.T) {
	sqlAsGo := regexp.MustCompile("(?i)" + vippsSettlementPatternSQL)
	for _, desc := range settlementPatternSamples {
		want := VippsSettlementPattern.FindStringSubmatch(desc)
		got := sqlAsGo.FindStringSubmatch(desc)
		if !slices.Equal(got, want) {
			t.Errorf("%q: SQL pattern = %q, Go pattern = %q", desc, got, want)
		}
	}
	// The groupless matcher the Tildel queues use (vippsPayoutRowSQL) accepts
	// the same descriptions, read the way Go reads it.
	sqlText := vippsPayoutRowSQL("a")
	quoted := sqlText[strings.Index(sqlText, "'")+1 : strings.LastIndex(sqlText, "'")]
	groupless := regexp.MustCompile("(?i)" + quoted)
	for _, desc := range settlementPatternSamples {
		if got, want := groupless.MatchString(desc), VippsSettlementPattern.MatchString(desc); got != want {
			t.Errorf("%q: vippsPayoutRowSQL matches %v, Go pattern %v", desc, got, want)
		}
	}
}

// settlementPatternSamples are synthetic bank descriptions: matches in
// several spellings, and near misses.
var settlementPatternSamples = []string{
	"Utb. 2000591 Vippsnr 100001",
	"UTB. 2000591 VIPPSNR 100001",
	"utb.2000591 vippsnr 100001",
	"Overføring Utb.  3000101   Vippsnr  100002 fra Vipps",
	"Utb. 2000591 Vippsnr 100001 Utb. 2000592 Vippsnr 100003",
	"Utb 2000591 Vippsnr 100001",
	"Utb. 2000591Vippsnr 100001",
	"Utb. A2000591 Vippsnr 100001",
	"Utb.\u00a02000591\u00a0Vippsnr\u00a0100001", // no-break spaces: no match in either
	"Utb.\t2000591\nVippsnr\r\n100001",
	"Vipps AS",
	"",
}
