package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// insertReviewTxn stores a Stripe balance transaction in review.
func insertReviewTxn(t *testing.T, c *fin.Club, btID, category, reason string, amountOre, feeOre int64) string {
	t.Helper()
	var id string
	if err := c.Pool.QueryRow(context.Background(),
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, stripe_type, amount_ore, fee_ore, net_ore,
		      currency, created_at, status, review_reason)
		 VALUES ($1, $2, $3, $3, $4::bigint, $5::bigint, $4::bigint - $5::bigint, 'nok', make_date($6, 6, 8), 'needs_review', $7)
		 RETURNING id`,
		c.ID, btID, category, amountOre, feeOre, c.Year, reason,
	).Scan(&id); err != nil {
		t.Fatalf("insert stripe txn: %v", err)
	}
	return id
}

// Board reads the review list with each row's actions; the manual actions
// need the treasurer/admin role and a fresh TOTP, are audited, and their
// refusals are 409 with a code.
func TestStripeTxnActionsGatedAndAudited(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h := NewAccountingHandler(svc, as, nil, nil, nil, zerolog.Nop())
		r.Route("/gated", func(r chi.Router) {
			h.MountLedgerRoutes(r, middleware.RequireRole("treasurer", "admin"), middleware.RequireFreshTOTPDefault())
		})
		r.Route("/open", func(r chi.Router) { h.MountLedgerRoutes(r) })
	})
	inv := env.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	charge := insertReviewTxn(t, env.club, "txn_test_h_charge", "charge", accounting.StripeReviewNoAttempt, 45000, 1125)
	walkup := insertReviewTxn(t, env.club, "txn_test_h_walkup", "charge", accounting.StripeReviewReceivableNotOpen, 20000, 500)
	refund := insertReviewTxn(t, env.club, "txn_test_h_refund", "refund", accounting.StripeReviewRefundPending, -5000, 0)

	rec := env.serve([]string{"board"}, http.MethodGet, "/gated/stripe/transactions?status=needs_review&limit=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET as board: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Items   []accounting.StripeTxnItem `json:"items"`
		Limit   int                        `json:"limit"`
		HasMore bool                       `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 3 || list.Limit != 10 || list.HasMore {
		t.Fatalf("list = %s (%v)", rec.Body, err)
	}
	actions := map[string][]string{}
	for _, it := range list.Items {
		actions[it.ID] = it.AllowedActions
	}
	if a := actions[charge]; len(a) != 3 || a[0] != accounting.StripeActionAssignInvoice {
		t.Errorf("charge actions = %v", a)
	}
	if a := actions[walkup]; len(a) != 2 || a[0] != accounting.StripeActionAssignAccount {
		t.Errorf("receivable_not_open actions = %v", a)
	}
	if a := actions[refund]; len(a) != 1 || a[0] != accounting.StripeActionDismiss {
		t.Errorf("refund actions = %v", a)
	}
	if rec := env.serve([]string{"board"}, http.MethodGet, "/gated/stripe/transactions?status=booked", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("status=booked: %d, want 400", rec.Code)
	}
	rec = env.serve([]string{"board"}, http.MethodGet, "/gated/stripe/transactions/"+charge+"/potential-invoices", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), inv.ID) {
		t.Errorf("potential invoices: %d %s", rec.Code, rec.Body)
	}
	rec = env.serve([]string{"board"}, http.MethodGet, "/gated/stripe/assignable-accounts", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"1500"`) || !strings.Contains(rec.Body.String(), `"3900"`) {
		t.Errorf("assignable accounts: %d %s", rec.Code, rec.Body)
	}

	invPath := "/stripe/transactions/" + charge + "/assign-invoice"
	invBody := map[string]string{"invoice_id": inv.ID}
	if rec := env.serve([]string{"board"}, http.MethodPost, "/gated"+invPath, invBody); rec.Code != http.StatusForbidden {
		t.Errorf("POST as board: %d, want 403", rec.Code)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/gated"+invPath, invBody); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "totp_fresh_required") {
		t.Errorf("POST as treasurer without step-up: %d %s", rec.Code, rec.Body)
	}

	conflict := func(path string, body any, code string) {
		t.Helper()
		rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open"+path, body)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusConflict || out["code"] != code || out["reason"] == "" {
			t.Errorf("POST %s: %d %s, want 409 %s", path, rec.Code, rec.Body, code)
		}
	}
	conflict("/stripe/transactions/"+refund+"/assign-account", map[string]string{"account_code": "3900"}, accounting.StripeTxnNotAllowed)
	conflict("/stripe/transactions/"+walkup+"/assign-account", map[string]string{"account_code": "1500"}, accounting.StripeTxnInvalidAccount)
	conflict("/stripe/transactions/"+refund+"/dismiss", map[string]string{"reason": "internal_transfer"}, accounting.StripeTxnInvalidReason)
	other := env.club.NewInvoice(t, fin.InvoiceOpts{Cents: 30000})
	conflict("/stripe/transactions/"+charge+"/assign-invoice", map[string]string{"invoice_id": other.ID}, accounting.StripeTxnAmountMismatch)

	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/open"+invPath, invBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign invoice: %d %s", rec.Code, rec.Body)
	}
	var res accounting.StripeTxnBookResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res.JournalEntryID == "" {
		t.Fatalf("result = %s (%v)", rec.Body, err)
	}
	if got := env.audit(audit.ActionStripeTxnAssignedInvoice); len(got) != 1 || got[0]["invoice_id"] != inv.ID ||
		got[0]["journal_entry_id"] != res.JournalEntryID || got[0]["balance_transaction_id"] != "txn_test_h_charge" {
		t.Errorf("audit = %v", got)
	}
	conflict(invPath, invBody, accounting.StripeTxnAlreadyBooked)

	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/open/stripe/transactions/"+walkup+"/assign-account",
		map[string]string{"account_code": "3900", "note": "Synthetic walk-up"})
	if rec.Code != http.StatusOK {
		t.Fatalf("assign account: %d %s", rec.Code, rec.Body)
	}
	if got := env.audit(audit.ActionStripeTxnAssignedAccount); len(got) != 1 || got[0]["account_code"] != "3900" || got[0]["journal_entry_id"] == "" {
		t.Errorf("audit = %v", got)
	}

	dismiss := "/open/stripe/transactions/" + refund + "/dismiss"
	for range 2 {
		if rec := env.serve([]string{"treasurer"}, http.MethodPost, dismiss, map[string]string{"reason": "refund_or_credit", "note": "Synthetic"}); rec.Code != http.StatusOK {
			t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
		}
	}
	if got := env.audit(audit.ActionStripeTxnDismissed); len(got) != 1 || got[0]["reason"] != "refund_or_credit" ||
		got[0]["review_reason"] != accounting.StripeReviewRefundPending {
		t.Errorf("dismiss audit = %v, want one row", got)
	}
	rec = env.serve([]string{"board"}, http.MethodGet, "/gated/stripe/transactions?status=dismissed", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"undismiss"`) {
		t.Errorf("dismissed list: %d %s", rec.Code, rec.Body)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open/stripe/transactions/"+refund+"/undismiss", nil); rec.Code != http.StatusOK {
		t.Fatalf("undismiss: %d %s", rec.Code, rec.Body)
	}
	if got := env.audit(audit.ActionStripeTxnUndismissed); len(got) != 1 || got[0]["status"] != accounting.StripeTxnNeedsReview {
		t.Errorf("undismiss audit = %v", got)
	}

	for _, p := range []string{"/open/stripe/transactions/not-a-uuid/dismiss", "/open/stripe/transactions/00000000-0000-0000-0000-000000000001/undismiss"} {
		if rec := env.serve([]string{"treasurer"}, http.MethodPost, p, map[string]string{"reason": "duplicate"}); rec.Code != http.StatusNotFound {
			t.Errorf("POST %s: %d, want 404", p, rec.Code)
		}
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open"+invPath, map[string]string{}); rec.Code != http.StatusBadRequest {
		t.Errorf("no invoice_id: %d, want 400", rec.Code)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open/stripe/transactions/"+charge+"/assign-account",
		map[string]string{"account_code": " "}); rec.Code != http.StatusBadRequest {
		t.Errorf("no account_code: %d, want 400", rec.Code)
	}
}
