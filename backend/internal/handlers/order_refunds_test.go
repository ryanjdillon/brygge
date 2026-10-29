package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// refundEnv is the refund button's API over a fake Stripe: the real routes
// behind the real gates (/gated), and the same handlers past the gate
// (/open), as a stepped-up treasurer reaches them.
type refundHTTPEnv struct {
	*stripeEnv
	r      chi.Router
	svc    *payments.RefundService
	userID string
}

func newRefundHTTPEnv(t *testing.T) *refundHTTPEnv {
	t.Helper()
	e := newStripeEnv(t)
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"treasurer"})
	reg := payments.NewRegistry(stripe.NewPaymentProvider(e.fake.client(), e.h.cfg.ClubSlug), refundlessProvider{})
	svc := payments.NewRefundService(e.pool, reg, zerolog.Nop())
	cfg := &config.Config{ClubSlug: e.h.cfg.ClubSlug, StripeSecretKey: stripeTestKey}
	h := NewOrderRefundHandler(svc, cfg, audit.NewService(e.pool, zerolog.Nop()), zerolog.Nop())

	r := chi.NewRouter()
	r.Route("/gated", func(r chi.Router) {
		h.Mount(r, middleware.RequireRole("treasurer", "admin"), middleware.RequireFreshTOTPDefault())
	})
	pass := func(next http.Handler) http.Handler { return next }
	r.Route("/open", func(r chi.Router) { h.Mount(r, pass, pass) })
	return &refundHTTPEnv{stripeEnv: e, r: r, svc: svc, userID: userID}
}

// refundlessProvider stands for Vipps: a provider that cannot refund.
type refundlessProvider struct{}

func (refundlessProvider) Name() string { return "vipps" }
func (refundlessProvider) CreatePayment(context.Context, payments.Request) (payments.Result, error) {
	return payments.Result{}, nil
}

func (e *refundHTTPEnv) serve(roles []string, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: e.userID, ClubID: e.clubID, Roles: roles}))
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

func (e *refundHTTPEnv) post(orderID string, body map[string]any) *httptest.ResponseRecorder {
	return e.serve([]string{"treasurer"}, http.MethodPost, "/open/orders/"+orderID+"/refund", body)
}

// paid adds a paid order of 450 kr with one captured Stripe attempt.
func (e *refundHTTPEnv) paid(t *testing.T, suffix string) (orderID, attemptID string) {
	t.Helper()
	orderID = e.seedOrder(t, "")
	attemptID = e.seedAttempt(t, orderID, "stripe", "cs_h"+suffix, "captured")
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE orders SET status = 'paid', paid_at = now(), provider = 'stripe' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	return orderID, attemptID
}

func refundBody(attemptID string, kr float64) map[string]any {
	return map[string]any{"attempt_id": attemptID, "amount": kr, "reason": "Dobbeltbetaling", "request_key": uuid.NewString()}
}

type refundResponse struct {
	Refund   payments.RefundRecord `json:"refund"`
	Replayed bool                  `json:"replayed"`
	Awaiting bool                  `json:"awaiting_confirmation"`
	Code     string                `json:"code"`
	Reason   string                `json:"reason"`
}

func decodeRefund(t *testing.T, rec *httptest.ResponseRecorder) refundResponse {
	t.Helper()
	var out refundResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return out
}

func (e *refundHTTPEnv) audits(t *testing.T) []map[string]any {
	t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT details FROM audit_log WHERE club_id = $1 AND action = 'order.refund_requested' ORDER BY created_at`, e.clubID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var d map[string]any
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// Starting a refund needs the treasurer or admin role and a fresh TOTP;
// reading an order's refunds needs the role only.
func TestOrderRefundRoutesGated(t *testing.T) {
	e := newRefundHTTPEnv(t)
	orderID, attemptID := e.paid(t, "g")
	path := "/gated/orders/" + orderID + "/refund"

	for _, roles := range [][]string{{"board"}, {"harbor_master"}, {"member"}} {
		if rec := e.serve(roles, http.MethodPost, path, refundBody(attemptID, 10)); rec.Code != http.StatusForbidden {
			t.Errorf("POST as %v: %d, want 403", roles, rec.Code)
		}
		if rec := e.serve(roles, http.MethodGet, "/gated/orders/"+orderID+"/refunds", nil); rec.Code != http.StatusForbidden {
			t.Errorf("GET as %v: %d, want 403", roles, rec.Code)
		}
	}
	rec := e.serve([]string{"treasurer"}, http.MethodPost, path, refundBody(attemptID, 10))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "totp_fresh_required") {
		t.Errorf("POST as treasurer without a step-up: %d %s", rec.Code, rec.Body)
	}
	if rec := e.serve([]string{"treasurer"}, http.MethodGet, "/gated/orders/"+orderID+"/refunds", nil); rec.Code != http.StatusOK {
		t.Errorf("GET as treasurer: %d %s", rec.Code, rec.Body)
	}
	if n := len(e.fake.refundCalls()); n != 0 {
		t.Errorf("Stripe was called %d times by refused requests", n)
	}
	if n := len(e.audits(t)); n != 0 {
		t.Errorf("%d audit rows from refused requests", n)
	}
}

// A refund is stored, sent to Stripe once with the request key as its
// Idempotency-Key and its metadata, audited, and repeating the click sends
// and audits nothing more.
func TestOrderRefundSendsOnceAndAudits(t *testing.T) {
	e := newRefundHTTPEnv(t)
	orderID, attemptID := e.paid(t, "1")
	body := refundBody(attemptID, 123)
	key := body["request_key"].(string)

	rec := e.post(orderID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("refund: %d %s", rec.Code, rec.Body)
	}
	out := decodeRefund(t, rec)
	if out.Replayed || out.Awaiting || out.Refund.Status != "succeeded" || out.Refund.Amount != 123 ||
		out.Refund.ProviderRefundID == nil || *out.Refund.ProviderRefundID != "re_fake_1" {
		t.Fatalf("response = %+v", out)
	}
	posts := e.fake.refundCalls()
	if len(posts) != 1 || posts[0].IdempotencyKey != "refund-"+key {
		t.Fatalf("stripe calls = %+v", posts)
	}
	f := posts[0].Form
	if f.Get("payment_intent") != fakeStripePI("cs_h1") || f.Get("amount") != "12300" || f.Get("reason") != "requested_by_customer" ||
		f.Get("metadata[refund_request_key]") != key || f.Get("metadata[order_id]") != orderID ||
		f.Get("metadata[attempt_id]") != attemptID || f.Get("metadata[club_slug]") != e.h.cfg.ClubSlug {
		t.Errorf("refund form = %v", f)
	}

	again := decodeRefund(t, e.post(orderID, body))
	if !again.Replayed || again.Refund.ID != out.Refund.ID {
		t.Errorf("repeat = %+v", again)
	}
	if n := len(e.fake.refundCalls()); n != 1 {
		t.Errorf("stripe refund calls = %d, want 1", n)
	}

	audits := e.audits(t)
	if len(audits) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(audits))
	}
	a := audits[0]
	if a["refund_id"] != out.Refund.ID || a["attempt_id"] != attemptID || a["amount_ore"] != float64(12300) ||
		a["request_key"] != key || a["status"] != "succeeded" || a["reason"] != "Dobbeltbetaling" {
		t.Errorf("audit details = %v", a)
	}
	if n := e.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, e.clubID); n != 0 {
		t.Errorf("the endpoint booked %d journal entries", n)
	}
	if st := e.order(t, orderID).Status; st != "paid" {
		t.Errorf("order = %s, want it left to the booking pass", st)
	}
}

func TestOrderRefundRefusals(t *testing.T) {
	e := newRefundHTTPEnv(t)
	orderID, attemptID := e.paid(t, "2")

	conflict := func(name string, rec *httptest.ResponseRecorder, code string) {
		t.Helper()
		out := decodeRefund(t, rec)
		if rec.Code != http.StatusConflict || out.Code != code {
			t.Errorf("%s: %d %s, want 409 %s", name, rec.Code, rec.Body, code)
		}
	}

	conflict("more than paid", e.post(orderID, refundBody(attemptID, 450.01)), payments.RefundAmountExceeds)

	partial := refundBody(attemptID, 100)
	if rec := e.post(orderID, partial); rec.Code != http.StatusOK {
		t.Fatalf("partial: %d %s", rec.Code, rec.Body)
	}
	conflict("key for another amount", e.post(orderID, map[string]any{
		"attempt_id": attemptID, "amount": 99, "reason": "x", "request_key": partial["request_key"]}), payments.RefundRequestKeyReused)
	conflict("more than is left", e.post(orderID, refundBody(attemptID, 350.01)), payments.RefundAmountExceeds)

	invID := seedPayableInvoice(t, e.pool, e.clubID, e.userID, "open", 801)
	addOrderSource(t, e.pool, orderID, invID)
	recollect := refundBody(attemptID, 350)
	recollect["outcome"] = "recollect"
	conflict("recollect after a partial", e.post(orderID, recollect), payments.RefundRecollectAfterPartly)

	if _, err := payments.UpsertDispute(context.Background(), e.pool, payments.Dispute{
		ClubID: e.clubID, Provider: "stripe", ProviderDisputeID: "dp_h2", AttemptID: attemptID, Status: "needs_response"}); err != nil {
		t.Fatal(err)
	}
	conflict("disputed", e.post(orderID, refundBody(attemptID, 10)), payments.RefundDisputed)

	vippsOrder := e.seedOrder(t, "")
	vippsAttempt := e.seedAttempt(t, vippsOrder, "vipps", "chk-h2", "captured")
	conflict("vipps", e.post(vippsOrder, refundBody(vippsAttempt, 10)), payments.RefundNotSupported)

	pendingOrder := e.seedOrder(t, "")
	pendingAttempt := e.seedAttempt(t, pendingOrder, "stripe", "cs_h2p", "authorized")
	conflict("not captured", e.post(pendingOrder, refundBody(pendingAttempt, 10)), payments.RefundNotCaptured)

	for name, tc := range map[string]struct {
		path string
		body map[string]any
		want int
	}{
		"unknown order":   {"/open/orders/" + uuid.NewString() + "/refund", refundBody(attemptID, 10), http.StatusNotFound},
		"malformed order": {"/open/orders/nope/refund", refundBody(attemptID, 10), http.StatusNotFound},
		"unknown attempt": {"/open/orders/" + orderID + "/refund", refundBody(uuid.NewString(), 10), http.StatusNotFound},
		"no reason":       {"/open/orders/" + orderID + "/refund", map[string]any{"attempt_id": attemptID, "amount": 1, "request_key": uuid.NewString()}, http.StatusBadRequest},
		"no key":          {"/open/orders/" + orderID + "/refund", map[string]any{"attempt_id": attemptID, "amount": 1, "reason": "x"}, http.StatusBadRequest},
		"sub-øre amount":  {"/open/orders/" + orderID + "/refund", refundBody(attemptID, 0.001), http.StatusBadRequest},
		"zero amount":     {"/open/orders/" + orderID + "/refund", refundBody(attemptID, 0), http.StatusBadRequest},
		"negative amount": {"/open/orders/" + orderID + "/refund", refundBody(attemptID, -5), http.StatusBadRequest},
		"unknown outcome": {"/open/orders/" + orderID + "/refund", map[string]any{"attempt_id": attemptID, "amount": 5, "reason": "x", "request_key": uuid.NewString(), "outcome": "waive"}, http.StatusBadRequest},
	} {
		if rec := e.serve([]string{"treasurer"}, http.MethodPost, tc.path, tc.body); rec.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", name, rec.Code, rec.Body, tc.want)
		}
	}
	if rec := e.serve([]string{"treasurer"}, http.MethodPost, "/open/orders/"+orderID+"/refund", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: %d, want 400", rec.Code)
	}

	if n := len(e.fake.refundCalls()); n != 1 {
		t.Errorf("stripe refund calls = %d, want only the one partial refund", n)
	}
	if n := len(e.audits(t)); n != 1 {
		t.Errorf("audit rows = %d, want 1 (refusals are not audited)", n)
	}
}

// Stripe refusing the refund answers 502 and frees the amount; a call whose
// answer is lost is looked up and not re-sent; one Stripe never got stays
// requested (202) until the sweep, run when the order's refunds are read,
// finds it.
func TestOrderRefundStripeFailures(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		e := newRefundHTTPEnv(t)
		orderID, attemptID := e.paid(t, "3")
		e.fake.refundFailStatus = http.StatusBadRequest
		e.fake.refundFailBody = `{"error":{"type":"invalid_request_error","code":"charge_disputed","message":"disputed"}}`

		rec := e.post(orderID, refundBody(attemptID, 450))
		out := decodeRefund(t, rec)
		if rec.Code != http.StatusBadGateway || out.Code != "refund_failed" || out.Reason != "charge_disputed" || out.Refund.Status != "failed" {
			t.Fatalf("refused refund: %d %s", rec.Code, rec.Body)
		}
		e.fake.refundFailStatus = 0
		if rec := e.post(orderID, refundBody(attemptID, 450)); rec.Code != http.StatusOK {
			t.Errorf("full refund after the failed one: %d %s", rec.Code, rec.Body)
		}
	})

	t.Run("answer lost after Stripe acted", func(t *testing.T) {
		e := newRefundHTTPEnv(t)
		orderID, attemptID := e.paid(t, "4")
		e.fake.refundLost = true

		rec := e.post(orderID, refundBody(attemptID, 200))
		out := decodeRefund(t, rec)
		if rec.Code != http.StatusOK || out.Refund.ProviderRefundID == nil || out.Refund.Status != "succeeded" {
			t.Fatalf("lost answer: %d %s", rec.Code, rec.Body)
		}
		if n := len(e.fake.refundCalls()); n != 1 {
			t.Errorf("stripe refund calls = %d, want 1 (looked up, not re-sent)", n)
		}
	})

	t.Run("answer lost, Stripe never got it", func(t *testing.T) {
		e := newRefundHTTPEnv(t)
		orderID, attemptID := e.paid(t, "5")
		e.fake.refundFailStatus = http.StatusInternalServerError
		e.fake.refundFailBody = `{"error":{"type":"api_error","message":"boom"}}`
		body := refundBody(attemptID, 200)

		rec := e.post(orderID, body)
		out := decodeRefund(t, rec)
		if rec.Code != http.StatusAccepted || !out.Awaiting || out.Refund.Status != "requested" || out.Refund.ProviderRefundID != nil {
			t.Fatalf("uncertain refund: %d %s", rec.Code, rec.Body)
		}
		if n := e.count(t, `SELECT count(*) FROM payment_refunds WHERE club_id = $1 AND status = 'requested'`, e.clubID); n != 1 {
			t.Fatalf("%d requested rows, want 1", n)
		}

		// Reading the refunds too early does not touch it.
		if rec := e.serve([]string{"treasurer"}, http.MethodGet, "/open/orders/"+orderID+"/refunds", nil); rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body)
		}
		if lists := len(e.fake.refundLists); lists != 1 {
			t.Errorf("stripe refund lists = %d, want the one made while resolving the click", lists)
		}

		// Later Stripe has the refund (made by the call that seemed lost).
		e.fake.addRefund("re_late", fakeStripePI("cs_h5"), "ch_late", "succeeded", 20000, body["request_key"].(string))
		if _, err := e.pool.Exec(context.Background(),
			`UPDATE payment_refunds SET created_at = now() - interval '11 minutes' WHERE club_id = $1`, e.clubID); err != nil {
			t.Fatal(err)
		}
		rec = e.serve([]string{"treasurer"}, http.MethodGet, "/open/orders/"+orderID+"/refunds", nil)
		var view payments.OrderRefunds
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s (%v)", rec.Code, rec.Body, err)
		}
		if len(view.Refunds) != 1 || view.Refunds[0].Status != "succeeded" ||
			view.Refunds[0].ProviderRefundID == nil || *view.Refunds[0].ProviderRefundID != "re_late" {
			t.Errorf("refunds after the sweep = %+v", view.Refunds)
		}
		if view.Attempts[0].Refundable != 250 {
			t.Errorf("refundable = %v, want 250", view.Attempts[0].Refundable)
		}
	})
}

// Two requests over the whole payment under different keys: one is sent to
// Stripe, the other refused.
func TestOrderRefundConcurrentKeys(t *testing.T) {
	e := newRefundHTTPEnv(t)
	orderID, attemptID := e.paid(t, "6")

	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.post(orderID, refundBody(attemptID, 450)).Code
		}()
	}
	wg.Wait()

	var ok, refused int
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			refused++
		default:
			t.Errorf("status %d", c)
		}
	}
	if ok != 1 || refused != len(codes)-1 {
		t.Errorf("%d accepted, %d refused; want 1 and %d", ok, refused, len(codes)-1)
	}
	if n := len(e.fake.refundCalls()); n != 1 {
		t.Errorf("stripe refund calls = %d, want 1", n)
	}
}

// The order's refund view: the payments with what is left, the duplicate
// capture labelled, the dispute, the recollect rule and the Dashboard link,
// which is a test-mode link for a test key and names no account.
func TestOrderRefundsView(t *testing.T) {
	e := newRefundHTTPEnv(t)
	orderID, attemptID := e.paid(t, "7")
	invID := seedPayableInvoice(t, e.pool, e.clubID, e.userID, "open", 802)
	addOrderSource(t, e.pool, orderID, invID)
	dup := e.seedAttempt(t, orderID, "stripe", "cs_h7d", "captured")
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, stripe_type, amount_ore, fee_ore, net_ore, currency,
		      created_at, status, attempt_id, payment_intent_id)
		 VALUES ($1, 'txn_test_view7', 'charge', 'charge', 45000, 0, 45000, 'nok', now(), 'booked', $2, 'pi_view7')`,
		e.clubID, attemptID); err != nil {
		t.Fatal(err)
	}

	rec := e.serve([]string{"treasurer"}, http.MethodGet, "/open/orders/"+orderID+"/refunds", nil)
	var view payments.OrderRefunds
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("view: %d %s (%v)", rec.Code, rec.Body, err)
	}
	if !view.CanRefund || !view.RecollectAllowed || !view.HasFakturas || len(view.Attempts) != 2 {
		t.Fatalf("view = %+v", view)
	}
	first, second := view.Attempts[0], view.Attempts[1]
	if first.ID != attemptID || first.Duplicate || first.PaymentURL != "https://dashboard.stripe.com/test/payments/pi_view7" ||
		second.ID != dup || !second.Duplicate || second.PaymentURL != "" {
		t.Errorf("attempts = %+v", view.Attempts)
	}
	if strings.Contains(rec.Body.String(), "acct_") {
		t.Errorf("view names an account: %s", rec.Body)
	}
	if rec := e.serve([]string{"treasurer"}, http.MethodGet, "/open/orders/"+uuid.NewString()+"/refunds", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown order: %d, want 404", rec.Code)
	}
}

// The payment overview and the faktura list say which payments the caller can
// refund, and only to a treasurer or admin.
func TestRefundFlagsOnLists(t *testing.T) {
	e := newRefundHTTPEnv(t)
	orderID, attemptID := e.paid(t, "8")
	invID := seedPayableInvoice(t, e.pool, e.clubID, e.userID, "open", 803)
	addOrderSource(t, e.pool, orderID, invID)
	if _, err := e.pool.Exec(context.Background(), `UPDATE invoices SET online_paid_at = now(), online_paid_provider = 'stripe' WHERE id = $1`, invID); err != nil {
		t.Fatal(err)
	}
	vipps := e.seedOrder(t, "")
	e.seedAttempt(t, vipps, "vipps", "chk-h8", "captured")
	if _, err := e.pool.Exec(context.Background(), `UPDATE orders SET status = 'paid', paid_at = now() WHERE id = $1`, vipps); err != nil {
		t.Fatal(err)
	}

	harbor := NewHarborHandler(e.pool, &config.Config{ClubSlug: e.h.cfg.ClubSlug}, zerolog.Nop())
	harbor.SetRefunds(e.svc)
	invoices := NewInvoiceHandler(e.pool, &config.Config{ClubSlug: e.h.cfg.ClubSlug}, nil, nil, nil, nil, zerolog.Nop())
	invoices.SetRefunds(e.svc)
	call := func(h http.HandlerFunc, roles []string, url string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: e.userID, ClubID: e.clubID, Roles: roles}))
		rec := httptest.NewRecorder()
		h(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", url, rec.Code, rec.Body)
		}
		return out
	}
	canRefund := func(roles []string) map[string]bool {
		out := map[string]bool{}
		for _, p := range call(harbor.HandleRecentPayments, roles, "/recent?kind=")["payments"].([]any) {
			p := p.(map[string]any)
			out[p["id"].(string)] = p["can_refund"].(bool)
		}
		return out
	}

	if got := canRefund([]string{"treasurer"}); !got[orderID] || got[vipps] {
		t.Errorf("treasurer's overview = %v, want the card payment refundable and the Vipps one not", got)
	}
	if got := canRefund([]string{"harbor_master"}); got[orderID] {
		t.Errorf("harbor master's overview = %v, want nothing refundable", got)
	}

	refundOrder := func(roles []string) any {
		for _, it := range call(invoices.HandleListInvoices, roles, "/invoices?status=sent")["items"].([]any) {
			if it.(map[string]any)["id"] == invID {
				return it.(map[string]any)["refund_order_id"]
			}
		}
		t.Fatal("faktura not listed")
		return nil
	}
	if got := refundOrder([]string{"treasurer"}); got != orderID {
		t.Errorf("treasurer's faktura list refund_order_id = %v, want %s", got, orderID)
	}
	if got := refundOrder([]string{"board"}); got != nil {
		t.Errorf("board's faktura list refund_order_id = %v, want none", got)
	}

	// Once the whole payment is refunded the button goes.
	if rec := e.post(orderID, refundBody(attemptID, 450)); rec.Code != http.StatusOK {
		t.Fatalf("refund: %d %s", rec.Code, rec.Body)
	}
	if got := refundOrder([]string{"treasurer"}); got != nil {
		t.Errorf("after a full refund refund_order_id = %v, want none", got)
	}
}
