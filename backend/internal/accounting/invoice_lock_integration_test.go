package accounting

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// contend makes bookings race for one faktura deterministically: a third
// transaction holds the faktura lock, every booking is started, and the lock
// is released only once all of them are waiting on it. Without the lock in
// the booking paths nobody waits and the test fails on the timeout.
func contend(t *testing.T, c *fin.Club, invoiceID string, bookings ...func()) {
	t.Helper()
	contendWhile(t, c, invoiceID, nil, bookings...)
}

// contendWhile is contend, running whileWaiting once every booking is
// waiting on the faktura lock and before the lock is released.
func contendWhile(t *testing.T, c *fin.Club, invoiceID string, whileWaiting func(), bookings ...func()) {
	t.Helper()
	ctx := context.Background()
	holder, err := c.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	if _, err := holder.Exec(ctx, `SELECT id FROM invoices WHERE id = $1 FOR NO KEY UPDATE`, invoiceID); err != nil {
		t.Fatalf("hold faktura lock: %v", err)
	}

	var wg sync.WaitGroup
	for _, b := range bookings {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b()
		}()
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		if err := c.Pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock'
			    AND query LIKE 'SELECT id FROM invoices WHERE id = $1 AND club_id = $2 FOR NO KEY UPDATE%'`,
		).Scan(&waiting); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if waiting >= len(bookings) {
			break
		}
		if time.Now().After(deadline) {
			_ = holder.Rollback(ctx)
			wg.Wait()
			t.Fatalf("only %d of %d bookings waited on the faktura lock", waiting, len(bookings))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if whileWaiting != nil {
		whileWaiting()
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("release holder: %v", err)
	}
	wg.Wait()
}

// countPosted counts posted entries whose source is the given table.
func countPosted(t *testing.T, c *fin.Club, sourceTable string) int {
	t.Helper()
	var n int
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND status = 'posted' AND source_table = $2`,
		c.ID, sourceTable).Scan(&n); err != nil {
		t.Fatalf("count posted: %v", err)
	}
	return n
}

// A sync's auto-assign and a manual assign of another row race for one
// faktura: exactly one books, the other row stays in Tildel.
func TestBankAutoAssignRacesManualAssign(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Kapplop")
	inv := f.faktura(t, m, 45000)
	auto := f.deposit(t, "Synthetic Kapplop", 45000)
	manual := f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Cents: 45000, Counterpart: "Synthetic Someone", FromAccount: "88880012345"})

	contend(t, f.club, inv.ID,
		func() {
			_, _ = f.svc.AutoAssignBankDeposits(ctx, f.club.ID, f.club.Treasurer.ID, BankAutoAssignOpts{Trigger: BankAutoAssignTriggerSync})
		},
		func() { _, _ = f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, manual, inv.ID) },
	)

	if n := countPosted(t, f.club, "bank_import_rows"); n != 1 {
		t.Errorf("posted entries = %d, want exactly one", n)
	}
	if (f.entry(t, auto) == "") == (f.entry(t, manual) == "") {
		t.Errorf("auto row booked %v, manual row booked %v; want exactly one booked",
			f.entry(t, auto) != "", f.entry(t, manual) != "")
	}
}

// A split payment assigned by hand races a sync's auto-assign of a single
// deposit to the same faktura: only one side books it.
func TestBankAutoAssignRacesSplitAssign(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Splitt")
	inv := f.faktura(t, m, 45000)
	auto := f.deposit(t, "Synthetic Splitt", 45000)
	partA := f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Cents: 20000, Counterpart: "Synthetic Other", FromAccount: "88880054321"})
	partB := f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Cents: 25000, Counterpart: "Synthetic Other", FromAccount: "88880054321"})

	contend(t, f.club, inv.ID,
		func() {
			_, _ = f.svc.AutoAssignBankDeposits(ctx, f.club.ID, f.club.Treasurer.ID, BankAutoAssignOpts{Trigger: BankAutoAssignTriggerSync})
		},
		func() {
			_, _ = f.svc.AssignMultipleBankRowsToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, []string{partA, partB}, inv.ID)
		},
	)

	autoBooked := f.entry(t, auto) != ""
	splitBooked := f.entry(t, partA) != "" && f.entry(t, partB) != ""
	if autoBooked == splitBooked {
		t.Fatalf("auto booked %v, split booked %v; want exactly one side", autoBooked, splitBooked)
	}
	want := 1
	if splitBooked {
		want = 2
	}
	if n := countPosted(t, f.club, "bank_import_rows"); n != want {
		t.Errorf("posted entries = %d, want %d (one side's bookings only)", n, want)
	}
}

// Vipps auto-assign and bank auto-assign can't both settle one faktura.
func TestVippsAndBankAutoAssignShareTheFakturaLock(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = true WHERE id = $1`, f.club.ID)
	m := f.member(t, "Tokanal")
	inv := f.faktura(t, m, 45000)
	bankRow := f.deposit(t, "Synthetic Tokanal", 45000)
	vippsRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Tokanal"})

	contend(t, f.club, inv.ID,
		func() {
			_, _ = f.svc.AutoAssignBankDeposits(ctx, f.club.ID, f.club.Treasurer.ID, BankAutoAssignOpts{Trigger: BankAutoAssignTriggerSync})
		},
		func() { _, _ = f.svc.AutoAssignVippsCharges(ctx, f.club.ID, f.club.Treasurer.ID) },
	)

	bankBooked, vippsBooked := f.entry(t, bankRow) != "", rowEntry(t, f.club, vippsRow) != ""
	if bankBooked == vippsBooked {
		t.Fatalf("bank booked %v, vipps booked %v; want exactly one", bankBooked, vippsBooked)
	}
	if n := countPosted(t, f.club, "bank_import_rows") + countPosted(t, f.club, "vipps_import_rows"); n != 1 {
		t.Errorf("posted entries = %d, want exactly one", n)
	}
	if !f.paid(t, inv.ID) {
		t.Errorf("faktura not marked paid")
	}
}
