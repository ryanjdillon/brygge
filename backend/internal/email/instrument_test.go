package email

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func collectSends(t *testing.T, reader *sdkmetric.ManualReader) map[[2]string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[[2]string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "brygge.email.send" {
				continue
			}
			if m.Unit != "{message}" {
				t.Errorf("unit = %q, want {message}", m.Unit)
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("data type %T, want Sum[int64]", m.Data)
			}
			if !sum.IsMonotonic || sum.Temporality != metricdata.CumulativeTemporality {
				t.Errorf("want a monotonic cumulative sum, got monotonic=%v temporality=%v", sum.IsMonotonic, sum.Temporality)
			}
			for _, dp := range sum.DataPoints {
				if dp.Attributes.Len() != 2 {
					t.Errorf("attributes = %v, want only kind and result", dp.Attributes.ToSlice())
				}
				kind, _ := dp.Attributes.Value(attribute.Key("kind"))
				result, _ := dp.Attributes.Value(attribute.Key("result"))
				out[[2]string{kind.AsString(), result.AsString()}] = dp.Value
			}
		}
	}
	return out
}

func newTestSender(next Sender) (Sender, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return instrument(next, mp.Meter("brygge-api")), reader
}

func TestInstrument_PreCreatesAllSeriesAtZero(t *testing.T) {
	_, reader := newTestSender(&MockSender{})
	got := collectSends(t, reader)
	if len(got) != len(allKinds)*2 {
		t.Fatalf("got %d series, want %d: %v", len(got), len(allKinds)*2, got)
	}
	for _, k := range allKinds {
		for _, r := range []string{resultOK, resultError} {
			v, ok := got[[2]string{string(k), r}]
			if !ok || v != 0 {
				t.Errorf("series kind=%s result=%s = %d (present %v), want 0", k, r, v, ok)
			}
		}
	}
}

func TestInstrument_CountsByKindAndResult(t *testing.T) {
	mock := &MockSender{}
	s, reader := newTestSender(mock)
	ctx := context.Background()

	if err := s.Send(WithKind(ctx, KindMagicLink), "a@example.invalid", "s", "<p>b</p>"); err != nil {
		t.Fatal(err)
	}
	if err := s.SendWithAttachment(WithKind(ctx, KindInvoice), "a@example.invalid", "s", "b", "f.pdf", []byte("x")); err != nil {
		t.Fatal(err)
	}
	// Untagged and unknown kinds collapse into "other".
	_ = s.SendWithHeaders(ctx, "a@example.invalid", "s", "b", nil)
	_ = s.Send(WithKind(ctx, Kind("made_up")), "a@example.invalid", "s", "b")

	mock.Err = errors.New("smtp dial: connection refused for a@example.invalid")
	if err := s.SendWithAttachment(WithKind(ctx, KindInvoiceReminder), "a@example.invalid", "s", "b", "f.pdf", nil); err == nil {
		t.Fatal("want the underlying error returned unchanged")
	}

	got := collectSends(t, reader)
	want := map[[2]string]int64{
		{"magic_link", "ok"}:          1,
		{"invoice", "ok"}:             1,
		{"other", "ok"}:               2,
		{"invoice_reminder", "error"}: 1,
		{"magic_link", "error"}:       0,
		{"invoice", "error"}:          0,
		{"invoice_reminder", "ok"}:    0,
		{"other", "error"}:            0,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("kind=%s result=%s = %d, want %d", k[0], k[1], got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d series, want %d: %v", len(got), len(want), got)
	}
	if len(mock.Calls) != 5 {
		t.Errorf("underlying sender got %d calls, want 5", len(mock.Calls))
	}
}

func TestInstrument_NilPassesThrough(t *testing.T) {
	if Instrument(nil) != nil {
		t.Fatal("Instrument(nil) must stay nil so handlers' nil checks still disable email")
	}
}
