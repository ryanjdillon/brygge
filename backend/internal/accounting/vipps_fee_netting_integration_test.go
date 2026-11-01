package accounting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// feeRowSpec is one imported fee row in øre: Amount is the column the
// clearing reads, Fee the Report API's fee column, which it ignores.
type feeRowSpec struct{ amount, fee int64 }

// feeNettingSettlement is a settlement with booked charges, hand-shaped fee
// rows and a payout/deposit pair of the given net, all synthetic.
type feeNettingSettlement struct {
	number   string
	booking  time.Time
	feeRows  []string
	bankRow  string
	payoutID string
}

func (f *payoutSweepFixture) feeSettlement(t *testing.T, number string, chargeCents []int64, fees []feeRowSpec, netCents int64) feeNettingSettlement {
	t.Helper()
	s := feeNettingSettlement{number: number, booking: time.Date(f.club.Year, 6, 10, 0, 0, 0, 0, time.UTC)}
	for _, c := range chargeCents {
		id := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: c, Date: s.booking, MSN: sweepMSN})
		f.book(t, id)
	}
	for _, fr := range fees {
		id := f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: fr.amount, Date: s.booking, MSN: sweepMSN})
		if _, err := f.club.Pool.Exec(context.Background(),
			`UPDATE vipps_import_rows SET fee = $2 WHERE id = $1`, id, fin.Kr(fr.fee)); err != nil {
			t.Fatalf("set fee column: %v", err)
		}
		s.feeRows = append(s.feeRows, id)
	}
	s.payoutID = f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -netCents, Date: s.booking,
		MSN: sweepMSN, Settlement: number})
	s.bankRow = f.club.NewBankRow(t, f.bankImport, fin.BankRowOpts{
		Date: s.booking.AddDate(0, 0, 1), Cents: netCents,
		Description: "Utb. " + number + " Vippsnr " + sweepMSN,
	})
	return s
}

// feeRowEntries returns the entry each fee row is linked to ("" when none).
func (f *payoutSweepFixture) feeRowEntries(t *testing.T, s feeNettingSettlement) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, id := range s.feeRows {
		var entry *string
		if err := f.club.Pool.QueryRow(context.Background(),
			`SELECT journal_entry_id::text FROM vipps_import_rows WHERE id = $1`, id).Scan(&entry); err != nil {
			t.Fatalf("read fee row: %v", err)
		}
		if entry != nil {
			out[id] = *entry
		} else {
			out[id] = ""
		}
	}
	return out
}

func (f *payoutSweepFixture) clearFee(t *testing.T, s feeNettingSettlement) (*VippsPayoutClearResult, error) {
	t.Helper()
	return f.svc.ClearVippsPayout(context.Background(), f.club.ID, s.bankRow, f.club.Treasurer.ID)
}

func (f *payoutSweepFixture) assertNets(t *testing.T, s feeNettingSettlement) {
	t.Helper()
	bal, err := f.svc.settlementClearingBalance(context.Background(), f.club.ID, s.number, sweepMSN, s.booking, s.bankRow)
	if err != nil {
		t.Fatalf("clearing balance: %v", err)
	}
	if !floatNear(bal, 0, 0.005) {
		t.Errorf("clearing balance = %.2f, want 0", bal)
	}
}

// A positive fee row is a refund of earlier fees. It must reduce the fee
// expense (DR clearing / CR fee account), not add to it.
func TestClearVippsPayoutBooksFeeRefundAsReduction(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	// 100.00 charged, a 5.00 fee refund: Vipps pays out 105.00.
	s := f.feeSettlement(t, "5000101", []int64{10000}, []feeRowSpec{{amount: 500}}, 10500)

	res, err := f.clearFee(t, s)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if res.FeeEntryID == "" {
		t.Fatalf("a refund books an entry, got %+v", res)
	}
	if dr, cr := entryLineSum(t, f.svc, ctx, res.FeeEntryID, vippsClearingAccountCode); dr != 5.00 || cr != 0 {
		t.Errorf("clearing lines on the refund entry = DR %.2f CR %.2f, want DR 5.00", dr, cr)
	}
	if dr, cr := entryLineSum(t, f.svc, ctx, res.FeeEntryID, vippsFeeAccountCode); dr != 0 || cr != 5.00 {
		t.Errorf("fee account lines on the refund entry = DR %.2f CR %.2f, want CR 5.00", dr, cr)
	}
	var desc string
	if err := f.club.Pool.QueryRow(ctx, `SELECT description FROM journal_entries WHERE id = $1`, res.FeeEntryID).Scan(&desc); err != nil {
		t.Fatalf("read description: %v", err)
	}
	if !strings.Contains(desc, "refusjon") {
		t.Errorf("refund entry description = %q, want it to say refund", desc)
	}
	f.assertNets(t, s)
}

// Deductions and refunds in one settlement net into one entry for the
// difference, and every row is linked to it.
func TestClearVippsPayoutNetsMixedFeeRows(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	// 1000.00 charged, fees -30.00 and +10.00: 20.00 expense, payout 980.00.
	s := f.feeSettlement(t, "5000201", []int64{100000}, []feeRowSpec{{amount: -3000}, {amount: 1000}}, 98000)

	res, err := f.clearFee(t, s)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if dr, _ := entryLineSum(t, f.svc, ctx, res.FeeEntryID, vippsFeeAccountCode); dr != 20.00 {
		t.Errorf("fee expense = %.2f, want 20.00 (the signed sum, not 40.00)", dr)
	}
	for id, entry := range f.feeRowEntries(t, s) {
		if entry != res.FeeEntryID {
			t.Errorf("fee row %s linked to %q, want the fee entry %s", id, entry, res.FeeEntryID)
		}
	}
	f.assertNets(t, s)
}

// Fee rows that cancel post nothing but are still linked, so a later attempt
// does not count them again.
func TestClearVippsPayoutLinksFeeRowsThatNetToZero(t *testing.T) {
	f := newPayoutSweepFixture(t)
	s := f.feeSettlement(t, "5000301", []int64{10000}, []feeRowSpec{{amount: -500}, {amount: 500}}, 10000)

	res, err := f.clearFee(t, s)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if res.FeeEntryID != "" {
		t.Errorf("fee entry %s posted for a zero net", res.FeeEntryID)
	}
	if res.BankEntryID == "" {
		t.Fatalf("bank entry missing: %+v", res)
	}
	for id, entry := range f.feeRowEntries(t, s) {
		if entry == "" {
			t.Errorf("fee row %s left unlinked", id)
		}
	}
	f.assertNets(t, s)
}

// The Report API gives each settlement three fee rows: the fee in the fee
// column (a negative and a positive of the same size, amount 0) and the
// deduction itself in the amount column. Only the amount counts.
//
// This is a regression guard: it also passes on the old abs sum. It catches
// a change that counts the fee column as well (its magnitudes would book the
// fee three times over).
func TestClearVippsPayoutReportAPIFeeShapeNetsToZero(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.feeSettlement(t, "5000401", []int64{10000},
		[]feeRowSpec{{amount: 0, fee: -1446}, {amount: 0, fee: 1446}, {amount: -1446, fee: 0}}, 8554)

	res, err := f.clearFee(t, s)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if dr, _ := entryLineSum(t, f.svc, ctx, res.FeeEntryID, vippsFeeAccountCode); dr != 14.46 {
		t.Errorf("fee expense = %.2f, want 14.46", dr)
	}
	for id, entry := range f.feeRowEntries(t, s) {
		if entry != res.FeeEntryID {
			t.Errorf("fee row %s linked to %q, want the fee entry %s", id, entry, res.FeeEntryID)
		}
	}
	f.assertNets(t, s)
}

// A settlement whose postings would not net on the clearing account is
// refused with nothing posted or linked, and the sweep reports it as skipped.
func TestClearVippsPayoutRefusesSettlementThatDoesNotNet(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	// 100.00 booked plus a 50.00 charge that is dismissed although Vipps paid
	// it out: the net payout is 150.00 - 5.00, but only 100.00 is in the ledger.
	s := f.feeSettlement(t, "5000501", []int64{10000}, []feeRowSpec{{amount: -500}}, 14500)
	stray := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 5000, Date: s.booking, MSN: sweepMSN})
	if _, err := f.club.Pool.Exec(ctx, `UPDATE vipps_import_rows SET dismissed_at = now() WHERE id = $1`, stray); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	entriesBefore := f.postedEntries(t)

	_, err := f.clearFee(t, s)
	if !errors.Is(err, ErrVippsSettlementNotNetting) {
		t.Fatalf("clear err = %v, want ErrVippsSettlementNotNetting", err)
	}
	if after := f.postedEntries(t); after != entriesBefore {
		t.Errorf("a refused clearing left %d new journal entries", after-entriesBefore)
	}
	if f.bankEntry(t, s.bankRow) != nil {
		t.Errorf("bank row linked after a refused clearing")
	}
	for id, entry := range f.feeRowEntries(t, s) {
		if entry != "" {
			t.Errorf("fee row %s linked to %s after a refused clearing", id, entry)
		}
	}
	var payoutEntry *string
	if err := f.club.Pool.QueryRow(ctx, `SELECT journal_entry_id::text FROM vipps_import_rows WHERE id = $1`, s.payoutID).Scan(&payoutEntry); err != nil {
		t.Fatalf("read payout row: %v", err)
	}
	if payoutEntry != nil {
		t.Errorf("payout row linked to %s after a refused clearing", *payoutEntry)
	}

	sweep, err := f.svc.ClearReadyVippsPayouts(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(sweep.Cleared) != 0 || len(sweep.Failed) != 0 {
		t.Errorf("sweep = %+v, want nothing cleared or failed", sweep)
	}
	if reason := settlementsOf(sweep.Skipped)[s.number]; !strings.Contains(reason, "går ikke i null") {
		t.Errorf("skipped = %+v, want %s with the not-netting reason", sweep.Skipped, s.number)
	}
	if after := f.postedEntries(t); after != entriesBefore {
		t.Errorf("the sweep left %d new journal entries", after-entriesBefore)
	}
}
