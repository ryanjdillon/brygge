package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// injectFault makes every UPDATE of table that matches the trigger condition
// fail with a database error, the way a lock timeout or a dropped connection
// fails one statement, and returns the function that heals it. A trigger
// rather than a closed pool: the lookups before the statement keep working, so
// the fault lands on exactly the write under test, and a settlement
// transaction that hits it rolls back as a real failure would.
func injectFault(t *testing.T, pool *pgxpool.Pool, table, when string) (heal func()) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`CREATE OR REPLACE FUNCTION settle_fault() RETURNS trigger LANGUAGE plpgsql AS
		 $$ BEGIN RAISE EXCEPTION 'injected settlement fault'; END $$`,
	); err != nil {
		t.Fatalf("create fault function: %v", err)
	}
	name := "settle_fault_" + table
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`CREATE TRIGGER %s BEFORE UPDATE ON %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION settle_fault()`,
		name, table, when,
	)); err != nil {
		t.Fatalf("create fault trigger: %v", err)
	}
	return func() {
		t.Helper()
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, table)); err != nil {
			t.Fatalf("drop fault trigger: %v", err)
		}
	}
}

// settleFaults are the writes a capture's settlement makes, each as a fault on
// the statement that makes it.
var settleFaults = []struct{ name, table, when string }{
	{"attempt update", "payment_attempts", "NEW.status = 'captured'"},
	{"order update", "orders", "NEW.status = 'paid'"},
	{"invoice marker", "invoices", "NEW.online_paid_at IS NOT NULL"},
}

func invoiceMarker(t *testing.T, pool *pgxpool.Pool, id string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT online_paid_at FROM invoices WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("load invoice: %v", err)
	}
	return at
}

func assertWebhookStatus(t *testing.T, rec *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s answered %d, want %d", what, rec.Code, want)
	}
}

// A decision that cannot be made is not a decision to capture. With the
// database failing there is no order row lock to decide under, so capturing
// could take a payment the order does not need; the authorization is left for
// the provider's redelivery.
func TestAuthorizeAttempt_DatabaseErrorDoesNotCapture(t *testing.T) {
	s := orderSettlement{db: closedPool(t), log: zerolog.Nop()}
	d, err := s.authorizeAttempt(context.Background(), paymentAttempt{ID: uuid.NewString(), OrderID: uuid.NewString()})
	if err == nil {
		t.Fatal("authorizeAttempt hid the database error")
	}
	if d == authCapture {
		t.Error("a failed decision answers capture")
	}
}

// The decision reads nothing until it holds the order row: while another
// transaction holds it, the decision waits (here until its deadline) instead of
// deciding on a stale read.
func TestAuthorizeAttempt_DecidesUnderTheOrderLock(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	bg := context.Background()
	s := orderSettlement{db: pool, log: zerolog.Nop()}
	clubID := testutil.SeedClub(t, pool)
	orderID, _ := seedCheckoutOrder(t, pool, clubID, "")
	var attemptID string
	if err := pool.QueryRow(bg,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount)
		 VALUES ($1, $2, 'stripe', 'cs_lock', 450) RETURNING id`, clubID, orderID,
	).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.attemptByReference(bg, clubID, "stripe", "cs_lock")
	if err != nil || !ok {
		t.Fatalf("attemptByReference = %v, %v", ok, err)
	}

	holder, err := pool.Begin(bg)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(bg)
	if _, err := holder.Exec(bg, `SELECT 1 FROM orders WHERE id = $1 FOR UPDATE`, orderID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(bg, 300*time.Millisecond)
	defer cancel()
	d, err := s.authorizeAttempt(ctx, a)
	if err == nil || d == authCapture {
		t.Fatalf("authorizeAttempt under a held order lock = %v, %v; want an error and no capture", d, err)
	}
	var status string
	if err := pool.QueryRow(bg, `SELECT status FROM payment_attempts WHERE id = $1`, attemptID).Scan(&status); err != nil || status != "created" {
		t.Errorf("attempt = %q (%v), want untouched", status, err)
	}

	if err := holder.Rollback(bg); err != nil {
		t.Fatal(err)
	}
	if d, err := s.authorizeAttempt(bg, a); err != nil || d != authCapture {
		t.Errorf("authorizeAttempt with the lock free = %v, %v; want capture", d, err)
	}
}

// A decision whose own write fails leaves the attempt as it was, nothing is
// called at Stripe, and the redelivery decides and settles exactly once.
func TestStripeWebhook_DecisionFailureNeitherCapturesNorCancels(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, e *stripeEnv, orderID string)
		fault string // the status the decision writes
		// after the redelivery:
		wantCaptures, wantCancels int
		wantAttempt, wantOrder    string
	}{
		{
			name:         "payment the order needs",
			fault:        "authorized",
			wantCaptures: 1, wantAttempt: "captured", wantOrder: "paid",
		},
		{
			name: "duplicate of an order another attempt paid",
			setup: func(t *testing.T, e *stripeEnv, orderID string) {
				if _, err := e.pool.Exec(context.Background(),
					`UPDATE orders SET status = 'paid', provider = 'vipps', paid_at = '2026-06-01T10:00:00Z' WHERE id = $1`, orderID,
				); err != nil {
					t.Fatal(err)
				}
				e.seedAttempt(t, orderID, "vipps", "chk-paid", "captured")
			},
			fault:       "cancelled",
			wantCancels: 1, wantAttempt: "cancelled", wantOrder: "paid",
		},
		{
			name: "second authorization of an order",
			setup: func(t *testing.T, e *stripeEnv, orderID string) {
				e.seedAttempt(t, orderID, "stripe", "cs_first", "authorized")
			},
			fault:       "cancelled",
			wantCancels: 1, wantAttempt: "cancelled", wantOrder: "pending",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newStripeEnv(t)
			orderID := e.seedOrder(t, "")
			if tc.setup != nil {
				tc.setup(t, e, orderID)
			}
			attemptID := e.seedAttempt(t, orderID, "stripe", "cs_d", "created")
			evt := sessionCompletedEvent("evt_d", "cs_d", 45000, "", "")
			wasOrder := e.order(t, orderID).Status

			heal := injectFault(t, e.pool, "payment_attempts", "NEW.status = '"+tc.fault+"'")
			assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "webhook with the decision failing")
			if c, x := e.fake.captureCalls(), e.fake.cancelCalls(); len(c) != 0 || len(x) != 0 {
				t.Errorf("captures %v, cancels %v with the decision failing; want none", c, x)
			}
			if s := e.attemptStatus(t, attemptID); s != "created" {
				t.Errorf("attempt = %q after a failed decision, want untouched", s)
			}
			if o := e.order(t, orderID).Status; o != wasOrder {
				t.Errorf("order = %q after a failed decision, want %q", o, wasOrder)
			}

			heal()
			assertWebhookOK(t, e.post(t, evt))
			if got := len(e.fake.captureCalls()); got != tc.wantCaptures {
				t.Errorf("captures after redelivery = %v, want %d", e.fake.captureCalls(), tc.wantCaptures)
			}
			if got := len(e.fake.cancelCalls()); got != tc.wantCancels {
				t.Errorf("cancels after redelivery = %v, want %d", e.fake.cancelCalls(), tc.wantCancels)
			}
			if s := e.attemptStatus(t, attemptID); s != tc.wantAttempt {
				t.Errorf("attempt = %q, want %q", s, tc.wantAttempt)
			}
			if o := e.order(t, orderID).Status; o != tc.wantOrder {
				t.Errorf("order = %q, want %q", o, tc.wantOrder)
			}
		})
	}
}

// A write that fails after Stripe took the money answers 5xx; the redelivery
// asks Stripe to capture under the same Idempotency-Key (so Stripe takes the
// money once), and finds the attempt still authorized, so it writes the whole
// settlement. Nothing is half-written in between.
func TestStripeWebhook_SettleWriteFailureAfterCaptureIsRetried(t *testing.T) {
	for _, f := range settleFaults {
		t.Run(f.name, func(t *testing.T) {
			e := newStripeEnv(t)
			var logs bytes.Buffer
			e.h.orders.log = zerolog.New(&logs)
			userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
			invID := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 940)
			orderID := e.seedOrder(t, userID)
			addOrderSource(t, e.pool, orderID, invID)
			attemptID := e.seedAttempt(t, orderID, "stripe", "cs_w", "created")
			evt := sessionCompletedEvent("evt_w", "cs_w", 45000, "", "")
			key := "pi_for_cs_w capture2-" + attemptID

			heal := injectFault(t, e.pool, f.table, f.when)
			assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "webhook with a settlement write failing")
			if got := e.fake.captureCalls(); len(got) != 1 || got[0] != key {
				t.Fatalf("captures = %v, want the one call under %q", got, key)
			}
			if s := e.attemptStatus(t, attemptID); s != "authorized" {
				t.Errorf("attempt = %q, want authorized: the settlement is all or nothing", s)
			}
			if o := e.order(t, orderID); o.Status != "pending" || o.PaidAt != nil {
				t.Errorf("order = %+v, want untouched", o)
			}
			if at := invoiceMarker(t, e.pool, invID); at != nil {
				t.Errorf("invoice marker = %v, want none", at)
			}

			heal()
			assertWebhookOK(t, e.post(t, evt))
			for _, c := range e.fake.captureCalls() {
				if c != key {
					t.Errorf("capture %q, want only calls under the attempt's key %q", c, key)
				}
			}
			if s := e.attemptStatus(t, attemptID); s != "captured" {
				t.Errorf("attempt = %q, want captured", s)
			}
			paid := e.order(t, orderID)
			if paid.Status != "paid" || paid.PaidAt == nil || paid.Provider != "stripe" {
				t.Fatalf("order = %+v, want paid by stripe", paid)
			}
			if at := invoiceMarker(t, e.pool, invID); at == nil {
				t.Error("invoice marker missing after the redelivery")
			}
			if c := e.fake.cancelCalls(); len(c) != 0 {
				t.Errorf("voided %v: the payment was the order's", c)
			}

			// A further delivery finds the attempt captured: no capture, no change.
			calls := len(e.fake.captureCalls())
			assertWebhookOK(t, e.post(t, evt))
			if got := len(e.fake.captureCalls()); got != calls {
				t.Errorf("captures = %d after a settled attempt, was %d", got, calls)
			}
			if again := e.order(t, orderID); !again.PaidAt.Equal(*paid.PaidAt) {
				t.Errorf("paid_at moved from %v to %v", paid.PaidAt, again.PaidAt)
			}
			if strings.Contains(logs.String(), "double payment") {
				t.Errorf("the retry was reported as a double payment: %s", logs.String())
			}
		})
	}
}

// payment_intent.succeeded settles through the same write, so it is retried
// the same way.
func TestStripeWebhook_IntentSucceededWriteFailureIsRetried(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_i", "authorized")
	evt := intentEvent(stripe.EventPaymentIntentSucceeded, "evt_i", "pi_for_cs_i", attemptID, 1782900000)

	heal := injectFault(t, e.pool, "orders", "NEW.status = 'paid'")
	assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "intent.succeeded with the order write failing")
	heal()
	assertWebhookOK(t, e.post(t, evt))
	if o := e.order(t, orderID); o.Status != "paid" || o.PaidAt == nil || !o.PaidAt.Equal(time.Unix(1782900000, 0)) {
		t.Errorf("order = %+v, want paid at the event time", o)
	}
	if s := e.attemptStatus(t, attemptID); s != "captured" {
		t.Errorf("attempt = %q, want captured", s)
	}
}

// The write that records a failed or abandoned attempt answers 5xx when it
// fails, and the redelivery finishes whichever of the two writes was lost.
func TestStripeWebhook_MarkFailedWriteFailureIsRetried(t *testing.T) {
	events := map[string]func(attemptID string) map[string]any{
		"session expired": func(string) map[string]any {
			return sessionEvent(stripe.EventCheckoutSessionExpired, "evt_x", "cs_x")
		},
		"intent canceled": func(attemptID string) map[string]any {
			return intentEvent(stripe.EventPaymentIntentCanceled, "evt_x", "pi_x", attemptID, 1)
		},
	}
	faults := []struct{ name, table, wantAttempt string }{
		{"attempt update", "payment_attempts", "created"},
		{"order update", "orders", "failed"},
	}
	for en, mk := range events {
		for _, f := range faults {
			t.Run(en+"/"+f.name, func(t *testing.T) {
				e := newStripeEnv(t)
				orderID := e.seedOrder(t, "")
				attemptID := e.seedAttempt(t, orderID, "stripe", "cs_x", "created")
				evt := mk(attemptID)

				heal := injectFault(t, e.pool, f.table, "NEW.status = 'failed'")
				assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "webhook with the failed-mark failing")
				if s := e.attemptStatus(t, attemptID); s != f.wantAttempt {
					t.Errorf("attempt = %q after the fault, want %q", s, f.wantAttempt)
				}
				if o := e.order(t, orderID).Status; o != "pending" {
					t.Errorf("order = %q after the fault, want pending", o)
				}

				heal()
				assertWebhookOK(t, e.post(t, evt))
				if s := e.attemptStatus(t, attemptID); s != "failed" {
					t.Errorf("attempt = %q after the redelivery, want failed", s)
				}
				if o := e.order(t, orderID).Status; o != "failed" {
					t.Errorf("order = %q after the redelivery, want failed", o)
				}
			})
		}
	}
}

// An amount that does not match is a fact about the event, not a failure to
// retry: it voids and fails the attempt and answers 200 as before, unless the
// write that fails the attempt itself fails.
func TestStripeWebhook_AmountMismatchWriteFailureIsRetried(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_m", "created")
	evt := sessionCompletedEvent("evt_m", "cs_m", 12300, "", "")

	heal := injectFault(t, e.pool, "payment_attempts", "NEW.status = 'failed'")
	assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "mismatched amount with the failed-mark failing")
	heal()
	assertWebhookOK(t, e.post(t, evt))
	if s := e.attemptStatus(t, attemptID); s != "failed" {
		t.Errorf("attempt = %q, want failed", s)
	}
	if len(e.fake.captureCalls()) != 0 {
		t.Error("a mismatched payment was captured")
	}
}

// Releasing the attempt after a failed capture is what lets the payer pay
// again; when it fails the event is delivered again rather than leaving the
// attempt authorized for good.
func TestStripeWebhook_ReleaseFailureIsRetried(t *testing.T) {
	e := newStripeEnv(t)
	e.fake.captureStatus = http.StatusInternalServerError
	e.fake.intentAfterCapture = "processing"
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_r2", "created")
	evt := sessionCompletedEvent("evt_r2", "cs_r2", 45000, "", "")

	heal := injectFault(t, e.pool, "payment_attempts", "OLD.status = 'authorized' AND NEW.status = 'created'")
	assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "webhook with the release failing")
	if s := e.attemptStatus(t, attemptID); s != "authorized" {
		t.Errorf("attempt = %q, want authorized until released", s)
	}
	heal()
	assertWebhookOK(t, e.post(t, evt))
	if s := e.attemptStatus(t, attemptID); s != "created" {
		t.Errorf("attempt = %q after the redelivery, want released to created", s)
	}
}

// Vipps: an AUTHORIZED whose decision fails captures and cancels nothing and is
// retried.
func TestVippsWebhook_DecisionFailureNeitherCapturesNorCancels(t *testing.T) {
	tests := []struct {
		name         string
		orderStatus  string
		fault        string
		wantCaptures int
		wantCancels  int
		wantAttempt  string
	}{
		{"payment the order needs", "pending", "authorized", 1, 0, "authorized"},
		// The order is already paid by another attempt: capturing would charge twice.
		{"duplicate of a paid order", "paid", "cancelled", 0, 1, "cancelled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newWebhookEnv(t, true)
			orderID := e.seedReferencedOrder(t, "", "chk-orig", tc.orderStatus, "")
			ref := "chk-dup"
			e.seedAttempt(t, orderID, ref, "created")
			evt := authorizedEvent(ref, "evt-dec")

			heal := injectFault(t, e.pool, "payment_attempts", "NEW.status = '"+tc.fault+"'")
			assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "webhook with the decision failing")
			if c, x := e.fake.captureCalls(), e.fake.cancelCalls(); len(c) != 0 || len(x) != 0 {
				t.Errorf("captures %v, cancels %v with the decision failing; want none", c, x)
			}
			if s := e.attemptStatus(t, ref); s != "created" {
				t.Errorf("attempt = %q after a failed decision, want untouched", s)
			}

			heal()
			assertWebhookOK(t, e.post(t, evt))
			caps := e.fake.captureCalls()
			if len(caps) != tc.wantCaptures || len(e.fake.cancelCalls()) != tc.wantCancels {
				t.Errorf("captures %v, cancels %v; want %d and %d", caps, e.fake.cancelCalls(), tc.wantCaptures, tc.wantCancels)
			}
			if tc.wantCaptures == 1 && caps[0].IdempotencyKey != "capture-"+ref {
				t.Errorf("capture key = %q, want the attempt's stable key", caps[0].IdempotencyKey)
			}
			if s := e.attemptStatus(t, ref); s != tc.wantAttempt {
				t.Errorf("attempt = %q, want %q", s, tc.wantAttempt)
			}
		})
	}
}

// Vipps settles on CAPTURED. A write that fails there answers 5xx, leaves the
// whole settlement undone, and the redelivery completes it once.
func TestVippsWebhook_CapturedWriteFailureIsRetried(t *testing.T) {
	for i, f := range settleFaults {
		t.Run(f.name, func(t *testing.T) {
			e := newWebhookEnv(t, true)
			var logs bytes.Buffer
			e.h.orders.log = zerolog.New(&logs)
			userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
			invID := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 950+i)
			ref := fmt.Sprintf("chk-cw-%d", i)
			orderID := e.seedReferencedOrder(t, userID, ref, "pending", "")
			addOrderSource(t, e.pool, orderID, invID)
			evt := capturedEvent(ref, "evt-cw", "2026-06-20T10:00:00Z")

			heal := injectFault(t, e.pool, f.table, f.when)
			assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "CAPTURED with a settlement write failing")
			if s := e.attemptStatus(t, ref); s != "created" {
				t.Errorf("attempt = %q, want untouched: the settlement is all or nothing", s)
			}
			if o := e.order(t, orderID); o.Status != "pending" || o.PaidAt != nil {
				t.Errorf("order = %+v, want untouched", o)
			}
			if at := invoiceMarker(t, e.pool, invID); at != nil {
				t.Errorf("invoice marker = %v, want none", at)
			}

			heal()
			assertWebhookOK(t, e.post(t, evt))
			want := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
			if o := e.order(t, orderID); o.Status != "paid" || o.PaidAt == nil || !o.PaidAt.Equal(want) {
				t.Errorf("order = %+v, want paid at %v", o, want)
			}
			if s := e.attemptStatus(t, ref); s != "captured" {
				t.Errorf("attempt = %q, want captured", s)
			}
			if at := invoiceMarker(t, e.pool, invID); at == nil || !at.Equal(want) {
				t.Errorf("invoice marker = %v, want %v", at, want)
			}

			// Redelivery of the settled event changes nothing and reports nothing.
			assertWebhookOK(t, e.post(t, evt))
			if o := e.order(t, orderID); !o.PaidAt.Equal(want) {
				t.Errorf("paid_at moved to %v", o.PaidAt)
			}
			if n := e.count(t, `SELECT count(*) FROM vipps_webhook_events WHERE club_id = $1 AND event_id = 'evt-cw'`, e.clubID); n != 1 {
				t.Errorf("stored events = %d, want 1", n)
			}
			if strings.Contains(logs.String(), "double payment") {
				t.Errorf("the retry was reported as a double payment: %s", logs.String())
			}
			if n := len(e.fake.captureCalls()); n != 0 {
				t.Errorf("CAPTURED called the capture API %d times", n)
			}
		})
	}
}

// A capture that fails at Vipps and cannot be released, and a terminal event
// that cannot be recorded, are retried the same way.
func TestVippsWebhook_ReleaseAndTerminalWriteFailuresAreRetried(t *testing.T) {
	t.Run("release after a failed capture", func(t *testing.T) {
		e := newWebhookEnv(t, true)
		e.fake.captureStatus = http.StatusInternalServerError
		e.seedReferencedOrder(t, "", "chk-rel", "pending", "")
		evt := authorizedEvent("chk-rel", "evt-rel")

		heal := injectFault(t, e.pool, "payment_attempts", "OLD.status = 'authorized' AND NEW.status = 'created'")
		assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "webhook with the release failing")
		if s := e.attemptStatus(t, "chk-rel"); s != "authorized" {
			t.Errorf("attempt = %q, want authorized until released", s)
		}
		heal()
		assertWebhookOK(t, e.post(t, evt))
		if s := e.attemptStatus(t, "chk-rel"); s != "created" {
			t.Errorf("attempt = %q after the redelivery, want released to created", s)
		}
	})

	for _, f := range []struct{ name, table string }{{"attempt update", "payment_attempts"}, {"order update", "orders"}} {
		t.Run("terminal/"+f.name, func(t *testing.T) {
			e := newWebhookEnv(t, true)
			orderID := e.seedReferencedOrder(t, "", "chk-term-"+f.table, "pending", "")
			evt := terminatedEvent("chk-term-"+f.table, "evt-term")

			heal := injectFault(t, e.pool, f.table, "NEW.status = 'failed'")
			assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "terminal event with the failed-mark failing")
			if o := e.order(t, orderID).Status; o != "pending" {
				t.Errorf("order = %q after the fault, want pending", o)
			}
			heal()
			assertWebhookOK(t, e.post(t, evt))
			if s := e.attemptStatus(t, "chk-term-"+f.table); s != "failed" {
				t.Errorf("attempt = %q, want failed", s)
			}
			if o := e.order(t, orderID).Status; o != "failed" {
				t.Errorf("order = %q, want failed", o)
			}
		})
	}
}

// Nothing that Vipps or Stripe can be told to stop retrying is turned into a
// 5xx: an event for an unknown payment, and a replay of a settled one, stay 200.
func TestSettlement_ExpectedNoOpsStay200(t *testing.T) {
	e := newWebhookEnv(t, true)
	ref := "chk-noop"
	e.seedReferencedOrder(t, "", ref, "pending", "")
	assertWebhookOK(t, e.post(t, capturedEvent(ref, "evt-noop-1", "")))
	assertWebhookOK(t, e.post(t, capturedEvent(ref, "evt-noop-2", "")))
	assertWebhookOK(t, e.post(t, terminatedEvent(ref, "evt-noop-3")))
	assertWebhookOK(t, e.post(t, authorizedEvent(ref, "evt-noop-4")))
	assertWebhookOK(t, e.post(t, capturedEvent("chk-not-ours", "evt-noop-5", "")))
	if n := len(e.fake.captureCalls()); n != 0 {
		t.Errorf("capture calls = %d, want none for a settled attempt", n)
	}
}

// injectDelay makes every UPDATE of table that matches the condition take
// delay longer, so a test can act while the statement is running.
func injectDelay(t *testing.T, pool *pgxpool.Pool, table, when string, delay time.Duration) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`CREATE OR REPLACE FUNCTION settle_delay() RETURNS trigger LANGUAGE plpgsql AS
		 $$ BEGIN PERFORM pg_sleep(%f); RETURN NEW; END $$`, delay.Seconds(),
	)); err != nil {
		t.Fatalf("create delay function: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`CREATE TRIGGER settle_delay_%s BEFORE UPDATE ON %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION settle_delay()`,
		table, table, when,
	)); err != nil {
		t.Fatalf("create delay trigger: %v", err)
	}
}

// The refund booking locks an order's fakturas and then writes the order.
// settleCapture takes the same locks in the same order, so a booking holding
// the faktura makes the settlement wait without it holding the order, and the
// booking's own write to the order goes through instead of deadlocking.
func TestSettleCapture_LocksFakturasBeforeTheOrder(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	bg := context.Background()
	s := orderSettlement{db: pool, log: zerolog.Nop()}
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 960)
	orderID, _ := seedCheckoutOrder(t, pool, clubID, userID)
	addOrderSource(t, pool, orderID, invID)
	if _, err := pool.Exec(bg,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount)
		 VALUES ($1, $2, 'stripe', 'cs_lockorder', 'authorized', 450)`, clubID, orderID,
	); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.attemptByReference(bg, clubID, "stripe", "cs_lockorder")
	if err != nil || !ok {
		t.Fatalf("attemptByReference = %v, %v", ok, err)
	}

	booking, err := pool.Begin(bg)
	if err != nil {
		t.Fatal(err)
	}
	defer booking.Rollback(bg)
	if _, err := booking.Exec(bg, `SELECT 1 FROM invoices WHERE id = $1 FOR NO KEY UPDATE`, invID); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- s.markAttemptCaptured(bg, a, time.Time{}, nil) }()

	select {
	case err := <-done:
		t.Fatalf("settlement finished (%v) while a booking held the faktura", err)
	case <-time.After(400 * time.Millisecond):
	}
	// Waiting on the faktura, the settlement does not hold the order yet.
	probe, err := pool.Begin(bg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Exec(bg, `SELECT 1 FROM orders WHERE id = $1 FOR UPDATE NOWAIT`, orderID); err != nil {
		t.Errorf("settlement holds the order while it waits for the faktura: %v", err)
	}
	probe.Rollback(bg)

	// The booking now writes the order, as markOrderRefundedTx does.
	if _, err := booking.Exec(bg, `UPDATE orders SET updated_at = now() WHERE id = $1`, orderID); err != nil {
		t.Fatalf("booking could not write the order: %v", err)
	}
	if err := booking.Commit(bg); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("settlement after the booking: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settlement still waiting after the booking committed")
	}
	var status string
	if err := pool.QueryRow(bg, `SELECT status::text FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil || status != "paid" {
		t.Errorf("order = %q (%v), want paid", status, err)
	}
}

// Once Stripe has taken the money, the request ending (a disconnect, a
// timeout upstream) must not roll the settlement back.
func TestStripeWebhook_SettlementSurvivesACancelledRequest(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_c", "created")
	injectDelay(t, e.pool, "payment_attempts", "NEW.status = 'captured'", 500*time.Millisecond)

	body, err := json.Marshal(sessionCompletedEvent("evt_c", "cs_c", 45000, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	var evt stripe.Event
	if err := json.Unmarshal(body, &evt); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// End the request while the settlement write is running, after the
	// capture has been answered.
	go func() {
		for len(e.fake.captureCalls()) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	if err := e.h.dispatch(ctx, e.clubID, &evt, nil, nil); err != nil {
		t.Fatalf("dispatch after the request ended: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("the request was not cancelled during the settlement; the test proves nothing")
	}
	if s := e.attemptStatus(t, attemptID); s != "captured" {
		t.Errorf("attempt = %q, want captured", s)
	}
	if o := e.order(t, orderID); o.Status != "paid" {
		t.Errorf("order = %q, want paid", o.Status)
	}
}
