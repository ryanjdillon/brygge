package accounting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
	"github.com/rs/zerolog"
)

const sweepMSN = "000001"

type payoutSweepFixture struct {
	svc        *Service
	club       *fin.Club
	bankImport string
}

func newPayoutSweepFixture(t *testing.T) *payoutSweepFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	if _, err := pool.Exec(context.Background(),
		`UPDATE clubs SET vipps_per_charge_enabled = true WHERE id = $1`, club.ID); err != nil {
		t.Fatalf("enable per-charge: %v", err)
	}
	return &payoutSweepFixture{svc: svc, club: club, bankImport: club.NewBankImport(t)}
}

// sweepSettlement is one synthetic Vipps settlement: its charges, a fee, the
// payout row and (optionally) the bank deposit naming it.
type sweepSettlement struct {
	number  string
	booking time.Time
	charges []string
	bankRow string
}

func (f *payoutSweepFixture) settlement(t *testing.T, number string, day int, chargeCents []int64, feeCents int64, deposit bool) sweepSettlement {
	t.Helper()
	return f.settlementOn(t, number, time.Date(f.club.Year, 6, day, 0, 0, 0, 0, time.UTC), chargeCents, feeCents, deposit)
}

// settlementOn is settlement with an explicit booking date; the deposit lands
// the day after.
func (f *payoutSweepFixture) settlementOn(t *testing.T, number string, booking time.Time, chargeCents []int64, feeCents int64, deposit bool) sweepSettlement {
	t.Helper()
	s := sweepSettlement{number: number, booking: booking}
	var gross int64
	for _, c := range chargeCents {
		gross += c
		s.charges = append(s.charges, f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: c, Date: s.booking, MSN: sweepMSN}))
	}
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -feeCents, Date: s.booking, MSN: sweepMSN})
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -(gross - feeCents), Date: s.booking,
		MSN: sweepMSN, Settlement: number})
	if deposit {
		s.bankRow = f.club.NewBankRow(t, f.bankImport, fin.BankRowOpts{
			Date: s.booking.AddDate(0, 0, 1), Cents: gross - feeCents,
			Description: "Utb. " + number + " Vippsnr " + sweepMSN,
		})
	}
	return s
}

func (f *payoutSweepFixture) book(t *testing.T, chargeIDs ...string) {
	t.Helper()
	for _, id := range chargeIDs {
		if _, err := f.svc.CreateVippsChargeBilag(context.Background(), f.club.ID, id, "3200", "", f.club.Treasurer.ID); err != nil {
			t.Fatalf("book charge: %v", err)
		}
	}
}

func (f *payoutSweepFixture) bankSync(t *testing.T) *BankSyncResult {
	t.Helper()
	res, err := f.svc.BankSync(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("BankSync: %v", err)
	}
	if res.VippsPayouts == nil {
		t.Fatalf("BankSync on the per-charge model returned no payout sweep")
	}
	return res
}

func (f *payoutSweepFixture) bankEntry(t *testing.T, rowID string) *string {
	t.Helper()
	var entry *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1`, rowID).Scan(&entry); err != nil {
		t.Fatalf("read bank row: %v", err)
	}
	return entry
}

func (f *payoutSweepFixture) postedEntries(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return n
}

func settlementsOf(items []VippsPayoutSweepItem) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		out[it.SettlementNumber] = it.Reason
	}
	return out
}

func TestBankSyncClearsReadyVippsPayouts(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()

	// Two settlements whose charges are all booked: both clear in one sync.
	ready := f.settlement(t, "3000101", 2, []int64{45000, 30000}, 3750, true)
	readyToo := f.settlement(t, "3000102", 3, []int64{20000}, 1000, true)
	f.book(t, ready.charges...)
	f.book(t, readyToo.charges...)
	// One charge still in Tildel: not cleared.
	waiting := f.settlement(t, "3000103", 4, []int64{10000, 15000}, 1250, true)
	f.book(t, waiting.charges[0])
	// Imported and booked, but the bank side carries no settlement reference.
	noRef := f.settlement(t, "3000104", 5, []int64{8000}, 400, false)
	f.book(t, noRef.charges...)
	noRefDeposit := f.club.NewBankRow(t, f.bankImport, fin.BankRowOpts{
		Date: noRef.booking.AddDate(0, 0, 1), Cents: 7600, Description: "Vipps AS"})
	// A deposit naming a settlement that was never imported.
	orphan := f.club.NewBankRow(t, f.bankImport, fin.BankRowOpts{
		Cents: 5000, Description: "Utb. 3000199 Vippsnr " + sweepMSN})

	res := f.bankSync(t)
	sweep := res.VippsPayouts

	cleared := settlementsOf(sweep.Cleared)
	if len(cleared) != 2 {
		t.Fatalf("cleared = %+v, want %s and %s", sweep.Cleared, ready.number, readyToo.number)
	}
	if _, ok := cleared[ready.number]; !ok {
		t.Errorf("settlement %s not cleared: %+v", ready.number, sweep)
	}
	if _, ok := cleared[readyToo.number]; !ok {
		t.Errorf("settlement %s not cleared: %+v", readyToo.number, sweep)
	}
	if res.VippsReconciled != 2 {
		t.Errorf("VippsReconciled = %d, want the 2 cleared payouts", res.VippsReconciled)
	}
	if w := settlementsOf(sweep.Waiting); len(w) != 1 || !strings.Contains(w[waiting.number], "1 belastning") {
		t.Errorf("waiting = %+v, want %s with 1 charge left", sweep.Waiting, waiting.number)
	}
	skipped := settlementsOf(sweep.Skipped)
	if !strings.Contains(skipped[noRef.number], "ingen bankinnbetaling") {
		t.Errorf("payout without a bank reference: skipped = %+v, want a no-deposit reason", sweep.Skipped)
	}
	if !strings.Contains(skipped["3000199"], "ikke importert") {
		t.Errorf("deposit for an unimported settlement: skipped = %+v, want a not-imported reason", sweep.Skipped)
	}
	if len(sweep.Failed) != 0 {
		t.Errorf("failed = %+v, want none", sweep.Failed)
	}

	// The cleared settlement's fee and bank entries are posted and its
	// clearing account nets to zero.
	bankEntry := f.bankEntry(t, ready.bankRow)
	if bankEntry == nil {
		t.Fatalf("ready settlement's bank row is still unbooked")
	}
	var feeEntry, feeStatus, bankStatus string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT je.id::text, je.status FROM vipps_import_rows r JOIN journal_entries je ON je.id = r.journal_entry_id
		  WHERE r.club_id = $1 AND r.row_type = 'fee' AND r.booking_date = $2`,
		f.club.ID, ready.booking).Scan(&feeEntry, &feeStatus); err != nil {
		t.Fatalf("read fee entry: %v", err)
	}
	if err := f.club.Pool.QueryRow(ctx, `SELECT status FROM journal_entries WHERE id = $1`, *bankEntry).Scan(&bankStatus); err != nil {
		t.Fatalf("read bank entry: %v", err)
	}
	if feeStatus != "posted" || bankStatus != "posted" {
		t.Errorf("fee entry %s, bank entry %s; want both posted", feeStatus, bankStatus)
	}
	if dr, _ := entryLineSum(t, f.svc, ctx, feeEntry, vippsFeeAccountCode); dr != 37.50 {
		t.Errorf("fee expense = %.2f, want 37.50", dr)
	}
	if dr, _ := entryLineSum(t, f.svc, ctx, *bankEntry, bankAccountCode); dr != 712.50 {
		t.Errorf("bank debit = %.2f, want 712.50", dr)
	}
	balance, err := f.svc.settlementClearingBalance(ctx, f.club.ID, ready.number, sweepMSN, ready.booking, ready.bankRow)
	if err != nil {
		t.Fatalf("clearing balance: %v", err)
	}
	if !floatNear(balance, 0, 0.005) {
		t.Errorf("settlement clearing balance = %.2f, want 0", balance)
	}

	for name, row := range map[string]string{"waiting": waiting.bankRow, "no reference": noRefDeposit, "orphan": orphan} {
		if f.bankEntry(t, row) != nil {
			t.Errorf("%s deposit was booked; want it left for review", name)
		}
	}

	// A second sync posts nothing new.
	before := f.postedEntries(t)
	again := f.bankSync(t)
	if len(again.VippsPayouts.Cleared) != 0 || again.VippsReconciled != 0 {
		t.Errorf("re-run cleared %+v, want nothing", again.VippsPayouts.Cleared)
	}
	if after := f.postedEntries(t); after != before {
		t.Errorf("re-run created %d journal entries, want 0", after-before)
	}

	// Booking the last charge lets the next Vipps sync clear the settlement.
	f.book(t, waiting.charges[1])
	resync, err := f.svc.ResyncVippsBilags(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("ResyncVippsBilags: %v", err)
	}
	if resync.Payouts == nil {
		t.Fatalf("Synk Vipps returned no payout sweep")
	}
	if _, ok := settlementsOf(resync.Payouts.Cleared)[waiting.number]; !ok {
		t.Errorf("Synk Vipps cleared %+v, want %s", resync.Payouts.Cleared, waiting.number)
	}
	if f.bankEntry(t, waiting.bankRow) == nil {
		t.Errorf("waiting settlement's bank row still unbooked after its last charge was booked")
	}
}

// A dismissed charge counts as resolved, so it does not hold the payout back.
func TestClearReadyVippsPayoutsTreatsDismissedChargesAsResolved(t *testing.T) {
	f := newPayoutSweepFixture(t)
	s := f.settlement(t, "3000201", 2, []int64{40000, 2500}, 2000, true)
	f.book(t, s.charges[0])
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE vipps_import_rows SET dismissed_at = now() WHERE id = $1`, s.charges[1]); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	res, err := f.svc.ClearReadyVippsPayouts(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("ClearReadyVippsPayouts: %v", err)
	}
	if _, ok := settlementsOf(res.Cleared)[s.number]; !ok {
		t.Errorf("cleared = %+v, want %s", res, s.number)
	}
}

// Clubs on the legacy single-bilag model keep their own payout matching.
func TestClearReadyVippsPayoutsStandsDownOnLegacyModel(t *testing.T) {
	f := newPayoutSweepFixture(t)
	s := f.settlement(t, "3000301", 2, []int64{40000}, 2000, true)
	f.book(t, s.charges...)
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE clubs SET vipps_per_charge_enabled = false WHERE id = $1`, f.club.ID); err != nil {
		t.Fatalf("disable per-charge: %v", err)
	}
	res, err := f.svc.ClearReadyVippsPayouts(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("ClearReadyVippsPayouts: %v", err)
	}
	if len(res.Cleared)+len(res.Waiting)+len(res.Skipped)+len(res.Failed) != 0 {
		t.Errorf("legacy model sweep = %+v, want empty", res)
	}
	if f.bankEntry(t, s.bankRow) != nil {
		t.Errorf("legacy model: bank row booked by the per-charge sweep")
	}
}

func (f *payoutSweepFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func (f *payoutSweepFixture) sweep(t *testing.T) *VippsPayoutSweepResult {
	t.Helper()
	res, err := f.svc.ClearReadyVippsPayouts(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("ClearReadyVippsPayouts: %v", err)
	}
	return res
}

// bookedFeeRows counts the settlement-day fee rows linked to an entry.
func (f *payoutSweepFixture) bookedFeeRows(t *testing.T, booking time.Time) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM vipps_import_rows
		  WHERE club_id = $1 AND row_type = 'fee' AND booking_date = $2 AND journal_entry_id IS NOT NULL`,
		f.club.ID, booking).Scan(&n); err != nil {
		t.Fatalf("count fee rows: %v", err)
	}
	return n
}

// A deposit that is not the imported net payout to the øre is refused before
// anything is posted: clearing it would leave the clearing account off.
func TestClearReadyVippsPayoutsRefusesAmountMismatch(t *testing.T) {
	f := newPayoutSweepFixture(t)
	s := f.settlement(t, "3000401", 2, []int64{45000, 30000}, 3750, true)
	f.book(t, s.charges...)
	f.exec(t, `UPDATE bank_import_rows SET amount = amount - 25 WHERE id = $1`, s.bankRow)
	before := f.postedEntries(t)

	res := f.sweep(t)
	if r, ok := settlementsOf(res.Failed)[s.number]; !ok || !strings.Contains(r, "beløpet") {
		t.Errorf("failed = %+v, want %s refused for its amount", res.Failed, s.number)
	}
	if len(res.Cleared) != 0 {
		t.Errorf("cleared = %+v, want none", res.Cleared)
	}
	if f.bankEntry(t, s.bankRow) != nil || f.bookedFeeRows(t, s.booking) != 0 {
		t.Errorf("mismatched deposit: bank or fee was booked")
	}
	if after := f.postedEntries(t); after != before {
		t.Errorf("mismatched deposit created %d entries, want 0", after-before)
	}
}

// Two deposits naming one settlement: the first clears it, the second is
// left for a human instead of crediting the payout to clearing twice — in
// the same sweep and on every later one.
func TestClearReadyVippsPayoutsBooksOneDepositPerSettlement(t *testing.T) {
	f := newPayoutSweepFixture(t)
	s := f.settlement(t, "3000501", 2, []int64{40000}, 2000, true)
	f.book(t, s.charges...)
	second := f.club.NewBankRow(t, f.bankImport, fin.BankRowOpts{
		Date: s.booking.AddDate(0, 0, 2), Cents: 38000,
		Description: "Utb. " + s.number + " Vippsnr " + sweepMSN + " korr",
	})

	for run := 0; run < 2; run++ {
		res := f.sweep(t)
		if r := settlementsOf(res.Skipped)[s.number]; !strings.Contains(r, "allerede avstemt") {
			t.Errorf("run %d: skipped = %+v, want the second deposit skipped as already cleared", run, res.Skipped)
		}
		var booked int
		if err := f.club.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM bank_import_rows WHERE id = ANY($1::uuid[]) AND journal_entry_id IS NOT NULL`,
			[]string{s.bankRow, second}).Scan(&booked); err != nil {
			t.Fatalf("count booked deposits: %v", err)
		}
		if booked != 1 {
			t.Errorf("run %d: %d deposits booked for one settlement, want 1", run, booked)
		}
	}
}

// A deposit dated in a period that is not open is refused before the fee is
// posted, so the settlement is not left half cleared.
func TestClearReadyVippsPayoutsChecksBothPeriodsFirst(t *testing.T) {
	f := newPayoutSweepFixture(t)
	f.exec(t,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'closed')`,
		f.club.ID, f.club.Year+1)
	s := f.settlementOn(t, "3000601", time.Date(f.club.Year, 12, 31, 0, 0, 0, 0, time.UTC), []int64{40000}, 2000, true)
	f.book(t, s.charges...)
	before := f.postedEntries(t)

	res := f.sweep(t)
	if r := settlementsOf(res.Failed)[s.number]; !strings.Contains(r, "lukket") {
		t.Errorf("failed = %+v, want %s refused for its closed bank period", res.Failed, s.number)
	}
	if f.bookedFeeRows(t, s.booking) != 0 {
		t.Errorf("fee was posted although the payout could not clear")
	}
	if after := f.postedEntries(t); after != before {
		t.Errorf("closed bank period: %d entries created, want 0", after-before)
	}
}

// Charges and fees carry only MSN + booking date, so two settlements on the
// same day for one MSN cannot be told apart: both are left for a human.
func TestClearReadyVippsPayoutsSkipsSameDaySettlements(t *testing.T) {
	f := newPayoutSweepFixture(t)
	a := f.settlement(t, "3000701", 2, []int64{40000}, 2000, true)
	b := f.settlement(t, "3000702", 2, []int64{10000}, 500, true)
	f.book(t, a.charges...)
	f.book(t, b.charges...)
	before := f.postedEntries(t)

	res := f.sweep(t)
	skipped := settlementsOf(res.Skipped)
	for _, s := range []sweepSettlement{a, b} {
		if !strings.Contains(skipped[s.number], "samme dag") {
			t.Errorf("skipped = %+v, want %s skipped as ambiguous", res.Skipped, s.number)
		}
	}
	if after := f.postedEntries(t); after != before {
		t.Errorf("ambiguous day: %d entries created, want 0", after-before)
	}
}

// Posting the draft backlog books charges, which completes settlements: the
// apply run clears them in the same request.
func TestPostVippsDraftBacklogClearsReadyPayouts(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3000801", 2, []int64{40000, 2500}, 2000, true)
	for i, c := range []int64{40000, 2500} {
		row := s.charges[i]
		src := "vipps_import_rows"
		e, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: f.club.PeriodID,
			EntryDate:      s.booking.Format("2006-01-02"),
			Description:    "Vipps innbetaling (legacy draft)",
			Source:         "vipps",
			SourceID:       &row,
			SourceTable:    &src,
			CreatedBy:      f.club.Treasurer.ID,
			ClubID:         f.club.ID,
			Lines: []CreateJournalLineInput{
				{AccountCode: vippsClearingAccountCode, Debit: fin.Kr(c)},
				{AccountCode: fallbackRevenueCode, Credit: fin.Kr(c)},
			},
		})
		if err != nil {
			t.Fatalf("draft: %v", err)
		}
		f.exec(t, `UPDATE vipps_import_rows SET journal_entry_id = $1 WHERE id = $2`, e.ID, row)
	}

	res, err := f.svc.PostVippsDraftBacklog(ctx, f.club.ID, f.club.PeriodID, f.club.Treasurer.ID, true)
	if err != nil {
		t.Fatalf("PostVippsDraftBacklog: %v", err)
	}
	if res.Payouts == nil {
		t.Fatalf("backlog apply returned no payout sweep")
	}
	if _, ok := settlementsOf(res.Payouts.Cleared)[s.number]; !ok {
		t.Errorf("backlog apply cleared %+v, want %s", res.Payouts, s.number)
	}
	if f.bankEntry(t, s.bankRow) == nil {
		t.Errorf("settlement's deposit still unbooked after the backlog apply")
	}
}

// Imported settlement and MSN values never reach a SQL regex: an MSN with
// pattern characters is reported like any other payout without a deposit.
func TestClearReadyVippsPayoutsToleratesOddImportedValues(t *testing.T) {
	f := newPayoutSweepFixture(t)
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -5000,
		Date: time.Date(f.club.Year, 6, 2, 0, 0, 0, 0, time.UTC), MSN: "12345.(", Settlement: "3000901"})
	// A booked deposit elsewhere, so the payout lookup has rows to compare.
	s := f.settlement(t, "3000902", 3, []int64{40000}, 2000, true)
	f.book(t, s.charges...)

	res := f.sweep(t)
	if r := settlementsOf(res.Skipped)["3000901"]; !strings.Contains(r, "ingen bankinnbetaling") {
		t.Errorf("skipped = %+v, want the odd payout reported without a deposit", res.Skipped)
	}
	if _, ok := settlementsOf(res.Cleared)[s.number]; !ok {
		t.Errorf("cleared = %+v, want %s", res.Cleared, s.number)
	}
}

// A payout whose only deposit was dismissed says so, rather than claiming
// there is no deposit.
func TestClearReadyVippsPayoutsReportsDismissedDeposit(t *testing.T) {
	f := newPayoutSweepFixture(t)
	s := f.settlement(t, "3001001", 2, []int64{40000}, 2000, true)
	f.book(t, s.charges...)
	f.exec(t, `UPDATE bank_import_rows SET dismissed_at = now(), dismissed_by = $2, dismissed_reason = 'duplicate'
	            WHERE id = $1`, s.bankRow, f.club.Treasurer.ID)

	res := f.sweep(t)
	if r := settlementsOf(res.Skipped)[s.number]; !strings.Contains(r, "avvist") {
		t.Errorf("skipped = %+v, want %s reported with a dismissed deposit", res.Skipped, s.number)
	}
}
