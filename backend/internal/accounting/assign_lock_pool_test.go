package accounting

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// The faktura-lock assigns run every statement on the lock transaction, so
// assigns queued on one faktura finish on a pool smaller than the queue.
// Each test runs four concurrent assigns of distinct payments to one faktura
// on a two-connection pool: exactly one books, the rest are refused, and
// nothing waits out the deadline.

const lockPoolAssigns = 4

// runLockPoolAssigns runs assign lockPoolAssigns times concurrently with a
// deadline and returns how many succeeded. Every failure must be a refusal,
// not the deadline: a deadline means a lock holder waited on the pool.
func runLockPoolAssigns(t *testing.T, assign func(ctx context.Context, i int) error) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	errs := make([]error, lockPoolAssigns)
	var wg sync.WaitGroup
	for i := range lockPoolAssigns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = assign(ctx, i)
		}()
	}
	wg.Wait()

	ok := 0
	for i, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
			t.Fatalf("assign %d hit the deadline (lock holder starved of a connection): %v", i, err)
		case errors.Is(err, errInvoiceSettled), errors.Is(err, errInvoiceHasBookedRow), errors.Is(err, errInvoiceNotOpen), errors.Is(err, errInvoicePaid):
			// The faktura was taken by the assign that won.
		default:
			t.Errorf("assign %d failed for a reason other than losing the faktura: %v", i, err)
		}
	}
	return ok
}

// countEntries returns the posted and draft entries of a source.
func countEntries(t *testing.T, f *matchingFixture, source string) (posted, drafts int) {
	t.Helper()
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE status = 'posted'), count(*) FILTER (WHERE status = 'draft')
		   FROM journal_entries WHERE club_id = $1 AND source = $2::transaction_source`, f.club.ID, source,
	).Scan(&posted, &drafts); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return posted, drafts
}

func countLinkedRows(t *testing.T, f *matchingFixture, table string) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE club_id = $1 AND journal_entry_id IS NOT NULL`, f.club.ID,
	).Scan(&n); err != nil {
		t.Fatalf("count linked rows: %v", err)
	}
	return n
}

func TestConcurrentManualAssignsOnSmallPool(t *testing.T) {
	f := newMatchingFixture(t, testutil.WithMaxConns(2))
	importID := f.club.NewBankImport(t)
	rows := make([]string, lockPoolAssigns)
	for i := range rows {
		rows[i] = f.club.NewBankRow(t, importID, fin.BankRowOpts{Cents: f.open.TotalCents})
	}

	ok := runLockPoolAssigns(t, func(ctx context.Context, i int) error {
		_, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, rows[i], f.open.ID)
		return err
	})
	if ok != 1 {
		t.Errorf("%d assigns booked; want exactly one", ok)
	}
	if posted, drafts := countEntries(t, f, "bank_import"); posted != 1 || drafts != 0 {
		t.Errorf("entries = %d posted, %d drafts; want 1 posted, 0 drafts", posted, drafts)
	}
	if n := countLinkedRows(t, f, "bank_import_rows"); n != 1 {
		t.Errorf("%d bank rows linked; want 1", n)
	}
	if !f.paymentID(t, f.open) {
		t.Error("faktura not back-linked to a payment")
	}
}

func TestConcurrentMultiRowAssignsOnSmallPool(t *testing.T) {
	f := newMatchingFixture(t, testutil.WithMaxConns(2))
	importID := f.club.NewBankImport(t)
	half := f.open.TotalCents / 2
	groups := make([][]string, lockPoolAssigns)
	for i := range groups {
		groups[i] = []string{
			f.club.NewBankRow(t, importID, fin.BankRowOpts{Cents: half}),
			f.club.NewBankRow(t, importID, fin.BankRowOpts{Cents: f.open.TotalCents - half}),
		}
	}

	ok := runLockPoolAssigns(t, func(ctx context.Context, i int) error {
		_, err := f.svc.AssignMultipleBankRowsToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, groups[i], f.open.ID)
		return err
	})
	if ok != 1 {
		t.Errorf("%d multi-row assigns booked; want exactly one", ok)
	}
	if posted, drafts := countEntries(t, f, "bank_import"); posted != 2 || drafts != 0 {
		t.Errorf("entries = %d posted, %d drafts; want 2 posted (one per row), 0 drafts", posted, drafts)
	}
	if n := countLinkedRows(t, f, "bank_import_rows"); n != 2 {
		t.Errorf("%d bank rows linked; want 2", n)
	}
	if !f.paymentID(t, f.open) {
		t.Error("faktura not back-linked to a payment")
	}
}

func TestConcurrentVippsAssignsOnSmallPool(t *testing.T) {
	f := newMatchingFixture(t, testutil.WithMaxConns(2))
	charges := make([]string, lockPoolAssigns)
	for i := range charges {
		charges[i] = f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: f.open.TotalCents, CustomerName: "Synthetic Payer"})
	}

	ok := runLockPoolAssigns(t, func(ctx context.Context, i int) error {
		res, err := f.svc.AssignVippsChargeToInvoice(ctx, f.club.ID, charges[i], f.open.ID, f.club.Treasurer.ID)
		if err == nil && res.AlreadyReconciled {
			t.Errorf("charge %d reported already reconciled; each charge is distinct", i)
		}
		return err
	})
	if ok != 1 {
		t.Errorf("%d Vipps assigns booked; want exactly one", ok)
	}
	if posted, drafts := countEntries(t, f, "vipps"); posted != 1 || drafts != 0 {
		t.Errorf("entries = %d posted, %d drafts; want 1 posted, 0 drafts", posted, drafts)
	}
	if n := countLinkedRows(t, f, "vipps_import_rows"); n != 1 {
		t.Errorf("%d Vipps charges linked; want 1", n)
	}
	var withInvoice int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM vipps_import_rows WHERE club_id = $1 AND invoice_id = $2`, f.club.ID, f.open.ID,
	).Scan(&withInvoice); err != nil || withInvoice != 1 {
		t.Errorf("%d charges record the faktura (err %v); want 1", withInvoice, err)
	}
	if !f.paymentID(t, f.open) {
		t.Error("faktura not back-linked to a payment")
	}
}

// A charge booked elsewhere between the load and the lock tx's guarded mark
// is errChargeRaced, and the losing draft is rolled back to the savepoint:
// committing the transaction afterwards leaves only the winner's entry.
func TestPostVippsChargeTxRaceLeavesNoDraft(t *testing.T) {
	f := newMatchingFixture(t)
	ctx := t.Context()
	charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 50000, CustomerName: "Synthetic Payer"})
	stale, err := f.svc.loadVippsBelastning(ctx, f.club.ID, charge)
	if err != nil {
		t.Fatalf("load charge: %v", err)
	}
	winner, err := f.svc.CreateVippsChargeBilag(ctx, f.club.ID, charge, "3900", "", f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("winning booking: %v", err)
	}

	tx, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	got, err := postVippsChargeTx(ctx, tx, f.club.ID, f.club.Treasurer.ID, receivablesAccountCode, "Synthetic race", stale)
	if !errors.Is(err, errChargeRaced) {
		t.Fatalf("err = %v, want errChargeRaced", err)
	}
	if got != winner.EntryID {
		t.Errorf("reported entry %s, want the winner's %s", got, winner.EntryID)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit after a lost race: %v", err)
	}
	if posted, drafts := countEntries(t, f, "vipps"); posted != 1 || drafts != 0 {
		t.Errorf("entries = %d posted, %d drafts; want only the winner's posted entry", posted, drafts)
	}
}
