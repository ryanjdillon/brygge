package handlers

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// Tests of the webhook's kick of the automatic Stripe run (BRY-570): an
// event about one of the club's payments that moved money kicks once it is
// handled; an error, a payment that is not ours and an ignored event do not.

type fakeKicker struct {
	mu     sync.Mutex
	clubs  []string
	onKick func(clubID string)
}

func (k *fakeKicker) Kick(clubID string) {
	if k.onKick != nil {
		k.onKick(clubID)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.clubs = append(k.clubs, clubID)
}

func (k *fakeKicker) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.clubs)
}

func (e *stripeEnv) withKicker() *fakeKicker {
	k := &fakeKicker{}
	e.h.SetBookingKicker(k)
	return k
}

func TestRefundEventKicksBooking(t *testing.T) {
	e := newStripeEnv(t)
	k := e.withKicker()
	_, _, piID := e.seedRefundablePayment(t, "k1")
	e.fake.addRefund("re_k1", piID, "ch_rk1", "succeeded", 12300, "")

	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_k_1", "ch_rk1", piID)))
	assertWebhookOK(t, e.post(t, refundUpdatedEvent("evt_k_2", "re_k1", "ch_rk1", piID, "succeeded")))
	if k.count() != 2 || k.clubs[0] != e.clubID || k.clubs[1] != e.clubID {
		t.Errorf("kicks = %v, want two for the club", k.clubs)
	}
	// The webhook itself still books nothing.
	if n := e.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, e.clubID); n != 0 {
		t.Errorf("journal entries = %d, want none", n)
	}
}

func TestForeignRefundDoesNotKick(t *testing.T) {
	e := newStripeEnv(t)
	k := e.withKicker()
	e.fake.intentMeta["pi_other"] = map[string]string{"attempt_id": "00000000-0000-0000-0000-000000000000", "club_slug": "another-club"}
	e.fake.addRefund("re_ko1", "pi_other", "ch_ko", "succeeded", 5000, "")
	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_k_3", "ch_ko", "pi_other")))

	e.fake.intentMissing["pi_unknown"] = true
	e.fake.addRefund("re_ko2", "pi_unknown", "ch_ku", "succeeded", 5000, "")
	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_k_4", "ch_ku", "pi_unknown")))

	// An event Brygge only stores kicks nothing either.
	assertWebhookOK(t, e.post(t, sessionEvent(stripe.EventCheckoutSessionExpired, "evt_k_5", "cs_none")))
	if n := k.count(); n != 0 {
		t.Errorf("kicks = %v, want none", k.clubs)
	}
}

func TestDispatchErrorDoesNotKick(t *testing.T) {
	e := newStripeEnv(t)
	k := e.withKicker()
	_, _, piID := e.seedRefundablePayment(t, "k2")
	e.fake.addRefund("re_k2", piID, "ch_rk2", "succeeded", 5000, "")

	e.fake.refundListStatus = http.StatusInternalServerError
	if rec := e.post(t, chargeRefundedEvent("evt_k_6", "ch_rk2", piID)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("refund list failing: %d, want 500", rec.Code)
	}
	e.fake.intentErrors[piID] = http.StatusInternalServerError
	e.fake.refundListStatus = 0
	if rec := e.post(t, chargeRefundedEvent("evt_k_6", "ch_rk2", piID)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("unplaced payment: %d, want 500", rec.Code)
	}
	if n := k.count(); n != 0 {
		t.Fatalf("kicks after failed deliveries = %v, want none", k.clubs)
	}
	delete(e.fake.intentErrors, piID)

	// Stripe's redelivery succeeds and kicks.
	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_k_6", "ch_rk2", piID)))
	if n := k.count(); n != 1 {
		t.Errorf("kicks after the redelivery = %d, want 1", n)
	}
}

func TestNoKickerIsSafe(t *testing.T) {
	e := newStripeEnv(t)
	_, _, piID := e.seedRefundablePayment(t, "k3")
	e.fake.addRefund("re_k3", piID, "ch_rk3", "succeeded", 5000, "")
	assertWebhookOK(t, e.post(t, chargeRefundedEvent("evt_k_7", "ch_rk3", piID)))
	if r, ok := e.refunds(t)["re_k3"]; !ok || r.Status != "succeeded" {
		t.Errorf("refunds = %+v", e.refunds(t))
	}
}

// A capture kicks; so does a payment_intent.succeeded that settles; a
// dispute kicks only when its funds move.
func TestCaptureAndDisputeFundsKick(t *testing.T) {
	e, _ := newDisputeEnv(t)
	k := e.withKicker()
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	invID := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 711)
	orderID := e.seedOrder(t, userID)
	addOrderSource(t, e.pool, orderID, invID)
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_k1", "created")

	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_k_10", "cs_k1", 45000, "Test Member", "member@example.invalid")))
	if s := e.attemptStatus(t, attemptID); s != "captured" || k.count() != 1 {
		t.Fatalf("after the capture: attempt %s, kicks %v", s, k.clubs)
	}

	assertWebhookOK(t, e.post(t, intentEvent(stripe.EventPaymentIntentSucceeded, "evt_k_11", fakeStripePI("cs_k1"), attemptID, time.Now().Unix())))
	if n := k.count(); n != 2 {
		t.Fatalf("after payment_intent.succeeded: kicks %v, want 2", k.clubs)
	}

	e.fake.intentMeta["pi_kd"] = map[string]string{"attempt_id": attemptID, "club_slug": e.h.cfg.ClubSlug}
	due := time.Now().Add(7 * 24 * time.Hour).Unix()
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_k_12", "dp_k1", "pi_kd", "ch_kd", "needs_response", due)))
	if n := k.count(); n != 2 {
		t.Fatalf("dispute created kicked: %v", k.clubs)
	}
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeFundsWithdrawn, "evt_k_13", "dp_k1", "pi_kd", "ch_kd", "needs_response", due)))
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeFundsReinstated, "evt_k_14", "dp_k1", "pi_kd", "ch_kd", "won", due)))
	if n := k.count(); n != 4 {
		t.Errorf("after the funds events: kicks %v, want 4", k.clubs)
	}
}

// A payout event kicks the run once the payout's members are linked, so the
// run's sweep can clear it.
func TestPayoutEventKicksAfterIngest(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool)
	var slug string
	if err := pool.QueryRow(context.Background(), `SELECT slug FROM clubs WHERE id = $1`, club.ID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ClubSlug: slug, StripeWebhookSecret: stripeTestWebhookSecret}
	e := &stripeEnv{pool: pool, clubID: club.ID,
		h: NewStripeWebhookHandler(pool, cfg, newFakeStripeBalanceAPI(t), zerolog.Nop())}
	e.h.SetLedger(svc)
	linkedAtKick := -1
	k := &fakeKicker{onKick: func(string) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM stripe_balance_transactions WHERE club_id = $1 AND payout_id IS NOT NULL`, club.ID,
		).Scan(&n); err != nil {
			t.Error(err)
		}
		linkedAtKick = n
	}}
	e.h.SetBookingKicker(k)

	rec := e.post(t, map[string]any{
		"id": "evt_test_payout_k1", "type": stripe.EventPayoutPaid, "created": 1790208000,
		"data": map[string]any{"object": map[string]any{"id": "po_test_1", "object": "payout", "status": "paid"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body)
	}
	e.h.payouts.Wait()
	if k.count() != 1 || k.clubs[0] != club.ID {
		t.Fatalf("kicks = %v, want one for the club", k.clubs)
	}
	if linkedAtKick != 2 {
		t.Errorf("members linked when kicked = %d, want 2 (kicked after the ingest)", linkedAtKick)
	}
}

// A capture that failed and was voided or released, and a succeeded intent
// whose amount does not match, take no money Brygge records: no kick. A
// capture that went through after all kicks.
func TestFailedCaptureDoesNotKick(t *testing.T) {
	for _, tc := range []struct {
		name, intentAfter string
		wantKicks         int
	}{
		{"voided", stripe.PaymentIntentRequiresCapture, 0},
		{"released", "processing", 0},
		{"captured after all", stripe.PaymentIntentSucceeded, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newStripeEnv(t)
			k := e.withKicker()
			e.fake.captureStatus = http.StatusInternalServerError
			e.fake.intentAfterCapture = tc.intentAfter
			orderID := e.seedOrder(t, "")
			e.seedAttempt(t, orderID, "stripe", "cs_kf", "created")
			assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_kf", "cs_kf", 45000, "", "")))
			if n := k.count(); n != tc.wantKicks {
				t.Errorf("kicks = %d, want %d", n, tc.wantKicks)
			}
		})
	}

	t.Run("partial capture intent", func(t *testing.T) {
		e := newStripeEnv(t)
		k := e.withKicker()
		orderID := e.seedOrder(t, "")
		attemptID := e.seedAttempt(t, orderID, "stripe", "cs_kp", "created")
		assertWebhookOK(t, e.post(t, intentEventReceived(stripe.EventPaymentIntentSucceeded, "evt_kp", "pi_for_cs_kp", attemptID, time.Now().Unix(), 20000)))
		if n := k.count(); n != 0 {
			t.Errorf("kicks = %d, want none for an amount that does not settle", n)
		}
	})
}

// A dispute Brygge cannot tie to one of its attempts is stored, but its
// funds events do not kick; the balance books it at the next run or sync.
func TestUntiedDisputeFundsDoNotKick(t *testing.T) {
	e, _ := newDisputeEnv(t)
	k := e.withKicker()
	due := time.Now().Add(7 * 24 * time.Hour).Unix()
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeFundsWithdrawn, "evt_ku", "dp_ku", "pi_ku", "ch_ku", "needs_response", due)))
	if d, ok := e.dispute(t, "dp_ku"); !ok || d.Attempt != nil {
		t.Fatalf("dispute = %+v (found %v), want stored with no attempt", d, ok)
	}
	if n := k.count(); n != 0 {
		t.Errorf("kicks = %d, want none", n)
	}
}
