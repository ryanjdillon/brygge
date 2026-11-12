package accounting

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// BRY-669: every pass that leaves a payment for a closed or locked fiscal
// period reports its year in ClosedPeriods, as a missing year is reported in
// MissingPeriods, on the upload, the sync and the automatic run alike.

// An upload with a chosen open period books its own deposit there even when
// the deposit's own year has no fiscal period, and never opens that year.
func TestBankAutoAssignImportOverrideBooksRowWithoutItsOwnYear(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Overstyrtny")
	f.faktura(t, m, 45000)
	imp := f.club.NewBankImport(t)
	next := f.club.Year + 1

	res, err := f.svc.ImportBankRows(ctx, f.club.ID, imp, f.club.PeriodID, statement(next, "Synthetic Overstyrtny", "77770054322"))
	if err != nil || res.AutoAssign == nil || len(res.AutoAssign.Assigned) != 1 {
		t.Fatalf("import = %+v, err %v; want the deposit booked", res.AutoAssign, err)
	}
	var period string
	if err := f.club.Pool.QueryRow(ctx, `SELECT fiscal_period_id::text FROM journal_entries WHERE id = $1`,
		res.AutoAssign.Assigned[0].EntryID).Scan(&period); err != nil || period != f.club.PeriodID {
		t.Errorf("entry period = %s (err %v), want the override %s", period, err, f.club.PeriodID)
	}
	if len(res.MissingPeriods) != 0 || len(res.ClosedPeriods) != 0 {
		t.Errorf("missing %v closed %v; want neither for a row booked into the override", res.MissingPeriods, res.ClosedPeriods)
	}
	var n int
	if err := f.club.Pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE club_id = $1 AND year = $2`,
		f.club.ID, next).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d fiscal periods for %d (err %v); the upload opened the row's own year", n, next, err)
	}
}

// A closed override leaves the upload's deposit unbooked as period_closed,
// and the row's own year is listed as closed (the label is the row's year,
// as in every pass, not the override period's).
func TestBankAutoAssignImportOverrideClosed(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	var override string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status, closed_at)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'closed', now()) RETURNING id`,
		f.club.ID, f.club.Year-1).Scan(&override); err != nil {
		t.Fatalf("period: %v", err)
	}
	m := f.member(t, "Lukketny")
	inv := f.faktura(t, m, 45000)
	imp := f.club.NewBankImport(t)

	res, err := f.svc.ImportBankRows(ctx, f.club.ID, imp, override, statement(f.club.Year, "Synthetic Lukketny", "77770054323"))
	if err != nil || res.AutoAssign == nil {
		t.Fatalf("import = %+v, err %v", res.AutoAssign, err)
	}
	if len(res.AutoAssign.Assigned) != 0 || res.AutoAssign.ByReason[reasonPeriodClosed] != 1 {
		t.Errorf("auto-assign = %+v, want the deposit left as period_closed", res.AutoAssign)
	}
	wantYears(t, "import ClosedPeriods", res.ClosedPeriods, strconv.Itoa(f.club.Year))
	if f.paid(t, inv.ID) {
		t.Error("the faktura was paid through a closed override")
	}
}

func TestBankSyncFoldsEveryClosedYear(t *testing.T) {
	for _, status := range []string{"closed", "locked"} {
		t.Run("bank auto-assign "+status, func(t *testing.T) {
			f := newBankNoPeriodFixture(t)
			f.exec(t, `UPDATE clubs SET bank_auto_assign = true WHERE id = $1`, f.club.ID)
			f.createYear(t, status)
			row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{
				Date: bankNoPeriodDay, Cents: 45000, Counterpart: "Synthetic Payerfour",
			})

			s := f.sync(t)
			if s.AutoAssign == nil || s.AutoAssign.ByReason[reasonPeriodClosed] != 1 {
				t.Fatalf("auto-assign = %+v, want the deposit skipped as period_closed", s.AutoAssign)
			}
			wantYears(t, "sync ClosedPeriods", s.ClosedPeriods, "2027")
			wantYears(t, "sync MissingPeriods", s.MissingPeriods)
			if f.rowEntryID(t, row) != "" {
				t.Error("sync booked the deposit into a period that is not open")
			}
		})
	}

	t.Run("vipps payout sweep", func(t *testing.T) {
		f := newNoPeriodFixture(t, bankNoPeriodDay)
		f.exec(t,
			`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status, closed_at)
			 VALUES ($1, 2027, '2027-01-01', '2027-12-31', 'locked', now())`, f.club.ID)
		s, err := f.svc.BankSync(context.Background(), f.club.ID, f.club.Treasurer.ID)
		if err != nil {
			t.Fatalf("BankSync: %v", err)
		}
		if s.VippsPayouts == nil || len(s.VippsPayouts.Failed) != 1 || s.VippsPayouts.Failed[0].Reason != "regnskapsperioden er låst" {
			t.Fatalf("sweep = %+v, want the settlement failed for the locked period", s.VippsPayouts)
		}
		wantYears(t, "sync ClosedPeriods", s.ClosedPeriods, "2027")
		if f.bankEntryID(t) != "" {
			t.Error("the deposit was cleared into a locked period")
		}
	})

	// The fee is booked on the settlement's date (31 December), the deposit
	// on 1 January: with the old year closed and the new one open, the
	// closed year reported is the fee's, not the deposit's.
	t.Run("vipps payout sweep, fee and deposit in different years", func(t *testing.T) {
		f := newNoPeriodFixture(t, bankNoPeriodDay)
		f.exec(t,
			`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
			 VALUES ($1, 2027, '2027-01-01', '2027-12-31', 'open')`, f.club.ID)
		f.exec(t, `UPDATE fiscal_periods SET status = 'closed', closed_at = now() WHERE id = $1`, f.club.PeriodID)
		s, err := f.svc.BankSync(context.Background(), f.club.ID, f.club.Treasurer.ID)
		if err != nil {
			t.Fatalf("BankSync: %v", err)
		}
		if s.VippsPayouts == nil || len(s.VippsPayouts.Failed) != 1 || s.VippsPayouts.Failed[0].Reason != "regnskapsperioden er lukket" {
			t.Fatalf("sweep = %+v, want the settlement failed for the closed fee year", s.VippsPayouts)
		}
		wantYears(t, "sync ClosedPeriods", s.ClosedPeriods, strconv.Itoa(f.club.Year))
		wantYears(t, "sync MissingPeriods", s.MissingPeriods)
	})

	t.Run("import", func(t *testing.T) {
		f := newBankNoPeriodFixture(t)
		f.exec(t, `UPDATE clubs SET bank_auto_assign = true WHERE id = $1`, f.club.ID)
		f.createYear(t, "closed")
		res, err := f.svc.ImportBankRows(context.Background(), f.club.ID, f.club.NewBankImport(t), "", []BankRow{{
			Date: bankNoPeriodDay, Amount: 450, Counterpart: "Synthetic Payerfive",
			Reference: "SYNCLOSED1", Description: "Synthetic giro",
		}})
		if err != nil {
			t.Fatalf("ImportBankRows: %v", err)
		}
		if res.AutoAssign == nil || res.AutoAssign.ByReason[reasonPeriodClosed] != 1 {
			t.Fatalf("auto-assign = %+v, want the deposit skipped as period_closed", res.AutoAssign)
		}
		wantYears(t, "import ClosedPeriods", res.ClosedPeriods, "2027")
		wantYears(t, "import MissingPeriods", res.MissingPeriods)
	})
}

// Vipps auto-assign records the year of a charge it left for a missing
// period and for a closed or locked one; before BRY-669 it recorded neither,
// so the run's lists never named a Vipps charge's year.
func TestVippsAutoAssignReportsMissingAndClosedYears(t *testing.T) {
	ctx := context.Background()
	f := newNoPeriodFixture(t, bankNoPeriodDay)
	run := func() *AutoAssignResult {
		t.Helper()
		res, err := f.svc.AutoAssignVippsCharges(ctx, f.club.ID, f.club.Treasurer.ID, VippsAutoAssignOpts{})
		if err != nil {
			t.Fatalf("AutoAssignVippsCharges: %v", err)
		}
		return res
	}

	res := run()
	if !slices.Equal(res.noPeriodYears(), []int{2027}) || len(res.closedPeriodYears()) != 0 {
		t.Errorf("missing %v closed %v, want [2027] and none", res.noPeriodYears(), res.closedPeriodYears())
	}
	if len(res.SkippedDetails) != 2 || res.SkippedDetails[0].Reason != "ingen regnskapsår for 2027" {
		t.Errorf("skipped = %+v, want both charges with the no-year reason", res.SkippedDetails)
	}

	f.exec(t,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status, closed_at)
		 VALUES ($1, 2027, '2027-01-01', '2027-12-31', 'locked', now())`, f.club.ID)
	res = run()
	if !slices.Equal(res.closedPeriodYears(), []int{2027}) || len(res.noPeriodYears()) != 0 {
		t.Errorf("missing %v closed %v, want none and [2027]", res.noPeriodYears(), res.closedPeriodYears())
	}
	if len(res.SkippedDetails) != 2 {
		t.Fatalf("skipped = %+v, want both charges", res.SkippedDetails)
	}
	for _, s := range res.SkippedDetails {
		if !strings.Contains(s.Reason, "2027 er låst") {
			t.Errorf("skipped %s with %q, want the locked reason", s.Ref, s.Reason)
		}
	}
}

// The automatic run lists the years of every pass: bank auto-assign's closed
// deposit, a Vipps charge in a year that does not exist and one in a locked
// year. A KID payment in the open year books, so the run writes its summary
// row, which carries the same lists.
func TestAutomaticBookingRunFoldsClosedAndMissingYears(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	wf := &waitFixture{svc: f.svc, club: f.club, imp: f.bankImp}
	wf.period(t, 2024, "closed", false)
	wf.period(t, 2023, "locked", false)

	closedDeposit := f.club.NewBankRow(t, f.bankImp, fin.BankRowOpts{
		Date: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), Cents: 45000,
		Counterpart: "Synthetic Payersix", FromAccount: "12345600" + testutil.RandomHex(2), Description: "Synthetic deposit",
	})
	missingCharge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, Date: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), CustomerName: "Synthetic Walkupa"})
	lockedCharge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, Date: time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC), CustomerName: "Synthetic Walkupb"})
	openKID, openInv := f.kidRow(t, time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC), 32000)

	res := f.runAuto(t, nil)
	if res.KIDMatched != 1 || f.bankRowEntry(t, openKID) == "" || !f.invoicePaid(t, openInv.ID) {
		t.Fatalf("KID matched %d; want the open year's row booked", res.KIDMatched)
	}
	if f.bankRowEntry(t, closedDeposit) != "" || rowEntry(t, f.club, missingCharge) != "" || rowEntry(t, f.club, lockedCharge) != "" {
		t.Error("a payment in a missing, closed or locked year was booked")
	}
	if res.BankAutoAssign == nil || res.BankAutoAssign.ByReason[reasonPeriodClosed] != 1 {
		t.Errorf("bank auto-assign = %+v, want the deposit left as period_closed", res.BankAutoAssign)
	}
	wantYears(t, "run ClosedPeriods", res.ClosedPeriods, "2023", "2024")
	wantYears(t, "run MissingPeriods", res.MissingPeriods, "2025")

	runs := f.autoRuns(t)
	if len(runs) != 1 {
		t.Fatalf("%d summary rows, want 1", len(runs))
	}
	list := func(key string) []string {
		raw, _ := runs[0].details[key].([]any)
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			s, _ := v.(string)
			out = append(out, s)
		}
		return out
	}
	wantYears(t, "audit closed_periods", list("closed_periods"), "2023", "2024")
	wantYears(t, "audit missing_periods", list("missing_periods"), "2025")
}
