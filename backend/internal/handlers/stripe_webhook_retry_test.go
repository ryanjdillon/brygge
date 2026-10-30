package handlers

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/stripe"
)

// Stripe emits payment_intent.succeeded the moment the capture happens, so it
// can reach the webhook while the capture's own handler is still waiting for
// the answer. Whichever settles first wins; the other finds the attempt
// captured and changes nothing, and the money is taken once.
func TestStripeWebhook_IntentSucceededDuringCapture(t *testing.T) {
	e := newStripeEnv(t)
	var logs bytes.Buffer
	e.h.orders.log = zerolog.New(&logs)
	e.fake.dedupCaptures = true
	gate := newCaptureGate()
	e.fake.captureGate = gate
	// A failed assertion while the capture is held would leave the fake's
	// handler blocked, and httptest.Server.Close waits for it.
	var releaseOnce sync.Once
	releaseCapture := func() { releaseOnce.Do(func() { close(gate.release) }) }
	t.Cleanup(releaseCapture)
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_race", "created")
	session := sessionCompletedEvent("evt_race_session", "cs_race", 45000, "", "")
	eventTime := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	intent := intentEvent(stripe.EventPaymentIntentSucceeded, "evt_race_intent", fakeStripePI("cs_race"), attemptID, eventTime.Unix())

	sessionDone := make(chan int, 1)
	go func() { sessionDone <- e.post(t, session).Code }()
	select {
	case <-gate.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the session handler never reached the capture")
	}
	if s := e.attemptStatus(t, attemptID); s != "authorized" {
		t.Fatalf("attempt = %q while the capture is in flight, want authorized", s)
	}

	assertWebhookOK(t, e.post(t, intent))
	if s := e.attemptStatus(t, attemptID); s != "captured" {
		t.Errorf("attempt = %q after payment_intent.succeeded, want captured", s)
	}
	releaseCapture()
	select {
	case code := <-sessionDone:
		if code != http.StatusOK {
			t.Errorf("session handler answered %d, want 200", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the session handler never finished")
	}

	if got := e.fake.captureCalls(); len(got) != 1 {
		t.Errorf("captures = %v, want one", got)
	}
	if c := e.fake.cancelCalls(); len(c) != 0 {
		t.Errorf("voided %v: the payment was the order's", c)
	}
	o := e.order(t, orderID)
	if o.Status != "paid" || o.Provider != "stripe" || o.PaidAt == nil || !o.PaidAt.Equal(eventTime) {
		t.Errorf("order = %+v, want paid by stripe at the event that settled first (%v)", o, eventTime)
	}
	if strings.Contains(logs.String(), "double payment") {
		t.Errorf("the race was reported as a double payment: %s", logs.String())
	}
}

// The same two events delivered at the same moment, in whichever order the
// database sees them, end in one settled attempt and one capture.
func TestStripeWebhook_SessionAndIntentSucceededConcurrently(t *testing.T) {
	e := newStripeEnv(t)
	var logs bytes.Buffer
	e.h.orders.log = zerolog.New(&logs)
	e.fake.dedupCaptures = true

	const trials = 12
	for i := 0; i < trials; i++ {
		ref := "cs_conc_" + string(rune('a'+i))
		orderID := e.seedOrder(t, "")
		attemptID := e.seedAttempt(t, orderID, "stripe", ref, "created")
		session := sessionCompletedEvent("evt_conc_s_"+ref, ref, 45000, "", "")
		intent := intentEvent(stripe.EventPaymentIntentSucceeded, "evt_conc_i_"+ref, fakeStripePI(ref), attemptID, time.Now().Unix())

		var wg sync.WaitGroup
		start := make(chan struct{})
		codes := make([]int, 2)
		for n, evt := range []map[string]any{session, intent} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				codes[n] = e.post(t, evt).Code
			}()
		}
		close(start)
		wg.Wait()

		for n, code := range codes {
			if code != http.StatusOK {
				t.Fatalf("trial %d: event %d answered %d, want 200", i, n, code)
			}
		}
		if s := e.attemptStatus(t, attemptID); s != "captured" {
			t.Errorf("trial %d: attempt = %q, want captured", i, s)
		}
		paid := e.order(t, orderID)
		if paid.Status != "paid" || paid.PaidAt == nil {
			t.Errorf("trial %d: order = %+v, want paid", i, paid)
			continue
		}
		// A redelivery of both changes nothing.
		assertWebhookOK(t, e.post(t, session))
		assertWebhookOK(t, e.post(t, intent))
		if again := e.order(t, orderID); !again.PaidAt.Equal(*paid.PaidAt) {
			t.Errorf("trial %d: paid_at moved from %v to %v", i, paid.PaidAt, again.PaidAt)
		}
	}
	if got := len(e.fake.captureCalls()); got > trials {
		t.Errorf("captures = %d for %d attempts, want at most one each", got, trials)
	}
	if c := e.fake.cancelCalls(); len(c) != 0 {
		t.Errorf("voided %v: every payment was its order's", c)
	}
	if strings.Contains(logs.String(), "double payment") {
		t.Errorf("a race was reported as a double payment: %s", logs.String())
	}
}

// A capture whose settlement failed is retried by Stripe's redelivery. When the
// redelivery comes after the capture's Idempotency-Key is gone (Stripe keeps
// keys for 24 hours), Stripe refuses the repeat with
// payment_intent_unexpected_state; the client reads the PaymentIntent, finds it
// succeeded, and the webhook settles it instead of failing the attempt or
// voiding a payment that was taken.
func TestStripeWebhook_RedeliveryAfterTheCaptureKeyExpired(t *testing.T) {
	tests := []struct {
		name         string
		forgetKeys   bool
		wantCaptures int
	}{
		{"key still known: the first answer is replayed", false, 2},
		{"key expired: unexpected_state, then the PaymentIntent is read", true, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newStripeEnv(t)
			var logs bytes.Buffer
			e.h.log = zerolog.New(&logs)
			e.fake.dedupCaptures = true
			orderID := e.seedOrder(t, "")
			attemptID := e.seedAttempt(t, orderID, "stripe", "cs_exp", "created")
			evt := sessionCompletedEvent("evt_exp", "cs_exp", 45000, "Test Payer", "payer@example.invalid")
			key := "pi_for_cs_exp capture2-" + attemptID

			heal := injectFault(t, e.pool, "payment_attempts", "NEW.status = 'captured'")
			assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "webhook with the settlement failing")
			if s := e.attemptStatus(t, attemptID); s != "authorized" {
				t.Fatalf("attempt = %q, want authorized: Stripe has the money, Brygge has not booked it", s)
			}

			heal()
			if tc.forgetKeys {
				e.fake.forgetCaptureKeys()
			}
			assertWebhookOK(t, e.post(t, evt))

			caps := e.fake.captureCalls()
			if len(caps) != tc.wantCaptures {
				t.Fatalf("captures = %v, want %d", caps, tc.wantCaptures)
			}
			for _, c := range caps {
				if c != key {
					t.Errorf("capture %q, want only calls under the attempt's key %q", c, key)
				}
			}
			// afterFailedCapture also reads the PaymentIntent, so the read alone
			// does not tell the paths apart: only the client's own re-read
			// leaves the capture counted as a success, with no failure logged.
			if strings.Contains(logs.String(), "capture failed") {
				t.Errorf("the refused repeat was handled as a failed capture: %s", logs.String())
			}
			if tc.forgetKeys && len(e.fake.intentGetCalls()) == 0 {
				t.Error("the refused repeat did not re-read the PaymentIntent")
			}
			if s := e.attemptStatus(t, attemptID); s != "captured" {
				t.Errorf("attempt = %q, want captured", s)
			}
			o := e.order(t, orderID)
			if o.Status != "paid" || o.PaidAt == nil || o.Provider != "stripe" {
				t.Errorf("order = %+v, want paid by stripe", o)
			}
			if o.GuestName != "Test Payer" {
				t.Errorf("guest name = %q: the redelivery's session details were not used", o.GuestName)
			}
			if c := e.fake.cancelCalls(); len(c) != 0 {
				t.Errorf("voided %v: the payment was taken", c)
			}
		})
	}
}

// A capture Stripe fails on (an outage answering 5xx) is not a failed
// payment: the handler asks where the PaymentIntent stands, and when it is
// succeeded it settles it. If that settlement write fails too, the webhook
// answers 5xx with the attempt still authorized, and the redelivery, whose
// capture fails the same way, finds the PaymentIntent succeeded again and
// finishes. At no point is the payment voided.
func TestStripeWebhook_FailedCaptureThenSucceededThenSettleFailure(t *testing.T) {
	e := newStripeEnv(t)
	e.fake.captureStatus = http.StatusInternalServerError
	e.fake.intentAfterCapture = stripe.PaymentIntentSucceeded
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_afc", "created")
	evt := sessionCompletedEvent("evt_afc", "cs_afc", 45000, "", "")

	heal := injectFault(t, e.pool, "payment_attempts", "NEW.status = 'captured'")
	assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "webhook with the settlement of a succeeded intent failing")
	if len(e.fake.captureCalls()) != 1 || len(e.fake.intentGetCalls()) == 0 {
		t.Fatalf("captures %v, retrieves %v; want the failed capture followed by a retrieve", e.fake.captureCalls(), e.fake.intentGetCalls())
	}
	if s := e.attemptStatus(t, attemptID); s != "authorized" {
		t.Errorf("attempt = %q, want authorized: not released, not failed, not half-settled", s)
	}
	if o := e.order(t, orderID); o.Status != "pending" || o.PaidAt != nil {
		t.Errorf("order = %+v, want untouched", o)
	}
	if c := e.fake.cancelCalls(); len(c) != 0 {
		t.Errorf("voided %v: the PaymentIntent had succeeded", c)
	}

	heal()
	assertWebhookOK(t, e.post(t, evt))
	if s := e.attemptStatus(t, attemptID); s != "captured" {
		t.Errorf("attempt = %q after the redelivery, want captured", s)
	}
	if o := e.order(t, orderID); o.Status != "paid" || o.PaidAt == nil || o.Provider != "stripe" {
		t.Errorf("order = %+v, want paid by stripe", o)
	}
	if c := e.fake.cancelCalls(); len(c) != 0 {
		t.Errorf("voided %v: the PaymentIntent had succeeded", c)
	}
}

// A redelivery that decides capture again for an attempt still authorized
// leaves updated_at alone: it is the age of the stuck authorization, and
// resetting it on every retry would hide it from the gauge.
func TestStripeWebhook_RedeliveredDecisionKeepsTheAuthorizedSince(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_since", "created")
	evt := sessionCompletedEvent("evt_since", "cs_since", 45000, "", "")
	updatedAt := func() time.Time {
		t.Helper()
		var at time.Time
		if err := e.pool.QueryRow(context.Background(), `SELECT updated_at FROM payment_attempts WHERE id = $1`, attemptID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}

	heal := injectFault(t, e.pool, "payment_attempts", "NEW.status = 'captured'")
	assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "webhook with the settlement failing")
	if s := e.attemptStatus(t, attemptID); s != "authorized" {
		t.Fatalf("attempt = %q, want authorized", s)
	}
	since := updatedAt()

	time.Sleep(20 * time.Millisecond)
	assertWebhookStatus(t, e.post(t, evt), http.StatusInternalServerError, "redelivery with the settlement still failing")
	if got := updatedAt(); !got.Equal(since) {
		t.Errorf("updated_at moved from %v to %v on a redelivered decision", since, got)
	}

	heal()
	assertWebhookOK(t, e.post(t, evt))
	if s := e.attemptStatus(t, attemptID); s != "captured" {
		t.Errorf("attempt = %q, want captured", s)
	}
	if got := updatedAt(); !got.After(since) {
		t.Errorf("updated_at = %v after the capture, want after %v", got, since)
	}
}
