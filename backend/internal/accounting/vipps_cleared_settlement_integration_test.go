package accounting

import (
	"context"
	"sync"
	"testing"
)

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

func (f *payoutSweepFixture) entryStatusOf(t *testing.T, id string) string {
	t.Helper()
	var status string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT status FROM journal_entries WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read entry: %v", err)
	}
	return status
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
