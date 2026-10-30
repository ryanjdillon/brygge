package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// closedPool is a pool every query on fails with a non-ErrNoRows error: the
// database being down, as far as a handler can tell. It needs no database.
func closedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://brygge:brygge@127.0.0.1:1/brygge?sslmode=disable")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	pool.Close()
	return pool
}

// A failed attempt lookup is an error, not "not our payment": the webhook
// answers 5xx on it so the provider delivers the event again.
func TestAttemptLookup_DatabaseErrorIsNotAMiss(t *testing.T) {
	s := orderSettlement{db: closedPool(t), log: zerolog.Nop()}
	ctx := context.Background()

	if _, ok, err := s.attemptByReference(ctx, uuid.NewString(), "vipps", "chk-lookup-1"); err == nil || ok {
		t.Errorf("attemptByReference = ok %v, err %v; want the database error", ok, err)
	}
	if _, ok, err := s.attemptByID(ctx, uuid.NewString(), "stripe", uuid.NewString()); err == nil || ok {
		t.Errorf("attemptByID = ok %v, err %v; want the database error", ok, err)
	}
	// An id that is not a UUID never reaches the database.
	if _, ok, err := s.attemptByID(ctx, uuid.NewString(), "stripe", "not-a-uuid"); err != nil || ok {
		t.Errorf("attemptByID(not a uuid) = ok %v, err %v; want a plain miss", ok, err)
	}
}

func TestVippsWebhook_DatabaseErrorAnswers5xx(t *testing.T) {
	cfg := &config.Config{ClubSlug: "testklubb", VippsWebhookSecret: webhookTestSecret}
	e := &webhookEnv{h: NewVippsWebhookHandler(closedPool(t), cfg, nil, zerolog.Nop())}

	rec := e.post(t, capturedEvent("chk-down-1", "evt-down-1", ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("webhook with the database down = %d, want 500 so Vipps retries", rec.Code)
	}
}

// With the club known, each event that needs the attempt fails the dispatch
// when the lookup fails, instead of being acknowledged as not ours.
func TestVippsWebhook_DispatchLookupError(t *testing.T) {
	h := NewVippsWebhookHandler(closedPool(t), &config.Config{}, nil, zerolog.Nop())
	for _, name := range []string{
		vipps.EventPaymentAuthorized, vipps.EventPaymentCaptured,
		vipps.EventPaymentCancelled, vipps.EventPaymentAborted, vipps.EventPaymentExpired, vipps.EventPaymentTerminated,
	} {
		t.Run(name, func(t *testing.T) {
			evt := &vipps.WebhookEvent{Name: name, Reference: "chk-down-2"}
			if err := h.dispatch(context.Background(), uuid.NewString(), evt); err == nil {
				t.Error("dispatch acknowledged an event whose attempt lookup failed")
			}
		})
	}
}

func TestStripeWebhook_DatabaseErrorAnswers5xx(t *testing.T) {
	cfg := &config.Config{ClubSlug: "testklubb", StripeWebhookSecret: stripeTestWebhookSecret}
	e := &stripeEnv{h: NewStripeWebhookHandler(closedPool(t), cfg, nil, zerolog.Nop())}

	rec := e.post(t, sessionCompletedEvent("evt_down", "cs_down", 45000, "", ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("webhook with the database down = %d, want 500 so Stripe retries", rec.Code)
	}
}

func TestStripeWebhook_DispatchLookupError(t *testing.T) {
	h := NewStripeWebhookHandler(closedPool(t), &config.Config{}, nil, zerolog.Nop())
	attemptID := uuid.NewString()
	events := map[string]map[string]any{
		"session completed": sessionCompletedEvent("evt_a", "cs_down", 45000, "", ""),
		"session expired":   sessionEvent(stripe.EventCheckoutSessionExpired, "evt_b", "cs_down"),
		"intent succeeded":  intentEvent(stripe.EventPaymentIntentSucceeded, "evt_c", "pi_down", attemptID, 1),
		"intent canceled":   intentEvent(stripe.EventPaymentIntentCanceled, "evt_d", "pi_down", attemptID, 1),
	}
	for name, raw := range events {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			var evt stripe.Event
			if err := json.Unmarshal(body, &evt); err != nil {
				t.Fatal(err)
			}
			if err := h.dispatch(context.Background(), uuid.NewString(), &evt, nil, nil); err == nil {
				t.Error("dispatch acknowledged an event whose attempt lookup failed")
			}
		})
	}
}

// A reference that is not Brygge's is the previous behaviour: acknowledged and
// stored under the instance club, nothing acted on.
func TestStripeWebhook_UnknownReferenceIsAcknowledged(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_known", "created")

	for _, evt := range []map[string]any{
		sessionCompletedEvent("evt_unknown_1", "cs_unknown", 45000, "", ""),
		sessionEvent(stripe.EventCheckoutSessionExpired, "evt_unknown_2", "cs_unknown"),
		intentEvent(stripe.EventPaymentIntentSucceeded, "evt_unknown_3", "pi_unknown", uuid.NewString(), 1),
	} {
		if rec := e.post(t, evt); rec.Code != http.StatusOK {
			t.Errorf("%v: status %d, want 200", evt["type"], rec.Code)
		}
	}
	if got := e.attemptStatus(t, attemptID); got != "created" {
		t.Errorf("unrelated attempt changed to %q", got)
	}
	if len(e.fake.captureCalls()) != 0 {
		t.Error("an unknown payment was captured")
	}
}

// A dispute or refund whose payment cannot be placed because Brygge's own
// tables could not be read is unplaced, like one Stripe could not be asked
// about, rather than "not one of our payments".
func TestIntentPayment_DatabaseErrorIsUnplaced(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{ClubSlug: "testklubb"}

	t.Run("attempt lookup", func(t *testing.T) {
		fake := newFakeStripe(t)
		fake.intentMeta["pi_db"] = map[string]string{"attempt_id": uuid.NewString()}
		h := NewStripeWebhookHandler(closedPool(t), cfg, fake.client(), zerolog.Nop())
		if p := h.resolveIntentPayment(ctx, "dp_db", "pi_db", "ch_db"); !p.dbLookupFailed || !p.unplaced() {
			t.Errorf("payment = %+v, want unplaced after the attempt lookup failed", p)
		}
	})

	t.Run("charge lookup", func(t *testing.T) {
		h := NewStripeWebhookHandler(closedPool(t), cfg, nil, zerolog.Nop())
		if p := h.resolveIntentPayment(ctx, "dp_db", "pi_db", "ch_db"); !p.dbLookupFailed || !p.unplaced() {
			t.Errorf("payment = %+v, want unplaced after the charge lookup failed", p)
		}
	})

	t.Run("nothing failed is a plain miss", func(t *testing.T) {
		if (intentPayment{}).unplaced() {
			t.Error("a payment nothing failed to look up is unplaced")
		}
	})
}

// With the club known, a refund or dispute whose charge lookup fails in the
// database answers 5xx and is stored nowhere; the same event for a payment
// that is simply not ours is acknowledged as before.
func TestStripeWebhook_PlacementDatabaseErrorAnswers5xx(t *testing.T) {
	e, mail := newDisputeEnv(t)
	e.fake.intentMissing["pi_gone"] = true

	if rec := e.post(t, chargeRefundedEvent("evt_rf_miss", "ch_gone", "pi_gone")); rec.Code != http.StatusOK {
		t.Fatalf("refund of a payment that is not ours = %d, want 200", rec.Code)
	}

	if _, err := e.pool.Exec(context.Background(), `ALTER TABLE stripe_balance_transactions RENAME COLUMN reporting_category TO reporting_category_gone`); err != nil {
		t.Fatal(err)
	}
	if rec := e.post(t, chargeRefundedEvent("evt_rf_db", "ch_gone", "pi_gone")); rec.Code != http.StatusInternalServerError {
		t.Errorf("refund with the charge lookup failing = %d, want 500", rec.Code)
	}
	rec := e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_dp_db", "dp_db", "pi_gone", "ch_gone", "needs_response", 0))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("dispute with the charge lookup failing = %d, want 500", rec.Code)
	}
	if _, ok := e.dispute(t, "dp_db"); ok || len(mail.Calls) != 0 {
		t.Errorf("stored %v, mails %d; want neither", ok, len(mail.Calls))
	}
}
