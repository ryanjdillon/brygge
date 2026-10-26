package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// wantImportedRefusal checks a 409 carrying the imported-faktura code and the
// club-agnostic message (BRY-386).
func wantImportedRefusal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]string
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil ||
		body["code"] != importedNoOpeningBalanceCode || body["error"] != accounting.ErrImportedNoOpeningBalance.Error() {
		t.Fatalf("got %d %s, want 409 with code %s and the opening-balance message",
			rec.Code, rec.Body.String(), importedNoOpeningBalanceCode)
	}
}

// Tildel answers 409 imported_no_opening_balance, booking nothing, when a
// bank row or Vipps charge is assigned to an imported faktura whose
// receivable isn't in the GL yet.
func TestAssignImportedFakturaIsConflict(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h := NewBankRowsHandler(svc, as, zerolog.Nop())
		r.Post("/bank-rows/{rowID}/assign-invoice", h.HandleAssignInvoice)
		r.Post("/bank-rows/assign-invoice-multi", h.HandleAssignInvoiceMulti)
		ah := NewAccountingHandler(svc, as, nil, nil, nil, zerolog.Nop())
		r.Post("/vipps-charges/{rowID}/assign-invoice", ah.HandleAssignVippsChargeToInvoice)
	})
	club := env.club
	m := club.NewMember(t, "member")
	imported := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 30000, Imported: true,
		IssueDate: time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)})
	imp := club.NewBankImport(t)
	booked := func(table, rowID string) bool {
		var b bool
		if err := club.Pool.QueryRow(context.Background(),
			`SELECT journal_entry_id IS NOT NULL FROM `+table+` WHERE id = $1`, rowID).Scan(&b); err != nil {
			t.Fatalf("row: %v", err)
		}
		return b
	}

	row := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 30000})
	wantImportedRefusal(t, env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+row+"/assign-invoice",
		map[string]any{"invoice_id": imported.ID}))
	if booked("bank_import_rows", row) {
		t.Errorf("refused assign booked the bank row")
	}

	rows := []string{club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 10000}), club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 20000})}
	wantImportedRefusal(t, env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/assign-invoice-multi",
		map[string]any{"row_ids": rows, "invoice_id": imported.ID}))

	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "Synthetic Payer"})
	wantImportedRefusal(t, env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-charges/"+charge+"/assign-invoice",
		map[string]any{"invoice_id": imported.ID}))
	if booked("vipps_import_rows", charge) {
		t.Errorf("refused Vipps assign booked the charge")
	}
	if len(env.audit(audit.ActionBankRowAssignedInvoice)) != 0 {
		t.Errorf("a refused assign was audited")
	}
}

// A member is still shown an imported faktura they owe, but without a Vipps
// pay token while its receivable isn't in the GL, and staff get 409 for its
// pay link. A Brygge faktura of the same member is unaffected.
func TestImportedFakturaHasNoVippsPayLink(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := fin.NewClub(t, pool)
	m := club.NewMember(t, "member")
	imported := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000, Imported: true,
		IssueDate: time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)})
	native := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000})

	h := &InvoiceHandler{
		db:     pool,
		config: &config.Config{FrontendURL: "https://example.org"},
		vipps:  vipps.New("", "", "", "", "", zerolog.Nop()),
		log:    zerolog.Nop(),
	}

	rec := httptest.NewRecorder()
	h.HandleGetInvoicePayLink(rec, payLinkReq(imported.ID, club.ID))
	wantImportedRefusal(t, rec)
	var orders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_sources WHERE invoice_id = $1`, imported.ID).Scan(&orders); err != nil || orders != 0 {
		t.Errorf("refused pay link created %d orders (err %v)", orders, err)
	}

	rec = httptest.NewRecorder()
	h.HandleGetInvoicePayLink(rec, payLinkReq(native.ID, club.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("Brygge faktura pay link: %d %s", rec.Code, rec.Body.String())
	}

	token := h.ensureUserPayablesToken(ctx, club.ID, m.ID, true)
	rec = httptest.NewRecorder()
	h.HandleGetPayables(rec, payablesReq(token))
	var resp struct {
		Invoices []struct {
			ID       string `json:"id"`
			PayToken string `json:"pay_token"`
		} `json:"invoices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Invoices) != 2 {
		t.Fatalf("payables: %d %s", rec.Code, rec.Body.String())
	}
	for _, inv := range resp.Invoices {
		switch inv.ID {
		case imported.ID:
			if inv.PayToken != "" {
				t.Errorf("imported faktura offered a Vipps pay token")
			}
		case native.ID:
			if inv.PayToken == "" {
				t.Errorf("Brygge faktura lost its Vipps pay token")
			}
		}
	}

	// An order minted for the imported faktura before the guard can't be
	// charged either.
	order := club.NewOrderForInvoice(t, imported)
	blocked, err := h.orderHasVippsBlockedSource(ctx, order.ID)
	if err != nil || !blocked {
		t.Errorf("orderHasVippsBlockedSource = %v, %v; want blocked", blocked, err)
	}
}
