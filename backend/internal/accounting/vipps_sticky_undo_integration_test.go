package accounting

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A person's undo is final for the automatic passes (BRY-565, ACC-13(b)).
// For each pass: it books a row, a person undoes it (the row is marked
// auto_declined_at), the pass runs again and leaves the row, counted as
// declined where it reports counts, and a person's booking books it and lifts
// the marker. Then the guarded links: a pass that read the row before the
// undo cannot book it after. TestRunAutomaticBookingHonoursDeclines
// (auto_booking_integration_test.go) runs the matrix through
// RunAutomaticBooking.

// declinedRow reports whether a bank_import_rows or vipps_import_rows row
// carries a person's decline.
func declinedRow(t *testing.T, c *fin.Club, table, rowID string) bool {
	t.Helper()
	var d bool
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT auto_declined_at IS NOT NULL FROM `+table+` WHERE id = $1`, rowID).Scan(&d); err != nil {
		t.Fatalf("read %s %s: %v", table, rowID, err)
	}
	return d
}

func declinedBank(t *testing.T, c *fin.Club, rowID string) bool {
	t.Helper()
	return declinedRow(t, c, "bank_import_rows", rowID)
}

func declinedVipps(t *testing.T, c *fin.Club, rowID string) bool {
	t.Helper()
	return declinedRow(t, c, "vipps_import_rows", rowID)
}

func stickyExec(t *testing.T, c *fin.Club, sql string, args ...any) {
	t.Helper()
	if _, err := c.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func stickyBankEntry(t *testing.T, c *fin.Club, rowID string) string {
	t.Helper()
	var id *string
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1`, rowID).Scan(&id); err != nil {
		t.Fatalf("read bank row: %v", err)
	}
	if id == nil {
		return ""
	}
	return *id
}

// newStickyClub is a club on the per-charge Vipps model with both
// auto-assign switches on.
func newStickyClub(t *testing.T, opts ...testutil.DBOption) (*Service, *fin.Club) {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, opts...)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	stickyExec(t, club, `UPDATE clubs SET vipps_auto_assign = true, bank_auto_assign = true, vipps_per_charge_enabled = true
	                       WHERE id = $1`, club.ID)
	return svc, club
}

// The order reconcile, undone by a ledger void (which covers the void
// release): the pass declines the charge, a person books it in Tildel.
func TestStickyUndoOrderReconcile(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s, inv := f.orderSettlement(t, "3700101", 2, false)
	charge := s.charges[0]

	res, err := f.svc.AutoReconcileOrderCharges(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil || res.Booked != 1 {
		t.Fatalf("first pass = %+v (err %v), want the order charge booked", res, err)
	}
	entry := *f.chargeEntry(t, charge)
	if declinedVipps(t, f.club, charge) {
		t.Fatal("a booked charge carries a decline")
	}

	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, entry, f.club.Treasurer.ID); err != nil {
		t.Fatalf("void: %v", err)
	}
	if !declinedVipps(t, f.club, charge) {
		t.Fatal("the ledger void did not decline the released charge")
	}

	res, err = f.svc.AutoReconcileOrderCharges(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil || res.Booked != 0 || res.DeclinedSkipped != 1 {
		t.Fatalf("pass after the void = %+v (err %v), want the charge skipped as declined", res, err)
	}
	if f.chargeEntry(t, charge) != nil || f.invoicePaid(t, inv.ID) {
		t.Fatal("the order reconcile booked a charge a person voided")
	}

	// A person books it by hand in Tildel (Søk faktura).
	r, err := f.svc.AssignVippsChargeToInvoice(ctx, f.club.ID, charge, inv.ID, f.club.Treasurer.ID)
	if err != nil || r.AlreadyReconciled {
		t.Fatalf("assign by hand = %+v (err %v)", r, err)
	}
	if declinedVipps(t, f.club, charge) || !f.invoicePaid(t, inv.ID) {
		t.Error("a person's booking left the charge declined or the faktura unpaid")
	}
	if res, err := f.svc.AutoReconcileOrderCharges(ctx, f.club.ID, f.club.Treasurer.ID); err != nil || res.Booked != 0 || res.DeclinedSkipped != 0 {
		t.Errorf("pass after the person's booking = %+v (err %v), want nothing to do", res, err)
	}
}

func TestStickyUndoVippsAutoAssign(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	f.priceItem(t, "service", "Synthetic slipping", 80000, "once", "all")
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, CustomerName: "Synthetic Walkup"})

	if res := f.run(t); len(res.Assigned) != 1 {
		t.Fatalf("first run = %+v, want the charge booked", res)
	}
	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !declinedVipps(t, f.club, row) {
		t.Fatal("UnassignVippsCharge did not decline the charge")
	}

	res := f.run(t)
	if len(res.Assigned) != 0 || res.DeclinedSkipped != 1 || res.NeedsReview != 1 {
		t.Fatalf("run after the undo = %+v, want the charge declined and waiting for a person", res)
	}
	if f.rowEntry(t, row) != "" {
		t.Fatal("auto-assign booked a charge a person undid")
	}

	account, _ := RevenueAccount("service")
	if _, err := f.svc.CreateVippsChargeBilag(ctx, f.club.ID, row, account, "", f.club.Treasurer.ID); err != nil {
		t.Fatalf("Lag bilag: %v", err)
	}
	if f.rowEntry(t, row) == "" || declinedVipps(t, f.club, row) {
		t.Error("a person's Lag bilag did not book the charge or left it declined")
	}
	if res := f.run(t); len(res.Assigned) != 0 || res.DeclinedSkipped != 0 {
		t.Errorf("run after the person's booking = %+v, want nothing to do", res)
	}
}

// The backlog release is a person's undo too.
func TestStickyUndoReleaseFromBacklog(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 77700, CustomerName: "Synthetic Nobody"})
	f.draftCharge(t, row, receivablesAccountCode, 77700)

	if _, err := f.svc.ReleaseVippsBacklogCharge(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("release: %v", err)
	}
	if !declinedVipps(t, f.club, row) {
		t.Fatal("the backlog release did not decline the charge")
	}
	// A price item now fits: without the decline auto-assign would book it.
	f.priceItem(t, "service", "Synthetic fitting item", 77700, "once", "all")
	res := f.run(t)
	if len(res.Assigned) != 0 || res.DeclinedSkipped != 1 || f.rowEntry(t, row) != "" {
		t.Fatalf("run after the release = %+v, want the charge declined", res)
	}
}

func TestStickyUndoBankAutoAssign(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Sticky")
	inv := f.faktura(t, m, 45000)
	row := f.deposit(t, "Synthetic Sticky", 45000)

	if res := f.run(t); len(res.Assigned) != 1 {
		t.Fatalf("first run = %+v, want the deposit booked", res)
	}
	if _, err := f.svc.UnassignBankRowReported(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !declinedBank(t, f.club, row) {
		t.Fatal("UnassignBankRow did not decline the row")
	}

	res := f.run(t)
	if len(res.Assigned) != 0 || res.ByReason[reasonDeclined] != 1 || res.DeclinedSkipped != 1 || res.NeedsReview != 1 {
		t.Fatalf("run after the undo = %+v, want the row declined", res)
	}
	if p, err := f.svc.PreviewBankAutoAssign(ctx, f.club.ID); err != nil || p.WouldAssign != 0 || p.ByReason[reasonDeclined] != 1 {
		t.Errorf("preview = %+v (err %v), want the row counted as declined", p, err)
	}
	synced, err := f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil || synced.AutoAssign == nil || len(synced.AutoAssign.Assigned) != 0 || synced.AutoAssign.DeclinedSkipped != 1 {
		t.Fatalf("sync = %+v (err %v), want the row declined", synced.AutoAssign, err)
	}
	if f.entry(t, row) != "" || f.paid(t, inv.ID) {
		t.Fatal("an automatic pass booked a row a person undid")
	}

	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID); err != nil {
		t.Fatalf("assign by hand: %v", err)
	}
	if f.entry(t, row) == "" || declinedBank(t, f.club, row) {
		t.Error("a person's assign did not book the row or left it declined")
	}
	if res := f.run(t); res.DeclinedSkipped != 0 {
		t.Errorf("run after the person's booking = %+v, want no declined row", res)
	}
}

// Undoing one row of a multi-row faktura assign declines every row it
// releases.
func TestStickyUndoDeclinesSiblings(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Split")
	inv := f.faktura(t, m, 60000)
	r1 := f.deposit(t, "Synthetic Split", 30000)
	r2 := f.deposit(t, "Synthetic Split", 30000)
	if _, err := f.svc.AssignMultipleBankRowsToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, []string{r1, r2}, inv.ID); err != nil {
		t.Fatalf("multi-row assign: %v", err)
	}
	res, err := f.svc.UnassignBankRowReported(ctx, f.club.ID, f.club.Treasurer.ID, r1)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !slices.Contains(res.SiblingRowIDs, r2) {
		t.Fatalf("siblings = %v, want %s", res.SiblingRowIDs, r2)
	}
	if !declinedBank(t, f.club, r1) || !declinedBank(t, f.club, r2) {
		t.Error("the unassign did not decline the row and its sibling")
	}
}

func TestStickyUndoKIDMatch(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	inv := c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	row := c.NewBankRow(t, c.NewBankImport(t), fin.BankRowOpts{Cents: 45000, KID: inv.KID, Description: "Synthetic KID payment"})
	sync := func() *BankSyncResult {
		t.Helper()
		s, err := svc.BankSync(ctx, c.ID, c.Treasurer.ID)
		if err != nil {
			t.Fatalf("BankSync: %v", err)
		}
		return s
	}
	if s := sync(); s.KIDMatched != 1 {
		t.Fatalf("first sync KIDMatched = %d, want 1", s.KIDMatched)
	}
	if _, _, err := svc.UnassignBankRow(ctx, c.ID, c.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !declinedBank(t, c, row) {
		t.Fatal("unassign did not decline the KID row")
	}
	if s := sync(); s.KIDMatched != 0 || stickyBankEntry(t, c, row) != "" {
		t.Fatalf("sync after the undo matched %d, row entry %q; want the row left", s.KIDMatched, stickyBankEntry(t, c, row))
	}
	if _, err := svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, row, inv.ID); err != nil {
		t.Fatalf("assign by hand: %v", err)
	}
	if declinedBank(t, c, row) {
		t.Error("a person's assign left the KID row declined")
	}
}

// ACC-11: "Angre innbetalingen" declines the deposit, so the sweep skips it
// (not as "no deposit"); a person rebooking a charge of the settlement lifts
// the decline, and the next sweep clears the settlement with a new entry.
func TestAngreInnbetalingenLiftedByRebookingACharge(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3800101", 2, []int64{120000, 80000}, 5000, true)
	f.book(t, s.charges...)
	first := f.sweep(t)
	if _, ok := settlementsOf(first.Cleared)[s.number]; !ok {
		t.Fatalf("first sweep = %+v, want the settlement cleared", first)
	}
	firstEntry := *f.bankEntry(t, s.bankRow)

	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, s.bankRow); err != nil {
		t.Fatalf("Angre innbetalingen: %v", err)
	}
	if !declinedBank(t, f.club, s.bankRow) {
		t.Fatal("Angre innbetalingen did not decline the deposit")
	}
	// The released per-charge payout row is not declined: no pass books it
	// on its own.
	var payoutRow string
	var payoutLinked bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id::text, journal_entry_id IS NOT NULL FROM vipps_import_rows
		  WHERE club_id = $1 AND row_type = 'payout' AND settlement_number = $2`, f.club.ID, s.number,
	).Scan(&payoutRow, &payoutLinked); err != nil {
		t.Fatalf("payout row: %v", err)
	}
	if payoutLinked || declinedVipps(t, f.club, payoutRow) {
		t.Errorf("payout row linked=%v declined=%v, want released and not declined", payoutLinked, declinedVipps(t, f.club, payoutRow))
	}

	sw := f.sweep(t)
	if sw.DeclinedSkipped != 1 || len(sw.Cleared) != 0 {
		t.Fatalf("sweep after Angre = %+v, want the deposit skipped as declined", sw)
	}
	var mentions int
	for _, it := range sw.Skipped {
		if it.SettlementNumber == s.number {
			mentions++
			if it.Reason != declinedDepositReason || it.BankRowID != s.bankRow {
				t.Errorf("skipped item = %+v, want the declined reason on the deposit", it)
			}
		}
	}
	if mentions != 1 {
		t.Errorf("settlement is listed %d times as skipped, want once (not also as having no deposit)", mentions)
	}
	if bs := f.bankSync(t); bs.VippsPayouts.DeclinedSkipped != 1 || len(bs.VippsPayouts.Cleared) != 0 {
		t.Errorf("bank sync sweep = %+v, want the deposit declined", bs.VippsPayouts)
	}
	if f.bankEntry(t, s.bankRow) != nil {
		t.Fatal("an automatic pass cleared a deposit a person undid")
	}

	// The person corrects a charge: undo it and book it again.
	if err := f.unassign(s.charges[0]); err != nil {
		t.Fatalf("unassign the charge: %v", err)
	}
	if !declinedBank(t, f.club, s.bankRow) {
		t.Fatal("undoing a charge lifted the deposit's decline")
	}
	f.book(t, s.charges[0])
	if declinedBank(t, f.club, s.bankRow) || declinedVipps(t, f.club, s.charges[0]) {
		t.Fatal("rebooking a charge did not lift the deposit's (and the charge's) decline")
	}

	again := f.sweep(t)
	if _, ok := settlementsOf(again.Cleared)[s.number]; !ok || again.DeclinedSkipped != 0 {
		t.Fatalf("sweep after the rebooking = %+v, want the settlement cleared again", again)
	}
	if e := f.bankEntry(t, s.bankRow); e == nil || *e == firstEntry {
		t.Errorf("deposit entry = %v, want a new payout entry", e)
	}
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net = %.2f, want 0", got)
	}
}

// The lift reaches only the charge's own settlement (MSN + booking date), and
// dismissing a charge (a refund) lifts nothing: the deposit then waits for a
// person's manual clearing (Utlign utbetaling).
func TestSettlementLiftIsScopedAndDismissLiftsNothing(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	a := f.settlement(t, "3900101", 2, []int64{50000, 30000}, 2000, true)
	b := f.settlement(t, "3900102", 3, []int64{40000}, 1000, true)
	f.book(t, a.charges...)
	f.sweep(t)
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, a.bankRow); err != nil {
		t.Fatalf("Angre innbetalingen: %v", err)
	}

	// Same MSN, another booking date: booking its charge lifts nothing on a.
	f.book(t, b.charges...)
	if !declinedBank(t, f.club, a.bankRow) {
		t.Fatal("booking another settlement's charge lifted this deposit's decline")
	}

	// Undo a charge of a and dismiss it: nothing lifts.
	if err := f.unassign(a.charges[1]); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if err := f.svc.DismissVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, a.charges[1], "duplicate"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if !declinedBank(t, f.club, a.bankRow) {
		t.Fatal("dismissing a charge lifted the deposit's decline")
	}
	if sw := f.sweep(t); sw.DeclinedSkipped != 1 {
		t.Errorf("sweep = %+v, want the deposit still declined", sw)
	}
}

// The guarded links: a pass that read a row before a person declined it does
// not book it after.
func TestDeclinedRowNotBookedByRacingPass(t *testing.T) {
	ctx := context.Background()

	t.Run("vipps charge, pool-backed mark", func(t *testing.T) {
		svc, c := newStickyClub(t)
		row := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 12300, CustomerName: "Synthetic Race"})
		stale, err := svc.loadVippsBelastning(ctx, c.ID, row)
		if err != nil {
			t.Fatal(err)
		}
		stickyExec(t, c, `UPDATE vipps_import_rows SET auto_declined_at = now() WHERE id = $1`, row)
		before := stickyEntries(t, c)
		if _, err := svc.postVippsCharge(ctx, c.ID, c.Treasurer.ID, fallbackRevenueCode, "Synthetic race", stale, automaticBooking); !errors.Is(err, errAutoDeclined) {
			t.Fatalf("automatic mark on a declined charge: err = %v, want errAutoDeclined", err)
		}
		if got := stickyEntries(t, c); got != before {
			t.Errorf("the lost mark left %d entries behind", got-before)
		}
		// A person's booking of the same charge is not guarded, and lifts it.
		if _, err := svc.postVippsCharge(ctx, c.ID, c.Treasurer.ID, fallbackRevenueCode, "Synthetic race", stale, personBooking); err != nil {
			t.Fatalf("person's booking: %v", err)
		}
		if declinedVipps(t, c, row) {
			t.Error("a person's booking left the charge declined")
		}
	})

	t.Run("vipps charge, mark in the lock transaction", func(t *testing.T) {
		svc, c := newStickyClub(t)
		row := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 12300, CustomerName: "Synthetic Race"})
		stale, err := svc.loadVippsBelastning(ctx, c.ID, row)
		if err != nil {
			t.Fatal(err)
		}
		stickyExec(t, c, `UPDATE vipps_import_rows SET auto_declined_at = now() WHERE id = $1`, row)
		before := stickyEntries(t, c)
		tx, err := c.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// A failed assertion must not leave the transaction holding a pool
		// connection, or the pool's close at cleanup waits for ever.
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := postVippsChargeTx(ctx, tx, c.ID, c.Treasurer.ID, fallbackRevenueCode, "Synthetic race", stale, automaticBooking); !errors.Is(err, errAutoDeclined) {
			t.Fatalf("automatic mark on a declined charge: err = %v, want errAutoDeclined", err)
		}
		// The loss rolled back to the savepoint: committing keeps nothing.
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit after the loss: %v", err)
		}
		if got := stickyEntries(t, c); got != before {
			t.Errorf("the lost mark committed %d entries", got-before)
		}
		if !declinedVipps(t, c, row) {
			t.Error("the charge lost its decline")
		}
	})

	t.Run("bank row link", func(t *testing.T) {
		svc, c := newStickyClub(t)
		row := c.NewBankRow(t, c.NewBankImport(t), fin.BankRowOpts{Cents: 45000, Counterpart: "Synthetic Race"})
		stickyExec(t, c, `UPDATE bank_import_rows SET auto_declined_at = now() WHERE id = $1`, row)
		e, err := svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: c.PeriodID, EntryDate: fmtDate(c.Year, 6, 1), Description: "Synthetic race",
			Source: "bank_import", CreatedBy: c.Treasurer.ID, ClubID: c.ID,
			Lines: []CreateJournalLineInput{{AccountCode: "1920", Debit: 450}, {AccountCode: "1500", Credit: 450}},
		})
		if err != nil {
			t.Fatal(err)
		}
		tx, err := c.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := linkBankRowsTx(ctx, tx, c.ID, e.ID, []string{row}, nil, true); !errors.Is(err, errAutoDeclined) {
			t.Fatalf("automatic link of a declined row: err = %v, want errAutoDeclined", err)
		}
	})

	t.Run("legacy confirm, a charge declined after the preview", func(t *testing.T) {
		f := newLegacyVippsFixture(t)
		f.createYear(t, "open")
		preview, err := f.svc.ReconcileVippsPreview(ctx, f.club.ID, f.bankRow)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE vipps_import_rows SET auto_declined_at = now()
		            WHERE club_id = $1 AND row_type = 'belastning' AND customer_name = 'Synthetic Walkup'`, f.club.ID)
		before := f.entries(t)
		if _, err := f.svc.reconcileVippsConfirm(ctx, f.club.ID, f.bankRow, "", f.club.Treasurer.ID, preview.Lines, true, automaticBooking); !errors.Is(err, errAutoDeclined) {
			t.Fatalf("automatic confirm with a declined charge: err = %v, want errAutoDeclined", err)
		}
		if f.entries(t) != before || stickyBankEntry(t, f.club, f.bankRow) != "" {
			t.Error("the refused confirm booked something")
		}
	})

	// The sweep reads the deposit, then waits for the settlement's lock; a
	// person's undo lands meanwhile. The guarded link refuses, and the sweep
	// reports the deposit declined, not cleared and not failed.
	t.Run("vipps payout sweep", func(t *testing.T) {
		f := newPayoutSweepFixture(t)
		s := f.settlement(t, "3900201", 4, []int64{60000}, 1000, true)
		f.book(t, s.charges...)
		lock := testutil.HoldRowLock(t, f.club.Pool,
			`SELECT 1 FROM vipps_import_rows WHERE club_id = $1 AND row_type = 'payout' AND msn = $2 FOR UPDATE`,
			f.club.ID, sweepMSN)
		done := make(chan error, 1)
		go func() {
			_, err := f.svc.clearVippsPayout(ctx, f.club.ID, s.bankRow, f.club.Treasurer.ID, automaticBooking)
			done <- err
		}()
		lock.WaitForBlocked(20 * time.Second)
		f.exec(t, `UPDATE bank_import_rows SET auto_declined_at = now() WHERE id = $1`, s.bankRow)
		lock.Release()
		err := <-done
		if !errors.Is(err, errAutoDeclined) {
			t.Fatalf("racing clear: err = %v, want errAutoDeclined", err)
		}
		if reason, skip := payoutSweepReason(err); !skip || reason != declinedDepositReason {
			t.Errorf("sweep reason = %q skip=%v, want the declined reason, skipped", reason, skip)
		}
		if f.bankEntry(t, s.bankRow) != nil {
			t.Error("the racing sweep cleared a declined deposit")
		}
	})
}

func stickyEntries(t *testing.T, c *fin.Club) int {
	t.Helper()
	var n int
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, c.ID).Scan(&n); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return n
}

func fmtDate(y int, m time.Month, d int) string {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

// Bank sync's legacy Vipps pass (pass 2): an undone settlement is declined
// with its charges (not its fee and payout rows) and left; a person's confirm
// books it and lifts every decline.
func TestStickyUndoLegacyPass2(t *testing.T) {
	f := newLegacyVippsFixture(t)
	ctx := context.Background()
	f.createYear(t, "open")
	if s := f.sync(t); s.VippsReconciled != 1 {
		t.Fatalf("first sync reconciled %d, want 1", s.VippsReconciled)
	}
	res, err := f.svc.UnassignBankRowReported(ctx, f.club.ID, f.club.Treasurer.ID, f.bankRow)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !declinedBank(t, f.club, f.bankRow) {
		t.Fatal("the deposit is not declined")
	}
	byType := map[string][2]int{} // row type → (released, declined)
	for _, id := range res.ReleasedVippsRowIDs {
		var typ string
		if err := f.club.Pool.QueryRow(ctx, `SELECT row_type FROM vipps_import_rows WHERE id = $1`, id).Scan(&typ); err != nil {
			t.Fatal(err)
		}
		c := byType[typ]
		c[0]++
		if declinedVipps(t, f.club, id) {
			c[1]++
		}
		byType[typ] = c
	}
	if byType["belastning"] != [2]int{2, 2} || byType["fee"][1] != 0 || byType["payout"][1] != 0 {
		t.Errorf("released (count, declined) by type = %v; want both charges declined, fee and payout not", byType)
	}

	s := f.sync(t)
	if s.VippsReconciled != 0 || s.LegacyVippsDeclined != 1 || s.VippsUnbalanced != 0 || stickyBankEntry(t, f.club, f.bankRow) != "" {
		t.Fatalf("sync after the undo = reconciled %d, declined %d, unbalanced %d; want the deposit declined",
			s.VippsReconciled, s.LegacyVippsDeclined, s.VippsUnbalanced)
	}

	preview, err := f.svc.ReconcileVippsPreview(ctx, f.club.ID, f.bankRow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ReconcileVippsConfirm(ctx, f.club.ID, f.bankRow, "", f.club.Treasurer.ID, preview.Lines); err != nil {
		t.Fatalf("person's confirm: %v", err)
	}
	if declinedBank(t, f.club, f.bankRow) {
		t.Error("the person's confirm left the deposit declined")
	}
	for _, id := range res.ReleasedVippsRowIDs {
		if declinedVipps(t, f.club, id) {
			t.Errorf("vipps row %s still declined after the person's confirm", id)
		}
	}
}

// Intra-bank transfer pairing: an undone transfer declines both rows, and the
// next sync does not pair them again.
func TestStickyUndoTransfers(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	rowA := c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Cents: -100000, Description: "Synthetic transfer out"})
	rowB := c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Cents: 100000, Description: "Synthetic transfer in"})
	sync := func() *BankSyncResult {
		t.Helper()
		s, err := svc.BankSync(ctx, c.ID, c.Treasurer.ID)
		if err != nil {
			t.Fatalf("BankSync: %v", err)
		}
		return s
	}
	if s := sync(); s.TransfersLinked != 1 {
		t.Fatalf("first sync linked %d transfers, want 1", s.TransfersLinked)
	}
	res, err := svc.UnassignBankRowReported(ctx, c.ID, c.Treasurer.ID, rowA)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !slices.Contains(res.SiblingRowIDs, rowB) || !declinedBank(t, c, rowA) || !declinedBank(t, c, rowB) {
		t.Fatalf("unassign = %+v; want both rows released and declined", res)
	}
	if s := sync(); s.TransfersLinked != 0 || stickyBankEntry(t, c, rowA) != "" || stickyBankEntry(t, c, rowB) != "" {
		t.Fatalf("sync after the undo linked %d transfers; want none", s.TransfersLinked)
	}
	// One side declined is enough to keep the pair apart.
	stickyExec(t, c, `UPDATE bank_import_rows SET auto_declined_at = NULL WHERE id = $1`, rowB)
	if s := sync(); s.TransfersLinked != 0 {
		t.Fatalf("sync with one side declined linked %d transfers; want none", s.TransfersLinked)
	}
	// A person's booking of a side lifts its decline. Pairing both rows by
	// hand (PairBankTransfer) is covered in bank_row_transfer_integration_test.go.
	if _, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, rowA, "6200", "expense", ""); err != nil {
		t.Fatalf("AssignBankRowToAccount: %v", err)
	}
	if declinedBank(t, c, rowA) {
		t.Error("a person's booking left the row declined")
	}
}

// The Stripe payout sweep skips a deposit a person undid (in its plan, in
// Tildel's reason and under the clearing's row lock); a person's manual
// clearing books it and lifts the decline.
func TestStickyUndoStripePayoutSweep(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	po := f.insertPayout(t, payoutSpec{id: "po_test_sticky", amountOre: 6000, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_sticky_fee", category: "fee", amountOre: 6000,
		created: time.Date(f.club.Year, 6, 8, 10, 0, 0, 0, time.UTC)})
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 10), 6000, "STRIPE")
	f.book(t)
	if first := f.sweep(t); sweepCounts(first) != [4]int{1, 0, 0, 0} {
		t.Fatalf("first sweep = %+v, want cleared", first)
	}
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !declinedBank(t, f.club, row) {
		t.Fatal("unassign did not decline the Stripe deposit")
	}

	second := f.sweep(t)
	if sweepCounts(second) != [4]int{0, 0, 1, 0} || second.DeclinedSkipped != 1 ||
		second.Skipped[0].Reason != stripeReasonDeclined || second.Skipped[0].BankRowID != row {
		t.Fatalf("sweep after the undo = %+v, want the payout skipped as declined", second)
	}
	payouts, err := loadProviderPayouts(ctx, f.club.Pool, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	reasons, err := f.svc.stripePayoutRowReasons(ctx, f.club.ID, payouts)
	if err != nil {
		t.Fatal(err)
	}
	if st := reasons[row]; st.reason != stripeReasonDeclined || !st.byHand {
		t.Errorf("Tildel status = %+v, want the declined reason, by hand", st)
	}
	// Under the lock: an automatic clearing of the pair refuses it.
	out, err := f.svc.clearStripePayout(ctx, f.club.ID, f.club.Treasurer.ID, po, row, automaticStripeClear)
	if err != nil || out.state != stripePlanSkipped || out.code != stripeClearDeclined {
		t.Fatalf("automatic clearing = %+v (err %v), want skipped as declined", out, err)
	}
	if stickyBankEntry(t, f.club, row) != "" {
		t.Fatal("the sweep booked a declined deposit")
	}

	// A person's manual clearing still works and lifts the decline.
	if _, err := f.svc.ClearStripePayoutManually(ctx, f.club.ID, f.club.Treasurer.ID, po, row); err != nil {
		t.Fatalf("manual clearing: %v", err)
	}
	if stickyBankEntry(t, f.club, row) == "" || declinedBank(t, f.club, row) {
		t.Error("the manual clearing did not book the deposit or left it declined")
	}
	if b := f.accountBalance(t, "1580"); !floatNear(b, 0, 0.001) {
		t.Errorf("1580 balance = %v, want 0", b)
	}
}

// A person's booking that links, posts and unwinds outside one transaction
// keeps the decline when the post fails (B1), and lifts it once it succeeds.
func TestFailedPostKeepsTheDecline(t *testing.T) {
	ctx := context.Background()
	failPosts := func(t *testing.T, c *fin.Club) {
		t.Helper()
		stickyExec(t, c, `CREATE FUNCTION sticky_fail_post() RETURNS trigger LANGUAGE plpgsql AS $$
		  BEGIN
		    IF OLD.status = 'draft' AND NEW.status = 'posted' AND OLD.description LIKE '%STICKYFAIL%' THEN
		      RAISE EXCEPTION 'synthetic post failure';
		    END IF;
		    RETURN NEW;
		  END $$`)
		stickyExec(t, c, `CREATE TRIGGER sticky_fail_post BEFORE UPDATE ON journal_entries
		                    FOR EACH ROW EXECUTE FUNCTION sticky_fail_post()`)
	}
	allowPosts := func(t *testing.T, c *fin.Club) {
		t.Helper()
		stickyExec(t, c, `DROP TRIGGER sticky_fail_post ON journal_entries`)
	}

	t.Run("vipps Lag bilag", func(t *testing.T) {
		svc, c := newStickyClub(t)
		row := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 25000, CustomerName: "STICKYFAIL Payer"})
		stickyExec(t, c, `UPDATE vipps_import_rows SET auto_declined_at = now() WHERE id = $1`, row)
		failPosts(t, c)
		before := stickyEntries(t, c)
		if _, err := svc.CreateVippsChargeBilag(ctx, c.ID, row, fallbackRevenueCode, "", c.Treasurer.ID); err == nil {
			t.Fatal("the post did not fail")
		}
		if !declinedVipps(t, c, row) || stickyEntries(t, c) != before {
			t.Fatalf("failed post: declined=%v, entries %d → %d; want the decline kept and no draft", declinedVipps(t, c, row), before, stickyEntries(t, c))
		}
		allowPosts(t, c)
		if _, err := svc.CreateVippsChargeBilag(ctx, c.ID, row, fallbackRevenueCode, "", c.Treasurer.ID); err != nil {
			t.Fatalf("Lag bilag: %v", err)
		}
		if declinedVipps(t, c, row) {
			t.Error("the successful booking left the charge declined")
		}
	})

	t.Run("bank account assign", func(t *testing.T) {
		svc, c := newStickyClub(t)
		row := c.NewBankRow(t, c.NewBankImport(t), fin.BankRowOpts{Cents: 25000, Description: "Synthetic gift"})
		stickyExec(t, c, `UPDATE bank_import_rows SET auto_declined_at = now() WHERE id = $1`, row)
		failPosts(t, c)
		if _, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, row, "3900", "revenue", "STICKYFAIL gift"); err == nil {
			t.Fatal("the post did not fail")
		}
		if !declinedBank(t, c, row) || stickyBankEntry(t, c, row) != "" {
			t.Fatalf("failed post: declined=%v entry=%q; want the decline kept and the row unlinked", declinedBank(t, c, row), stickyBankEntry(t, c, row))
		}
		allowPosts(t, c)
		if _, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, row, "3900", "revenue", "Synthetic gift"); err != nil {
			t.Fatalf("assign: %v", err)
		}
		if declinedBank(t, c, row) {
			t.Error("the successful booking left the row declined")
		}
	})
}

// A reimport replaces unbooked rows from the file; a declined row is kept, so
// it does not come back without its decline and get booked again.
func TestReimportKeepsDeclinedRows(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	imp := c.NewBankImport(t)
	rows := []BankRow{{Date: time.Date(c.Year, 6, 3, 0, 0, 0, 0, time.UTC), Amount: 300, Reference: "SYNSTICKY1", Description: "Synthetic transfer"}}
	if _, err := svc.ImportBankRows(ctx, c.ID, imp, "", rows); err != nil {
		t.Fatalf("import: %v", err)
	}
	var row string
	if err := c.Pool.QueryRow(ctx, `SELECT id::text FROM bank_import_rows WHERE club_id = $1 AND reference = 'SYNSTICKY1'`, c.ID).Scan(&row); err != nil {
		t.Fatal(err)
	}
	stickyExec(t, c, `UPDATE bank_import_rows SET auto_declined_at = now() WHERE id = $1`, row)

	if _, err := svc.ReimportBankRows(ctx, c.ID, imp, "", rows); err != nil {
		t.Fatalf("reimport: %v", err)
	}
	var n int
	if err := c.Pool.QueryRow(ctx, `SELECT count(*) FROM bank_import_rows WHERE club_id = $1 AND reference = 'SYNSTICKY1'`, c.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !declinedBank(t, c, row) {
		t.Errorf("after the reimport: %d rows, declined=%v; want the one declined row kept", n, declinedBank(t, c, row))
	}

	// A corrected file changes the row's text, so its hash: the kept declined
	// row still represents it (date, amount, arkivref), and no unmarked twin
	// is inserted for the automatic passes to book.
	corrected := []BankRow{{Date: rows[0].Date, Amount: 300, Reference: "SYNSTICKY1", Description: "Synthetic transfer, corrected text"}}
	res, err := svc.ReimportBankRows(ctx, c.ID, imp, "", corrected)
	if err != nil {
		t.Fatalf("corrected reimport: %v", err)
	}
	if err := c.Pool.QueryRow(ctx, `SELECT count(*) FROM bank_import_rows WHERE club_id = $1 AND row_date = $2 AND amount = 300`, c.ID, rows[0].Date).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 || res.SkippedBooked != 1 || res.Imported != 0 {
		t.Errorf("corrected reimport: %d rows, skipped %d, imported %d; want the declined row alone, the parsed one skipped", n, res.SkippedBooked, res.Imported)
	}

	// The same for an originally mis-parsed row whose arkivref landed in the
	// KID column, dismissed by a person, in a full-account reimport.
	day := time.Date(c.Year, 6, 4, 0, 0, 0, 0, time.UTC)
	dismissed := c.NewBankRow(t, imp, fin.BankRowOpts{Date: day, Cents: 40000, KID: "SYNSTICKY2", Reference: "-", Description: "Synthetic misparsed"})
	stickyExec(t, c, `UPDATE bank_import_rows SET dismissed_at = now(), dismissed_by = $2, dismissed_reason = 'duplicate' WHERE id = $1`, dismissed, c.Treasurer.ID)
	acct, err := svc.ReimportAccountBankRows(ctx, c.ID, "1920", imp, "",
		[]BankRow{{Date: day, Amount: 400, Reference: "SYNSTICKY2", Description: "Synthetic parsed right"}})
	if err != nil {
		t.Fatalf("account reimport: %v", err)
	}
	if err := c.Pool.QueryRow(ctx, `SELECT count(*) FROM bank_import_rows WHERE club_id = $1 AND row_date = $2 AND amount = 400`, c.ID, day).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 || acct.SkippedBooked != 1 {
		t.Errorf("account reimport: %d rows, skipped %d; want the dismissed row alone", n, acct.SkippedBooked)
	}

	// A kept row with a real arkivref is matched on it alone: its KID column
	// equalling another row's arkivref is no evidence of the same booking, so
	// that row is imported.
	day2 := time.Date(c.Year, 6, 5, 0, 0, 0, 0, time.UTC)
	other := c.NewBankRow(t, imp, fin.BankRowOpts{Date: day2, Cents: 50000, KID: "SYNSTICKYX", Reference: "SYNSTICKYR1", Description: "Synthetic kept"})
	stickyExec(t, c, `UPDATE bank_import_rows SET dismissed_at = now(), dismissed_by = $2, dismissed_reason = 'duplicate' WHERE id = $1`, other, c.Treasurer.ID)
	neg, err := svc.ReimportBankRows(ctx, c.ID, imp, "",
		[]BankRow{{Date: day2, Amount: 500, Reference: "SYNSTICKYX", Description: "Synthetic other booking"}})
	if err != nil {
		t.Fatalf("reimport with a KID-equal arkivref: %v", err)
	}
	if err := c.Pool.QueryRow(ctx, `SELECT count(*) FROM bank_import_rows WHERE club_id = $1 AND row_date = $2 AND amount = 500`, c.ID, day2).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 || neg.SkippedBooked != 0 || neg.Imported != 1 {
		t.Errorf("KID-equal arkivref: %d rows, skipped %d, imported %d; want the parsed row imported", n, neg.SkippedBooked, neg.Imported)
	}
}

// Only declined rows in a year with no fiscal period: nothing waits for the
// year (BRY-575 counts what an automatic pass would book, and a pass leaves
// a declined row alone).
func TestWaitingFiscalYearsLeavesOutDeclinedRows(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	day := time.Date(c.Year+1, 1, 3, 0, 0, 0, 0, time.UTC)
	imp := c.NewBankImport(t)
	inv := c.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	kid := c.NewBankRow(t, imp, fin.BankRowOpts{Date: day, Cents: 45000, KID: inv.KID, Description: "Synthetic KID payment"})
	charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: day})
	deposit := c.NewBankRow(t, imp, fin.BankRowOpts{Date: day, Cents: 29000, Description: "Utb. 4100001 Vippsnr 000001"})
	for _, id := range []string{kid, deposit} {
		stickyExec(t, c, `UPDATE bank_import_rows SET auto_declined_at = now() WHERE id = $1`, id)
	}
	stickyExec(t, c, `UPDATE vipps_import_rows SET auto_declined_at = now() WHERE id = $1`, charge)
	got, err := svc.WaitingFiscalYears(ctx, c.ID)
	if err != nil {
		t.Fatalf("WaitingFiscalYears: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("waiting = %+v, want nothing: every row is declined", got)
	}
	// Control: undeclined, the same rows wait for the year.
	stickyExec(t, c, `UPDATE bank_import_rows SET auto_declined_at = NULL WHERE id = $1`, kid)
	if got, err := svc.WaitingFiscalYears(ctx, c.ID); err != nil || len(got) != 1 || got[0].Year != c.Year+1 {
		t.Errorf("control: waiting = %+v (err %v), want the KID row waiting for %d", got, err, c.Year+1)
	}
}
