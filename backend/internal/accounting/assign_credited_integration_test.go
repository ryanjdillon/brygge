package accounting

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// creditedFixture is a club with 450 kr fakturas in every state a manual
// assignment can meet (BRY-508): open, credited by a linked credit note,
// credited in full by card-refund credit notes, only partly refunded,
// captured by a Vipps pay link, voided, and imported without an opening
// balance; plus a stand-alone credit note.
type creditedFixture struct {
	*stripeFixture
	open, linked, refunded, partial, vippsCaptured, voided, imported, creditNoteTarget fin.Invoice
}

func newCreditedFixture(t *testing.T) *creditedFixture {
	t.Helper()
	f := &creditedFixture{stripeFixture: newStripeFixture(t)}
	c := f.club
	f.open = c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.linked = c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.refunded = c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.partial = c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.vippsCaptured = c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, VippsPaid: true})
	f.voided = c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Status: "voided"})
	f.imported = c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Imported: true})
	f.creditNoteTarget = f.creditNote(t, f.open, 45000)

	// A credit note linked by hand credits the whole faktura.
	cn := f.creditNote(t, f.linked, 45000)
	f.exec(t, `UPDATE invoices SET credits_invoice_id = $1 WHERE id = $2`, f.linked.ID, cn.ID)

	// Card refunds record their credit notes in refund_documents: the full
	// 450 credits the faktura, 200 of it does not.
	f.refundCredit(t, "txn_test_cr_full", f.refunded, 45000)
	f.refundCredit(t, "txn_test_cr_part", f.partial, 20000)
	return f
}

func (f *creditedFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// creditNote seeds a credit note of cents for inv's member.
func (f *creditedFixture) creditNote(t *testing.T, inv fin.Invoice, cents int64) fin.Invoice {
	t.Helper()
	m := fin.Member{ID: inv.MemberID}
	return f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{{Category: "slip_fee", Cents: -cents}}})
}

// refundCredit records a refund of cents on inv with its credit note, as the
// refund booking does (kind 'credit').
func (f *creditedFixture) refundCredit(t *testing.T, btID string, inv fin.Invoice, cents int64) {
	t.Helper()
	refundTxn := f.insertBT(t, stripeBT{id: btID, category: "refund", amountOre: -cents, status: StripeTxnBooked})
	cn := f.creditNote(t, inv, cents)
	f.exec(t,
		`INSERT INTO refund_documents (club_id, refund_txn_id, invoice_id, credit_invoice_id, amount_ore, kind)
		 VALUES ($1, $2, $3, $4, $5, 'credit')`,
		f.club.ID, refundTxn, inv.ID, cn.ID, cents)
}

func (f *creditedFixture) journalEntries(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *creditedFixture) rowBooked(t *testing.T, table, id string) bool {
	t.Helper()
	var booked bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id IS NOT NULL FROM `+table+` WHERE id = $1`, id).Scan(&booked); err != nil {
		t.Fatal(err)
	}
	return booked
}

func suggestionIDs(list []InvoiceSuggestion) []string {
	ids := make([]string, 0, len(list))
	for _, s := range list {
		ids = append(ids, s.InvoiceID)
	}
	sort.Strings(ids)
	return ids
}

func wantIDs(t *testing.T, what string, got []InvoiceSuggestion, want ...fin.Invoice) {
	t.Helper()
	ids := make([]string, 0, len(want))
	for _, w := range want {
		ids = append(ids, w.ID)
	}
	sort.Strings(ids)
	g := suggestionIDs(got)
	if len(g) != len(ids) {
		t.Errorf("%s: %d fakturas offered, want %d", what, len(g), len(ids))
		return
	}
	for i := range g {
		if g[i] != ids[i] {
			t.Errorf("%s: offered %v, want %v", what, g, ids)
			return
		}
	}
}

// A manual assignment is not offered a faktura it would refuse as already
// cleared: credited (by a linked credit note or by refund credit notes
// adding up to its total), voided, a credit note, or already covered by a
// booked row. A partly refunded faktura is not credited, and one captured by
// a Vipps pay link stays offered, because the charge being assigned is
// usually the payment that set online_paid_at. An imported faktura without
// an opening balance stays offered on purpose: the assign refuses it with a
// message that tells the treasurer to wait, and hiding it would make the
// deposit look orphaned.
func TestAssignmentSuggestionsExcludeUnassignableFakturas(t *testing.T) {
	f := newCreditedFixture(t)
	ctx := context.Background()
	c := f.club

	// A faktura a booked row is recorded against, its payment back-link not
	// landed yet (the window invoiceBookedRowSQL exists for).
	booked := c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	imp := c.NewBankImport(t)
	if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000}), booked.ID); err != nil {
		t.Fatalf("book the control faktura: %v", err)
	}
	f.exec(t, `UPDATE invoices SET payment_id = NULL WHERE id = $1`, booked.ID)

	bankRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
	charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
	txn := f.reviewRow(t, "txn_test_cr_sugg", "charge", StripeReviewNoAttempt, 45000, 0, "")
	want := []fin.Invoice{f.open, f.partial, f.vippsCaptured, f.imported}

	got, err := f.svc.PotentialInvoicesForRow(ctx, c.ID, bankRow, "", 0, 0)
	if err != nil {
		t.Fatalf("potential for row: %v", err)
	}
	wantIDs(t, "bank row search", got, want...)

	got, err = f.svc.PotentialInvoicesForVippsCharge(ctx, c.ID, charge, "", 0)
	if err != nil {
		t.Fatalf("potential for charge: %v", err)
	}
	wantIDs(t, "Vipps charge search", got, want...)

	got, err = f.svc.PotentialInvoicesForStripeTxn(ctx, c.ID, txn, "", 0)
	if err != nil {
		t.Fatalf("potential for stripe: %v", err)
	}
	wantIDs(t, "Stripe charge search", got, want...)

	ranked, err := f.svc.BankRowSuggestionsFor(ctx, c.ID, bankRow)
	if err != nil {
		t.Fatalf("suggestions for row: %v", err)
	}
	wantIDs(t, "bank row ranked suggestions", ranked.Invoices, want...)

	rankedCharge, err := f.svc.VippsChargeSuggestionsFor(ctx, c.ID, charge)
	if err != nil {
		t.Fatalf("suggestions for charge: %v", err)
	}
	wantIDs(t, "Vipps charge ranked suggestions", rankedCharge.Invoices, want...)

	// The queue's "worth opening?" counts agree with the lists.
	rows, err := f.svc.ListUnmatchedBankRows(ctx, c.ID, "", "", 0, 50, 0)
	if err != nil {
		t.Fatalf("list unmatched: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == bankRow {
			found = true
			if r.SuggestionCount != len(want) {
				t.Errorf("bank row suggestion_count = %d, want %d", r.SuggestionCount, len(want))
			}
		}
	}
	if !found {
		t.Fatalf("bank row %s missing from the queue", bankRow)
	}
	items, err := f.svc.ListUnreconciledVippsCharges(ctx, c.ID)
	if err != nil {
		t.Fatalf("list vipps charges: %v", err)
	}
	found = false
	for _, it := range items {
		if it.ID == charge {
			found = true
			if it.SuggestionCount != len(want) {
				t.Errorf("Vipps charge suggestion_count = %d, want %d", it.SuggestionCount, len(want))
			}
		}
	}
	if !found {
		t.Fatalf("Vipps charge %s missing from the queue", charge)
	}
}

// Assigning a bank row, bank rows together, a Vipps charge or a Stripe charge
// to a credited faktura is refused under the faktura lock with nothing
// booked: 1500 was already credited by the credit note. So is assigning to a
// credit note itself, and to an imported faktura without an opening balance.
// An ordinary open faktura, a partly refunded one and one captured by a Vipps
// pay link still take the payment.
func TestManualAssignRefusesCreditedFaktura(t *testing.T) {
	f := newCreditedFixture(t)
	ctx := context.Background()
	c := f.club
	imp := c.NewBankImport(t)
	before := f.journalEntries(t)

	for name, inv := range map[string]fin.Invoice{"linked credit note": f.linked, "refund credit notes": f.refunded} {
		row := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
		if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, row, inv.ID); !errors.Is(err, ErrInvoiceCredited) {
			t.Errorf("%s: bank assign err = %v, want ErrInvoiceCredited", name, err)
		}
		if f.rowBooked(t, "bank_import_rows", row) {
			t.Errorf("%s: refused bank assign booked the row", name)
		}

		part1 := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 20000})
		part2 := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 25000})
		if _, err := f.svc.AssignMultipleBankRowsToInvoice(ctx, c.ID, c.Treasurer.ID, []string{part1, part2}, inv.ID); !errors.Is(err, ErrInvoiceCredited) {
			t.Errorf("%s: multi bank assign err = %v, want ErrInvoiceCredited", name, err)
		}
		if f.rowBooked(t, "bank_import_rows", part1) || f.rowBooked(t, "bank_import_rows", part2) {
			t.Errorf("%s: refused multi assign booked a row", name)
		}

		charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
		if _, err := f.svc.AssignVippsChargeToInvoice(ctx, c.ID, charge, inv.ID, c.Treasurer.ID); !errors.Is(err, ErrInvoiceCredited) {
			t.Errorf("%s: Vipps assign err = %v, want ErrInvoiceCredited", name, err)
		}
		if f.rowBooked(t, "vipps_import_rows", charge) {
			t.Errorf("%s: refused Vipps assign booked the charge", name)
		}

		txn := f.reviewRow(t, "txn_test_cr_"+inv.ID[:8], "charge", StripeReviewNoAttempt, 45000, 0, "")
		_, err := f.svc.AssignStripeTxnToInvoice(ctx, c.ID, c.Treasurer.ID, txn, inv.ID)
		wantRefusal(t, err, StripeTxnInvoiceCredited)
		if row := f.bt(t, txn); row.status != StripeTxnNeedsReview || row.entry != "" {
			t.Errorf("%s: refused Stripe assign left the row %+v", name, row)
		}
	}
	if n := f.journalEntries(t); n != before {
		t.Errorf("%d journal entries after the refusals, want %d", n, before)
	}

	// A credit note is not a debt: no path may aim a payment at it.
	cnRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, cnRow, f.creditNoteTarget.ID); !errors.Is(err, ErrInvoiceNotDebt) {
		t.Errorf("credit note: bank assign err = %v, want ErrInvoiceNotDebt", err)
	}
	cnPart1 := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 20000})
	cnPart2 := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 25000})
	if _, err := f.svc.AssignMultipleBankRowsToInvoice(ctx, c.ID, c.Treasurer.ID, []string{cnPart1, cnPart2}, f.creditNoteTarget.ID); !errors.Is(err, ErrInvoiceNotDebt) {
		t.Errorf("credit note: multi bank assign err = %v, want ErrInvoiceNotDebt", err)
	}
	cnCharge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
	if _, err := f.svc.AssignVippsChargeToInvoice(ctx, c.ID, cnCharge, f.creditNoteTarget.ID, c.Treasurer.ID); !errors.Is(err, ErrInvoiceNotDebt) {
		t.Errorf("credit note: Vipps assign err = %v, want ErrInvoiceNotDebt", err)
	}
	cnTxn := f.reviewRow(t, "txn_test_cr_creditnote", "charge", StripeReviewNoAttempt, 45000, 0, "")
	_, err := f.svc.AssignStripeTxnToInvoice(ctx, c.ID, c.Treasurer.ID, cnTxn, f.creditNoteTarget.ID)
	wantRefusal(t, err, StripeTxnInvoiceNotDebt)
	for _, id := range []string{cnRow, cnPart1, cnPart2} {
		if f.rowBooked(t, "bank_import_rows", id) {
			t.Errorf("refused credit-note assign booked bank row %s", id)
		}
	}
	if f.rowBooked(t, "vipps_import_rows", cnCharge) {
		t.Errorf("refused credit-note assign booked the Vipps charge")
	}

	// An imported faktura without an opening balance is offered but refused.
	impRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, impRow, f.imported.ID); !errors.Is(err, ErrImportedNoOpeningBalance) {
		t.Errorf("imported: bank assign err = %v, want ErrImportedNoOpeningBalance", err)
	}

	// A voided faktura keeps its own refusals.
	voidedRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, voidedRow, f.voided.ID); !errors.Is(err, ErrInvoiceVoided) {
		t.Errorf("voided: bank assign err = %v, want ErrInvoiceVoided", err)
	}
	voidedTxn := f.reviewRow(t, "txn_test_cr_voided", "charge", StripeReviewNoAttempt, 45000, 0, "")
	_, err = f.svc.AssignStripeTxnToInvoice(ctx, c.ID, c.Treasurer.ID, voidedTxn, f.voided.ID)
	wantRefusal(t, err, StripeTxnInvoiceSettled)
	if n := f.journalEntries(t); n != before {
		t.Errorf("%d journal entries after the voided refusals, want %d", n, before)
	}

	// Nothing legitimate regresses.
	if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000}), f.open.ID); err != nil {
		t.Errorf("open faktura: bank assign: %v", err)
	}
	if _, err := f.svc.AssignVippsChargeToInvoice(ctx, c.ID,
		c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"}), f.vippsCaptured.ID, c.Treasurer.ID); err != nil {
		t.Errorf("Vipps-captured faktura: Vipps assign: %v", err)
	}
	if _, err := f.svc.AssignStripeTxnToInvoice(ctx, c.ID, c.Treasurer.ID,
		f.reviewRow(t, "txn_test_cr_part_ok", "charge", StripeReviewNoAttempt, 45000, 0, ""), f.partial.ID); err != nil {
		t.Errorf("partly refunded faktura: Stripe assign: %v", err)
	}
}

// The legacy Vipps settlement reconcile links fakturas from an editable
// preview, so it re-checks them too.
func TestReconcileVippsConfirmRefusesCreditedFaktura(t *testing.T) {
	f := newCreditedFixture(t)
	ctx := context.Background()
	c := f.club
	row := c.NewBankRow(t, c.NewBankImport(t), fin.BankRowOpts{Cents: 45000})
	before := f.journalEntries(t)

	charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
	lines := []VippsReconcileLine{
		{AccountCode: "1920", Debit: 450},
		{AccountCode: receivablesAccountCode, Credit: 450, LinkedInvoiceID: f.linked.ID, VippsRowID: charge},
	}
	if _, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, row, "", c.Treasurer.ID, lines); !errors.Is(err, ErrInvoiceCredited) {
		t.Fatalf("confirm err = %v, want ErrInvoiceCredited", err)
	}
	if f.rowBooked(t, "bank_import_rows", row) || f.journalEntries(t) != before {
		t.Errorf("refused confirm booked something")
	}
}
