package handlers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func chargeRefundedEvent(eventID, chargeID, piID string) map[string]any {
	return map[string]any{
		"id": eventID, "type": stripe.EventChargeRefunded, "created": time.Now().Unix(),
		"data": map[string]any{"object": map[string]any{
			"id": chargeID, "object": "charge", "payment_intent": piID, "amount_refunded": 12300,
		}},
	}
}

func refundUpdatedEvent(eventID, refundID, chargeID, piID, status string) map[string]any {
	return map[string]any{
		"id": eventID, "type": stripe.EventChargeRefundUpdated, "created": time.Now().Unix(),
		"data": map[string]any{"object": map[string]any{
			"id": refundID, "object": "refund", "charge": chargeID, "payment_intent": piID, "status": status, "amount": 12300,
		}},
	}
}

type storedWebhookRefund struct {
	ID, AttemptID, Status, Reason, Currency, ChargeRef, PaymentRef string
	AmountOre                                                      int64
	CreatedBy, Outcome, FailureReason, RequestKey                  *string
}

// refunds returns the club's payment_refunds rows by Stripe refund id.
func (e *stripeEnv) refunds(t *testing.T) map[string]storedWebhookRefund {
	t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT COALESCE(provider_refund_id, ''), attempt_id::text, status, reason, currency, COALESCE(charge_ref, ''),
		        COALESCE(provider_payment_ref, ''), round(amount * 100)::bigint,
		        created_by::text, outcome, failure_reason, request_key::text
		   FROM payment_refunds WHERE club_id = $1`, e.clubID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]storedWebhookRefund{}
	for rows.Next() {
		var r storedWebhookRefund
		if err := rows.Scan(&r.ID, &r.AttemptID, &r.Status, &r.Reason, &r.Currency, &r.ChargeRef, &r.PaymentRef,
			&r.AmountOre, &r.CreatedBy, &r.Outcome, &r.FailureReason, &r.RequestKey); err != nil {
			t.Fatal(err)
		}
		out[r.ID] = r
	}
	return out
}

// seedRefundablePayment adds a captured card payment whose PaymentIntent
// names its attempt, as a mint leaves it.
func (e *stripeEnv) seedRefundablePayment(t *testing.T, suffix string) (orderID, attemptID, piID string) {
	t.Helper()
	orderID = e.seedOrder(t, "")
	attemptID = e.seedAttempt(t, orderID, "stripe", "cs_r"+suffix, "captured")
	piID = "pi_r" + suffix
	e.fake.intentMeta[piID] = map[string]string{"attempt_id": attemptID, "club_slug": e.h.cfg.ClubSlug}
	return orderID, attemptID, piID
}

// A refund made in the Stripe Dashboard gets a payment_refunds row with no
// person, the Dashboard as its reason and no outcome; a redelivery changes
// nothing, and the webhook writes only that table.
func TestStripeWebhook_DashboardRefundStoredOnce(t *testing.T) {
	e := newStripeEnv(t)
	orderID, attemptID, piID := e.seedRefundablePayment(t, "1")
	e.fake.addRefund("re_d1", piID, "ch_r1", "succeeded", 12300, "")
	orderBefore := e.order(t, orderID)

	for i := 0; i < 2; i++ {
		assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_rf_1", "ch_r1", piID)))
	}
	assertWebhookOK(t, e.post(t, refundUpdatedEvent("evt_rf_2", "re_d1", "ch_r1", piID, "succeeded")))

	got := e.refunds(t)
	if len(got) != 1 {
		t.Fatalf("refunds = %+v, want one", got)
	}
	r := got["re_d1"]
	if r.AttemptID != attemptID || r.Status != "succeeded" || r.Reason != "Stripe Dashboard" || r.CreatedBy != nil ||
		r.Outcome != nil || r.AmountOre != 12300 || r.Currency != "NOK" || r.ChargeRef != "ch_r1" || r.PaymentRef != piID {
		t.Errorf("stored refund = %+v", r)
	}
	if after := e.order(t, orderID); after != orderBefore {
		t.Errorf("order changed: %+v -> %+v", orderBefore, after)
	}
	if s := e.attemptStatus(t, attemptID); s != "captured" {
		t.Errorf("attempt = %s, want captured", s)
	}
	for table, want := range map[string]int{"journal_entries": 0, "refund_documents": 0, "payment_disputes": 0} {
		if n := e.count(t, `SELECT count(*) FROM `+table+` WHERE club_id = $1`, e.clubID); n != want {
			t.Errorf("%s has %d rows, want %d", table, n, want)
		}
	}
}

// A refund made by the button is found by the request key Brygge sent with
// it: the row learns the Stripe id and state and keeps what the person
// entered.
func TestStripeWebhook_ButtonRefundMatchedByKey(t *testing.T) {
	e := newStripeEnv(t)
	_, attemptID, piID := e.seedRefundablePayment(t, "2")
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"treasurer"})
	const key = "0b2f5f0e-6c55-4a2b-9f0e-1c7d3a6e9b21"
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, status, reason, created_by, request_key, outcome)
		 VALUES ($1, $2, 'stripe', 123, 'requested', 'Feil beløp', $3, $4::uuid, 'recollect')`,
		e.clubID, attemptID, userID, key); err != nil {
		t.Fatal(err)
	}
	e.fake.addRefund("re_b1", piID, "ch_r2", "pending", 12300, key)

	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_rf_3", "ch_r2", piID)))
	e.fake.setRefundStatus("re_b1", "succeeded")
	assertWebhookOK(t, e.post(t, refundUpdatedEvent("evt_rf_4", "re_b1", "ch_r2", piID, "succeeded")))

	got := e.refunds(t)
	if len(got) != 1 {
		t.Fatalf("refunds = %+v, want the one row", got)
	}
	r := got["re_b1"]
	if r.Status != "succeeded" || r.Reason != "Feil beløp" || r.CreatedBy == nil || *r.CreatedBy != userID ||
		r.Outcome == nil || *r.Outcome != "recollect" || r.RequestKey == nil || *r.RequestKey != key {
		t.Errorf("stored refund = %+v", r)
	}
}

// Stripe's refund states map onto Brygge's: pending and requires_action stay
// requested, failed and canceled are failed (with Stripe's reason), and a
// refund that failed after it succeeded follows.
func TestStripeWebhook_RefundStatusMapping(t *testing.T) {
	e := newStripeEnv(t)
	_, _, piID := e.seedRefundablePayment(t, "3")
	cases := []struct{ id, stripeStatus, want string }{
		{"re_s1", "pending", "requested"},
		{"re_s2", "requires_action", "requested"},
		{"re_s3", "succeeded", "succeeded"},
		{"re_s4", "failed", "failed"},
		{"re_s5", "canceled", "failed"},
	}
	for _, c := range cases {
		e.fake.addRefund(c.id, piID, "ch_r3", c.stripeStatus, 1000, "")
	}
	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_rf_5", "ch_r3", piID)))
	got := e.refunds(t)
	for _, c := range cases {
		if r, ok := got[c.id]; !ok || r.Status != c.want {
			t.Errorf("stripe %s stored as %+v, want %s", c.stripeStatus, r, c.want)
		}
	}

	e.fake.setRefundStatus("re_s1", "succeeded")
	e.fake.setRefundStatus("re_s3", "failed")
	assertWebhookOK(t, e.post(t, refundUpdatedEvent("evt_rf_6", "re_s1", "ch_r3", piID, "succeeded")))
	got = e.refunds(t)
	if got["re_s1"].Status != "succeeded" || got["re_s3"].Status != "failed" {
		t.Errorf("after the updates: re_s1 %s, re_s3 %s", got["re_s1"].Status, got["re_s3"].Status)
	}
	if len(got) != len(cases) {
		t.Errorf("%d rows, want %d", len(got), len(cases))
	}
}

// A payment that cannot be placed because Stripe could not be asked, or a
// refund list that cannot be read, answers 5xx so Stripe delivers the event
// again; the redelivery then stores the refund.
func TestStripeWebhook_RefundLookupFailureRedelivers(t *testing.T) {
	e := newStripeEnv(t)
	_, _, piID := e.seedRefundablePayment(t, "4")
	e.fake.addRefund("re_f1", piID, "ch_r4", "succeeded", 5000, "")

	e.fake.intentErrors[piID] = http.StatusInternalServerError
	if rec := e.post(t, chargeRefundedEvent("evt_rf_7", "ch_r4", piID)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("payment intent lookup failing: %d, want 500", rec.Code)
	}
	delete(e.fake.intentErrors, piID)

	e.fake.refundListStatus = http.StatusInternalServerError
	if rec := e.post(t, chargeRefundedEvent("evt_rf_7", "ch_r4", piID)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("refund list failing: %d, want 500", rec.Code)
	}
	if n := len(e.refunds(t)); n != 0 {
		t.Fatalf("%d refunds stored by failed deliveries", n)
	}
	e.fake.refundListStatus = 0

	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_rf_7", "ch_r4", piID)))
	if r, ok := e.refunds(t)["re_f1"]; !ok || r.Status != "succeeded" {
		t.Errorf("after the redelivery: %+v", e.refunds(t))
	}
}

// A refund of another deployment's payment, or of a charge Brygge knows
// nothing about, is left alone.
func TestStripeWebhook_RefundOfOtherPaymentsIgnored(t *testing.T) {
	e := newStripeEnv(t)
	e.fake.intentMeta["pi_other"] = map[string]string{"attempt_id": "00000000-0000-0000-0000-000000000000", "club_slug": "another-club"}
	e.fake.addRefund("re_o1", "pi_other", "ch_o", "succeeded", 5000, "")
	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_rf_8", "ch_o", "pi_other")))

	e.fake.intentMissing["pi_unknown"] = true
	e.fake.addRefund("re_o2", "pi_unknown", "ch_u", "succeeded", 5000, "")
	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_rf_9", "ch_u", "pi_unknown")))

	if n := len(e.refunds(t)); n != 0 {
		t.Errorf("%d refunds stored for payments that are not ours", n)
	}
}

// The payment is found from the stored charge when the PaymentIntent is not
// ours to read.
func TestStripeWebhook_RefundPlacedByStoredCharge(t *testing.T) {
	e := newStripeEnv(t)
	_, attemptID, _ := e.seedRefundablePayment(t, "5")
	e.fake.intentMissing["pi_gone"] = true
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, stripe_type, amount_ore, fee_ore, net_ore, currency,
		      created_at, status, attempt_id, charge_id, payment_intent_id)
		 VALUES ($1, 'txn_test_rf5', 'charge', 'charge', 45000, 0, 45000, 'nok', now(), 'booked', $2, 'ch_r5', 'pi_gone')`,
		e.clubID, attemptID); err != nil {
		t.Fatal(err)
	}
	e.fake.addRefund("re_c1", "pi_gone", "ch_r5", "succeeded", 2500, "")

	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_rf_10", "ch_r5", "pi_gone")))
	if r, ok := e.refunds(t)["re_c1"]; !ok || r.AttemptID != attemptID || r.AmountOre != 2500 {
		t.Errorf("refunds = %+v", e.refunds(t))
	}
}
