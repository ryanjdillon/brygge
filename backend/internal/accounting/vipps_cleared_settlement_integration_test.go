package accounting

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// BRY-555: a posted charge booking cannot be taken back once its settlement's
// payout is cleared against the bank deposit, or 2900 is left with the fee and
// payout credits and no charge debit against them.

func (f *payoutSweepFixture) clearingNet(t *testing.T) float64 {
	t.Helper()
	var net float64
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(jl.debit) - sum(jl.credit), 0)
		   FROM journal_lines jl JOIN journal_entries je ON je.id = jl.journal_entry_id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1 AND a.code = $2 AND je.status IN ('posted', 'voided')`,
		f.club.ID, vippsClearingAccountCode).Scan(&net); err != nil {
		t.Fatalf("clearing net: %v", err)
	}
	return net
}

func (f *payoutSweepFixture) accountNet(t *testing.T, code string) float64 {
	t.Helper()
	var net float64
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(jl.debit) - sum(jl.credit), 0)
		   FROM journal_lines jl JOIN journal_entries je ON je.id = jl.journal_entry_id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1 AND a.code = $2 AND je.status IN ('posted', 'voided')`,
		f.club.ID, code).Scan(&net); err != nil {
		t.Fatalf("net %s: %v", code, err)
	}
	return net
}

func (f *payoutSweepFixture) chargeEntry(t *testing.T, rowID string) *string {
	t.Helper()
	var id *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM vipps_import_rows WHERE id = $1`, rowID).Scan(&id); err != nil {
		t.Fatalf("read charge: %v", err)
	}
	return id
}

func (f *payoutSweepFixture) entryCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return n
}

func (f *payoutSweepFixture) clear(t *testing.T, s sweepSettlement) *VippsPayoutClearResult {
	t.Helper()
	res, err := f.svc.ClearVippsPayout(context.Background(), f.club.ID, s.bankRow, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("clear %s: %v", s.number, err)
	}
	if !res.Ready || res.AlreadyCleared {
		t.Fatalf("clear %s = %+v, want a fresh clearing", s.number, res)
	}
	return res
}

func (f *payoutSweepFixture) unassign(rowID string) error {
	_, _, err := f.svc.UnassignVippsCharge(context.Background(), f.club.ID, f.club.Treasurer.ID, rowID)
	return err
}

// Replays BRY-555: the settlement is cleared, then Angre on one of its
// charges. The refusal names the settlement, changes nothing, and undoing the
// deposit first (what the message says) makes the charge undoable and the
// settlement clearable again with 2900 netting to zero.
func TestUnassignChargeInClearedSettlementIsRefused(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3100101", 2, []int64{120000, 80000}, 5000, true)
	f.book(t, s.charges...)
	f.clear(t, s)
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Fatalf("2900 net after clearing = %.2f, want 0", got)
	}
	entriesBefore := f.entryCount(t)
	chargeEntry := *f.chargeEntry(t, s.charges[0])

	err := f.unassign(s.charges[0])
	if !errors.Is(err, ErrVippsChargeInClearedSettlement) {
		t.Fatalf("unassign err = %v, want ErrVippsChargeInClearedSettlement", err)
	}
	var cleared *ChargeInClearedSettlementError
	if !errors.As(err, &cleared) || cleared.SettlementNumber != s.number || cleared.MSN != sweepMSN || cleared.BankRowID != s.bankRow {
		t.Errorf("err = %+v, want settlement %s / %s cleared by deposit %s", cleared, s.number, sweepMSN, s.bankRow)
	}
	if msg := err.Error(); !strings.Contains(msg, "Utb. "+s.number+" Vippsnr "+sweepMSN) || !strings.Contains(msg, "Angre") {
		t.Errorf("message %q does not name the clearing to undo", msg)
	}
	if got := f.chargeEntry(t, s.charges[0]); got == nil || *got != chargeEntry {
		t.Errorf("refused unassign unlinked the charge: %v", got)
	}
	if got := f.entryStatusOf(t, chargeEntry); got != "posted" {
		t.Errorf("charge entry is %s after a refused unassign, want posted", got)
	}
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net after the refusal = %.2f, want 0", got)
	}
	if got := f.entryCount(t); got != entriesBefore {
		t.Errorf("refused unassign created %d entries", got-entriesBefore)
	}
	if f.bankEntry(t, s.bankRow) == nil {
		t.Errorf("refused unassign released the deposit")
	}

	// Undo the deposit, then the charge, then book it again: the sweep clears
	// the settlement once more and 2900 nets to zero with the fee booked once.
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, s.bankRow); err != nil {
		t.Fatalf("undo the deposit: %v", err)
	}
	if err := f.unassign(s.charges[0]); err != nil {
		t.Fatalf("unassign after undoing the deposit: %v", err)
	}
	if f.chargeEntry(t, s.charges[0]) != nil {
		t.Fatalf("charge still booked after a successful unassign")
	}
	f.book(t, s.charges[0])
	sweep, err := f.svc.ClearReadyVippsPayouts(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, ok := settlementsOf(sweep.Cleared)[s.number]; !ok {
		t.Fatalf("sweep = %+v, want %s cleared again", sweep, s.number)
	}
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net after re-clearing = %.2f, want 0", got)
	}
	if got := f.accountNet(t, vippsFeeAccountCode); !floatNear(got, 50, 0.005) {
		t.Errorf("7700 net = %.2f, want the 50 kr fee booked once", got)
	}
}

func (f *payoutSweepFixture) entryStatusOf(t *testing.T, id string) string {
	t.Helper()
	var status string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT status FROM journal_entries WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read entry: %v", err)
	}
	return status
}

// A settlement that is not cleared yet keeps the plain undo: no deposit
// imported, or a deposit still waiting on another charge.
func TestUnassignChargeInUnclearedSettlementStillWorks(t *testing.T) {
	f := newPayoutSweepFixture(t)

	noDeposit := f.settlement(t, "3100201", 2, []int64{45000, 30000}, 2000, false)
	f.book(t, noDeposit.charges...)
	if err := f.unassign(noDeposit.charges[0]); err != nil {
		t.Errorf("unassign with no deposit imported: %v", err)
	}

	waiting := f.settlement(t, "3100202", 3, []int64{10000, 15000}, 1250, true)
	f.book(t, waiting.charges[0])
	if err := f.unassign(waiting.charges[0]); err != nil {
		t.Errorf("unassign while the settlement waits on another charge: %v", err)
	}
	for _, id := range []string{noDeposit.charges[0], waiting.charges[0]} {
		if f.chargeEntry(t, id) != nil {
			t.Errorf("charge %s is still booked", id)
		}
	}
	if got := f.clearingNet(t); !floatNear(got, 300, 0.005) {
		t.Errorf("2900 net = %.2f, want 300 (the one charge still booked)", got)
	}
}

// A draft charge entry is not in the ledger, so releasing it from a cleared
// settlement leaves 2900 as it was. Such a pairing predates the posted-only
// clearing gate, so it is built by replacing a charge's posted link.
func TestUnassignDraftChargeInClearedSettlementStillWorks(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3100301", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, s.charges...)
	f.clear(t, s)

	draft, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID,
		EntryDate:      s.booking.Format("2006-01-02"),
		Description:    "Synthetic parked charge",
		Source:         "vipps",
		CreatedBy:      f.club.Treasurer.ID,
		ClubID:         f.club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: vippsClearingAccountCode, Debit: 250},
			{AccountCode: "3900", Credit: 250},
		},
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE vipps_import_rows SET journal_entry_id = $1 WHERE id = $2`, draft.ID, s.charges[1]); err != nil {
		t.Fatalf("link draft: %v", err)
	}
	before := f.clearingNet(t)

	if err := f.unassign(s.charges[1]); err != nil {
		t.Fatalf("release of a draft charge in a cleared settlement: %v", err)
	}
	if f.chargeEntry(t, s.charges[1]) != nil {
		t.Errorf("draft charge still linked")
	}
	if got := f.clearingNet(t); got != before {
		t.Errorf("2900 net changed from %.2f to %.2f by releasing a draft", before, got)
	}
}

// The ledger's void is refused for the same charge entry: an order-booked
// charge is voided there, which would leave 2900 off the same way.
func TestVoidLedgerEntryRefusesChargeInClearedSettlement(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3100401", 2, []int64{40000}, 2000, true)
	f.book(t, s.charges...)
	entry := *f.chargeEntry(t, s.charges[0])

	f.clear(t, s)
	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, entry, f.club.Treasurer.ID); !errors.Is(err, ErrVippsChargeInClearedSettlement) {
		t.Fatalf("void err = %v, want ErrVippsChargeInClearedSettlement", err)
	}
	if got := f.entryStatusOf(t, entry); got != "posted" {
		t.Errorf("charge entry is %s after a refused void, want posted", got)
	}
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net = %.2f, want 0", got)
	}
}

// A clearing racing an unassign of one of its charges ends consistent: either
// the clearing commits first and the unassign is refused, or the unassign
// commits first and the clearing finds the charge unbooked and waits. The
// deposit is never booked against a settlement with an unbooked charge.
func TestClearVersusUnassignLeavesAConsistentSettlement(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()

	const rounds = 8
	clearWon, unassignWon := 0, 0
	for i := range rounds {
		booking := time.Date(f.club.Year, 7, 1+i, 0, 0, 0, 0, time.UTC)
		s := f.settlementOn(t, "32001"+string(rune('0'+i)), booking, []int64{30000, 20000}, 1000, true)
		f.book(t, s.charges...)

		start := make(chan struct{})
		var wg sync.WaitGroup
		var clearErr, unassignErr error
		var clearRes *VippsPayoutClearResult
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			clearRes, clearErr = f.svc.ClearVippsPayout(ctx, f.club.ID, s.bankRow, f.club.Treasurer.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			unassignErr = f.unassign(s.charges[0])
		}()
		close(start)
		wg.Wait()

		deposit := f.bankEntry(t, s.bankRow) != nil
		charge := f.chargeEntry(t, s.charges[0]) != nil
		switch {
		case deposit && charge:
			clearWon++
			if clearErr != nil || !errors.Is(unassignErr, ErrVippsChargeInClearedSettlement) {
				t.Errorf("round %d: clear won but clear err = %v, unassign err = %v", i, clearErr, unassignErr)
			}
			bal, err := f.svc.settlementClearingBalance(ctx, f.club.ID, s.number, sweepMSN, s.booking, s.bankRow)
			if err != nil || !floatNear(bal, 0, 0.005) {
				t.Errorf("round %d: cleared settlement clearing balance = %.2f (err %v), want 0", i, bal, err)
			}
		case !deposit && !charge:
			unassignWon++
			if unassignErr != nil || clearErr != nil || clearRes == nil || clearRes.Ready {
				t.Errorf("round %d: unassign won but unassign err = %v, clear err = %v, clear = %+v", i, unassignErr, clearErr, clearRes)
			}
		default:
			t.Errorf("round %d: inconsistent: deposit booked = %v, charge booked = %v (clear err %v, unassign err %v)",
				i, deposit, charge, clearErr, unassignErr)
		}
	}
	t.Logf("clear won %d rounds, unassign won %d", clearWon, unassignWon)
}

// Two clearings of one deposit book it once: the loser sees the winner's
// entry after waiting on the settlement lock.
func TestConcurrentClearsOfOneDepositBookItOnce(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3100501", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, s.charges...)
	before := f.entryCount(t)

	const callers = 3
	results := make([]*VippsPayoutClearResult, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = f.svc.ClearVippsPayout(ctx, f.club.ID, s.bankRow, f.club.Treasurer.ID)
		}()
	}
	close(start)
	wg.Wait()

	fresh := 0
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("clear %d: %v", i, errs[i])
		}
		if !results[i].AlreadyCleared {
			fresh++
		}
		if results[i].BankEntryID == "" {
			t.Errorf("clear %d reported no bank entry", i)
		}
	}
	if fresh != 1 {
		t.Errorf("%d clears booked the settlement, want exactly 1", fresh)
	}
	if got := f.entryCount(t) - before; got != 2 {
		t.Errorf("clearing created %d entries, want the fee and the bank entry", got)
	}
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net = %.2f, want 0", got)
	}
}
