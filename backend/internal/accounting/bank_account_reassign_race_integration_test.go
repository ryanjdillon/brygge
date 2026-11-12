package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
	"github.com/rs/zerolog"
)

// BRY-674: every booking path reads the bank account it debits or credits
// under the import's share lock, so a bank-account reassign racing a booking
// ends with the entry on the new account (swapped by the reassign, or booked
// there), or the booking refused: never an entry on the old account after the
// reassign committed. The booking service runs on a two-connection pool; the
// held locks, the reassign (ReassignBankImport on a service of its own) and
// the polls run on a second pool.

// raceSide is the second pool on the test's database and a service on it
// that runs the reassigns, so the booking service's small pool stays its own.
type raceSide struct {
	pool *pgxpool.Pool
	svc  *Service
}

func newRaceSide(t *testing.T, c *fin.Club) raceSide {
	t.Helper()
	cfg := c.Pool.Config()
	cfg.MaxConns = 6
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("side pool: %v", err)
	}
	t.Cleanup(p.Close)
	return raceSide{pool: p, svc: NewService(p, audit.NewService(p, zerolog.Nop()), zerolog.Nop())}
}

// reassign runs the real bank-account reassign of importID to newCode.
func (s raceSide) reassign(ctx context.Context, clubID, importID, newCode string) error {
	res, err := s.svc.ReassignBankImport(ctx, clubID, importID, newCode)
	if err == nil && res.Unchanged {
		return errors.New("reassign: unchanged")
	}
	return err
}

// waitLockWaiters polls on pool until n backends wait on a lock.
func waitLockWaiters(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var got int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d backends waiting on a lock after 30 s", n)
}

// rowEntryCodes is the entry a bank row is linked to ("" when unbooked) and
// its lines by account code.
func rowEntryCodes(t *testing.T, c *fin.Club, rowID string) (string, map[string][2]float64) {
	t.Helper()
	var entry *string
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1`, rowID).Scan(&entry); err != nil {
		t.Fatalf("read bank row: %v", err)
	}
	if entry == nil {
		return "", nil
	}
	return *entry, readTransferEntry(t, c, *entry).lines
}

func importOf(t *testing.T, c *fin.Club, rowID string) string {
	t.Helper()
	var id string
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT bank_import_id::text FROM bank_import_rows WHERE id = $1`, rowID).Scan(&id); err != nil {
		t.Fatalf("read bank row import: %v", err)
	}
	return id
}

// bookingPastItsRead holds rowID's lock on the side pool, starts book, waits
// until book queues on that lock (it has read whatever it reads before its
// row lock), reassigns importID to newCode to completion while it waits, then
// releases the row. A reassign never waits for a bank row, so it must finish
// first.
func bookingPastItsRead(t *testing.T, c *fin.Club, side raceSide, rowID, importID, newCode string, book func(context.Context) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	lock := testutil.HoldRowLock(t, side.pool, `SELECT id FROM bank_import_rows WHERE id = $1 FOR UPDATE`, rowID)
	defer lock.Release()
	done := make(chan error, 1)
	go func() { done <- book(ctx) }()
	lock.WaitForBlocked(30 * time.Second)

	rctx, rcancel := context.WithTimeout(ctx, 15*time.Second)
	defer rcancel()
	if err := side.reassign(rctx, c.ID, importID, newCode); err != nil {
		t.Fatalf("reassign while the booking waits on its row: %v (a reassign must never wait for a bank row)", err)
	}
	lock.Release()
	return <-done
}

// The KID cascade read its bank account with its candidate (m.bankAccount).
// A reassign that commits while the cascade waits on the row now leaves the
// entry on the new account.
func TestKIDCascadeBooksOnAccountReassignedWhileItWaited(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	side := newRaceSide(t, c)
	date := day(c.Year, 6, 3)
	inv := c.NewInvoice(t, fin.InvoiceOpts{Cents: 41000})
	imp := c.NewBankImportOn(t, "1920")
	row := c.NewBankRow(t, imp, fin.BankRowOpts{Date: date, Cents: 41000})

	err := bookingPastItsRead(t, c, side, row, imp, "1925", func(ctx context.Context) error {
		return svc.bookKIDCascadeMatch(ctx, c.ID, kidCascadeMatch{
			rowID: row, invoiceID: inv.ID, actorID: c.Treasurer.ID, bankAccount: "1920", // the stale candidate read
			kid: inv.KID, desc: "Synthetic", date: date, amount: fin.Kr(41000),
		})
	})
	if err != nil {
		t.Fatalf("KID cascade: %v", err)
	}
	_, lines := rowEntryCodes(t, c, row)
	if _, ok := lines["1920"]; ok {
		t.Fatalf("entry lines %v: booked on the old account after the reassign committed", lines)
	}
	if lines["1925"] != [2]float64{410, 0} {
		t.Fatalf("entry lines %v: want DR 1925 410", lines)
	}
}

// Transfer detection read both bank accounts with its candidates. A reassign
// of one side's import that commits while the pairing waits on a row now
// books the transfer on the new account; one that puts both rows on one
// account leaves them unpaired, with no entry.
func TestTransferDetectionBooksOnAccountReassignedWhileItWaited(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	side := newRaceSide(t, c)

	t.Run("new account", func(t *testing.T) {
		out, in, outImport := transferPairRows(t, c, 52000, day(c.Year, 6, 10), day(c.Year, 6, 10))
		var matched int
		err := bookingPastItsRead(t, c, side, in, importOf(t, c, in), "1900", func(ctx context.Context) error {
			var err error
			matched, _, _, err = svc.detectIntraBankTransfers(ctx, c.ID, outImport, "", c.Treasurer.ID)
			return err
		})
		if err != nil || matched != 1 {
			t.Fatalf("detect: matched %d, err %v; want the pair booked", matched, err)
		}
		entry, lines := rowEntryCodes(t, c, in)
		if outEntry, _ := rowEntryCodes(t, c, out); outEntry != entry {
			t.Fatalf("rows on entries %q / %q, want one", outEntry, entry)
		}
		if _, ok := lines["1925"]; ok {
			t.Fatalf("entry lines %v: booked on the old account after the reassign committed", lines)
		}
		if lines["1900"] != [2]float64{520, 0} || lines["1920"] != [2]float64{0, 520} {
			t.Fatalf("entry lines %v: want DR 1900 / CR 1920 520", lines)
		}
	})

	t.Run("one account", func(t *testing.T) {
		out, in, outImport := transferPairRows(t, c, 53000, day(c.Year, 6, 11), day(c.Year, 6, 11))
		entries := clubEntries(t, c)
		var matched int
		err := bookingPastItsRead(t, c, side, in, importOf(t, c, in), "1920", func(ctx context.Context) error {
			var err error
			matched, _, _, err = svc.detectIntraBankTransfers(ctx, c.ID, outImport, "", c.Treasurer.ID)
			return err
		})
		if err != nil || matched != 0 {
			t.Fatalf("detect: matched %d, err %v; want nothing paired once both rows are on 1920", matched, err)
		}
		if got := clubEntries(t, c) - entries; got != 0 {
			t.Fatalf("%d new entries, want none", got)
		}
		if s := readTransferRow(t, c, out); s.entry != "" || s.declined {
			t.Fatalf("outgoing row %+v, want unbooked and undeclined", s)
		}
		if s := readTransferRow(t, c, in); s.entry != "" || s.declined {
			t.Fatalf("incoming row %+v, want unbooked and undeclined", s)
		}
	})
}

// The group booking reads its rows and their imports (FOR SHARE) on its one
// transaction before it locks the rows: a reassign that arrives while the
// group waits on a row queues behind it and swaps the committed entry; a
// group that arrives while a reassign holds the import queues behind it and
// books on the new account.
func TestGroupBookingAndReassignEndOnTheNewAccount(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	side := newRaceSide(t, c)
	group := func(d int) (imp string, ids []string) {
		imp = c.NewBankImportOn(t, "1920")
		ids = []string{
			c.NewBankRow(t, imp, fin.BankRowOpts{Date: day(c.Year, 7, d), Cents: 30000, Description: "Synthetic buy-back"}),
			c.NewBankRow(t, imp, fin.BankRowOpts{Date: day(c.Year, 7, d), Cents: -10000, Description: "Synthetic return"}),
		}
		return imp, ids
	}
	check := func(t *testing.T, res *GroupAssignResult, err error, row string) {
		t.Helper()
		if err != nil {
			t.Fatalf("group: %v", err)
		}
		entry, lines := rowEntryCodes(t, c, row)
		if entry != res.JournalEntryID {
			t.Fatalf("row on %q, want the group's %q", entry, res.JournalEntryID)
		}
		if _, ok := lines["1920"]; ok {
			t.Fatalf("entry lines %v: on the old account after the reassign committed", lines)
		}
		if lines["1925"] != [2]float64{200, 0} || lines["3900"] != [2]float64{0, 200} {
			t.Fatalf("entry lines %v: want DR 1925 / CR 3900 200", lines)
		}
	}

	t.Run("reassign queues behind the group", func(t *testing.T) {
		imp, ids := group(1)
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		lock := testutil.HoldRowLock(t, side.pool, `SELECT id FROM bank_import_rows WHERE id = $1 FOR UPDATE`, ids[1])
		defer lock.Release()
		var res *GroupAssignResult
		groupDone := make(chan error, 1)
		go func() {
			var err error
			res, err = svc.AssignBankRowsToAccountGroup(ctx, c.ID, c.Treasurer.ID, ids, "3900", "")
			groupDone <- err
		}()
		lock.WaitForBlocked(30 * time.Second)
		reassignDone := make(chan error, 1)
		go func() { reassignDone <- side.reassign(ctx, c.ID, imp, "1925") }()
		waitLockWaiters(t, side.pool, 2) // the group on the row, the reassign on the import
		lock.Release()
		err := <-groupDone
		if rerr := <-reassignDone; rerr != nil {
			t.Fatalf("reassign: %v", rerr)
		}
		check(t, res, err, ids[0])
	})

	t.Run("group queues behind the reassign", func(t *testing.T) {
		imp, ids := group(2)
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		// A holder of the import makes the reassign take it first and the
		// group queue behind the reassign, on the import's tuple lock.
		lock := testutil.HoldRowLock(t, side.pool, `SELECT id FROM bank_imports WHERE id = $1 FOR UPDATE`, imp)
		defer lock.Release()
		reassignDone := make(chan error, 1)
		go func() { reassignDone <- side.reassign(ctx, c.ID, imp, "1925") }()
		waitLockWaiters(t, side.pool, 1)
		var res *GroupAssignResult
		groupDone := make(chan error, 1)
		go func() {
			var err error
			res, err = svc.AssignBankRowsToAccountGroup(ctx, c.ID, c.Treasurer.ID, ids, "3900", "")
			groupDone <- err
		}()
		waitLockWaiters(t, side.pool, 2)
		lock.Release()
		if err := <-reassignDone; err != nil {
			t.Fatalf("reassign: %v", err)
		}
		check(t, res, <-groupDone, ids[0])
	})
}

// newSmallPoolPayoutFixture is newPayoutSweepFixture on a two-connection
// pool; perCharge picks the per-charge model or the legacy confirm.
func newSmallPoolPayoutFixture(t *testing.T, perCharge bool) *payoutSweepFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(2))
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	if _, err := pool.Exec(context.Background(),
		`UPDATE clubs SET vipps_per_charge_enabled = $2 WHERE id = $1`, club.ID, perCharge); err != nil {
		t.Fatalf("set per-charge: %v", err)
	}
	return &payoutSweepFixture{svc: svc, club: club, bankImport: club.NewBankImport(t)}
}

// The Vipps payout clearing read its deposit's bank account before it took
// the settlement's lock. A reassign that commits while it waits on the
// deposit now leaves the bank entry on the new account.
func TestVippsPayoutClearingBooksOnAccountReassignedWhileItWaited(t *testing.T) {
	f := newSmallPoolPayoutFixture(t, true)
	side := newRaceSide(t, f.club)
	s := f.settlement(t, "3740101", 2, []int64{45000}, 2000, true)
	f.book(t, s.charges...)

	var res *VippsPayoutClearResult
	err := bookingPastItsRead(t, f.club, side, s.bankRow, f.bankImport, "1925", func(ctx context.Context) error {
		var err error
		res, err = f.svc.ClearVippsPayout(ctx, f.club.ID, s.bankRow, f.club.Treasurer.ID)
		return err
	})
	if err != nil || res == nil || res.BankEntryID == "" {
		t.Fatalf("clear: %+v, err %v; want the payout cleared", res, err)
	}
	lines := readTransferEntry(t, f.club, res.BankEntryID).lines
	if _, ok := lines["1920"]; ok {
		t.Fatalf("bank entry lines %v: on the old account after the reassign committed", lines)
	}
	if lines["1925"] != [2]float64{430, 0} {
		t.Fatalf("bank entry lines %v: want DR 1925 430", lines)
	}
}

// The legacy Vipps confirm carries the account its preview read. A reassign
// that commits while the confirm waits on the deposit makes that preview
// stale: the confirm is refused and books nothing, and a fresh preview books
// on the new account.
func TestLegacyVippsConfirmRefusesAPreviewOlderThanAReassign(t *testing.T) {
	f := newSmallPoolPayoutFixture(t, false)
	side := newRaceSide(t, f.club)
	ctx := context.Background()
	s := f.settlement(t, "3740201", 3, []int64{45000}, 2000, true)
	preview, err := f.svc.ReconcileVippsPreview(ctx, f.club.ID, s.bankRow)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	entries := f.entryCount(t)

	err = bookingPastItsRead(t, f.club, side, s.bankRow, f.bankImport, "1925", func(ctx context.Context) error {
		_, err := f.svc.ReconcileVippsConfirm(ctx, f.club.ID, s.bankRow, "", f.club.Treasurer.ID, preview.Lines)
		return err
	})
	if !errors.Is(err, ErrVippsPreviewStale) {
		t.Fatalf("confirm err = %v, want ErrVippsPreviewStale", err)
	}
	if got := f.entryCount(t) - entries; got != 0 || f.bankEntry(t, s.bankRow) != nil {
		t.Fatalf("a refused confirm kept %d entries / a link", got)
	}

	fresh, err := f.svc.ReconcileVippsPreview(ctx, f.club.ID, s.bankRow)
	if err != nil {
		t.Fatalf("fresh preview: %v", err)
	}
	entryID, err := f.svc.ReconcileVippsConfirm(ctx, f.club.ID, s.bankRow, "", f.club.Treasurer.ID, fresh.Lines)
	if err != nil {
		t.Fatalf("confirm a fresh preview: %v", err)
	}
	lines := readTransferEntry(t, f.club, entryID).lines
	if _, ok := lines["1920"]; ok || lines["1925"] != [2]float64{430, 0} {
		t.Fatalf("entry lines %v: want DR 1925 430 and nothing on 1920", lines)
	}
}

// A person's edited confirm: the bank_in line on another account than the
// import's is a stale preview; no bank_in line, two of them, or another line
// on a bank account (the cash range, or a configured bank account outside
// it) is refused as ErrVippsConfirmBankLine. Nothing is booked. The line
// shapes themselves are covered by TestCheckVippsConfirmBankLine.
func TestLegacyVippsConfirmRefusesEditedBankLines(t *testing.T) {
	f := newSmallPoolPayoutFixture(t, false)
	ctx := context.Background()
	s := f.settlement(t, "3740301", 4, []int64{45000}, 2000, true)
	// A configured bank account outside the cash range.
	if _, err := f.club.Pool.Exec(ctx,
		`INSERT INTO club_bank_accounts (club_id, account_number, role, gl_code) VALUES ($1, '12345678903', 'other', '1960')`,
		f.club.ID); err != nil {
		t.Fatalf("configured bank account: %v", err)
	}
	preview, err := f.svc.ReconcileVippsPreview(ctx, f.club.ID, s.bankRow)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	bankAt := -1
	for i, l := range preview.Lines {
		if l.Kind == "bank_in" {
			bankAt = i
		}
	}
	if bankAt < 0 {
		t.Fatalf("preview has no bank_in line: %+v", preview.Lines)
	}
	edit := func(fn func([]VippsReconcileLine) []VippsReconcileLine) []VippsReconcileLine {
		return fn(append([]VippsReconcileLine(nil), preview.Lines...))
	}
	entries := f.entryCount(t)
	for _, tc := range []struct {
		name  string
		lines []VippsReconcileLine
		want  error
	}{
		{"bank_in on another bank account", edit(func(ls []VippsReconcileLine) []VippsReconcileLine {
			ls[bankAt].AccountCode = "1925"
			return ls
		}), ErrVippsPreviewStale},
		{"no bank_in line", edit(func(ls []VippsReconcileLine) []VippsReconcileLine {
			ls[bankAt].Kind = "revenue"
			return ls
		}), ErrVippsConfirmBankLine},
		{"two bank_in lines", edit(func(ls []VippsReconcileLine) []VippsReconcileLine {
			half := ls[bankAt]
			half.Debit /= 2
			ls[bankAt].Debit -= half.Debit
			return append(ls, half)
		}), ErrVippsConfirmBankLine},
		{"another line on a bank account", edit(func(ls []VippsReconcileLine) []VippsReconcileLine {
			for i := range ls {
				if i != bankAt {
					ls[i].AccountCode = "1925"
					break
				}
			}
			return ls
		}), ErrVippsConfirmBankLine},
		{"another line on a configured bank account", edit(func(ls []VippsReconcileLine) []VippsReconcileLine {
			for i := range ls {
				if i != bankAt {
					ls[i].AccountCode = "1960"
					break
				}
			}
			return ls
		}), ErrVippsConfirmBankLine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.ReconcileVippsConfirm(ctx, f.club.ID, s.bankRow, "", f.club.Treasurer.ID, tc.lines)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := f.entryCount(t) - entries; got != 0 || f.bankEntry(t, s.bankRow) != nil {
				t.Fatalf("a refused confirm kept %d entries / a link", got)
			}
		})
	}
}

// A count refresh over several imports skips an import a booking holds FOR
// SHARE instead of waiting for it (it would hold the others meanwhile and
// could deadlock with a booking that share-locks two imports, BRY-674); the
// skipped import's count stays stale until the next refresh.
func TestRefreshMatchedCountsSkipsAShareLockedImport(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	side := newRaceSide(t, c)
	ctx := context.Background()
	free := c.NewBankImportOn(t, "1920")
	held := c.NewBankImportOn(t, "1925")
	for _, imp := range []string{free, held} {
		row := c.NewBankRow(t, imp, fin.BankRowOpts{Date: day(c.Year, 8, 1), Cents: -1500})
		if _, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, row, "6200", "expense", ""); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	lock := testutil.HoldRowLock(t, side.pool, `SELECT id FROM bank_imports WHERE id = $1 FOR SHARE`, held)
	defer lock.Release()

	done := make(chan struct{})
	go func() {
		svc.refreshMatchedCounts(ctx, c.ID, map[string]bool{free: true, held: true})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("refreshMatchedCounts waited for a share-locked import")
	}
	count := func(id string) int {
		var n int
		if err := side.pool.QueryRow(ctx, `SELECT matched_count FROM bank_imports WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(free) != 1 || count(held) != 0 {
		t.Fatalf("matched counts free %d, held %d; want 1 and the held one skipped (0)", count(free), count(held))
	}
	lock.Release()
	svc.refreshMatchedCounts(ctx, c.ID, map[string]bool{held: true})
	if count(held) != 1 {
		t.Fatalf("held import's count %d after the next refresh, want 1", count(held))
	}
}

// A reassign of an id that is not a UUID, or not the club's import, is
// ErrBankImportNotFound (the handler's 404), not a database error.
func TestReassignBankImportNotFound(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	for _, id := range []string{"not-a-uuid", "00000000-0000-4000-8000-000000000000"} {
		if _, err := svc.ReassignBankImport(ctx, c.ID, id, "1925"); !errors.Is(err, ErrBankImportNotFound) {
			t.Errorf("reassign %q: err = %v, want ErrBankImportNotFound", id, err)
		}
	}
}

// Unordered races of a reassign against each booking path, both orders: no
// deadlock, and a booking that stands is on the new account once the
// reassign committed.
func TestReassignRacingBookingsNeverLeavesTheOldAccount(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	side := newRaceSide(t, c)
	ctx := context.Background()
	for i := range 6 {
		// The KID cascade.
		date := day(c.Year, 9, 1+i)
		inv := c.NewInvoice(t, fin.InvoiceOpts{Cents: int64(61000 + i)})
		imp := c.NewBankImportOn(t, "1920")
		row := c.NewBankRow(t, imp, fin.BankRowOpts{Date: date, Cents: int64(61000 + i)})
		errKID, errRe := runBoth(i, func() error {
			return svc.bookKIDCascadeMatch(ctx, c.ID, kidCascadeMatch{
				rowID: row, invoiceID: inv.ID, actorID: c.Treasurer.ID, bankAccount: "1920",
				kid: inv.KID, desc: "Synthetic", date: date, amount: fin.Kr(int64(61000 + i)),
			})
		}, func() error { return side.reassign(ctx, c.ID, imp, "1925") })
		noDeadlock(t, i, errKID, errRe)
		if errKID != nil || errRe != nil {
			t.Fatalf("round %d: KID err %v, reassign err %v", i, errKID, errRe)
		}
		if _, lines := rowEntryCodes(t, c, row); lines["1920"] != [2]float64{} || lines["1925"][0] == 0 {
			t.Fatalf("round %d: KID entry lines %v, want the bank line on 1925 only", i, lines)
		}

		// Transfer detection.
		out, in, outImport := transferPairRows(t, c, int64(62000+i), day(c.Year, 9, 10+i), day(c.Year, 9, 10+i))
		inImport := importOf(t, c, in)
		var matched int
		errDet, errRe := runBoth(i, func() error {
			var err error
			matched, _, _, err = svc.detectIntraBankTransfers(ctx, c.ID, outImport, "", c.Treasurer.ID)
			return err
		}, func() error { return side.reassign(ctx, c.ID, inImport, "1900") })
		noDeadlock(t, i, errDet, errRe)
		if errDet != nil || errRe != nil || matched != 1 {
			t.Fatalf("round %d: detect matched %d err %v, reassign err %v", i, matched, errDet, errRe)
		}
		if _, lines := rowEntryCodes(t, c, out); lines["1925"] != [2]float64{} || lines["1900"][0] == 0 {
			t.Fatalf("round %d: transfer lines %v, want DR 1900 and nothing on 1925", i, lines)
		}

		// The group booking.
		gImp := c.NewBankImportOn(t, "1920")
		ids := []string{
			c.NewBankRow(t, gImp, fin.BankRowOpts{Date: day(c.Year, 10, 1+i), Cents: int64(30000 + i)}),
			c.NewBankRow(t, gImp, fin.BankRowOpts{Date: day(c.Year, 10, 1+i), Cents: -10000}),
		}
		errGroup, errRe := runBoth(i, func() error {
			_, err := svc.AssignBankRowsToAccountGroup(ctx, c.ID, c.Treasurer.ID, ids, "3900", "")
			return err
		}, func() error { return side.reassign(ctx, c.ID, gImp, "1925") })
		noDeadlock(t, i, errGroup, errRe)
		if errGroup != nil || errRe != nil {
			t.Fatalf("round %d: group err %v, reassign err %v", i, errGroup, errRe)
		}
		if _, lines := rowEntryCodes(t, c, ids[0]); lines["1920"] != [2]float64{} || lines["1925"][0] == 0 {
			t.Fatalf("round %d: group lines %v, want the bank line on 1925 only", i, lines)
		}
	}
}
