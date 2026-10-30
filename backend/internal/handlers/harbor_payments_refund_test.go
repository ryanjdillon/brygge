package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// capturedAttempt adds a captured attempt of amountKr kr to the order and
// returns its id.
func (e *recentPaymentsEnv) capturedAttempt(t *testing.T, orderID string, amountKr float64) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount)
		 VALUES ($1, $2, 'stripe', 'cs_' || md5(random()::text), 'captured', $3) RETURNING id`,
		e.clubID, orderID, amountKr).Scan(&id); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	return id
}

func (e *recentPaymentsEnv) refund(t *testing.T, attemptID string, amountKr float64, status, paymentRef string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, status, provider_payment_ref)
		 VALUES ($1, $2, 'stripe', $3, $4, NULLIF($5, ''))`,
		e.clubID, attemptID, amountKr, status, paymentRef); err != nil {
		t.Fatalf("insert refund: %v", err)
	}
}

func (e *recentPaymentsEnv) dispute(t *testing.T, attemptID, orderID, id, status string, ageMin int) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO payment_disputes (club_id, attempt_id, order_id, provider, provider_dispute_id, status,
		                               evidence_due_by, created_at)
		 VALUES ($1, $2, $3, 'stripe', $4, $5, now() + interval '5 days', now() - make_interval(mins => $6))`,
		e.clubID, attemptID, orderID, id, status, ageMin); err != nil {
		t.Fatalf("insert dispute: %v", err)
	}
}

// getAs reads the overview as a caller with the given roles (none: no claims).
func (e *recentPaymentsEnv) getAs(t *testing.T, roles []string, query string) (map[string]recentPayment, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/harbor/recent-payments?"+query, nil)
	if roles != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{ClubID: e.clubID, Roles: roles}))
	}
	rec := httptest.NewRecorder()
	e.h.HandleRecentPayments(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Payments []recentPayment `json:"payments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &top)
	byID := map[string]recentPayment{}
	for _, p := range body.Payments {
		byID[p.ID] = p
	}
	return byID, top
}

// A refunded payment stays in the overview, with how much came back and any
// dispute against it, and the default harbour view still lists it.
func TestRecentPaymentsRefundsAndDisputes(t *testing.T) {
	e := newRecentPaymentsEnv(t)
	const pi = "pi_3Synthetic0001"

	plain := e.paidOrder(t, "stripe", 10, "guest_harbor")
	e.capturedAttempt(t, plain, 450)

	partial := e.paidOrder(t, "stripe", 20, "guest_harbor")
	a := e.capturedAttempt(t, partial, 450)
	e.refund(t, a, 100, "succeeded", pi)
	e.refund(t, a, 50, "requested", pi)
	e.refund(t, a, 25, "failed", pi)

	full := e.paidOrder(t, "stripe", 30, "guest_harbor")
	a = e.capturedAttempt(t, full, 450)
	e.refund(t, a, 450, "succeeded", pi)
	if _, err := e.pool.Exec(context.Background(), `UPDATE orders SET status = 'refunded' WHERE id = $1`, full); err != nil {
		t.Fatal(err)
	}

	// Marked refunded by the booking pass with no refund row seen.
	bare := e.paidOrder(t, "stripe", 35, "guest_harbor")
	e.capturedAttempt(t, bare, 450)
	if _, err := e.pool.Exec(context.Background(), `UPDATE orders SET status = 'refunded' WHERE id = $1`, bare); err != nil {
		t.Fatal(err)
	}

	// A refunded duplicate capture says nothing about the order it duplicated.
	dup := e.paidOrder(t, "stripe", 40, "guest_harbor")
	e.capturedAttempt(t, dup, 450)
	e.refund(t, e.capturedAttempt(t, dup, 450), 450, "succeeded", pi)

	disputed := e.paidOrder(t, "stripe", 50, "guest_harbor")
	a = e.capturedAttempt(t, disputed, 450)
	e.dispute(t, a, disputed, "dp_old", "warning_closed", 60)
	e.dispute(t, a, disputed, "dp_new", "needs_response", 5)

	// A pending order is still never listed.
	pending := e.paidOrder(t, "stripe", 5, "guest_harbor")
	if _, err := e.pool.Exec(context.Background(), `UPDATE orders SET status = 'pending', paid_at = NULL WHERE id = $1`, pending); err != nil {
		t.Fatal(err)
	}

	// The guest group an existing club got from its price items.
	e.backfill(t)
	got, top := e.getAs(t, nil, "kind=guest,motorhome")
	if _, ok := got[pending]; ok {
		t.Error("pending order listed")
	}
	if len(got) != 6 {
		t.Fatalf("listed %d payments, want 6", len(got))
	}
	check := func(id, state string, amount float64) {
		t.Helper()
		if p := got[id]; p.RefundState != state || p.RefundedAmount != amount {
			t.Errorf("order %s: refund = %s %.2f, want %s %.2f", id, p.RefundState, p.RefundedAmount, state, amount)
		}
	}
	check(plain, "none", 0)
	check(partial, "partial", 100)
	check(full, "full", 450)
	check(bare, "full", 450)
	check(dup, "none", 0)
	check(disputed, "none", 0)

	// That a named member charged back is for the treasurer and admin: the
	// harbor master reads this list, and sees the refunds but no dispute.
	for _, id := range []string{disputed, plain} {
		if p := got[id]; p.DisputeStatus != nil || p.DisputeDueBy != nil {
			t.Errorf("order %s: dispute %v shown to an anonymous caller", id, *p.DisputeStatus)
		}
	}
	harbor, _ := e.getAs(t, []string{"harbor_master"}, "kind=guest,motorhome")
	if p := harbor[disputed]; p.DisputeStatus != nil || p.DisputeDueBy != nil {
		t.Errorf("harbor master sees dispute %v", *p.DisputeStatus)
	}
	if p := harbor[partial]; p.RefundState != "partial" || p.RefundedAmount != 100 {
		t.Errorf("harbor master refund = %s %.2f, want partial 100", p.RefundState, p.RefundedAmount)
	}
	// Without a role the Dashboard is not offered, nor the dispute count.
	for id, p := range got {
		if p.PaymentURL != "" || p.DisputeURL != "" {
			t.Errorf("order %s: links %q %q for an anonymous caller", id, p.PaymentURL, p.DisputeURL)
		}
	}
	if _, ok := top["open_disputes"]; ok {
		t.Error("open_disputes shown to an anonymous caller")
	}

	t.Run("treasurer gets Dashboard links", func(t *testing.T) {
		got, top := e.getAs(t, []string{"treasurer"}, "kind=guest,motorhome")
		if u := got[partial].PaymentURL; !strings.HasSuffix(u, "/payments/"+pi) {
			t.Errorf("payment link = %q, want the payment's Dashboard page", u)
		}
		if p := got[disputed]; p.DisputeStatus == nil || *p.DisputeStatus != "needs_response" || p.DisputeDueBy == nil {
			t.Errorf("dispute = %v due %v, want the latest, needs_response with a deadline", p.DisputeStatus, p.DisputeDueBy)
		}
		if p := got[plain]; p.DisputeStatus != nil {
			t.Errorf("plain payment dispute = %v, want none", *p.DisputeStatus)
		}
		if u := got[disputed].DisputeURL; !strings.HasSuffix(u, "/disputes/dp_new") {
			t.Errorf("dispute link = %q, want the latest dispute's page", u)
		}
		if n, _ := top["open_disputes"].(float64); n != 1 {
			t.Errorf("open_disputes = %v, want 1", top["open_disputes"])
		}
		if u, _ := top["open_disputes_url"].(string); !strings.Contains(u, "statuses[0]=needs_response") ||
			!strings.Contains(u, "sort=evidence_due_by") {
			t.Errorf("disputes list link = %q", u)
		}
	})

	t.Run("provider links are for the treasurer and admin", func(t *testing.T) {
		cfg := e.h.config
		saved := *cfg
		t.Cleanup(func() { *cfg = saved })
		cfg.StripeSecretKey, cfg.StripeWebhookSecret = "sk_live_synthetic", "whsec_synthetic"
		cfg.VippsMSN, cfg.VippsTestMode = "123456", false

		_, top := e.getAs(t, []string{"treasurer"}, "")
		links, _ := top["provider_links"].(map[string]any)
		if links["stripe"] != "https://dashboard.stripe.com/payments" ||
			links["vipps"] != "https://portal.vippsmobilepay.com/123456/home" {
			t.Errorf("provider_links = %v", top["provider_links"])
		}
		for _, roles := range [][]string{nil, {"harbor_master"}} {
			if _, top := e.getAs(t, roles, ""); top["provider_links"] != nil {
				t.Errorf("provider_links = %v for roles %v", top["provider_links"], roles)
			}
		}
		cfg.StripeSecretKey, cfg.VippsMSN = "", ""
		if _, top := e.getAs(t, []string{"admin"}, ""); top["provider_links"] != nil {
			t.Errorf("provider_links = %v with no provider configured", top["provider_links"])
		}
	})

	t.Run("no waiting dispute, no reminder", func(t *testing.T) {
		if _, err := e.pool.Exec(context.Background(),
			`UPDATE payment_disputes SET status = 'won' WHERE provider_dispute_id = 'dp_new'`); err != nil {
			t.Fatal(err)
		}
		_, top := e.getAs(t, []string{"admin"}, "")
		if _, ok := top["open_disputes"]; ok {
			t.Errorf("open_disputes = %v with nothing waiting", top["open_disputes"])
		}
	})
}
