package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// settlementFixture is a synthetic Vipps settlement wired end-to-end: two
// customer charges (one payable against a faktura, one walk-up), a per-
// settlement fee, a payout row, and the matching bank deposit. The clearing
// account nets to zero once every piece is booked:
//
//	charges 450 + 300 = 750 (DR clearing)
//	fee                = 37.50 (CR clearing)
//	net payout         = 712.50 (CR clearing)
type settlementFixture struct {
	clubID    string
	userID    string
	periodID  string
	invoiceID string
	chargeA   string // 450, matches the faktura
	chargeB   string // 300, walk-up
	feeRow    string
	payoutRow string
	bankRowID string
}

func newSettlement(t *testing.T) (*Service, context.Context, settlementFixture) {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	var f settlementFixture
	f.clubID = testutil.SeedClub(t, pool)
	f.userID, _ = testutil.SeedUser(t, pool, f.clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())
	if _, err := svc.SeedKontoplan(ctx, f.clubID); err != nil {
		t.Fatalf("seed kontoplan: %v", err)
	}

	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		f.clubID,
	).Scan(&f.periodID); err != nil {
		t.Fatalf("insert period: %v", err)
	}

	// Open, unpaid 450 faktura for the treasurer's user (charge A pays it).
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date, total_amount,
		    status, fiscal_period_id, recipient_kind, recipient_email, sent_at)
		 VALUES ($1, $2, 700, '000007000015', '2026-06-14', 450.00,
		    'open', $3, 'private', 'm@example.com', now()) RETURNING id`,
		f.clubID, f.userID, f.periodID,
	).Scan(&f.invoiceID); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by)
		 VALUES ($1, 'settlement.csv', '548005', $2) RETURNING id`,
		f.clubID, f.userID,
	).Scan(&importID); err != nil {
		t.Fatalf("insert vipps import: %v", err)
	}

	// Charges A + B, fee, payout — all in settlement 2000167 / MSN 548005,
	// booking 2026-06-04. Fee/payout amounts are stored negative per Vipps.
	rows := []struct {
		hash, rowType, cust, msg, settlement string
		amount                               float64
		dest                                 *string
	}{
		{"a", "belastning", "Ola Testperson", "Sesong 2026", "", 450.00, &f.chargeA},
		{"b", "belastning", "Kari Walkup", "Gjest B12", "", 300.00, &f.chargeB},
		{"f", "fee", "", "", "", -37.50, &f.feeRow},
		{"p", "payout", "", "", "2000167", -712.50, &f.payoutRow},
	}
	for _, r := range rows {
		if err := pool.QueryRow(ctx,
			`INSERT INTO vipps_import_rows
			   (vipps_import_id, club_id, row_hash, row_type, booking_date, amount,
			    customer_name, message, settlement_number, msn)
			 VALUES ($1, $2, repeat($3,64), $4, '2026-06-04', $5, $6, $7, $8, '548005')
			 RETURNING id`,
			importID, f.clubID, r.hash, r.rowType, r.amount, r.cust, r.msg, r.settlement,
		).Scan(r.dest); err != nil {
			t.Fatalf("insert vipps row %s: %v", r.hash, err)
		}
	}

	var bankImportID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'bank.csv', 'sparebank-norge-v1', '1920', $2, 0) RETURNING id`,
		f.clubID, f.userID,
	).Scan(&bankImportID); err != nil {
		t.Fatalf("insert bank import: %v", err)
	}
	if _, err := svc.ImportBankRows(ctx, f.clubID, bankImportID, "", []BankRow{{
		Date:        time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		Description: "Utb. 2000167 Vippsnr 548005",
		Amount:      712.50,
		Reference:   "BANK1",
	}}); err != nil {
		t.Fatalf("import bank row: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM bank_import_rows WHERE club_id = $1 AND bank_import_id = $2 LIMIT 1`,
		f.clubID, bankImportID,
	).Scan(&f.bankRowID); err != nil {
		t.Fatalf("get bank row id: %v", err)
	}

	return svc, ctx, f
}

// entryLineSum returns the debit/credit totals a journal entry books against a
// given account code.
func entryLineSum(t *testing.T, svc *Service, ctx context.Context, entryID, accountCode string) (debit, credit float64) {
	t.Helper()
	if err := svc.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(jl.debit),0), COALESCE(SUM(jl.credit),0)
		   FROM journal_lines jl
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1 AND a.code = $2`,
		entryID, accountCode,
	).Scan(&debit, &credit); err != nil {
		t.Fatalf("sum lines for %s/%s: %v", entryID, accountCode, err)
	}
	return debit, credit
}

func TestAssignVippsChargeToInvoice(t *testing.T) {
	svc, ctx, f := newSettlement(t)

	res, err := svc.AssignVippsChargeToInvoice(ctx, f.clubID, f.chargeA, f.invoiceID, f.userID)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if res.AlreadyReconciled {
		t.Fatal("first assign should not be idempotent no-op")
	}

	// DR clearing 450 / CR 1500 450.
	if dr, cr := entryLineSum(t, svc, ctx, res.EntryID, vippsClearingAccountCode); dr != 450 || cr != 0 {
		t.Errorf("clearing line = dr %.2f cr %.2f, want dr 450 cr 0", dr, cr)
	}
	if dr, cr := entryLineSum(t, svc, ctx, res.EntryID, receivablesAccountCode); dr != 0 || cr != 450 {
		t.Errorf("receivables line = dr %.2f cr %.2f, want dr 0 cr 450", dr, cr)
	}

	// Charge row marked reconciled.
	var chargeEntry *string
	if err := svc.db.QueryRow(ctx,
		`SELECT journal_entry_id FROM vipps_import_rows WHERE id = $1`, f.chargeA,
	).Scan(&chargeEntry); err != nil {
		t.Fatalf("read charge: %v", err)
	}
	if chargeEntry == nil || *chargeEntry != res.EntryID {
		t.Errorf("charge journal_entry_id = %v, want %s", chargeEntry, res.EntryID)
	}

	// Faktura back-linked as paid.
	var paymentID *string
	if err := svc.db.QueryRow(ctx,
		`SELECT payment_id FROM invoices WHERE id = $1`, f.invoiceID,
	).Scan(&paymentID); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if paymentID == nil {
		t.Error("invoice payment_id not back-linked")
	}

	// Idempotent second call.
	again, err := svc.AssignVippsChargeToInvoice(ctx, f.clubID, f.chargeA, f.invoiceID, f.userID)
	if err != nil {
		t.Fatalf("second assign: %v", err)
	}
	if !again.AlreadyReconciled || again.EntryID != res.EntryID {
		t.Errorf("second assign = %+v, want already-reconciled with same entry", again)
	}
}

func TestAssignVippsChargeAmountMismatch(t *testing.T) {
	svc, ctx, f := newSettlement(t)

	// Charge B is 300; the faktura is 450 → assignment must refuse.
	if _, err := svc.AssignVippsChargeToInvoice(ctx, f.clubID, f.chargeB, f.invoiceID, f.userID); err == nil {
		t.Fatal("expected amount-mismatch error, got nil")
	}
	// The charge must remain unbooked.
	var entry *string
	if err := svc.db.QueryRow(ctx,
		`SELECT journal_entry_id FROM vipps_import_rows WHERE id = $1`, f.chargeB,
	).Scan(&entry); err != nil {
		t.Fatalf("read charge: %v", err)
	}
	if entry != nil {
		t.Errorf("charge should stay unbooked after refusal, got entry %s", *entry)
	}
}

func TestCreateVippsChargeBilag(t *testing.T) {
	svc, ctx, f := newSettlement(t)

	res, err := svc.CreateVippsChargeBilag(ctx, f.clubID, f.chargeB, "3200", "Gjestehavn", f.userID)
	if err != nil {
		t.Fatalf("lag bilag: %v", err)
	}
	// DR clearing 300 / CR 3200 300.
	if dr, cr := entryLineSum(t, svc, ctx, res.EntryID, vippsClearingAccountCode); dr != 300 || cr != 0 {
		t.Errorf("clearing line = dr %.2f cr %.2f, want dr 300 cr 0", dr, cr)
	}
	if dr, cr := entryLineSum(t, svc, ctx, res.EntryID, "3200"); dr != 0 || cr != 300 {
		t.Errorf("revenue line = dr %.2f cr %.2f, want dr 0 cr 300", dr, cr)
	}

	// Idempotent.
	again, err := svc.CreateVippsChargeBilag(ctx, f.clubID, f.chargeB, "3200", "Gjestehavn", f.userID)
	if err != nil {
		t.Fatalf("second lag bilag: %v", err)
	}
	if !again.AlreadyReconciled || again.EntryID != res.EntryID {
		t.Errorf("second lag bilag = %+v, want already-reconciled with same entry", again)
	}
}

func TestClearVippsPayoutGatedOnCharges(t *testing.T) {
	svc, ctx, f := newSettlement(t)

	// No charges booked yet → not ready, nothing posted.
	res, err := svc.ClearVippsPayout(ctx, f.clubID, f.bankRowID, f.userID)
	if err != nil {
		t.Fatalf("clear (0 booked): %v", err)
	}
	if res.Ready || res.UnreconciledCharges != 2 {
		t.Errorf("clear with 0 booked = ready %v unreconciled %d, want not-ready with 2", res.Ready, res.UnreconciledCharges)
	}
	var bankEntry *string
	if err := svc.db.QueryRow(ctx,
		`SELECT journal_entry_id FROM bank_import_rows WHERE id = $1`, f.bankRowID,
	).Scan(&bankEntry); err != nil {
		t.Fatalf("read bank row: %v", err)
	}
	if bankEntry != nil {
		t.Fatal("bank row must stay unlinked while charges remain")
	}

	// Book one of two charges → still not ready.
	if _, err := svc.AssignVippsChargeToInvoice(ctx, f.clubID, f.chargeA, f.invoiceID, f.userID); err != nil {
		t.Fatalf("assign A: %v", err)
	}
	res, err = svc.ClearVippsPayout(ctx, f.clubID, f.bankRowID, f.userID)
	if err != nil {
		t.Fatalf("clear (1 booked): %v", err)
	}
	if res.Ready || res.UnreconciledCharges != 1 {
		t.Errorf("clear with 1 booked = ready %v unreconciled %d, want not-ready with 1", res.Ready, res.UnreconciledCharges)
	}
}

func TestClearVippsPayoutNetsToZero(t *testing.T) {
	svc, ctx, f := newSettlement(t)

	// Book both charges.
	if _, err := svc.AssignVippsChargeToInvoice(ctx, f.clubID, f.chargeA, f.invoiceID, f.userID); err != nil {
		t.Fatalf("assign A: %v", err)
	}
	if _, err := svc.CreateVippsChargeBilag(ctx, f.clubID, f.chargeB, "3200", "", f.userID); err != nil {
		t.Fatalf("bilag B: %v", err)
	}

	res, err := svc.ClearVippsPayout(ctx, f.clubID, f.bankRowID, f.userID)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !res.Ready {
		t.Fatalf("expected ready clear, got %+v", res)
	}
	if res.FeeEntryID == "" || res.BankEntryID == "" {
		t.Fatalf("expected fee + bank entries, got %+v", res)
	}

	// Fee entry: DR 7700 37.50 / CR clearing 37.50.
	if dr, cr := entryLineSum(t, svc, ctx, res.FeeEntryID, vippsFeeAccountCode); dr != 37.50 || cr != 0 {
		t.Errorf("fee expense line = dr %.2f cr %.2f, want dr 37.50 cr 0", dr, cr)
	}
	if dr, cr := entryLineSum(t, svc, ctx, res.FeeEntryID, vippsClearingAccountCode); dr != 0 || cr != 37.50 {
		t.Errorf("fee clearing line = dr %.2f cr %.2f, want dr 0 cr 37.50", dr, cr)
	}
	// Bank entry: DR 1920 712.50 / CR clearing 712.50.
	if dr, cr := entryLineSum(t, svc, ctx, res.BankEntryID, bankAccountCode); dr != 712.50 || cr != 0 {
		t.Errorf("bank line = dr %.2f cr %.2f, want dr 712.50 cr 0", dr, cr)
	}
	if dr, cr := entryLineSum(t, svc, ctx, res.BankEntryID, vippsClearingAccountCode); dr != 0 || cr != 712.50 {
		t.Errorf("bank clearing line = dr %.2f cr %.2f, want dr 0 cr 712.50", dr, cr)
	}

	// The whole settlement's clearing nets to ~0.
	if !floatNear(res.ClearingBalance, 0, 0.01) {
		t.Errorf("settlement clearing balance = %.4f, want ~0", res.ClearingBalance)
	}

	// Bank row + payout + fee rows all marked reconciled.
	for _, id := range []string{f.payoutRow, f.feeRow} {
		var entry *string
		if err := svc.db.QueryRow(ctx,
			`SELECT journal_entry_id FROM vipps_import_rows WHERE id = $1`, id,
		).Scan(&entry); err != nil {
			t.Fatalf("read row %s: %v", id, err)
		}
		if entry == nil {
			t.Errorf("row %s should be reconciled after clear", id)
		}
	}

	// Idempotent second clear.
	again, err := svc.ClearVippsPayout(ctx, f.clubID, f.bankRowID, f.userID)
	if err != nil {
		t.Fatalf("second clear: %v", err)
	}
	if !again.AlreadyCleared || again.BankEntryID != res.BankEntryID {
		t.Errorf("second clear = %+v, want already-cleared with same bank entry", again)
	}
}

func TestDismissVippsCharge(t *testing.T) {
	svc, ctx, f := newSettlement(t)

	// Invalid reason is rejected.
	if err := svc.DismissVippsCharge(ctx, f.clubID, f.userID, f.chargeB, "made_up"); err == nil {
		t.Error("invalid dismiss reason should be rejected")
	}

	// Dismiss the walk-up charge B.
	if err := svc.DismissVippsCharge(ctx, f.clubID, f.userID, f.chargeB, "test_transaction"); err != nil {
		t.Fatalf("dismiss B: %v", err)
	}

	// B drops out of the Tildel queue; only A remains.
	items, err := svc.ListUnreconciledVippsCharges(ctx, f.clubID)
	if err != nil {
		t.Fatalf("list charges: %v", err)
	}
	if len(items) != 1 || items[0].ID != f.chargeA {
		t.Errorf("after dismiss, queue = %+v, want only charge A", items)
	}

	// A dismissed charge can't be assigned.
	if _, err := svc.AssignVippsChargeToInvoice(ctx, f.clubID, f.chargeB, f.invoiceID, f.userID); err == nil {
		t.Error("assigning a dismissed charge should fail")
	}

	// Re-dismiss is idempotent.
	if err := svc.DismissVippsCharge(ctx, f.clubID, f.userID, f.chargeB, "test_transaction"); err != nil {
		t.Errorf("re-dismiss should be a no-op, got %v", err)
	}

	// The dismissed charge no longer blocks the payout gate.
	res, err := svc.ClearVippsPayout(ctx, f.clubID, f.bankRowID, f.userID)
	if err != nil {
		t.Fatalf("clear (A unbooked, B dismissed): %v", err)
	}
	if res.Ready || res.UnreconciledCharges != 1 {
		t.Errorf("gate should count only charge A: ready %v unreconciled %d", res.Ready, res.UnreconciledCharges)
	}

	// Book A, then the payout clears despite B being dismissed. Clearing does
	// NOT net to zero — B's money is in the payout but was never booked — which
	// is the documented dismiss caveat.
	if _, err := svc.AssignVippsChargeToInvoice(ctx, f.clubID, f.chargeA, f.invoiceID, f.userID); err != nil {
		t.Fatalf("assign A: %v", err)
	}
	res, err = svc.ClearVippsPayout(ctx, f.clubID, f.bankRowID, f.userID)
	if err != nil {
		t.Fatalf("clear after booking A: %v", err)
	}
	if !res.Ready {
		t.Fatalf("expected ready clear, got %+v", res)
	}
	if floatNear(res.ClearingBalance, 0, 0.01) {
		t.Errorf("clearing should be off by the dismissed 300, got %.2f", res.ClearingBalance)
	}
}

func TestVippsPostingRefusesClosedPeriod(t *testing.T) {
	svc, ctx, f := newSettlement(t)

	if _, err := svc.db.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'closed', closed_at = now(), closed_by = $2 WHERE id = $1`,
		f.periodID, f.userID,
	); err != nil {
		t.Fatalf("close period: %v", err)
	}

	if _, err := svc.AssignVippsChargeToInvoice(ctx, f.clubID, f.chargeA, f.invoiceID, f.userID); err == nil {
		t.Error("assign into closed period should fail")
	}
	if _, err := svc.CreateVippsChargeBilag(ctx, f.clubID, f.chargeB, "3200", "", f.userID); err == nil {
		t.Error("lag bilag into closed period should fail")
	}
	// Charges stay unbooked.
	var n int
	if err := svc.db.QueryRow(ctx,
		`SELECT count(*) FROM vipps_import_rows
		  WHERE club_id = $1 AND row_type = 'belastning' AND journal_entry_id IS NOT NULL`,
		f.clubID,
	).Scan(&n); err != nil {
		t.Fatalf("count booked: %v", err)
	}
	if n != 0 {
		t.Errorf("no charges should be booked into a closed period, got %d", n)
	}
}
