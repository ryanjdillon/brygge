package accounting

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// rebookVippsChargeTx replaces a charge's booking by another with the same
// clearing debit, leaving the settlement's payout clearing untouched.

func (f *payoutSweepFixture) rebook(rowID string, cents int64, account, invoiceID string) (*vippsRebook, error) {
	ctx := context.Background()
	tx, err := f.svc.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rb, err := rebookVippsChargeTx(ctx, tx, f.club.ID, f.club.Treasurer.ID, rowID,
		[]CreateJournalLineInput{{AccountCode: account, Credit: fin.Kr(cents), Description: "Synthetic rebooking"}}, invoiceID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return rb, nil
}

func (f *payoutSweepFixture) entryAccounts(t *testing.T, entryID string) map[string]float64 {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT a.code, jl.debit - jl.credit FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1`, entryID)
	if err != nil {
		t.Fatalf("entry lines: %v", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var code string
		var net float64
		if err := rows.Scan(&code, &net); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[code] += net
	}
	return out
}

func TestRebookChargeInClearedSettlementLeavesTheClearingUntouched(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3500101", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, s.charges...)
	f.clear(t, s)
	feeEntry, bankEntry := f.feeEntry(t, s), *f.bankEntry(t, s.bankRow)
	oldEntry := *f.chargeEntry(t, s.charges[0])
	net := func(code string) float64 { return clubNet(t, f.svc, f.club, code) }
	clearingBefore, revenueBefore := net(vippsClearingAccountCode), net("3200")
	entriesBefore := f.entryCount(t)

	rb, err := f.rebook(s.charges[0], 40000, "3300", "")
	if err != nil {
		t.Fatalf("rebook: %v", err)
	}
	if rb.PriorEntryID != oldEntry || rb.EntryID == oldEntry {
		t.Errorf("rebook = %+v, want a new entry replacing %s", rb, oldEntry)
	}
	if got := f.entryStatusOf(t, oldEntry); got != "voided" {
		t.Errorf("old entry is %s, want voided", got)
	}
	if got := f.entryStatusOf(t, rb.EntryID); got != "posted" {
		t.Errorf("replacement is %s, want posted", got)
	}
	if cur := f.chargeEntry(t, s.charges[0]); cur == nil || *cur != rb.EntryID {
		t.Errorf("charge is linked to %v, want the replacement %s", cur, rb.EntryID)
	}
	if got := f.entryAccounts(t, rb.EntryID); !floatNear(got[vippsClearingAccountCode], 400, 0.005) || !floatNear(got["3300"], -400, 0.005) {
		t.Errorf("replacement lines = %v, want DR 2900 400 / CR 3300 400", got)
	}

	// The settlement's clearing is the same entries, still posted and linked.
	if f.feeEntry(t, s) != feeEntry || *f.bankEntry(t, s.bankRow) != bankEntry {
		t.Errorf("the settlement's fee or bank link changed")
	}
	for _, id := range []string{feeEntry, bankEntry} {
		if got := f.entryStatusOf(t, id); got != "posted" {
			t.Errorf("clearing entry %s is %s, want posted", id, got)
		}
	}
	// Two entries were added: the reversal and the replacement.
	if got := f.entryCount(t) - entriesBefore; got != 2 {
		t.Errorf("rebook added %d entries, want the reversal and the replacement", got)
	}
	if got := net(vippsClearingAccountCode); !floatNear(got, clearingBefore, 0.005) {
		t.Errorf("2900 moved from %.2f to %.2f", clearingBefore, got)
	}
	if got := net("3200"); !floatNear(got, revenueBefore+400, 0.005) {
		t.Errorf("3200 = %.2f, want %.2f (the charge left it)", got, revenueBefore+400)
	}
	if got := net("3300"); !floatNear(got, -400, 0.005) {
		t.Errorf("3300 = %.2f, want -400 (the charge arrived)", got)
	}
	bal, err := f.svc.settlementClearingBalance(ctx, f.club.ID, s.number, sweepMSN, s.booking, s.bankRow)
	if err != nil || !floatNear(bal, 0, 0.005) {
		t.Errorf("settlement clearing balance = %.2f (err %v), want 0", bal, err)
	}
}

func TestRebookRefusesADifferentClearingDebit(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3500201", 2, []int64{40000}, 2000, true)
	f.book(t, s.charges...)
	f.clear(t, s)
	oldEntry := *f.chargeEntry(t, s.charges[0])
	entries := f.entryCount(t)

	// Replacement lines that do not sum to the charge.
	if _, err := f.rebook(s.charges[0], 40100, "3300", ""); !errors.Is(err, ErrRebookClearingDebit) {
		t.Errorf("rebook with unequal credits: err = %v, want ErrRebookClearingDebit", err)
	}
	// An old entry that does not debit the clearing account by the charge.
	for _, stmt := range []string{
		`UPDATE journal_lines SET debit = debit + 1 WHERE journal_entry_id = $1 AND debit > 0`,
		`UPDATE journal_lines SET credit = credit + 1 WHERE journal_entry_id = $1 AND credit > 0`,
	} {
		if _, err := f.club.Pool.Exec(ctx, stmt, oldEntry); err != nil {
			t.Fatalf("tamper: %v", err)
		}
	}
	if _, err := f.rebook(s.charges[0], 40000, "3300", ""); !errors.Is(err, ErrRebookClearingDebit) {
		t.Errorf("rebook of an entry with another clearing debit: err = %v, want ErrRebookClearingDebit", err)
	}

	if cur := f.chargeEntry(t, s.charges[0]); cur == nil || *cur != oldEntry || f.entryStatusOf(t, oldEntry) != "posted" || f.entryCount(t) != entries {
		t.Errorf("a refused rebook changed the charge or the ledger")
	}
}

func TestRebookRefusesLinesOnTheClearingAccount(t *testing.T) {
	f := newPayoutSweepFixture(t)
	s := f.settlement(t, "3500251", 2, []int64{40000}, 2000, true)
	f.book(t, s.charges...)
	oldEntry := *f.chargeEntry(t, s.charges[0])
	if _, err := f.rebook(s.charges[0], 40000, vippsClearingAccountCode, ""); !errors.Is(err, ErrRebookClearingDebit) {
		t.Errorf("rebook onto the clearing account: err = %v, want ErrRebookClearingDebit", err)
	}
	if cur := f.chargeEntry(t, s.charges[0]); cur == nil || *cur != oldEntry {
		t.Errorf("a refused rebook changed the charge")
	}
}

func TestRebookMovesAChargeBetweenAFakturaAndAnAccount(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	m := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 40000, Lines: []fin.Line{{Category: "membership", Cents: 40000}}})
	s := f.settlement(t, "3500301", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, s.charges...)
	f.clear(t, s)
	paid := func() bool {
		t.Helper()
		var p *string
		if err := f.club.Pool.QueryRow(ctx, `SELECT payment_id::text FROM invoices WHERE id = $1`, inv.ID).Scan(&p); err != nil {
			t.Fatalf("read invoice: %v", err)
		}
		return p != nil
	}
	clearingBefore := clubNet(t, f.svc, f.club, vippsClearingAccountCode)

	rb, err := f.rebook(s.charges[0], 40000, receivablesAccountCode, inv.ID)
	if err != nil {
		t.Fatalf("rebook to the faktura: %v", err)
	}
	if err := f.svc.linkInvoicePayment(ctx, f.club.ID, rb.InvoiceID, rb.Date); err != nil {
		t.Fatalf("link payment: %v", err)
	}
	var recorded *string
	if err := f.club.Pool.QueryRow(ctx, `SELECT invoice_id::text FROM vipps_import_rows WHERE id = $1`, s.charges[0]).Scan(&recorded); err != nil ||
		recorded == nil || *recorded != inv.ID || !paid() {
		t.Fatalf("after rebooking to the faktura: recorded %v (err %v), paid %v", recorded, err, paid())
	}

	rb, err = f.rebook(s.charges[0], 40000, "3200", "")
	if err != nil {
		t.Fatalf("rebook back to an account: %v", err)
	}
	if rb.PriorInvoiceID != inv.ID || paid() {
		t.Errorf("rebooking away from the faktura: prior %q, paid %v, want it reopened", rb.PriorInvoiceID, paid())
	}
	if got := clubNet(t, f.svc, f.club, vippsClearingAccountCode); !floatNear(got, clearingBefore, 0.005) {
		t.Errorf("2900 moved from %.2f to %.2f", clearingBefore, got)
	}
}

// A clearing racing a rebook of one of its charges ends consistent: the
// charge stays posted throughout, the deposit is booked and the settlement
// nets to zero, whichever commits first.
func TestClearVersusRebookLeavesAConsistentSettlement(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()

	for i := range 8 {
		booking := time.Date(f.club.Year, 8, 1+i, 0, 0, 0, 0, time.UTC)
		s := f.settlementOn(t, "35004"+string(rune('0'+i)), booking, []int64{30000, 20000}, 1000, true)
		f.book(t, s.charges...)

		start := make(chan struct{})
		var wg sync.WaitGroup
		var clearErr, rebookErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, clearErr = f.svc.ClearVippsPayout(ctx, f.club.ID, s.bankRow, f.club.Treasurer.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, rebookErr = f.rebook(s.charges[0], 30000, "3300", "")
		}()
		close(start)
		wg.Wait()

		if clearErr != nil || rebookErr != nil {
			t.Fatalf("round %d: clear err = %v, rebook err = %v", i, clearErr, rebookErr)
		}
		cur := f.chargeEntry(t, s.charges[0])
		if cur == nil || f.entryStatusOf(t, *cur) != "posted" || f.entryAccounts(t, *cur)["3300"] != -300 {
			t.Errorf("round %d: charge not on a posted 3300 entry after the rebook", i)
		}
		if f.bankEntry(t, s.bankRow) == nil {
			t.Errorf("round %d: the settlement was not cleared", i)
		}
		bal, err := f.svc.settlementClearingBalance(ctx, f.club.ID, s.number, sweepMSN, s.booking, s.bankRow)
		if err != nil || !floatNear(bal, 0, 0.005) {
			t.Errorf("round %d: clearing balance = %.2f (err %v), want 0", i, bal, err)
		}
	}
}
