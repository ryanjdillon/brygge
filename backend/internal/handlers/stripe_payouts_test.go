package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// insertTestPayout stores a paid NOK payout for the club, listed with no
// members, and returns its row id.
func insertTestPayout(t *testing.T, c *fin.Club, payoutID string, ore int64, automatic bool) string {
	t.Helper()
	var id string
	if err := c.Pool.QueryRow(context.Background(),
		`INSERT INTO stripe_payouts (club_id, payout_id, amount_ore, currency, arrival_date, status, automatic, members_listed_at)
		 VALUES ($1, $2, $3, 'nok', make_date($4, 6, 10), 'paid', $5, now()) RETURNING id`,
		c.ID, payoutID, ore, c.Year, automatic,
	).Scan(&id); err != nil {
		t.Fatalf("insert payout: %v", err)
	}
	return id
}

// Board reads the uncleared payouts; clearing needs the treasurer/admin role
// and a fresh TOTP, is audited, and its refusals are 409 with a code.
func TestStripePayoutClearGatedAndAudited(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h := NewAccountingHandler(svc, as, nil, nil, nil, zerolog.Nop())
		r.Route("/gated", func(r chi.Router) {
			h.MountLedgerRoutes(r, middleware.RequireRole("treasurer", "admin"), middleware.RequireFreshTOTPDefault())
		})
		r.Route("/open", func(r chi.Router) { h.MountLedgerRoutes(r) })
	})
	ctx := context.Background()
	po := insertTestPayout(t, env.club, "po_test_handler", 2500, false)
	open := insertTestPayout(t, env.club, "po_test_handler_open", 3500, true)
	if _, err := env.club.Pool.Exec(ctx,
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, stripe_type, amount_ore, fee_ore, net_ore,
		      currency, created_at, status, payout_id)
		 VALUES ($1, 'txn_test_handler_c', 'charge', 'charge', 3500, 0, 3500, 'nok', make_date($2, 6, 8), 'unbooked', $3)`,
		env.club.ID, env.club.Year, open); err != nil {
		t.Fatal(err)
	}
	imp := env.club.NewBankImport(t)
	day := time.Date(env.club.Year, 6, 10, 0, 0, 0, 0, time.UTC)
	row := env.club.NewBankRow(t, imp, fin.BankRowOpts{Date: day, Cents: 2500, Description: "STRIPE PAYOUT"})
	openRow := env.club.NewBankRow(t, imp, fin.BankRowOpts{Date: day, Cents: 3500, Description: "STRIPE PAYOUT"})

	rec := env.serve([]string{"board"}, http.MethodGet, "/gated/stripe/payouts?cleared=false", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET as board: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Items []accounting.StripePayoutSummary `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 2 {
		t.Fatalf("list = %s (%v), want both payouts", rec.Body, err)
	}
	if rec := env.serve([]string{"board"}, http.MethodGet, "/gated/stripe/payouts?cleared=true", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("GET cleared=true: %d, want 400", rec.Code)
	}

	path := "/stripe/payouts/" + po + "/clear"
	body := map[string]string{"bank_row_id": row}
	if rec := env.serve([]string{"board"}, http.MethodPost, "/gated"+path, body); rec.Code != http.StatusForbidden {
		t.Errorf("POST as board: %d, want 403", rec.Code)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/gated"+path, body); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "totp_fresh_required") {
		t.Errorf("POST as treasurer without step-up: %d %s, want 403 totp_fresh_required", rec.Code, rec.Body)
	}

	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/open"+path, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	var res accounting.StripePayoutClearResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res.JournalEntryID == "" || res.PayoutID != "po_test_handler" {
		t.Fatalf("result = %s (%v)", rec.Body, err)
	}
	got := env.audit(audit.ActionStripePayoutCleared)
	if len(got) != 1 || got[0]["payout_id"] != "po_test_handler" || got[0]["bank_row_id"] != row ||
		got[0]["journal_entry_id"] != res.JournalEntryID || got[0]["manual"] != true ||
		got[0]["members_net_ore"] != float64(0) || got[0]["clearing_balance_ore"] != nil {
		t.Errorf("audit = %v", got)
	}

	conflict := func(path string, body any, code string) map[string]any {
		t.Helper()
		rec := env.serve([]string{"treasurer"}, http.MethodPost, path, body)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusConflict || out["code"] != code {
			t.Errorf("POST %s: %d %s, want 409 %s", path, rec.Code, rec.Body, code)
		}
		return out
	}
	conflict("/open"+path, body, accounting.StripeClearPayoutNotClearable)
	if out := conflict("/open/stripe/payouts/"+open+"/clear", map[string]string{"bank_row_id": openRow},
		accounting.StripeClearMembersOpen); out["open_members"] != float64(1) {
		t.Errorf("members_open body = %v, want open_members 1", out)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open/stripe/payouts/not-a-uuid/clear", body); rec.Code != http.StatusNotFound {
		t.Errorf("unknown payout: %d, want 404", rec.Code)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open"+path, map[string]string{}); rec.Code != http.StatusBadRequest {
		t.Errorf("no bank row: %d, want 400", rec.Code)
	}
	if got := env.audit(audit.ActionStripePayoutCleared); len(got) != 1 {
		t.Errorf("refusals audited: %v", got)
	}
}

// A manual assign of a provider payout deposit is 409 provider_payout with
// the provider.
func TestAssignProviderPayoutConflict(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h := NewBankRowsHandler(svc, as, zerolog.Nop())
		r.Post("/bank-rows/{rowID}/assign-account", h.HandleAssignAccount)
		r.Post("/bank-rows/{rowID}/assign-invoice", h.HandleAssignInvoice)
	})
	row := env.club.NewBankRow(t, env.club.NewBankImport(t), fin.BankRowOpts{Cents: 41265, Description: "Utb. 3000100 Vippsnr 548005"})
	inv := env.club.NewInvoice(t, fin.InvoiceOpts{Cents: 41265})
	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"/bank-rows/" + row + "/assign-account", map[string]any{"account_code": "3900", "kind": "revenue", "confirm_alone": true}},
		{"/bank-rows/" + row + "/assign-invoice", map[string]any{"invoice_id": inv.ID}},
	} {
		rec := env.serve([]string{"treasurer"}, http.MethodPost, c.path, c.body)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusConflict || out["code"] != "provider_payout" || out["provider"] != accounting.ProviderPayoutVipps {
			t.Errorf("POST %s: %d %s, want 409 provider_payout vipps", c.path, rec.Code, rec.Body)
		}
	}
	if got := env.audit(audit.ActionBankRowAssignedAccount); len(got) != 0 {
		t.Errorf("refused assign audited: %v", got)
	}
}
