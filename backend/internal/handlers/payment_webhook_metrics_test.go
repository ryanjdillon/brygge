package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// webhookFailureCounts reads brygge.payment.webhook.failure as
// {provider, event_type} -> count.
func webhookFailureCounts(t *testing.T, reader *sdkmetric.ManualReader) map[[2]string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[[2]string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "brygge.payment.webhook.failure" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				p, _ := dp.Attributes.Value(attribute.Key("provider"))
				e, _ := dp.Attributes.Value(attribute.Key("event_type"))
				out[[2]string{p.AsString(), e.AsString()}] = dp.Value
			}
		}
	}
	return out
}

func nonZero(m map[[2]string]int64) map[[2]string]int64 {
	out := map[[2]string]int64{}
	for k, v := range m {
		if v != 0 {
			out[k] = v
		}
	}
	return out
}

func testMeter() (*sdkmetric.ManualReader, *sdkmetric.MeterProvider) {
	reader := sdkmetric.NewManualReader()
	return reader, sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
}

// Each 5xx a webhook answers is counted under its provider and event type,
// including the ones given before the event is stored; an event type outside
// the allowlist is "other".
func TestWebhookFailureMetric_CountsEach5xx(t *testing.T) {
	t.Run("vipps", func(t *testing.T) {
		reader, mp := testMeter()
		cfg := &config.Config{ClubSlug: "testklubb", VippsWebhookSecret: webhookTestSecret}
		h := NewVippsWebhookHandler(closedPool(t), cfg, nil, zerolog.Nop())
		h.failures = payments.NewWebhookFailuresWithMeter(mp.Meter("brygge-api"), vipps.ProviderName, vipps.WebhookEventNames)
		e := &webhookEnv{h: h}

		for range 2 {
			if rec := e.post(t, capturedEvent("chk-m-1", "evt-m-1", "")); rec.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500", rec.Code)
			}
		}
		if rec := e.post(t, authorizedEvent("chk-m-2", "evt-m-2")); rec.Code != http.StatusInternalServerError {
			t.Fatalf("status %d, want 500", rec.Code)
		}
		want := map[[2]string]int64{{"vipps", "CAPTURED"}: 2, {"vipps", "AUTHORIZED"}: 1}
		got := nonZero(webhookFailureCounts(t, reader))
		if len(got) != len(want) || got[[2]string{"vipps", "CAPTURED"}] != 2 || got[[2]string{"vipps", "AUTHORIZED"}] != 1 {
			t.Errorf("failures = %v, want %v", got, want)
		}
	})

	t.Run("stripe", func(t *testing.T) {
		reader, mp := testMeter()
		cfg := &config.Config{ClubSlug: "testklubb", StripeWebhookSecret: stripeTestWebhookSecret}
		h := NewStripeWebhookHandler(closedPool(t), cfg, nil, zerolog.Nop())
		h.failures = payments.NewWebhookFailuresWithMeter(mp.Meter("brygge-api"), stripe.ProviderName, stripe.WebhookEventTypes)
		e := &stripeEnv{h: h}

		if rec := e.post(t, sessionCompletedEvent("evt_m1", "cs_m1", 45000, "", "")); rec.Code != http.StatusInternalServerError {
			t.Fatalf("status %d, want 500", rec.Code)
		}
		unknown := sessionEvent("customer.subscription.created", "evt_m2", "cs_m2")
		if rec := e.post(t, unknown); rec.Code != http.StatusInternalServerError {
			t.Fatalf("status %d, want 500", rec.Code)
		}
		got := nonZero(webhookFailureCounts(t, reader))
		if len(got) != 2 || got[[2]string{"stripe", stripe.EventCheckoutSessionCompleted}] != 1 || got[[2]string{"stripe", "other"}] != 1 {
			t.Errorf("failures = %v, want one completed and one other", got)
		}
	})
}

// A 200 is not a failure: acknowledging an event that is not ours, or one
// that settled, adds nothing.
func TestWebhookFailureMetric_Ignores200(t *testing.T) {
	reader, mp := testMeter()
	e := newStripeEnv(t)
	e.h.failures = payments.NewWebhookFailuresWithMeter(mp.Meter("brygge-api"), stripe.ProviderName, stripe.WebhookEventTypes)
	orderID := e.seedOrder(t, "")
	e.seedAttempt(t, orderID, "stripe", "cs_ok", "created")

	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_ok1", "cs_ok", 45000, "", "")))
	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_ok2", "cs_unknown", 45000, "", "")))
	if got := nonZero(webhookFailureCounts(t, reader)); len(got) != 0 {
		t.Errorf("failures = %v, want none", got)
	}
}
