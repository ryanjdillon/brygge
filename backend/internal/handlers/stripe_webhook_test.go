package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

type stripeEnv struct {
	pool   *pgxpool.Pool
	fake   *fakeStripe
	h      *StripeWebhookHandler
	clubID string
}

func newStripeEnv(t *testing.T) *stripeEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	var slug string
	if err := pool.QueryRow(context.Background(), `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatalf("club slug: %v", err)
	}
	fake := newFakeStripe(t)
	cfg := &config.Config{ClubSlug: slug, StripeWebhookSecret: stripeTestWebhookSecret}
	return &stripeEnv{
		pool:   pool,
		fake:   fake,
		h:      NewStripeWebhookHandler(pool, cfg, fake.client(), zerolog.Nop()),
		clubID: clubID,
	}
}

// post signs evt with secret the way Stripe does and delivers it.
func (e *stripeEnv) post(t *testing.T, evt map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return e.postSigned(t, evt, stripeTestWebhookSecret)
}

func (e *stripeEnv) postSigned(t *testing.T, evt map[string]any, secret string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(body)))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/stripe/webhooks", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", "t="+ts+",v1="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	e.h.Handle(rec, req)
	return rec
}

// seedOrder inserts a pending order of 450 kr; userID "" makes it a guest
// order.
func (e *stripeEnv) seedOrder(t *testing.T, userID string) string {
	t.Helper()
	var uid *string
	if userID != "" {
		uid = &userID
	}
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO orders (club_id, user_id, total_amount, token) VALUES ($1, $2, 450, $3) RETURNING id`,
		e.clubID, uid, genCheckoutToken(),
	).Scan(&id); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	return id
}

// seedAttempt adds a payment attempt for 450 kr as a mint leaves it.
func (e *stripeEnv) seedAttempt(t *testing.T, orderID, provider, ref, status string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, created_at)
		 VALUES ($1, $2, $3, $4, $5, 450, clock_timestamp()) RETURNING id`,
		e.clubID, orderID, provider, ref, status,
	).Scan(&id); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	return id
}

func (e *stripeEnv) attemptStatus(t *testing.T, id string) string {
	t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM payment_attempts WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

type stripeOrderState struct {
	Status, Provider, GuestName, GuestEmail string
	PaidAt                                  *time.Time
}

func (e *stripeEnv) order(t *testing.T, id string) stripeOrderState {
	t.Helper()
	var s stripeOrderState
	if err := e.pool.QueryRow(context.Background(),
		`SELECT status::text, COALESCE(provider, ''), guest_name, guest_email, paid_at FROM orders WHERE id = $1`, id,
	).Scan(&s.Status, &s.Provider, &s.GuestName, &s.GuestEmail, &s.PaidAt); err != nil {
		t.Fatalf("load order: %v", err)
	}
	return s
}

func sessionCompletedEvent(eventID, sessionID string, amount int64, name, email string) map[string]any {
	return map[string]any{
		"id": eventID, "type": stripe.EventCheckoutSessionCompleted, "created": time.Now().Unix(),
		"data": map[string]any{"object": map[string]any{
			"id": sessionID, "object": "checkout.session", "status": "complete", "payment_status": "unpaid",
			"amount_total": amount, "currency": "nok", "payment_intent": fakeStripePI(sessionID),
			"customer_details": map[string]any{"name": name, "email": email},
		}},
	}
}

func sessionEvent(eventType, eventID, sessionID string) map[string]any {
	return map[string]any{
		"id": eventID, "type": eventType, "created": time.Now().Unix(),
		"data": map[string]any{"object": map[string]any{"id": sessionID, "object": "checkout.session", "status": "expired"}},
	}
}

func intentEvent(eventType, eventID, piID, attemptID string, created int64) map[string]any {
	return intentEventReceived(eventType, eventID, piID, attemptID, created, 45000)
}

func intentEventReceived(eventType, eventID, piID, attemptID string, created, received int64) map[string]any {
	return map[string]any{
		"id": eventID, "type": eventType, "created": created,
		"data": map[string]any{"object": map[string]any{
			"id": piID, "object": "payment_intent", "metadata": map[string]any{"attempt_id": attemptID},
			"amount": 45000, "amount_received": received, "currency": "nok",
		}},
	}
}

func TestStripeWebhook_CompletedCapturesAndPays(t *testing.T) {
	e := newStripeEnv(t)
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	invID := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 701)
	orderID := e.seedOrder(t, userID)
	addOrderSource(t, e.pool, orderID, invID)
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_a", "created")

	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_1", "cs_a", 45000, "Test Member", "member@example.invalid")))

	if got := e.fake.captureCalls(); len(got) != 1 || got[0] != "pi_for_cs_a capture-"+attemptID {
		t.Fatalf("captures = %v", got)
	}
	if s := e.attemptStatus(t, attemptID); s != "captured" {
		t.Errorf("attempt = %q, want captured", s)
	}
	o := e.order(t, orderID)
	if o.Status != "paid" || o.Provider != "stripe" || o.PaidAt == nil {
		t.Errorf("order = %+v, want paid by stripe", o)
	}
	if o.GuestName != "" || o.GuestEmail != "" {
		t.Errorf("member order got guest details %+v", o)
	}
	var provider *string
	var vippsPaid *time.Time
	if err := e.pool.QueryRow(context.Background(),
		`SELECT online_paid_provider, vipps_paid_at FROM invoices WHERE id = $1`, invID,
	).Scan(&provider, &vippsPaid); err != nil {
		t.Fatal(err)
	}
	if provider == nil || *provider != "stripe" || vippsPaid != nil {
		t.Errorf("invoice marker provider/vipps_paid_at = %v/%v", provider, vippsPaid)
	}
	var stored int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payment_webhook_events WHERE club_id = $1 AND provider = 'stripe' AND event_id = 'evt_1' AND reference = 'cs_a'`,
		e.clubID,
	).Scan(&stored); err != nil || stored != 1 {
		t.Errorf("stored events = %d (%v), want 1", stored, err)
	}
}

func TestStripeWebhook_GuestNameAndEmailBackfill(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	e.seedAttempt(t, orderID, "stripe", "cs_g", "created")

	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_g", "cs_g", 45000, "Test Guest", "guest@example.invalid")))

	o := e.order(t, orderID)
	if o.Status != "paid" || o.GuestName != "Test Guest" || o.GuestEmail != "guest@example.invalid" {
		t.Errorf("order = %+v, want paid with guest name and email", o)
	}

	// A name and email the payer already gave are kept.
	order2 := e.seedOrder(t, "")
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE orders SET guest_name = 'Typed Name', guest_email = 'typed@example.invalid' WHERE id = $1`, order2,
	); err != nil {
		t.Fatal(err)
	}
	e.seedAttempt(t, order2, "stripe", "cs_g2", "created")
	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_g2", "cs_g2", 45000, "Other Name", "other@example.invalid")))
	if o := e.order(t, order2); o.GuestName != "Typed Name" || o.GuestEmail != "typed@example.invalid" {
		t.Errorf("typed guest details overwritten: %+v", o)
	}
}

func TestStripeWebhook_CompletedAfterVippsPaidIsVoided(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE orders SET status = 'paid', provider = 'vipps', paid_at = '2026-06-01T10:00:00Z' WHERE id = $1`, orderID,
	); err != nil {
		t.Fatal(err)
	}
	e.seedAttempt(t, orderID, "vipps", "chk-paid", "captured")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_late", "created")

	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_late", "cs_late", 45000, "", "")))

	if got := e.fake.captureCalls(); len(got) != 0 {
		t.Errorf("captured %v for an order already paid", got)
	}
	if got := e.fake.cancelCalls(); len(got) != 1 || got[0] != "pi_for_cs_late" {
		t.Errorf("cancels = %v, want the late PaymentIntent", got)
	}
	if s := e.attemptStatus(t, attemptID); s != "cancelled" {
		t.Errorf("attempt = %q, want cancelled", s)
	}
	o := e.order(t, orderID)
	if want := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC); o.Status != "paid" || o.Provider != "vipps" || !o.PaidAt.Equal(want) {
		t.Errorf("order changed: %+v", o)
	}

	// Stripe then reports the PaymentIntent cancelled; the order stays paid.
	assertWebhookOK(t, e.post(t, intentEvent(stripe.EventPaymentIntentCanceled, "evt_late_c", "pi_for_cs_late", attemptID, time.Now().Unix())))
	if s := e.attemptStatus(t, attemptID); s != "cancelled" {
		t.Errorf("attempt after canceled event = %q", s)
	}
}

func TestStripeWebhook_TwoSessionsCaptureOnlyFirst(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	first := e.seedAttempt(t, orderID, "stripe", "cs_1", "created")
	second := e.seedAttempt(t, orderID, "stripe", "cs_2", "created")

	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_1", "cs_1", 45000, "", "")))
	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_2", "cs_2", 45000, "", "")))

	if got := e.fake.captureCalls(); len(got) != 1 || got[0] != "pi_for_cs_1 capture-"+first {
		t.Errorf("captures = %v, want only the first", got)
	}
	if got := e.fake.cancelCalls(); len(got) != 1 || got[0] != "pi_for_cs_2" {
		t.Errorf("cancels = %v, want the second", got)
	}
	if a, b := e.attemptStatus(t, first), e.attemptStatus(t, second); a != "captured" || b != "cancelled" {
		t.Errorf("attempts = %q/%q, want captured/cancelled", a, b)
	}
	// The second session's cancellation must not fail the order the first paid.
	assertWebhookOK(t, e.post(t, intentEvent(stripe.EventPaymentIntentCanceled, "evt_2c", "pi_for_cs_2", second, time.Now().Unix())))
	if o := e.order(t, orderID); o.Status != "paid" {
		t.Errorf("order = %q, want paid", o.Status)
	}
}

func TestStripeWebhook_CaptureFailure(t *testing.T) {
	tests := []struct {
		name        string
		intentAfter string
		wantCancel  bool
		wantAttempt string
		wantOrder   string
	}{
		// Still authorized: the hold is released at once and the payer retries.
		{"still authorized is voided", stripe.PaymentIntentRequiresCapture, true, "failed", "failed"},
		// The capture went through although its call failed.
		{"captured after all is settled", stripe.PaymentIntentSucceeded, false, "captured", "paid"},
		// Unknown state: released; the hold lapses or a later event settles it.
		{"other state is released", "processing", false, "created", "pending"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newStripeEnv(t)
			e.fake.captureStatus = http.StatusInternalServerError
			e.fake.intentAfterCapture = tc.intentAfter
			orderID := e.seedOrder(t, "")
			attemptID := e.seedAttempt(t, orderID, "stripe", "cs_f", "created")

			assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_f", "cs_f", 45000, "", "")))

			if got := len(e.fake.cancelCalls()) == 1; got != tc.wantCancel {
				t.Errorf("cancels = %v, want voided %v", e.fake.cancelCalls(), tc.wantCancel)
			}
			if s := e.attemptStatus(t, attemptID); s != tc.wantAttempt {
				t.Errorf("attempt = %q, want %q", s, tc.wantAttempt)
			}
			if o := e.order(t, orderID); o.Status != tc.wantOrder {
				t.Errorf("order = %q, want %q", o.Status, tc.wantOrder)
			}
		})
	}
}

func TestStripeWebhook_IntentSucceededAfterRelease(t *testing.T) {
	e := newStripeEnv(t)
	e.fake.captureStatus = http.StatusInternalServerError
	e.fake.intentAfterCapture = "processing"
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_f", "created")
	assertWebhookOK(t, e.post(t, sessionCompletedEvent("evt_f", "cs_f", 45000, "", "")))

	// The capture went through after all: payment_intent.succeeded settles it.
	assertWebhookOK(t, e.post(t, intentEvent(stripe.EventPaymentIntentSucceeded, "evt_f_s", "pi_for_cs_f", attemptID, 1782900000)))
	o := e.order(t, orderID)
	if o.Status != "paid" || o.PaidAt == nil || !o.PaidAt.Equal(time.Unix(1782900000, 0)) {
		t.Errorf("order = %+v, want paid at the event time", o)
	}
	if s := e.attemptStatus(t, attemptID); s != "captured" {
		t.Errorf("attempt = %q, want captured", s)
	}
}

func TestStripeWebhook_PartialCaptureDoesNotPay(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_p", "created")

	assertWebhookOK(t, e.post(t, intentEventReceived(stripe.EventPaymentIntentSucceeded, "evt_p", "pi_for_cs_p", attemptID, time.Now().Unix(), 20000)))

	if o := e.order(t, orderID); o.Status != "pending" {
		t.Errorf("order = %q, want pending after a partial capture", o.Status)
	}
	if s := e.attemptStatus(t, attemptID); s != "created" {
		t.Errorf("attempt = %q, want untouched", s)
	}
}

func TestStripeWebhook_ExpiredFailsAttemptOrderStaysPayable(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_x", "created")

	assertWebhookOK(t, e.post(t, sessionEvent(stripe.EventCheckoutSessionExpired, "evt_x", "cs_x")))

	if s := e.attemptStatus(t, attemptID); s != "failed" {
		t.Errorf("attempt = %q, want failed", s)
	}
	// 'failed' is retryable: the pay routes mint again for it.
	if o := e.order(t, orderID); o.Status != "failed" {
		t.Errorf("order = %q, want failed (retryable)", o.Status)
	}
	addOrderLine(t, e.pool, orderID, "Test item", 450)
	var token string
	if err := e.pool.QueryRow(context.Background(), `SELECT token FROM orders WHERE id = $1`, orderID).Scan(&token); err != nil {
		t.Fatal(err)
	}
	h := newStripeMintHandler(e.pool, e.fake)
	rec := serve(mintTestRouter(h), http.MethodPost, "/checkout/"+token+"/pay", "")
	if rec.Code != http.StatusOK {
		t.Errorf("retry after expiry = %d %q", rec.Code, rec.Body.String())
	}

	// async_payment_failed is handled the same way.
	order2 := e.seedOrder(t, "")
	a2 := e.seedAttempt(t, order2, "stripe", "cs_y", "created")
	assertWebhookOK(t, e.post(t, sessionEvent(stripe.EventCheckoutSessionAsyncPaymentFailed, "evt_y", "cs_y")))
	if s := e.attemptStatus(t, a2); s != "failed" {
		t.Errorf("async failed attempt = %q", s)
	}
}

func TestStripeWebhook_AmountMismatchVoidsAndFails(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
	}{
		{"amount", 100, "nok"},
		{"currency", 45000, "eur"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newStripeEnv(t)
			orderID := e.seedOrder(t, "")
			attemptID := e.seedAttempt(t, orderID, "stripe", "cs_m", "created")
			evt := sessionCompletedEvent("evt_m", "cs_m", tc.amount, "", "")
			evt["data"].(map[string]any)["object"].(map[string]any)["currency"] = tc.currency

			assertWebhookOK(t, e.post(t, evt))

			if got := e.fake.captureCalls(); len(got) != 0 {
				t.Errorf("captured a mismatched payment: %v", got)
			}
			if got := e.fake.cancelCalls(); len(got) != 1 {
				t.Errorf("cancels = %v, want the PaymentIntent voided", got)
			}
			if s := e.attemptStatus(t, attemptID); s != "failed" {
				t.Errorf("attempt = %q, want failed", s)
			}
			if o := e.order(t, orderID); o.Status == "paid" {
				t.Error("mismatched payment paid the order")
			}
		})
	}
}

func TestStripeWebhook_RedeliveryIsNoOp(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	e.seedAttempt(t, orderID, "stripe", "cs_r", "created")
	evt := sessionCompletedEvent("evt_r", "cs_r", 45000, "", "")

	assertWebhookOK(t, e.post(t, evt))
	first := e.order(t, orderID)
	assertWebhookOK(t, e.post(t, evt))

	if got := e.fake.captureCalls(); len(got) != 1 {
		t.Errorf("captures = %v, want one", got)
	}
	if got := e.order(t, orderID); got.Status != "paid" || !got.PaidAt.Equal(*first.PaidAt) {
		t.Errorf("redelivery changed the order: %+v vs %+v", got, first)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payment_webhook_events WHERE event_id = 'evt_r'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("stored events = %d (%v), want 1", n, err)
	}
}

func TestStripeWebhook_Rejected(t *testing.T) {
	e := newStripeEnv(t)
	orderID := e.seedOrder(t, "")
	e.seedAttempt(t, orderID, "stripe", "cs_s", "created")
	evt := sessionCompletedEvent("evt_s", "cs_s", 45000, "", "")

	if rec := e.postSigned(t, evt, "whsec_wrong_fake"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret = %d, want 401", rec.Code)
	}

	// An instance without a webhook secret accepts nothing, even an event
	// signed with the empty key.
	e.h.cfg = &config.Config{ClubSlug: e.h.cfg.ClubSlug}
	if rec := e.postSigned(t, evt, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("empty secret = %d, want 401", rec.Code)
	}
	if o := e.order(t, orderID); o.Status != "pending" || len(e.fake.captureCalls()) != 0 {
		t.Errorf("rejected event acted on: %+v", o)
	}
}

func TestStripeWebhook_UnhandledEventsAreStored(t *testing.T) {
	e := newStripeEnv(t)
	evt := map[string]any{
		"id": "evt_refund", "type": "charge.refunded", "created": time.Now().Unix(),
		"data": map[string]any{"object": map[string]any{"id": "ch_1", "object": "charge", "payment_intent": "pi_unknown"}},
	}
	assertWebhookOK(t, e.post(t, evt))
	var ref string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT reference FROM payment_webhook_events WHERE club_id = $1 AND event_id = 'evt_refund'`, e.clubID,
	).Scan(&ref); err != nil || ref != "pi_unknown" {
		t.Errorf("stored reference = %q (%v), want the PaymentIntent", ref, err)
	}
}
