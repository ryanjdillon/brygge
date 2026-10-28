package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

const webhookTestSecret = "test-webhook-secret"

type webhookEnv struct {
	pool   *pgxpool.Pool
	fake   *fakeVipps
	h      *VippsWebhookHandler
	clubID string
}

// newWebhookEnv seeds a club and a webhook handler talking to a fake Vipps
// API; withClient=false is an instance without Vipps credentials.
func newWebhookEnv(t *testing.T, withClient bool) *webhookEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	var slug string
	if err := pool.QueryRow(context.Background(), `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatalf("club slug: %v", err)
	}
	fake := newFakeVipps(t)
	var client *vipps.Client
	if withClient {
		client = fake.client()
	}
	cfg := &config.Config{ClubSlug: slug, VippsWebhookSecret: webhookTestSecret}
	return &webhookEnv{
		pool:   pool,
		fake:   fake,
		h:      NewVippsWebhookHandler(pool, cfg, client, zerolog.Nop()),
		clubID: clubID,
	}
}

// post signs evt the way Vipps does and delivers it to the handler.
func (e *webhookEnv) post(t *testing.T, evt map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	return e.postRaw(body, webhookTestSecret)
}

func (e *webhookEnv) postRaw(body []byte, secret string) *httptest.ResponseRecorder {
	const date = "Mon, 26 Oct 2026 08:00:00 GMT"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/vipps/webhooks", bytes.NewReader(body))
	sum := sha256.Sum256(body)
	hash := base64.StdEncoding.EncodeToString(sum[:])
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("POST\n" + req.URL.RequestURI() + "\n" + date + ";" + req.Host + ";" + hash))
	req.Header.Set("x-ms-date", date)
	req.Header.Set("x-ms-content-sha256", hash)
	req.Header.Set("Authorization", "HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature="+
		base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	e.h.Handle(rec, req)
	return rec
}

// seedReferencedOrder inserts an order carrying a Vipps reference and the
// payment attempt that reference belongs to, as a mint leaves them. userID ""
// makes it a guest order.
func (e *webhookEnv) seedReferencedOrder(t *testing.T, userID, ref, status, guestName string) string {
	t.Helper()
	var uid *string
	if userID != "" {
		uid = &userID
	}
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO orders (club_id, user_id, guest_name, total_amount, vipps_reference, status, token)
		 VALUES ($1, $2, $3, 450, $4, $5::order_status, $6) RETURNING id`,
		e.clubID, uid, guestName, ref, status, genCheckoutToken(),
	).Scan(&id); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	attemptStatus := map[string]string{"paid": "captured", "refunded": "captured", "failed": "failed"}[status]
	if attemptStatus == "" {
		attemptStatus = "created"
	}
	e.seedAttempt(t, id, ref, attemptStatus)
	return id
}

// seedAttempt adds a Vipps payment attempt to an order. Attempts seeded later
// are newer.
func (e *webhookEnv) seedAttempt(t *testing.T, orderID, ref, status string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, created_at)
		 VALUES ($1, $2, 'vipps', $3, $4, 450, clock_timestamp()) RETURNING id`,
		e.clubID, orderID, ref, status,
	).Scan(&id); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	return id
}

type orderState struct {
	Status    string
	PaidAt    *time.Time
	GuestName string
}

func (e *webhookEnv) order(t *testing.T, id string) orderState {
	t.Helper()
	var s orderState
	if err := e.pool.QueryRow(context.Background(),
		`SELECT status::text, paid_at, guest_name FROM orders WHERE id = $1`, id,
	).Scan(&s.Status, &s.PaidAt, &s.GuestName); err != nil {
		t.Fatalf("load order: %v", err)
	}
	return s
}

func (e *webhookEnv) invoiceVippsPaidAt(t *testing.T, id string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := e.pool.QueryRow(context.Background(),
		`SELECT vipps_paid_at FROM invoices WHERE id = $1`, id,
	).Scan(&at); err != nil {
		t.Fatalf("load invoice: %v", err)
	}
	return at
}

func (e *webhookEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func capturedEvent(ref, eventID, ts string) map[string]any {
	evt := map[string]any{
		"name":           vipps.EventPaymentCaptured,
		"reference":      ref,
		"idempotencyKey": eventID,
		"pspReference":   "psp-" + eventID,
		"amount":         map[string]any{"currency": "NOK", "value": 45000},
	}
	if ts != "" {
		evt["timestamp"] = ts
	}
	return evt
}

func assertWebhookOK(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("webhook answered %d %q, want 200 with empty body", rec.Code, rec.Body.String())
	}
}

func TestVippsWebhook_CapturedGuestOrder(t *testing.T) {
	e := newWebhookEnv(t, true)
	e.fake.payerName = "Test Guest"
	orderID := e.seedReferencedOrder(t, "", "chk-guest-1", "pending", "")

	assertWebhookOK(t, e.post(t, capturedEvent("chk-guest-1", "evt-1", "2026-06-20T10:00:00Z")))

	got := e.order(t, orderID)
	if got.Status != "paid" || got.PaidAt == nil {
		t.Fatalf("order = %+v, want paid with paid_at", got)
	}
	// paid_at is the provider's capture time, as the faktura marker is.
	if want := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC); !got.PaidAt.Equal(want) {
		t.Errorf("paid_at = %v, want the event timestamp %v", got.PaidAt, want)
	}
	if got.GuestName != "Test Guest" {
		t.Errorf("guest_name = %q, want backfilled from the Vipps profile", got.GuestName)
	}
	if gets := e.fake.getCalls(); len(gets) != 1 || gets[0] != "chk-guest-1" {
		t.Errorf("GetPayment calls = %v", gets)
	}
	if n := e.count(t, `SELECT count(*) FROM vipps_webhook_events WHERE club_id = $1 AND event_id = 'evt-1'`, e.clubID); n != 1 {
		t.Errorf("persisted events = %d, want 1", n)
	}
}

func TestVippsWebhook_CapturedKeepsTypedGuestName(t *testing.T) {
	e := newWebhookEnv(t, true)
	e.fake.payerName = "Test Guest"
	orderID := e.seedReferencedOrder(t, "", "chk-guest-2", "pending", "Typed Name")

	assertWebhookOK(t, e.post(t, capturedEvent("chk-guest-2", "evt-2", "")))
	if got := e.order(t, orderID); got.Status != "paid" || got.GuestName != "Typed Name" {
		t.Errorf("order = %+v, want paid keeping the typed name", got)
	}
}

func TestVippsWebhook_CapturedFakturaOrder(t *testing.T) {
	e := newWebhookEnv(t, true)
	e.fake.payerName = "Test Guest"
	ctx := context.Background()
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	invOpen := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 930)
	invMarked := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 931)
	earlier := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if _, err := e.pool.Exec(ctx, `UPDATE invoices SET vipps_paid_at = $2 WHERE id = $1`, invMarked, earlier); err != nil {
		t.Fatal(err)
	}
	orderID := e.seedReferencedOrder(t, userID, "chk-faktura-1", "pending", "")
	addOrderSource(t, e.pool, orderID, invOpen)
	addOrderSource(t, e.pool, orderID, invMarked)

	assertWebhookOK(t, e.post(t, capturedEvent("chk-faktura-1", "evt-3", "2026-06-20T10:00:00Z")))

	got := e.order(t, orderID)
	if got.Status != "paid" || got.GuestName != "" {
		t.Errorf("member order = %+v, want paid with no guest name", got)
	}
	// Only guests share their name, so a member order is not looked up.
	if gets := e.fake.getCalls(); len(gets) != 0 {
		t.Errorf("GetPayment calls = %v, want none for a member order", gets)
	}
	want := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	if at := e.invoiceVippsPaidAt(t, invOpen); at == nil || !at.Equal(want) {
		t.Errorf("vipps_paid_at = %v, want the event timestamp %v", at, want)
	}
	if at := e.invoiceVippsPaidAt(t, invMarked); at == nil || !at.Equal(earlier) {
		t.Errorf("already-marked vipps_paid_at = %v, want unchanged %v", at, earlier)
	}

	// A soft marker only: no payment link, no status change, no GL, no audit.
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE club_id = $1 AND (payment_id IS NOT NULL OR status <> 'open')`, e.clubID); n != 0 {
		t.Errorf("%d invoices got a payment_id or status change", n)
	}
	if n := e.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, e.clubID); n != 0 {
		t.Errorf("%d journal entries booked", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE club_id = $1`, e.clubID); n != 0 {
		t.Errorf("%d audit rows written", n)
	}
}

func TestVippsWebhook_CapturedWithoutTimestampUsesNow(t *testing.T) {
	e := newWebhookEnv(t, true)
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	inv := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 932)
	orderID := e.seedReferencedOrder(t, userID, "chk-faktura-2", "pending", "")
	addOrderSource(t, e.pool, orderID, inv)

	assertWebhookOK(t, e.post(t, capturedEvent("chk-faktura-2", "evt-4", "")))
	if at := e.invoiceVippsPaidAt(t, inv); at == nil || time.Since(*at) > time.Minute {
		t.Errorf("vipps_paid_at = %v, want about now", at)
	}
}

func TestVippsWebhook_CapturedFailedOrderIsPaid(t *testing.T) {
	e := newWebhookEnv(t, true)
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	inv := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 933)
	orderID := e.seedReferencedOrder(t, userID, "chk-failed-1", "failed", "")
	addOrderSource(t, e.pool, orderID, inv)

	// A capture means the money was taken, so it pays an order an earlier
	// terminal event marked failed.
	assertWebhookOK(t, e.post(t, capturedEvent("chk-failed-1", "evt-5", "2026-06-20T10:00:00Z")))
	want := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	if got := e.order(t, orderID); got.Status != "paid" || got.PaidAt == nil || !got.PaidAt.Equal(want) {
		t.Errorf("failed order = %+v, want paid at %v", got, want)
	}
	if at := e.invoiceVippsPaidAt(t, inv); at == nil || !at.Equal(want) {
		t.Errorf("vipps_paid_at = %v, want %v", at, want)
	}
}

func TestVippsWebhook_CapturedReplayIsHarmless(t *testing.T) {
	e := newWebhookEnv(t, true)
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	inv := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 934)
	orderID := e.seedReferencedOrder(t, userID, "chk-replay-1", "pending", "")
	addOrderSource(t, e.pool, orderID, inv)

	evt := capturedEvent("chk-replay-1", "evt-6", "2026-06-20T10:00:00Z")
	assertWebhookOK(t, e.post(t, evt))
	first := e.order(t, orderID)
	firstMarker := e.invoiceVippsPaidAt(t, inv)

	// Same event redelivered, then a distinct later CAPTURED for the reference.
	assertWebhookOK(t, e.post(t, evt))
	assertWebhookOK(t, e.post(t, capturedEvent("chk-replay-1", "evt-7", "2026-06-21T10:00:00Z")))

	again := e.order(t, orderID)
	if again.Status != "paid" || again.PaidAt == nil || !again.PaidAt.Equal(*first.PaidAt) {
		t.Errorf("replayed order = %+v, want paid at the first %v", again, first.PaidAt)
	}
	if at := e.invoiceVippsPaidAt(t, inv); at == nil || !at.Equal(*firstMarker) {
		t.Errorf("replayed vipps_paid_at = %v, want first %v", at, firstMarker)
	}
	if n := e.count(t, `SELECT count(*) FROM vipps_webhook_events WHERE club_id = $1`, e.clubID); n != 2 {
		t.Errorf("persisted events = %d, want 2 (one deduplicated)", n)
	}
}

func TestVippsWebhook_TerminalEvents(t *testing.T) {
	e := newWebhookEnv(t, true)
	names := []string{
		vipps.EventPaymentCancelled, vipps.EventPaymentAborted,
		vipps.EventPaymentExpired, vipps.EventPaymentTerminated,
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			pending := e.seedReferencedOrder(t, "", "chk-term-p-"+name, "pending", "")
			paid := e.seedReferencedOrder(t, "", "chk-term-d-"+name, "paid", "")
			for _, ref := range []string{"chk-term-p-" + name, "chk-term-d-" + name} {
				assertWebhookOK(t, e.post(t, map[string]any{
					"name": name, "reference": ref, "idempotencyKey": "evt-" + ref,
				}))
			}
			if got := e.order(t, pending); got.Status != "failed" {
				t.Errorf("pending order after %s = %q, want failed", name, got.Status)
			}
			if got := e.order(t, paid); got.Status != "paid" {
				t.Errorf("paid order after %s = %q, want still paid", name, got.Status)
			}
		})
	}
	if n := len(e.fake.getCalls()) + len(e.fake.captureCalls()); n != 0 {
		t.Errorf("terminal events called Vipps %d times", n)
	}
}

func TestVippsWebhook_AuthorizedCapturesKnownOrders(t *testing.T) {
	e := newWebhookEnv(t, true)
	orderID := e.seedReferencedOrder(t, "", "chk-auth-1", "pending", "")

	authorized := func(ref, id string, withAmount bool) map[string]any {
		evt := map[string]any{"name": vipps.EventPaymentAuthorized, "reference": ref, "idempotencyKey": id}
		if withAmount {
			evt["amount"] = map[string]any{"currency": "NOK", "value": 45000}
		}
		return evt
	}
	assertWebhookOK(t, e.post(t, authorized("chk-auth-1", "evt-a1", true)))
	assertWebhookOK(t, e.post(t, authorized("chk-unknown", "evt-a2", true)))
	assertWebhookOK(t, e.post(t, authorized("chk-auth-1", "evt-a3", false)))

	caps := e.fake.captureCalls()
	if len(caps) != 1 {
		t.Fatalf("capture calls = %+v, want exactly one for the known order", caps)
	}
	c := caps[0]
	if c.Reference != "chk-auth-1" || c.IdempotencyKey != "capture-chk-auth-1" ||
		c.Body.ModificationAmount != (vipps.Amount{Currency: "NOK", Value: 45000}) {
		t.Errorf("capture = %+v", c)
	}
	if got := e.order(t, orderID); got.Status != "pending" {
		t.Errorf("authorized order = %q, want still pending until CAPTURED", got.Status)
	}
}

func TestVippsWebhook_NoClientStillMarksPaid(t *testing.T) {
	e := newWebhookEnv(t, false)
	e.fake.payerName = "Test Guest"
	orderID := e.seedReferencedOrder(t, "", "chk-noclient-1", "pending", "")

	assertWebhookOK(t, e.post(t, capturedEvent("chk-noclient-1", "evt-n1", "")))
	if got := e.order(t, orderID); got.Status != "paid" || got.GuestName != "" {
		t.Errorf("order = %+v, want paid with no name lookup", got)
	}
	if n := len(e.fake.getCalls()); n != 0 {
		t.Errorf("GetPayment called %d times without a client", n)
	}
}

func TestVippsWebhook_UnknownReference(t *testing.T) {
	e := newWebhookEnv(t, true)
	e.fake.payerName = "Test Guest"
	orderID := e.seedReferencedOrder(t, "", "chk-known-1", "pending", "")

	assertWebhookOK(t, e.post(t, capturedEvent("chk-unknown-1", "evt-u1", "2026-06-20T10:00:00Z")))
	assertWebhookOK(t, e.post(t, map[string]any{
		"name": vipps.EventPaymentTerminated, "reference": "chk-unknown-2", "idempotencyKey": "evt-u2",
	}))

	if n := len(e.fake.getCalls()); n != 0 {
		t.Errorf("GetPayment called %d times for unknown references", n)
	}
	if got := e.order(t, orderID); got.Status != "pending" || got.PaidAt != nil || got.GuestName != "" {
		t.Errorf("unrelated order changed: %+v", got)
	}
	// Unknown references resolve to the instance club and are still stored.
	if n := e.count(t, `SELECT count(*) FROM vipps_webhook_events
		WHERE club_id = $1 AND event_id IN ('evt-u1', 'evt-u2')`, e.clubID); n != 2 {
		t.Errorf("persisted events = %d, want 2", n)
	}
}

func TestVippsWebhook_BadSignature(t *testing.T) {
	e := newWebhookEnv(t, true)
	orderID := e.seedReferencedOrder(t, "", "chk-sig-1", "pending", "")
	body, _ := json.Marshal(capturedEvent("chk-sig-1", "evt-s1", ""))

	assertBody(t, e.postRaw(body, "wrong-secret"), http.StatusUnauthorized, "unauthorized\n")
	if got := e.order(t, orderID); got.Status != "pending" {
		t.Errorf("unsigned event changed order to %q", got.Status)
	}
}
