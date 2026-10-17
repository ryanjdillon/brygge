package accounting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// importStatement runs a synthetic statement through the real import cascade
// on a fresh bank import, as an upload would.
func (f *payoutSweepFixture) importStatement(t *testing.T, rows ...BankRow) ImportResult {
	t.Helper()
	res, err := f.svc.ImportBankRows(context.Background(), f.club.ID, f.club.NewBankImport(t), "", rows)
	if err != nil {
		t.Fatalf("ImportBankRows: %v", err)
	}
	return res
}

func (f *payoutSweepFixture) rowByRef(t *testing.T, ref string) (id string, dismissedReason *string) {
	t.Helper()
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT id::text, dismissed_reason FROM bank_import_rows WHERE club_id = $1 AND reference = $2`,
		f.club.ID, ref).Scan(&id, &dismissedReason); err != nil {
		t.Fatalf("read bank row %s: %v", ref, err)
	}
	return id, dismissedReason
}

// provisional is the in-flight form of a transfer on the day a statement is
// exported; settled is the same transfer in a later export.
func provisional(d time.Time, kr float64, ref string) BankRow {
	return BankRow{Date: d, Amount: kr, Description: "Overførsel", Reference: ref}
}

func settledPayout(d time.Time, kr float64, settlement, ref string) BankRow {
	return BankRow{Date: d, Amount: kr, Description: "Utb. " + settlement + " Vippsnr " + sweepMSN,
		Counterpart: "Vipps Mobilepay As", Reference: ref}
}

func filler(d time.Time, ref string) BankRow {
	return BankRow{Date: d, Amount: 123.45, Description: "Synthetic giro", Counterpart: "Synthetic Payer", Reference: ref}
}

// A Vipps payout exported while in flight lands as a bare "Overførsel" on the
// export day. The sweep points at it; the next export's settled row
// supersedes it and clears the payout.
func TestProvisionalPayoutRowIsSupersededAndPayoutClears(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()

	s := f.settlement(t, "3000301", 10, []int64{30000, 10000}, 1000, false)
	f.book(t, s.charges...)
	exportDay := s.booking.AddDate(0, 0, 2)

	first := f.importStatement(t,
		filler(s.booking, "SYNP-A1"),
		provisional(exportDay, 390.00, "SYNP-A2"),
	)
	if first.LastRowDate != exportDay.Format("2006-01-02") || first.ProvisionalRows != 1 {
		t.Errorf("first import: last day %s, provisional rows %d; want %s and 1",
			first.LastRowDate, first.ProvisionalRows, exportDay.Format("2006-01-02"))
	}
	if first.Superseded != 0 {
		t.Errorf("first import superseded %d rows, want 0", first.Superseded)
	}

	sync := f.bankSync(t)
	reason := settlementsOf(sync.VippsPayouts.Skipped)[s.number]
	if !strings.Contains(reason, "foreløpig") || !strings.Contains(reason, exportDay.Format("02.01.2006")) {
		t.Errorf("payout with only a provisional deposit: reason %q, want the provisional hint for %s",
			reason, exportDay.Format("02.01.2006"))
	}
	provID, _ := f.rowByRef(t, "SYNP-A2")
	if f.bankEntry(t, provID) != nil {
		t.Fatalf("provisional row was booked")
	}

	// The next export starts at the export day and carries the settled row.
	second := f.importStatement(t,
		settledPayout(exportDay, 390.00, s.number, "SYNP-B1"),
		filler(exportDay.AddDate(0, 0, 3), "SYNP-B2"),
	)
	if second.Superseded != 1 || len(second.ProvisionalAmbiguous) != 0 {
		t.Errorf("second import: superseded %d, ambiguous %+v; want 1 and none", second.Superseded, second.ProvisionalAmbiguous)
	}
	if _, why := f.rowByRef(t, "SYNP-A2"); why == nil || *why != "superseded" {
		t.Errorf("provisional row dismissed as %v, want superseded", why)
	}
	var dismissedBy string
	if err := f.club.Pool.QueryRow(ctx, `SELECT dismissed_by::text FROM bank_import_rows WHERE id = $1`, provID).Scan(&dismissedBy); err != nil {
		t.Fatalf("read dismissed_by: %v", err)
	}
	if dismissedBy != f.club.Treasurer.ID {
		t.Errorf("dismissed_by = %s, want the importing treasurer", dismissedBy)
	}
	var audited int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE club_id = $1 AND resource_id = $2
		    AND action = 'bank_row.dismissed' AND details->>'reason' = 'superseded'`,
		f.club.ID, provID).Scan(&audited); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if audited != 1 {
		t.Errorf("audit rows for the supersede = %d, want 1", audited)
	}

	again := f.bankSync(t)
	if _, ok := settlementsOf(again.VippsPayouts.Cleared)[s.number]; !ok {
		t.Errorf("sync after the settled row arrived cleared %+v, want %s", again.VippsPayouts.Cleared, s.number)
	}
	finalID, _ := f.rowByRef(t, "SYNP-B1")
	if f.bankEntry(t, finalID) == nil {
		t.Errorf("settled row not booked by the sweep")
	}

	rerun, err := f.svc.SupersedeProvisionalBankRows(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if rerun.Superseded != 0 || len(rerun.Ambiguous) != 0 {
		t.Errorf("re-run = %+v, want nothing to do", rerun)
	}
}

// The settled row may already be in the books when an older export, taken
// while the transfer was in flight, is imported afterwards. Bank sync heals
// rows that were already present.
func TestProvisionalRowImportedAfterItsSettledRow(t *testing.T) {
	f := newPayoutSweepFixture(t)
	day := time.Date(f.club.Year, 7, 14, 0, 0, 0, 0, time.UTC)

	f.importStatement(t,
		settledPayout(day, 250.00, "3000401", "SYNR-B1"),
		filler(day.AddDate(0, 0, 2), "SYNR-B2"),
	)
	older := f.importStatement(t,
		filler(day.AddDate(0, 0, -3), "SYNR-A1"),
		provisional(day, 250.00, "SYNR-A2"),
	)
	if older.Superseded != 1 {
		t.Errorf("older export imported last: superseded %d, want 1", older.Superseded)
	}
	if _, why := f.rowByRef(t, "SYNR-A2"); why == nil || *why != "superseded" {
		t.Errorf("provisional row dismissed as %v, want superseded", why)
	}

	// Rows already present before this logic shipped: seeded directly, then
	// healed by bank sync.
	imp1, imp2 := f.club.NewBankImport(t), f.club.NewBankImport(t)
	later := day.AddDate(0, 0, 5)
	f.club.NewBankRow(t, imp1, fin.BankRowOpts{Date: later.AddDate(0, 0, -1), Cents: 1000, Description: "Synthetic giro"})
	prov := f.club.NewBankRow(t, imp1, fin.BankRowOpts{Date: later, Cents: 41265, Description: "Overførsel"})
	f.club.NewBankRow(t, imp2, fin.BankRowOpts{Date: later, Cents: 41265, Description: "Utb. 3000402 Vippsnr " + sweepMSN})
	res := f.bankSync(t)
	if res.ProvisionalSuperseded != 1 {
		t.Errorf("bank sync superseded %d rows, want 1", res.ProvisionalSuperseded)
	}
	var why *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT dismissed_reason FROM bank_import_rows WHERE id = $1`, prov).Scan(&why); err != nil {
		t.Fatalf("read provisional: %v", err)
	}
	if why == nil || *why != "superseded" {
		t.Errorf("pre-existing provisional row dismissed as %v, want superseded", why)
	}
}

// Anything not one to one is left for the treasurer, as are booked rows and
// bare rows that are not on their import's last day.
func TestProvisionalSupersedeLeavesUnclearCasesAlone(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	base := time.Date(f.club.Year, 8, 3, 0, 0, 0, 0, time.UTC)

	// Two provisional rows of the same amount on the export day, one settled row.
	f.importStatement(t,
		provisional(base, 294.75, "SYNA-1"),
		provisional(base, 294.75, "SYNA-2"),
	)
	twoProv := f.importStatement(t,
		settledPayout(base, 294.75, "3000501", "SYNA-3"),
		filler(base.AddDate(0, 0, 1), "SYNA-4"),
	)
	if twoProv.Superseded != 0 || len(twoProv.ProvisionalAmbiguous) != 2 {
		t.Errorf("two provisional rows, one settled: superseded %d, ambiguous %+v; want 0 and both reported",
			twoProv.Superseded, twoProv.ProvisionalAmbiguous)
	}

	// One provisional row, two settled rows of the same amount and day.
	d2 := base.AddDate(0, 0, 10)
	f.importStatement(t, filler(d2.AddDate(0, 0, -1), "SYNB-0"), provisional(d2, 500.00, "SYNB-1"))
	twoFinal := f.importStatement(t,
		settledPayout(d2, 500.00, "3000502", "SYNB-2"),
		settledPayout(d2, 500.00, "3000503", "SYNB-3"),
		filler(d2.AddDate(0, 0, 1), "SYNB-4"),
	)
	oneProvID, _ := f.rowByRef(t, "SYNB-1")
	candidates := 0
	for _, a := range twoFinal.ProvisionalAmbiguous {
		if a.RowID == oneProvID {
			candidates = a.Candidates
		}
	}
	if twoFinal.Superseded != 0 || candidates != 2 {
		t.Errorf("one provisional, two settled: superseded %d, ambiguous %+v; want 0 and the row reported with 2 candidates",
			twoFinal.Superseded, twoFinal.ProvisionalAmbiguous)
	}

	// A booked provisional row is never touched.
	d3 := base.AddDate(0, 0, 20)
	f.importStatement(t, filler(d3.AddDate(0, 0, -1), "SYNC-0"), provisional(d3, 77.00, "SYNC-1"))
	bookedID, _ := f.rowByRef(t, "SYNC-1")
	if _, err := f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, bookedID, "3900", "revenue", "synthetic"); err != nil {
		t.Fatalf("book provisional row: %v", err)
	}
	// A bare row in the middle of a statement is a settled transfer with a
	// terse text, not an in-flight one.
	d4 := base.AddDate(0, 0, 30)
	f.importStatement(t, BankRow{Date: d4, Amount: 88.00, Description: "Overførsel", Reference: "SYND-1"},
		filler(d4.AddDate(0, 0, 2), "SYND-2"))
	f.importStatement(t, BankRow{Date: d4, Amount: 88.00, Description: "Synthetic sender", Counterpart: "Synthetic Payer", Reference: "SYND-3"},
		filler(d4.AddDate(0, 0, 3), "SYND-4"))
	f.importStatement(t, BankRow{Date: d3, Amount: 77.00, Description: "Synthetic sender", Counterpart: "Synthetic Payer", Reference: "SYNC-2"},
		filler(d3.AddDate(0, 0, 3), "SYNC-3"))

	for _, ref := range []string{"SYNA-1", "SYNA-2", "SYNB-1", "SYNC-1", "SYND-1"} {
		if _, why := f.rowByRef(t, ref); why != nil {
			t.Errorf("row %s dismissed as %s, want it left alone", ref, *why)
		}
	}
	if f.bankEntry(t, bookedID) == nil {
		t.Errorf("booked provisional row lost its booking")
	}
}

// The bank usually keeps the reference (arkivref) when a provisional row
// settles, so the settled row would be skipped as already imported. Instead
// the provisional row takes the settled text in place, and the payout clears.
func TestSettledRowRefreshesProvisionalRowWithSameReference(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()

	s := f.settlement(t, "3000601", 12, []int64{50000}, 1250, false)
	f.book(t, s.charges...)
	exportDay := s.booking.AddDate(0, 0, 1)

	f.importStatement(t, filler(s.booking, "SYNS-1"), provisional(exportDay, 487.50, "SYNS-2"))
	provID, _ := f.rowByRef(t, "SYNS-2")

	settled := settledPayout(exportDay, 487.50, s.number, "SYNS-2")
	res := f.importStatement(t, filler(s.booking, "SYNS-1"), settled, filler(exportDay.AddDate(0, 0, 2), "SYNS-3"))
	if res.Refreshed != 1 || res.Imported != 1 || res.SkippedDup != 1 {
		t.Errorf("re-export: refreshed %d, imported %d, skipped %d; want 1, 1 (the new day) and 1 (the old filler)",
			res.Refreshed, res.Imported, res.SkippedDup)
	}
	var id, desc, counterpart, hash string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id::text, description, counterpart, row_hash FROM bank_import_rows WHERE club_id = $1 AND reference = 'SYNS-2'`,
		f.club.ID).Scan(&id, &desc, &counterpart, &hash); err != nil {
		t.Fatalf("read refreshed row: %v", err)
	}
	if id != provID || desc != settled.Description || counterpart != settled.Counterpart {
		t.Errorf("refreshed row = %s %q %q, want %s with the settled text", id, desc, counterpart, provID)
	}
	if hash != BankRowHash(f.club.ID, settled) {
		t.Errorf("refreshed row kept its provisional hash")
	}
	var audited int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE club_id = $1 AND resource_id = $2 AND action = 'bank_row.refreshed'`,
		f.club.ID, provID).Scan(&audited); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if audited != 1 {
		t.Errorf("audit rows for the refresh = %d, want 1", audited)
	}

	sync := f.bankSync(t)
	if _, ok := settlementsOf(sync.VippsPayouts.Cleared)[s.number]; !ok {
		t.Errorf("sync cleared %+v, want %s", sync.VippsPayouts.Cleared, s.number)
	}
	if f.bankEntry(t, provID) == nil {
		t.Errorf("refreshed row not booked by the sweep")
	}

	// Importing the same statement again changes nothing.
	again := f.importStatement(t, filler(s.booking, "SYNS-1"), settled, filler(exportDay.AddDate(0, 0, 2), "SYNS-3"))
	if again.Refreshed != 0 || again.Imported != 0 || again.SkippedDup != 3 {
		t.Errorf("same statement again: refreshed %d, imported %d, skipped %d; want 0, 0, 3",
			again.Refreshed, again.Imported, again.SkippedDup)
	}
}

// Only an open provisional row is refreshed; booked or dismissed rows with the
// reference are skipped as before, and a date beyond the gap is reported.
func TestReferenceRefreshLeavesSettledHistoryAlone(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	day := time.Date(f.club.Year, 9, 1, 0, 0, 0, 0, time.UTC)

	f.importStatement(t,
		provisional(day, 100.00, "SYNT-BOOKED"),
		provisional(day, 200.00, "SYNT-DISMISSED"),
		provisional(day, 300.00, "SYNT-OTHERDAY"),
	)
	bookedID, _ := f.rowByRef(t, "SYNT-BOOKED")
	if _, err := f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, bookedID, "3900", "revenue", "synthetic"); err != nil {
		t.Fatalf("book row: %v", err)
	}
	dismissedID, _ := f.rowByRef(t, "SYNT-DISMISSED")
	if err := f.svc.DismissBankRow(ctx, f.club.ID, f.club.Treasurer.ID, dismissedID, "unidentifiable"); err != nil {
		t.Fatalf("dismiss row: %v", err)
	}

	res := f.importStatement(t,
		settledPayout(day, 100.00, "3000701", "SYNT-BOOKED"),
		settledPayout(day, 200.00, "3000702", "SYNT-DISMISSED"),
		settledPayout(day.AddDate(0, 0, 3), 300.00, "3000703", "SYNT-OTHERDAY"),
	)
	if res.Refreshed != 0 || res.SkippedDup != 3 || len(res.ReferenceMismatches) != 1 {
		t.Errorf("refreshed %d, skipped %d, mismatches %+v; want 0, 3 and the three-day gap",
			res.Refreshed, res.SkippedDup, res.ReferenceMismatches)
	}
	var bare int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FROM bank_import_rows WHERE club_id = $1 AND reference LIKE 'SYNT-%' AND description = 'Overførsel'`,
		f.club.ID).Scan(&bare); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if bare != 3 {
		t.Errorf("%d rows still read Overførsel, want all 3 untouched", bare)
	}
}

func (f *payoutSweepFixture) reseen(t *testing.T, ref string) bool {
	t.Helper()
	var seen bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT reseen_at IS NOT NULL FROM bank_import_rows WHERE club_id = $1 AND reference = $2`,
		f.club.ID, ref).Scan(&seen); err != nil {
		t.Fatalf("read reseen_at for %s: %v", ref, err)
	}
	return seen
}

// A bare "Overførsel" that a later statement, running past its date, shows
// again still bare is a settled transfer with a terse text. It must not be
// superseded by an unrelated payment of the same amount on the same day.
func TestBareRowSeenAgainIsNeverSuperseded(t *testing.T) {
	f := newPayoutSweepFixture(t)
	day := time.Date(f.club.Year, 5, 15, 0, 0, 0, 0, time.UTC)

	f.importStatement(t, filler(day.AddDate(0, 0, -3), "SYNU-0"), provisional(day, 450.00, "SYNU-A"))
	next := f.importStatement(t,
		filler(day.AddDate(0, 0, -3), "SYNU-0"),
		provisional(day, 450.00, "SYNU-A"),
		BankRow{Date: day, Amount: 450.00, Description: "Fra: Synthetic Payer Betalt: 15.05", Counterpart: "Synthetic Payer", Reference: "SYNU-C"},
		filler(day.AddDate(0, 0, 1), "SYNU-D"),
	)
	if !f.reseen(t, "SYNU-A") {
		t.Errorf("bare row shown again by a later statement was not marked reseen")
	}
	if next.Superseded != 0 {
		t.Errorf("later statement superseded %d rows, want 0", next.Superseded)
	}
	if _, why := f.rowByRef(t, "SYNU-A"); why != nil {
		t.Errorf("persistent bare row dismissed as %s, want it left alone", *why)
	}
	if res := f.bankSync(t); res.ProvisionalSuperseded != 0 {
		t.Errorf("bank sync superseded %d rows, want 0", res.ProvisionalSuperseded)
	}

	// Re-exporting the same day again (the statement still ends on it) is
	// no evidence: the row may still be in flight.
	d2 := day.AddDate(0, 1, 0)
	f.importStatement(t, filler(d2.AddDate(0, 0, -1), "SYNU-E"), provisional(d2, 60.00, "SYNU-F"))
	f.importStatement(t, filler(d2.AddDate(0, 0, -1), "SYNU-E"), provisional(d2, 60.00, "SYNU-F"))
	if f.reseen(t, "SYNU-F") {
		t.Errorf("bare row re-exported on its own export day was marked reseen")
	}
}

// The settled row may be dated a banking day or two after the export showed
// the transfer. Within that gap the row is refreshed and moves forward;
// beyond it, or with another amount, the pair is reported and left alone.
func TestReferenceRefreshDateGap(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	day := time.Date(f.club.Year, 6, 5, 0, 0, 0, 0, time.UTC)

	f.importStatement(t,
		provisional(day, 412.65, "SYNG-1"),
		provisional(day, 200.00, "SYNG-2"),
		provisional(day, 300.00, "SYNG-3"),
	)
	res := f.importStatement(t,
		settledPayout(day.AddDate(0, 0, 2), 412.65, "3000801", "SYNG-1"),
		settledPayout(day.AddDate(0, 0, 3), 200.00, "3000802", "SYNG-2"),
		settledPayout(day, 300.50, "3000803", "SYNG-3"),
	)
	if res.Refreshed != 1 {
		t.Errorf("refreshed %d, want 1 (the two-day gap)", res.Refreshed)
	}
	if len(res.ReferenceMismatches) != 2 {
		t.Errorf("reference mismatches = %+v, want the three-day gap and the other amount", res.ReferenceMismatches)
	}
	var moved time.Time
	var id string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id::text, row_date FROM bank_import_rows WHERE club_id = $1 AND reference = 'SYNG-1'`, f.club.ID,
	).Scan(&id, &moved); err != nil {
		t.Fatalf("read refreshed row: %v", err)
	}
	if !moved.Equal(day.AddDate(0, 0, 2)) {
		t.Errorf("refreshed row dated %s, want the settled date", moved.Format("2006-01-02"))
	}
	var oldDate, newDate string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT details->>'old_date', details->>'new_date' FROM audit_log
		  WHERE club_id = $1 AND resource_id = $2 AND action = 'bank_row.refreshed'`, f.club.ID, id,
	).Scan(&oldDate, &newDate); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if oldDate != day.Format("2006-01-02") || newDate != day.AddDate(0, 0, 2).Format("2006-01-02") {
		t.Errorf("audit dates %s → %s, want %s → %s", oldDate, newDate, day.Format("2006-01-02"), day.AddDate(0, 0, 2).Format("2006-01-02"))
	}
	for _, ref := range []string{"SYNG-2", "SYNG-3"} {
		var desc string
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT description FROM bank_import_rows WHERE club_id = $1 AND reference = $2`, f.club.ID, ref).Scan(&desc); err != nil {
			t.Fatalf("read %s: %v", ref, err)
		}
		if desc != "Overførsel" {
			t.Errorf("%s was changed to %q, want it left alone", ref, desc)
		}
	}
}

// Refresh needs the existing row to be provisional (on its statement's last
// day) and on the importing bank account, and an actor to audit it.
func TestReferenceRefreshScope(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	day := time.Date(f.club.Year, 7, 2, 0, 0, 0, 0, time.UTC)

	// A bare row mid-statement is not provisional.
	f.importStatement(t, provisional(day, 120.00, "SYNH-1"), filler(day.AddDate(0, 0, 2), "SYNH-2"))
	// A provisional row on another bank account.
	var otherImport string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'synthetic-savings.csv', 'sparebank-norge-v1', '1921', $2, 0) RETURNING id`,
		f.club.ID, f.club.Treasurer.ID).Scan(&otherImport); err != nil {
		t.Fatalf("other-account import: %v", err)
	}
	if _, err := f.svc.ImportBankRows(ctx, f.club.ID, otherImport, "", []BankRow{provisional(day, 130.00, "SYNH-3")}); err != nil {
		t.Fatalf("import other account: %v", err)
	}

	res := f.importStatement(t,
		settledPayout(day, 120.00, "3000901", "SYNH-1"),
		settledPayout(day, 130.00, "3000902", "SYNH-3"),
	)
	if res.Refreshed != 0 || res.SkippedDup != 2 {
		t.Errorf("refreshed %d, skipped %d; want 0 and 2", res.Refreshed, res.SkippedDup)
	}

	// No actor: nothing changes.
	d2 := day.AddDate(0, 0, 10)
	f.importStatement(t, provisional(d2, 140.00, "SYNH-4"))
	imp := statementImport{id: f.club.NewBankImport(t), account: "1920", periodTo: d2}
	out, _, err := f.svc.refreshProvisionalByReference(ctx, f.club.ID, "", imp, settledPayout(d2, 140.00, "3000903", "SYNH-4"),
		BankRowHash(f.club.ID, settledPayout(d2, 140.00, "3000903", "SYNH-4")))
	if err != nil {
		t.Fatalf("refresh without actor: %v", err)
	}
	if out != refDuplicate {
		t.Errorf("refresh without an actor = %v, want a plain skip", out)
	}
}

// The statement's last day is recorded on the import, so an overwrite
// reimport that deletes an older import's later rows does not turn a bare
// mid-statement row into a provisional one.
func TestOverwriteReimportKeepsStatementPeriod(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	day := time.Date(f.club.Year, 8, 20, 0, 0, 0, 0, time.UTC)

	f.importStatement(t,
		filler(day.AddDate(0, 0, -5), "SYNO-1"),
		provisional(day, 250.00, "SYNO-2"),
		filler(day.AddDate(0, 0, 1), "SYNO-3"),
		filler(day.AddDate(0, 0, 2), "SYNO-4"),
	)
	over, err := f.svc.ReimportAccountBankRows(ctx, f.club.ID, "1920", f.club.NewBankImport(t), "",
		[]BankRow{filler(day.AddDate(0, 0, 1), "SYNO-5"), filler(day.AddDate(0, 0, 2), "SYNO-6")})
	if err != nil {
		t.Fatalf("overwrite reimport: %v", err)
	}
	if over.Deleted != 2 {
		t.Fatalf("overwrite deleted %d rows, want the older import's 2 rows in range", over.Deleted)
	}
	rows, err := f.svc.ListUnmatchedBankRows(ctx, f.club.ID, "", "", 0, 200, 0)
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	for _, r := range rows {
		if r.Reference == "SYNO-2" && r.Provisional {
			t.Errorf("mid-statement bare row reads as provisional after the overwrite")
		}
	}
	other := f.importStatement(t,
		BankRow{Date: day, Amount: 250.00, Description: "Synthetic sender", Counterpart: "Synthetic Payer", Reference: "SYNO-7"},
		filler(day.AddDate(0, 0, 4), "SYNO-8"),
	)
	if other.Superseded != 0 {
		t.Errorf("superseded %d rows, want 0", other.Superseded)
	}

	// Overwrite reimport over an import that still holds a provisional
	// row: the unbooked provisional row is cleared with the range and the
	// settled row lands once.
	d2 := day.AddDate(0, 0, 20)
	f.importStatement(t, filler(d2.AddDate(0, 0, -1), "SYNO-9"), provisional(d2, 75.00, "SYNO-10"))
	if _, err := f.svc.ReimportAccountBankRows(ctx, f.club.ID, "1920", f.club.NewBankImport(t), "",
		[]BankRow{settledPayout(d2, 75.00, "3001001", "SYNO-10"), filler(d2.AddDate(0, 0, 1), "SYNO-11")}); err != nil {
		t.Fatalf("overwrite over provisional: %v", err)
	}
	var n int
	var desc string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*), max(description) FROM bank_import_rows WHERE club_id = $1 AND reference = 'SYNO-10'`,
		f.club.ID).Scan(&n, &desc); err != nil {
		t.Fatalf("read rows: %v", err)
	}
	if n != 1 || desc == "Overførsel" {
		t.Errorf("after overwrite: %d rows with the reference, text %q; want 1 settled row", n, desc)
	}
}

// A refreshed row that now carries a KID is matched to its faktura by the
// next bank sync.
func TestRefreshedRowWithKIDMatchesOnSync(t *testing.T) {
	f := newPayoutSweepFixture(t)
	m := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000,
		Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	day := time.Date(f.club.Year, 9, 9, 0, 0, 0, 0, time.UTC)

	f.importStatement(t, filler(day.AddDate(0, 0, -1), "SYNK-0"), provisional(day, 450.00, "SYNK-1"))
	res := f.importStatement(t, BankRow{Date: day, Amount: 450.00, Description: "Giro " + inv.KID,
		Counterpart: "Synthetic Payer", KID: inv.KID, Reference: "SYNK-1"})
	if res.Refreshed != 1 {
		t.Fatalf("refreshed %d, want 1", res.Refreshed)
	}
	id, _ := f.rowByRef(t, "SYNK-1")
	if f.bankEntry(t, id) != nil {
		t.Fatalf("refreshed row was booked at import; the KID match belongs to the next sync")
	}
	sync := f.bankSync(t)
	if sync.KIDMatched != 1 {
		t.Errorf("bank sync KID-matched %d rows, want 1", sync.KIDMatched)
	}
	if f.bankEntry(t, id) == nil {
		t.Errorf("refreshed row with a KID not booked by the sync")
	}
	var paid bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, inv.ID).Scan(&paid); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if !paid {
		t.Errorf("faktura not marked paid after the KID match")
	}
}
