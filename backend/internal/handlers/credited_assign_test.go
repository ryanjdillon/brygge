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
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// wantCreditedRefusal checks a 409 carrying the credited-faktura code (BRY-508).
func wantCreditedRefusal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantRefusalCode(t, rec, invoiceCreditedCode)
}

func wantRefusalCode(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	var body map[string]string
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["code"] != code {
		t.Fatalf("got %d %s, want 409 with code %s", rec.Code, rec.Body.String(), code)
	}
}

// Tildel and the Stripe card list answer 409 invoice_credited, booking
// nothing, when a bank row, several bank rows, a Vipps charge or a Stripe
// charge is assigned to a faktura a credit note credits in full, and
// invoice_not_debt when it is assigned to a credit note.
func TestAssignCreditedFakturaIsConflict(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h := NewBankRowsHandler(svc, as, zerolog.Nop())
		r.Post("/bank-rows/{rowID}/assign-invoice", h.HandleAssignInvoice)
		r.Post("/bank-rows/assign-invoice-multi", h.HandleAssignInvoiceMulti)
		ah := NewAccountingHandler(svc, as, nil, nil, nil, zerolog.Nop())
		r.Post("/vipps-charges/{rowID}/assign-invoice", ah.HandleAssignVippsChargeToInvoice)
		r.Post("/stripe/transactions/{txnID}/assign-invoice", ah.HandleAssignStripeTxnToInvoice)
	})
	club := env.club
	ctx := context.Background()
	m := club.NewMember(t, "member")
	credited := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 30000})
	cn := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{{Category: "slip_fee", Cents: -30000}}})
	if _, err := club.Pool.Exec(ctx, `UPDATE invoices SET credits_invoice_id = $1 WHERE id = $2`, credited.ID, cn.ID); err != nil {
		t.Fatalf("link credit note: %v", err)
	}
	imp := club.NewBankImport(t)
	booked := func(table, rowID string) bool {
		var b bool
		if err := club.Pool.QueryRow(ctx,
			`SELECT journal_entry_id IS NOT NULL FROM `+table+` WHERE id = $1`, rowID).Scan(&b); err != nil {
			t.Fatalf("row: %v", err)
		}
		return b
	}

	row := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 30000})
	wantCreditedRefusal(t, env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+row+"/assign-invoice",
		map[string]any{"invoice_id": credited.ID}))
	if booked("bank_import_rows", row) {
		t.Errorf("refused assign booked the bank row")
	}

	rows := []string{club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 10000}), club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 20000})}
	wantCreditedRefusal(t, env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/assign-invoice-multi",
		map[string]any{"row_ids": rows, "invoice_id": credited.ID}))
	if booked("bank_import_rows", rows[0]) || booked("bank_import_rows", rows[1]) {
		t.Errorf("refused multi assign booked a row")
	}

	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "Synthetic Payer"})
	wantCreditedRefusal(t, env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-charges/"+charge+"/assign-invoice",
		map[string]any{"invoice_id": credited.ID}))
	if booked("vipps_import_rows", charge) {
		t.Errorf("refused Vipps assign booked the charge")
	}

	var txn string
	if err := club.Pool.QueryRow(ctx,
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, stripe_type, amount_ore, fee_ore, net_ore,
		      currency, created_at, status, review_reason)
		 VALUES ($1, 'txn_test_handler_cr', 'charge', 'charge', 30000, 0, 30000, 'nok', $2, $3, $4) RETURNING id`,
		club.ID, time.Date(club.Year, 6, 15, 10, 0, 0, 0, time.UTC), accounting.StripeTxnNeedsReview, accounting.StripeReviewNoAttempt,
	).Scan(&txn); err != nil {
		t.Fatalf("stripe row: %v", err)
	}
	wantCreditedRefusal(t, env.serve([]string{"treasurer"}, http.MethodPost, "/stripe/transactions/"+txn+"/assign-invoice",
		map[string]any{"invoice_id": credited.ID}))
	if booked("stripe_balance_transactions", txn) {
		t.Errorf("refused Stripe assign booked the charge")
	}

	creditNote := cn.ID
	row = club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 30000})
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+row+"/assign-invoice",
		map[string]any{"invoice_id": creditNote}), invoiceNotDebtCode)
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-charges/"+charge+"/assign-invoice",
		map[string]any{"invoice_id": creditNote}), invoiceNotDebtCode)
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/stripe/transactions/"+txn+"/assign-invoice",
		map[string]any{"invoice_id": creditNote}), invoiceNotDebtCode)
	if booked("bank_import_rows", row) || booked("vipps_import_rows", charge) || booked("stripe_balance_transactions", txn) {
		t.Errorf("a refused credit-note assign booked something")
	}

	if len(env.audit(audit.ActionBankRowAssignedInvoice)) != 0 || len(env.audit(audit.ActionStripeTxnAssignedInvoice)) != 0 {
		t.Errorf("a refused assign was audited")
	}
}

// A voided faktura is refused as 409 invoice_voided, booking nothing, by the
// bank-row assigns, the Vipps charge assign and the legacy Vipps reconcile
// confirm.
func TestAssignVoidedFakturaIsConflict(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h := NewBankRowsHandler(svc, as, zerolog.Nop())
		r.Post("/bank-rows/{rowID}/assign-invoice", h.HandleAssignInvoice)
		r.Post("/bank-rows/assign-invoice-multi", h.HandleAssignInvoiceMulti)
		ah := NewAccountingHandler(svc, as, nil, nil, nil, zerolog.Nop())
		r.Post("/vipps-charges/{rowID}/assign-invoice", ah.HandleAssignVippsChargeToInvoice)
		r.Post("/bank-rows/{rowID}/reconcile-vipps/confirm", ah.HandleVippsReconcileConfirm)
	})
	club := env.club
	ctx := context.Background()
	m := club.NewMember(t, "member")
	voided := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 30000, Status: "voided"})
	imp := club.NewBankImport(t)
	booked := func(table, rowID string) bool {
		var b bool
		if err := club.Pool.QueryRow(ctx,
			`SELECT journal_entry_id IS NOT NULL FROM `+table+` WHERE id = $1`, rowID).Scan(&b); err != nil {
			t.Fatalf("row: %v", err)
		}
		return b
	}

	row := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 30000})
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+row+"/assign-invoice",
		map[string]any{"invoice_id": voided.ID}), invoiceVoidedCode)

	rows := []string{club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 10000}), club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 20000})}
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/assign-invoice-multi",
		map[string]any{"row_ids": rows, "invoice_id": voided.ID}), invoiceVoidedCode)

	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "Synthetic Payer"})
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-charges/"+charge+"/assign-invoice",
		map[string]any{"invoice_id": voided.ID}), invoiceVoidedCode)

	confirmRow := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 30000})
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+confirmRow+"/reconcile-vipps/confirm",
		map[string]any{"lines": []accounting.VippsReconcileLine{
			{AccountCode: "1920", Debit: 300},
			{AccountCode: "1500", Credit: 300, LinkedInvoiceID: voided.ID, VippsRowID: club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000})},
		}}), invoiceVoidedCode)

	if booked("bank_import_rows", row) || booked("bank_import_rows", rows[0]) || booked("bank_import_rows", rows[1]) ||
		booked("bank_import_rows", confirmRow) || booked("vipps_import_rows", charge) {
		t.Errorf("a refused assign of a voided faktura booked something")
	}

	// A confirm that links one open faktura from two lines is refused.
	open := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 30000})
	dupRow := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 60000})
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+dupRow+"/reconcile-vipps/confirm",
		map[string]any{"lines": []accounting.VippsReconcileLine{
			{AccountCode: "1920", Debit: 600},
			{AccountCode: "1500", Credit: 300, LinkedInvoiceID: open.ID, VippsRowID: charge},
			{AccountCode: "1500", Credit: 300, LinkedInvoiceID: open.ID, VippsRowID: club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000})},
		}}), settlementOverpaidCode)
	if booked("bank_import_rows", dupRow) {
		t.Errorf("a refused confirm booked the bank row")
	}
}
