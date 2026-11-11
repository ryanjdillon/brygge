package accounting

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// BRY-578: the KID cascade, intra-bank transfer pairing and bank sync's
// legacy Vipps pass never open a fiscal year, whoever started the import or
// sync. The club has a 2026 period and nothing else; the payments are dated
// 1 January 2027.

var bankNoPeriodDay = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

type bankNoPeriodFixture struct {
	*noPeriodFixture
}

// newBankNoPeriodFixture is a club with only its 2026 period. opts tune the
// test database (a small pool for the lock tests).
func newBankNoPeriodFixture(t *testing.T, opts ...testutil.DBOption) *bankNoPeriodFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, opts...)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &bankNoPeriodFixture{&noPeriodFixture{svc: svc, club: club, date: bankNoPeriodDay}}
	if got := f.periods(t); got != 1 {
		t.Fatalf("fixture has %d fiscal periods, want only 2026", got)
	}
	return f
}

// rowEntryID is the journal entry a bank row is linked to, "" when unbooked.
func (f *bankNoPeriodFixture) rowEntryID(t *testing.T, rowID string) string {
	t.Helper()
	var id *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1`, rowID).Scan(&id); err != nil {
		t.Fatalf("read bank row: %v", err)
	}
	if id == nil {
		return ""
	}
	return *id
}

// rowByReference is the ID of the bank row an import stored for reference.
func (f *bankNoPeriodFixture) rowByReference(t *testing.T, reference string) string {
	t.Helper()
	var id string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT id::text FROM bank_import_rows WHERE club_id = $1 AND reference = $2`, f.club.ID, reference).Scan(&id); err != nil {
		t.Fatalf("read bank row %s: %v", reference, err)
	}
	return id
}

func (f *bankNoPeriodFixture) sync(t *testing.T) *BankSyncResult {
	t.Helper()
	res, err := f.svc.BankSync(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("BankSync: %v", err)
	}
	return res
}

func (f *bankNoPeriodFixture) createYear(t *testing.T, status string) *FiscalPeriod {
	t.Helper()
	p, err := f.svc.CreatePeriod(context.Background(), f.club.ID, 2027)
	if err != nil {
		t.Fatalf("CreatePeriod: %v", err)
	}
	if status != "open" {
		f.exec(t, `UPDATE fiscal_periods SET status = $2::fiscal_period_status WHERE id = $1`, p.ID, status)
	}
	return p
}

// assertBookedIn checks that entryID is dated on the payment's day in period.
func (f *bankNoPeriodFixture) assertBookedIn(t *testing.T, what, entryID, periodID string) {
	t.Helper()
	date, got := f.entry(t, entryID)
	if date != "2027-01-01" || got != periodID {
		t.Errorf("%s entry dated %s in period %s, want 2027-01-01 in period %s", what, date, got, periodID)
	}
}

func wantYears(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// kidRow is a bank statement row paying inv by KID on the 2027 day.
func kidRow(inv fin.Invoice, reference string) BankRow {
	return BankRow{
		Date: bankNoPeriodDay, Amount: fin.Kr(inv.TotalCents), KID: inv.KID,
		Reference: reference, Description: "Synthetic KID payment",
	}
}

func TestKIDCascadeNeverCreatesFiscalPeriod(t *testing.T) {
	ctx := context.Background()

	t.Run("import, then sync, then the year is created", func(t *testing.T) {
		f := newBankNoPeriodFixture(t)
		inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
		imp := f.club.NewBankImport(t)
		entriesBefore := f.entries(t)

		res, err := f.svc.ImportBankRows(ctx, f.club.ID, imp, "", []BankRow{kidRow(inv, "SYNKID1")})
		if err != nil {
			t.Fatalf("ImportBankRows: %v", err)
		}
		row := f.rowByReference(t, "SYNKID1")
		if res.Matched != 0 || len(res.RowErrors) != 0 || len(res.ClosedPeriods) != 0 {
			t.Errorf("import matched %d, row errors %+v, closed %v; want the row to wait without an error",
				res.Matched, res.RowErrors, res.ClosedPeriods)
		}
		wantYears(t, "import MissingPeriods", res.MissingPeriods, "2027")
		if f.periods(t) != 1 || f.entries(t) != entriesBefore || f.rowEntryID(t, row) != "" || f.invoicePaid(t, inv.ID) {
			t.Fatalf("import opened a year or booked the row")
		}

		s := f.sync(t)
		if s.KIDMatched != 0 {
			t.Errorf("sync KIDMatched = %d, want 0", s.KIDMatched)
		}
		wantYears(t, "sync MissingPeriods", s.MissingPeriods, "2027")
		if f.periods(t) != 1 || f.entries(t) != entriesBefore || f.rowEntryID(t, row) != "" {
			t.Fatalf("sync opened a year or booked the row")
		}

		p := f.createYear(t, "open")
		s = f.sync(t)
		if s.KIDMatched != 1 || len(s.MissingPeriods) != 0 {
			t.Fatalf("after the year exists: KIDMatched %d, MissingPeriods %v; want the row booked", s.KIDMatched, s.MissingPeriods)
		}
		f.assertBookedIn(t, "KID", f.rowEntryID(t, row), p.ID)
		if !f.invoicePaid(t, inv.ID) {
			t.Errorf("faktura not marked paid once booked")
		}
		if got := f.periods(t); got != 2 {
			t.Errorf("fiscal_periods rows = %d, want 2026 and 2027", got)
		}
	})

	// An import's period override is a person's choice of an existing
	// period: the row is booked there and no period is created.
	t.Run("import period override", func(t *testing.T) {
		f := newBankNoPeriodFixture(t)
		inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
		imp := f.club.NewBankImport(t)

		res, err := f.svc.ImportBankRows(ctx, f.club.ID, imp, f.club.PeriodID, []BankRow{kidRow(inv, "SYNKID2")})
		if err != nil {
			t.Fatalf("ImportBankRows: %v", err)
		}
		if res.Matched != 1 || len(res.MissingPeriods) != 0 || len(res.RowErrors) != 0 {
			t.Fatalf("override import: matched %d, missing %v, row errors %+v; want the row booked", res.Matched, res.MissingPeriods, res.RowErrors)
		}
		f.assertBookedIn(t, "override", f.rowEntryID(t, f.rowByReference(t, "SYNKID2")), f.club.PeriodID)
		if got := f.periods(t); got != 1 {
			t.Errorf("fiscal_periods rows = %d, want only 2026", got)
		}
	})

	// The period is looked up on the transaction holding the faktura lock
	// (BRY-433): with a one-connection pool, a lookup on the pool would hang.
	t.Run("one connection", func(t *testing.T) {
		f := newBankNoPeriodFixture(t, testutil.WithMaxConns(1))
		inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
		imp := f.club.NewBankImport(t)
		row := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: bankNoPeriodDay, Cents: inv.TotalCents, KID: inv.KID})
		tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		m := kidCascadeMatch{
			rowID: row, invoiceID: inv.ID, actorID: f.club.Treasurer.ID, bankAccount: "1920",
			kid: inv.KID, desc: "Synthetic", date: bankNoPeriodDay, amount: fin.Kr(inv.TotalCents),
		}
		if err := f.svc.bookKIDCascadeMatch(tctx, f.club.ID, m); !errors.Is(err, errNoFiscalPeriod) {
			t.Fatalf("no year: err = %v, want errNoFiscalPeriod", err)
		}
		p := f.createYear(t, "open")
		if err := f.svc.bookKIDCascadeMatch(tctx, f.club.ID, m); err != nil {
			t.Fatalf("with the year: %v", err)
		}
		f.assertBookedIn(t, "KID", f.rowEntryID(t, row), p.ID)
	})
}

// transferPair seeds one side of an intra-bank transfer on a second bank
// account (1925) and returns the import on 1920 the other side is uploaded to.
func (f *bankNoPeriodFixture) transferPair(t *testing.T) (importA, rowB string) {
	t.Helper()
	importA = f.club.NewBankImportOn(t, "1920")
	impB := f.club.NewBankImportOn(t, "1925")
	rowB = f.club.NewBankRow(t, impB, fin.BankRowOpts{Date: bankNoPeriodDay, Cents: 100000, Description: "Synthetic transfer in"})
	return importA, rowB
}

func transferOut(reference string) BankRow {
	return BankRow{Date: bankNoPeriodDay, Amount: -1000, Reference: reference, Description: "Synthetic transfer out"}
}

func TestTransferDetectionNeverCreatesFiscalPeriod(t *testing.T) {
	ctx := context.Background()

	t.Run("import, then sync, then the year is created", func(t *testing.T) {
		f := newBankNoPeriodFixture(t)
		impA, rowB := f.transferPair(t)
		entriesBefore := f.entries(t)

		res, err := f.svc.ImportBankRows(ctx, f.club.ID, impA, "", []BankRow{transferOut("SYNTR1")})
		if err != nil {
			t.Fatalf("ImportBankRows: %v", err)
		}
		rowA := f.rowByReference(t, "SYNTR1")
		if res.Transfers != 0 || len(res.ClosedPeriods) != 0 {
			t.Errorf("import transfers %d, closed %v; want none", res.Transfers, res.ClosedPeriods)
		}
		wantYears(t, "import MissingPeriods", res.MissingPeriods, "2027")
		if f.periods(t) != 1 || f.entries(t) != entriesBefore {
			t.Fatalf("import opened a year or drafted the transfer")
		}

		s := f.sync(t)
		if s.TransfersLinked != 0 {
			t.Errorf("sync TransfersLinked = %d, want 0", s.TransfersLinked)
		}
		wantYears(t, "sync MissingPeriods", s.MissingPeriods, "2027")
		if f.periods(t) != 1 || f.entries(t) != entriesBefore {
			t.Fatalf("sync opened a year or drafted the transfer")
		}

		p := f.createYear(t, "open")
		s = f.sync(t)
		if s.TransfersLinked != 1 || len(s.MissingPeriods) != 0 {
			t.Fatalf("after the year exists: TransfersLinked %d, MissingPeriods %v; want the pair linked", s.TransfersLinked, s.MissingPeriods)
		}
		entry := f.rowEntryID(t, rowA)
		if entry == "" || f.rowEntryID(t, rowB) != entry {
			t.Fatalf("pair not linked to one entry: %q and %q", entry, f.rowEntryID(t, rowB))
		}
		f.assertBookedIn(t, "transfer", entry, p.ID)
	})

	// A locked period is skipped and reported, as a closed one: the old
	// status test only knew "closed", so a locked year reached the insert.
	for _, status := range []string{"locked", "closed"} {
		t.Run(status+" period", func(t *testing.T) {
			f := newBankNoPeriodFixture(t)
			impA, _ := f.transferPair(t)
			f.createYear(t, status)
			entriesBefore := f.entries(t)

			res, err := f.svc.ImportBankRows(ctx, f.club.ID, impA, "", []BankRow{transferOut("SYNTR2")})
			if err != nil {
				t.Fatalf("ImportBankRows: %v", err)
			}
			if res.Transfers != 0 || len(res.MissingPeriods) != 0 {
				t.Errorf("import transfers %d, missing %v; want none", res.Transfers, res.MissingPeriods)
			}
			wantYears(t, "import ClosedPeriods", res.ClosedPeriods, "2027")

			s := f.sync(t)
			if s.TransfersLinked != 0 {
				t.Errorf("sync TransfersLinked = %d, want 0", s.TransfersLinked)
			}
			wantYears(t, "sync ClosedPeriods", s.ClosedPeriods, "2027")
			if got := f.entries(t); got != entriesBefore {
				t.Errorf("journal entries = %d, want %d", got, entriesBefore)
			}
		})
	}
}

const legacyNoPeriodMSN = "000003"

type legacyVippsFixture struct {
	*bankNoPeriodFixture
	bankRow string
	invoice fin.Invoice
}

// newLegacyVippsFixture is a club on the legacy single-bilag Vipps model with
// one settlement: a member's faktura charge and a walk-up charge booked on
// 31 December 2026, a fee, and the payout deposited on 1 January 2027.
func newLegacyVippsFixture(t *testing.T, opts ...testutil.DBOption) *legacyVippsFixture {
	t.Helper()
	f := &legacyVippsFixture{bankNoPeriodFixture: newBankNoPeriodFixture(t, opts...)}
	c := f.club
	f.exec(t, `UPDATE clubs SET vipps_per_charge_enabled = false WHERE id = $1`, c.ID)
	payer := c.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = 'Payertwo' WHERE id = $1`, payer.ID)
	// The legacy preview links a member's charge only to a faktura issued in
	// the last 90 days by the real clock (findOpenInvoiceForMember:
	// issue_date > CURRENT_DATE - 90 days), so the faktura is issued 10 days
	// before now rather than on a fixed date. That keeps it inside the window
	// on any run date, and before the 2026-12-31 charge (and the 2027-01-01
	// deposit) only while now is on or before 2027-01-10: run later, the
	// faktura is dated after the payment it settles. Tests that also use the
	// fixed 2026 club year share this coupling to the clock.
	issued := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	f.invoice = c.NewInvoice(t, fin.InvoiceOpts{Member: &payer, IssueDate: issued, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})

	booking := bankNoPeriodDay.AddDate(0, 0, -1)
	c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: booking, MSN: legacyNoPeriodMSN, CustomerName: "Synthetic Payertwo"})
	c.NewVippsRow(t, fin.VippsRowOpts{Cents: 20000, Date: booking, MSN: legacyNoPeriodMSN, CustomerName: "Synthetic Walkup"})
	c.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -1000, Date: booking, MSN: legacyNoPeriodMSN})
	c.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -64000, Date: booking, MSN: legacyNoPeriodMSN, Settlement: "4000002"})
	f.bankRow = c.NewBankRow(t, c.NewBankImport(t), fin.BankRowOpts{
		Date: bankNoPeriodDay, Cents: 64000, Description: "Utb. 4000002 Vippsnr " + legacyNoPeriodMSN,
	})
	preview, err := f.svc.ReconcileVippsPreview(context.Background(), c.ID, f.bankRow)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !preview.Balanced {
		t.Fatalf("fixture settlement does not balance: %s", preview.Reason)
	}
	if !slices.ContainsFunc(preview.Lines, func(l VippsReconcileLine) bool { return l.LinkedInvoiceID == f.invoice.ID }) {
		t.Fatalf("fixture member charge is not linked to the faktura: %+v", preview.Lines)
	}
	return f
}

func TestLegacyVippsConfirmNeverCreatesFiscalPeriodInSync(t *testing.T) {
	ctx := context.Background()

	t.Run("sync waits, then books once the year is created", func(t *testing.T) {
		f := newLegacyVippsFixture(t)
		entriesBefore := f.entries(t)

		preview, err := f.svc.ReconcileVippsPreview(ctx, f.club.ID, f.bankRow)
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		if !preview.PeriodMissing || preview.PeriodClosed {
			t.Errorf("preview PeriodMissing %v, PeriodClosed %v; want missing", preview.PeriodMissing, preview.PeriodClosed)
		}

		s := f.sync(t)
		if s.VippsReconciled != 0 || s.VippsUnbalanced != 0 {
			t.Errorf("sync reconciled %d, unbalanced %d; want the deposit to wait, not count as unbalanced", s.VippsReconciled, s.VippsUnbalanced)
		}
		wantYears(t, "sync MissingPeriods", s.MissingPeriods, "2027")
		if f.periods(t) != 1 || f.entries(t) != entriesBefore || f.rowEntryID(t, f.bankRow) != "" || f.invoicePaid(t, f.invoice.ID) {
			t.Fatalf("sync opened a year or booked the settlement")
		}

		p := f.createYear(t, "open")
		s = f.sync(t)
		if s.VippsReconciled != 1 || s.VippsUnbalanced != 0 || len(s.MissingPeriods) != 0 {
			t.Fatalf("after the year exists: reconciled %d, unbalanced %d, missing %v; want the settlement booked",
				s.VippsReconciled, s.VippsUnbalanced, s.MissingPeriods)
		}
		f.assertBookedIn(t, "settlement", f.rowEntryID(t, f.bankRow), p.ID)
		if !f.invoicePaid(t, f.invoice.ID) {
			t.Errorf("faktura not marked paid once booked")
		}
	})

	// A person's confirm keeps today's behaviour and opens the year.
	t.Run("human confirm still creates the year", func(t *testing.T) {
		f := newLegacyVippsFixture(t)
		preview, err := f.svc.ReconcileVippsPreview(ctx, f.club.ID, f.bankRow)
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		entryID, err := f.svc.ReconcileVippsConfirm(ctx, f.club.ID, f.bankRow, "", f.club.Treasurer.ID, preview.Lines)
		if err != nil {
			t.Fatalf("ReconcileVippsConfirm: %v", err)
		}
		_, periodID := f.entry(t, entryID)
		var year int
		var status string
		if err := f.club.Pool.QueryRow(ctx, `SELECT year, status FROM fiscal_periods WHERE id = $1`, periodID).Scan(&year, &status); err != nil {
			t.Fatalf("read period: %v", err)
		}
		if year != 2027 || status != "open" || f.periods(t) != 2 {
			t.Errorf("confirm booked in the %d period (%s), %d periods; want a new open 2027 period", year, status, f.periods(t))
		}
		f.assertBookedIn(t, "settlement", entryID, periodID)
	})

	for _, status := range []string{"locked", "closed"} {
		t.Run(status+" period is reported closed, not unbalanced", func(t *testing.T) {
			f := newLegacyVippsFixture(t)
			f.createYear(t, status)
			entriesBefore := f.entries(t)

			s := f.sync(t)
			if s.VippsReconciled != 0 || s.VippsUnbalanced != 0 || len(s.MissingPeriods) != 0 {
				t.Errorf("sync reconciled %d, unbalanced %d, missing %v; want the deposit left as closed",
					s.VippsReconciled, s.VippsUnbalanced, s.MissingPeriods)
			}
			wantYears(t, "sync ClosedPeriods", s.ClosedPeriods, "2027")
			if got := f.entries(t); got != entriesBefore {
				t.Errorf("journal entries = %d, want %d", got, entriesBefore)
			}

			// A person's confirm is refused before any entry, and a locked
			// period is not offered as reopenable (ReopenPeriod only reopens
			// a closed one).
			preview, err := f.svc.ReconcileVippsPreview(ctx, f.club.ID, f.bankRow)
			if err != nil {
				t.Fatalf("preview: %v", err)
			}
			if !preview.PeriodClosed || preview.PeriodLocked != (status == "locked") {
				t.Errorf("preview PeriodClosed %v, PeriodLocked %v for a %s period", preview.PeriodClosed, preview.PeriodLocked, status)
			}
			want := "reopen it"
			if status == "locked" {
				want = "cannot be reopened in Brygge"
			}
			_, err = f.svc.ReconcileVippsConfirm(ctx, f.club.ID, f.bankRow, "", f.club.Treasurer.ID, preview.Lines)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("confirm err = %v, want one saying %q", err, want)
			}
			if got := f.entries(t); got != entriesBefore {
				t.Errorf("journal entries after confirm = %d, want %d", got, entriesBefore)
			}
		})
	}

	// The existing-only confirm looks the period up on the transaction that
	// holds the faktura locks (BRY-433): with a one-connection pool, a lookup
	// on the pool would hang.
	t.Run("existing-only confirm on one connection", func(t *testing.T) {
		f := newLegacyVippsFixture(t, testutil.WithMaxConns(1))
		preview, err := f.svc.ReconcileVippsPreview(ctx, f.club.ID, f.bankRow)
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		_, err = f.svc.reconcileVippsConfirm(tctx, f.club.ID, f.bankRow, "", f.club.Treasurer.ID, preview.Lines, true, periodExistingOnly)
		if !errors.Is(err, errNoFiscalPeriod) {
			t.Fatalf("no year: err = %v, want errNoFiscalPeriod", err)
		}
		if f.periods(t) != 1 || f.invoicePaid(t, f.invoice.ID) {
			t.Fatalf("existing-only confirm opened a year or paid the faktura")
		}
		p := f.createYear(t, "open")
		entryID, err := f.svc.reconcileVippsConfirm(tctx, f.club.ID, f.bankRow, "", f.club.Treasurer.ID, preview.Lines, true, periodExistingOnly)
		if err != nil {
			t.Fatalf("with the year: %v", err)
		}
		f.assertBookedIn(t, "settlement", entryID, p.ID)
	})
}

// The other automatic passes that leave a payment for a missing year (bank
// auto-assign's no_period, the Vipps payout sweep) fold that year into the
// same MissingPeriods list, so the result names every year to create once.
func TestBankSyncFoldsEveryMissingYear(t *testing.T) {
	t.Run("bank auto-assign", func(t *testing.T) {
		f := newBankNoPeriodFixture(t)
		f.exec(t, `UPDATE clubs SET bank_auto_assign = true WHERE id = $1`, f.club.ID)
		row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{
			Date: bankNoPeriodDay, Cents: 45000, Counterpart: "Synthetic Payerthree",
		})

		s := f.sync(t)
		if s.AutoAssign == nil || s.AutoAssign.ByReason[reasonNoPeriod] != 1 {
			t.Fatalf("auto-assign = %+v, want the deposit skipped as no_period", s.AutoAssign)
		}
		wantYears(t, "sync MissingPeriods", s.MissingPeriods, "2027")
		if f.periods(t) != 1 || f.rowEntryID(t, row) != "" {
			t.Errorf("sync opened a year or booked the deposit")
		}
	})

	t.Run("vipps payout sweep", func(t *testing.T) {
		f := newNoPeriodFixture(t, bankNoPeriodDay)
		s, err := f.svc.BankSync(context.Background(), f.club.ID, f.club.Treasurer.ID)
		if err != nil {
			t.Fatalf("BankSync: %v", err)
		}
		if s.VippsPayouts == nil || len(s.VippsPayouts.Skipped) != 1 {
			t.Fatalf("sweep = %+v, want the settlement skipped", s.VippsPayouts)
		}
		wantYears(t, "sync MissingPeriods", s.MissingPeriods, "2027")
		if f.periods(t) != 1 {
			t.Errorf("sync opened a year")
		}
	})
}
