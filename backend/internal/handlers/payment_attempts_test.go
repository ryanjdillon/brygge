package handlers

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

type attemptRow struct {
	ID        string
	Reference *string
	Status    string
	Provider  string
	Amount    float64
}

// attempts lists an order's payment attempts, oldest first.
func (e *webhookEnv) attempts(t *testing.T, orderID string) []attemptRow {
	t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT id, provider_reference, status, provider, amount::float8
		   FROM payment_attempts WHERE order_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		t.Fatalf("load attempts: %v", err)
	}
	defer rows.Close()
	var out []attemptRow
	for rows.Next() {
		var a attemptRow
		if err := rows.Scan(&a.ID, &a.Reference, &a.Status, &a.Provider, &a.Amount); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func (e *webhookEnv) attemptStatus(t *testing.T, ref string) string {
	t.Helper()
	var status string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT status FROM payment_attempts WHERE provider = 'vipps' AND provider_reference = $1`, ref,
	).Scan(&status); err != nil {
		t.Fatalf("load attempt %s: %v", ref, err)
	}
	return status
}

// withLogBuffer swaps the webhook handler for one logging into a buffer.
func (e *webhookEnv) withLogBuffer() *bytes.Buffer {
	var buf bytes.Buffer
	e.h = NewVippsWebhookHandler(e.pool, e.h.cfg, e.fake.client(), zerolog.New(&buf))
	return &buf
}

func authorizedEvent(ref, eventID string) map[string]any {
	return map[string]any{
		"name": vipps.EventPaymentAuthorized, "reference": ref, "idempotencyKey": eventID,
		"amount": map[string]any{"currency": "NOK", "value": 45000},
	}
}

func terminatedEvent(ref, eventID string) map[string]any {
	return map[string]any{"name": vipps.EventPaymentTerminated, "reference": ref, "idempotencyKey": eventID}
}

func TestMintOrderPayment_RecordsAttempts(t *testing.T) {
	e := newWebhookEnv(t, true)
	router := mintTestRouter(newMintTestHandler(e.pool, e.fake.client()))
	orderID, token := seedCheckoutOrder(t, e.pool, e.clubID, "")
	addOrderLine(t, e.pool, orderID, "Gjesteplass", 200)

	for range 2 {
		if rec := serve(router, http.MethodPost, "/checkout/"+token+"/pay", ""); rec.Code != http.StatusOK {
			t.Fatalf("mint: %d %q", rec.Code, rec.Body.String())
		}
	}
	calls := e.fake.createCalls()
	if len(calls) != 2 {
		t.Fatalf("create calls = %d, want 2", len(calls))
	}
	first, second := calls[0].Body.Reference, calls[1].Body.Reference

	got := e.attempts(t, orderID)
	if len(got) != 2 {
		t.Fatalf("attempts = %+v, want one per mint", got)
	}
	for i, ref := range []string{first, second} {
		a := got[i]
		if a.Reference == nil || *a.Reference != ref || a.Status != "created" || a.Provider != "vipps" || a.Amount != 200 {
			t.Errorf("attempt %d = %+v, want created vipps 200 with reference %s", i, a, ref)
		}
	}
	if ref, _ := orderReference(t, e.pool, orderID); ref != second {
		t.Errorf("orders.vipps_reference = %q, want the latest %q", ref, second)
	}

	// The payer completes the first attempt after starting the second: its
	// capture still pays the order, and the abandoned newer one doesn't fail it.
	assertWebhookOK(t, e.post(t, authorizedEvent(first, "evt-ra1")))
	if caps := e.fake.captureCalls(); len(caps) != 1 || caps[0].Reference != first {
		t.Errorf("capture calls = %+v, want the earlier attempt captured", caps)
	}
	assertWebhookOK(t, e.post(t, capturedEvent(first, "evt-ra2", "2026-06-20T10:00:00Z")))
	assertWebhookOK(t, e.post(t, terminatedEvent(second, "evt-ra3")))

	if o := e.order(t, orderID); o.Status != "paid" {
		t.Errorf("order = %+v, want paid by the earlier attempt", o)
	}
	if s := e.attemptStatus(t, first); s != "captured" {
		t.Errorf("earlier attempt = %q, want captured", s)
	}
	if s := e.attemptStatus(t, second); s != "failed" {
		t.Errorf("newer attempt = %q, want failed", s)
	}
}

func TestMintOrderPayment_ProviderErrorFailsAttempt(t *testing.T) {
	e := newWebhookEnv(t, true)
	e.fake.createStatus = http.StatusInternalServerError
	router := mintTestRouter(newMintTestHandler(e.pool, e.fake.client()))
	orderID, token := seedCheckoutOrder(t, e.pool, e.clubID, "")
	addOrderLine(t, e.pool, orderID, "Test item", 100)

	if rec := serve(router, http.MethodPost, "/checkout/"+token+"/pay", ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("mint: %d, want 502", rec.Code)
	}
	got := e.attempts(t, orderID)
	if len(got) != 1 || got[0].Status != "failed" || got[0].Reference != nil {
		t.Errorf("attempts = %+v, want one failed attempt without a reference", got)
	}
}

func TestVippsWebhook_StaleAuthorizationCancelled(t *testing.T) {
	e := newWebhookEnv(t, true)
	orderID := e.seedReferencedOrder(t, "", "chk-stale-a", "paid", "")
	e.seedAttempt(t, orderID, "chk-stale-b", "created")

	assertWebhookOK(t, e.post(t, authorizedEvent("chk-stale-b", "evt-sa1")))

	if caps := e.fake.captureCalls(); len(caps) != 0 {
		t.Errorf("capture calls = %+v, want none for an order already paid", caps)
	}
	if cancels := e.fake.cancelCalls(); len(cancels) != 1 || cancels[0] != "chk-stale-b" {
		t.Errorf("cancel calls = %v, want the stale authorization voided", cancels)
	}
	if s := e.attemptStatus(t, "chk-stale-b"); s != "cancelled" {
		t.Errorf("stale attempt = %q, want cancelled", s)
	}

	// Vipps confirms the void; the attempt keeps the reason and the order is
	// untouched.
	assertWebhookOK(t, e.post(t, map[string]any{
		"name": vipps.EventPaymentCancelled, "reference": "chk-stale-b", "idempotencyKey": "evt-sa2",
	}))
	if s := e.attemptStatus(t, "chk-stale-b"); s != "cancelled" {
		t.Errorf("stale attempt after CANCELLED = %q, want cancelled", s)
	}
	if o := e.order(t, orderID); o.Status != "paid" {
		t.Errorf("order = %+v, want still paid", o)
	}
}

func TestVippsWebhook_DoubleCaptureLeavesOrder(t *testing.T) {
	e := newWebhookEnv(t, true)
	logs := e.withLogBuffer()
	orderID := e.seedReferencedOrder(t, "", "chk-double-a", "pending", "")
	e.seedAttempt(t, orderID, "chk-double-b", "created")

	assertWebhookOK(t, e.post(t, capturedEvent("chk-double-a", "evt-d1", "2026-06-20T10:00:00Z")))
	paid := e.order(t, orderID)
	if strings.Contains(logs.String(), "double payment") {
		t.Fatalf("first capture logged a double payment: %s", logs.String())
	}

	assertWebhookOK(t, e.post(t, capturedEvent("chk-double-b", "evt-d2", "2026-06-21T10:00:00Z")))
	assertWebhookOK(t, e.post(t, capturedEvent("chk-double-b", "evt-d3", "2026-06-21T10:00:00Z")))

	again := e.order(t, orderID)
	if again.Status != "paid" || again.PaidAt == nil || !again.PaidAt.Equal(*paid.PaidAt) {
		t.Errorf("order = %+v, want unchanged %+v", again, paid)
	}
	if s := e.attemptStatus(t, "chk-double-b"); s != "captured" {
		t.Errorf("second attempt = %q, want captured", s)
	}
	if n := strings.Count(logs.String(), "double payment"); n != 1 {
		t.Errorf("double payment logged %d times, want once: %s", n, logs.String())
	}
	if !strings.Contains(logs.String(), `"level":"error"`) {
		t.Errorf("double payment not logged at error level: %s", logs.String())
	}
}

func TestVippsWebhook_TerminatedOnlyFailsCurrentAttempt(t *testing.T) {
	e := newWebhookEnv(t, true)
	orderID := e.seedReferencedOrder(t, "", "chk-cur-a", "pending", "")
	e.seedAttempt(t, orderID, "chk-cur-b", "created")

	assertWebhookOK(t, e.post(t, terminatedEvent("chk-cur-a", "evt-c1")))
	if o := e.order(t, orderID); o.Status != "pending" {
		t.Errorf("order after the older attempt ended = %q, want pending", o.Status)
	}
	if s := e.attemptStatus(t, "chk-cur-a"); s != "failed" {
		t.Errorf("older attempt = %q, want failed", s)
	}

	// An authorization of the older attempt on a still-pending order is
	// captured: the payer finished it after all.
	e.seedAttempt(t, orderID, "chk-cur-c", "created")
	assertWebhookOK(t, e.post(t, authorizedEvent("chk-cur-b", "evt-c2")))
	if caps := e.fake.captureCalls(); len(caps) != 1 || caps[0].Reference != "chk-cur-b" {
		t.Errorf("capture calls = %+v, want the non-current attempt captured", caps)
	}
	if s := e.attemptStatus(t, "chk-cur-b"); s != "authorized" {
		t.Errorf("authorized attempt = %q", s)
	}

	assertWebhookOK(t, e.post(t, terminatedEvent("chk-cur-c", "evt-c3")))
	if o := e.order(t, orderID); o.Status != "failed" {
		t.Errorf("order after its current attempt ended = %q, want failed", o.Status)
	}
}

func TestVippsWebhook_CapturedRecordsProviderNeutralMarker(t *testing.T) {
	e := newWebhookEnv(t, true)
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	inv := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 940)
	orderID := e.seedReferencedOrder(t, userID, "chk-marker-1", "pending", "")
	addOrderSource(t, e.pool, orderID, inv)
	if _, err := e.pool.Exec(context.Background(), `UPDATE orders SET provider = 'other' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}

	assertWebhookOK(t, e.post(t, capturedEvent("chk-marker-1", "evt-m1", "2026-06-20T10:00:00Z")))

	var (
		online   *time.Time
		provider *string
		orderPrv string
	)
	if err := e.pool.QueryRow(context.Background(),
		`SELECT i.online_paid_at, i.online_paid_provider, o.provider
		   FROM invoices i, orders o WHERE i.id = $1 AND o.id = $2`, inv, orderID,
	).Scan(&online, &provider, &orderPrv); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	if online == nil || !online.Equal(want) || provider == nil || *provider != "vipps" {
		t.Errorf("online marker = %v/%v, want %v/vipps", online, provider, want)
	}
	if orderPrv != "vipps" {
		t.Errorf("orders.provider = %q, want the paying attempt's provider", orderPrv)
	}
}

func TestMintOrderPayment_CancelledRequestStillRecordsAttempt(t *testing.T) {
	e := newWebhookEnv(t, true)
	h := newMintTestHandler(e.pool, e.fake.client())
	orderID, _ := seedCheckoutOrder(t, e.pool, e.clubID, "")
	addOrderLine(t, e.pool, orderID, "Test item", 100)

	// The payer's request goes away while the provider call is in flight.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.fake.onCreate = cancel
	if _, err := h.mintOrderPayment(ctx, vipps.ProviderName, orderID); err == nil {
		t.Fatal("mint succeeded on a cancelled request")
	}
	if got := e.attempts(t, orderID); len(got) != 1 || got[0].Status != "failed" {
		t.Errorf("attempts = %+v, want the attempt marked failed despite the cancelled request", got)
	}
}

func TestVippsWebhook_AuthorizedReplayAfterCaptureSkips(t *testing.T) {
	e := newWebhookEnv(t, true)
	logs := e.withLogBuffer()
	orderID := e.seedReferencedOrder(t, "", "chk-replay-auth", "pending", "")

	assertWebhookOK(t, e.post(t, capturedEvent("chk-replay-auth", "evt-ra1", "2026-06-20T10:00:00Z")))
	assertWebhookOK(t, e.post(t, authorizedEvent("chk-replay-auth", "evt-ra2")))

	if n := len(e.fake.cancelCalls()) + len(e.fake.captureCalls()); n != 0 {
		t.Errorf("replayed AUTHORIZED called Vipps %d times", n)
	}
	if strings.Contains(logs.String(), "cancelling") || strings.Contains(logs.String(), `"level":"warn"`) {
		t.Errorf("replayed AUTHORIZED logged a warning: %s", logs.String())
	}
	if o := e.order(t, orderID); o.Status != "paid" {
		t.Errorf("order = %+v, want paid", o)
	}
	if s := e.attemptStatus(t, "chk-replay-auth"); s != "captured" {
		t.Errorf("attempt = %q, want captured", s)
	}
}

func TestVippsWebhook_TwoAuthorizationsCaptureOnlyFirst(t *testing.T) {
	e := newWebhookEnv(t, true)
	orderID := e.seedReferencedOrder(t, "", "chk-two-a", "pending", "")
	e.seedAttempt(t, orderID, "chk-two-b", "created")

	assertWebhookOK(t, e.post(t, authorizedEvent("chk-two-a", "evt-t1")))
	assertWebhookOK(t, e.post(t, authorizedEvent("chk-two-b", "evt-t2")))
	// A redelivery of the first does not cancel itself.
	assertWebhookOK(t, e.post(t, authorizedEvent("chk-two-a", "evt-t3")))

	for _, c := range e.fake.captureCalls() {
		if c.Reference != "chk-two-a" {
			t.Errorf("captured %s, want only the first authorization", c.Reference)
		}
	}
	if cancels := e.fake.cancelCalls(); len(cancels) != 1 || cancels[0] != "chk-two-b" {
		t.Errorf("cancel calls = %v, want the second authorization voided", cancels)
	}
	if s := e.attemptStatus(t, "chk-two-a"); s != "authorized" {
		t.Errorf("first attempt = %q, want authorized", s)
	}
	if s := e.attemptStatus(t, "chk-two-b"); s != "cancelled" {
		t.Errorf("second attempt = %q, want cancelled", s)
	}
}

func TestVippsWebhook_FailedCaptureDoesNotBlockRetry(t *testing.T) {
	e := newWebhookEnv(t, true)
	orderID := e.seedReferencedOrder(t, "", "chk-fc-a", "pending", "")
	e.seedAttempt(t, orderID, "chk-fc-b", "created")

	e.fake.captureStatus = http.StatusInternalServerError
	assertWebhookOK(t, e.post(t, authorizedEvent("chk-fc-a", "evt-f1")))
	if s := e.attemptStatus(t, "chk-fc-a"); s != "created" {
		t.Errorf("attempt after a failed capture = %q, want released to created", s)
	}

	e.fake.captureStatus = 0
	assertWebhookOK(t, e.post(t, authorizedEvent("chk-fc-b", "evt-f2")))
	caps := e.fake.captureCalls()
	if len(caps) != 2 || caps[1].Reference != "chk-fc-b" {
		t.Errorf("capture calls = %+v, want the retry captured", caps)
	}
	if n := len(e.fake.cancelCalls()); n != 0 {
		t.Errorf("retry was cancelled %d times", n)
	}
}

func TestVippsWebhook_TerminatedAuthorizationUnblocksSibling(t *testing.T) {
	e := newWebhookEnv(t, true)
	orderID := e.seedReferencedOrder(t, "", "chk-tu-a", "pending", "")
	e.seedAttempt(t, orderID, "chk-tu-b", "created")

	assertWebhookOK(t, e.post(t, authorizedEvent("chk-tu-a", "evt-u1")))
	assertWebhookOK(t, e.post(t, terminatedEvent("chk-tu-a", "evt-u2")))
	if s := e.attemptStatus(t, "chk-tu-a"); s != "failed" {
		t.Errorf("terminated attempt = %q, want failed", s)
	}
	assertWebhookOK(t, e.post(t, authorizedEvent("chk-tu-b", "evt-u3")))
	caps := e.fake.captureCalls()
	if len(caps) != 2 || caps[1].Reference != "chk-tu-b" {
		t.Errorf("capture calls = %+v, want the sibling captured once the first ended", caps)
	}
	if n := len(e.fake.cancelCalls()); n != 0 {
		t.Errorf("sibling was cancelled %d times", n)
	}
}

func TestVippsWebhook_NoClientLeavesStaleAuthorization(t *testing.T) {
	e := newWebhookEnv(t, false)
	orderID := e.seedReferencedOrder(t, "", "chk-nc-a", "paid", "")
	e.seedAttempt(t, orderID, "chk-nc-b", "created")

	assertWebhookOK(t, e.post(t, authorizedEvent("chk-nc-b", "evt-n1")))
	if n := len(e.fake.cancelCalls()) + len(e.fake.captureCalls()); n != 0 {
		t.Errorf("Vipps called %d times without a client", n)
	}
	if s := e.attemptStatus(t, "chk-nc-b"); s != "created" {
		t.Errorf("stale attempt = %q, want left as created", s)
	}
	if o := e.order(t, orderID); o.Status != "paid" {
		t.Errorf("order = %+v, want still paid", o)
	}
}

func TestVippsWebhook_UnmintedAttemptIsNotCurrent(t *testing.T) {
	e := newWebhookEnv(t, true)
	orderID := e.seedReferencedOrder(t, "", "chk-um-a", "pending", "")
	// A newer row whose provider call never returned a reference.
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO payment_attempts (club_id, order_id, provider, amount, created_at)
		 VALUES ($1, $2, 'vipps', 450, clock_timestamp())`, e.clubID, orderID,
	); err != nil {
		t.Fatal(err)
	}

	assertWebhookOK(t, e.post(t, terminatedEvent("chk-um-a", "evt-um1")))
	if o := e.order(t, orderID); o.Status != "failed" {
		t.Errorf("order = %q, want failed: the attempt without a reference is not current", o.Status)
	}
}
