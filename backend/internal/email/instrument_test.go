package email

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/brygge-klubb/brygge/internal/sesevents"
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
				if dp.Attributes.Len() != 3 {
					t.Errorf("attributes = %v, want only kind, provider and result", dp.Attributes.ToSlice())
				}
				if p, _ := dp.Attributes.Value(attribute.Key("provider")); p.AsString() != string(ProviderSES) {
					t.Errorf("provider = %q, want %q", p.AsString(), ProviderSES)
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
	return instrument(next, ProviderSES, mp.Meter("brygge-api")), reader
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
	// SendMeta's kind wins over WithKind.
	_ = s.Send(WithSendMeta(WithKind(ctx, KindInvoice), SendMeta{Kind: KindContact}), "a@example.invalid", "s", "b")

	mock.Err = errors.New("smtp dial: connection refused for a@example.invalid")
	if err := s.SendWithAttachment(WithKind(ctx, KindInvoiceReminder), "a@example.invalid", "s", "b", "f.pdf", nil); err == nil {
		t.Fatal("want the underlying error returned unchanged")
	}

	got := collectSends(t, reader)
	want := map[[2]string]int64{
		{"magic_link", "ok"}:             1,
		{"invoice", "ok"}:                1,
		{"other", "ok"}:                  2,
		{"invoice_reminder", "error"}:    1,
		{"magic_link", "error"}:          0,
		{"invoice", "error"}:             0,
		{"invoice_reminder", "ok"}:       0,
		{"other", "error"}:               0,
		{"contact", "ok"}:                1,
		{"contact", "error"}:             0,
		{"credit_note", "ok"}:            0,
		{"credit_note", "error"}:         0,
		{"dispute", "ok"}:                0,
		{"dispute", "error"}:             0,
		{"join_confirm", "ok"}:           0,
		{"join_confirm", "error"}:        0,
		{"join_already_member", "ok"}:    0,
		{"join_already_member", "error"}: 0,
		{"join_contact_board", "ok"}:     0,
		{"join_contact_board", "error"}:  0,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("kind=%s result=%s = %d, want %d", k[0], k[1], got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d series, want %d: %v", len(got), len(want), got)
	}
	if len(mock.Calls) != 6 {
		t.Errorf("underlying sender got %d calls, want 6", len(mock.Calls))
	}
}

func TestInstrument_NilPassesThrough(t *testing.T) {
	if Instrument(nil, ProviderStalwart) != nil {
		t.Fatal("Instrument(nil) must stay nil so handlers' nil checks still disable email")
	}
}

// The delivery-event metric normalises the SES kind tag against its own
// list; a kind missing there would count every event of it as "other".
func TestKindsMatchDeliveryEventVocabulary(t *testing.T) {
	got := make([]string, len(allKinds))
	for i, k := range allKinds {
		got[i] = string(k)
	}
	want := slices.Clone(sesevents.Kinds)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("email kinds %v != sesevents.Kinds %v", got, want)
	}
}
