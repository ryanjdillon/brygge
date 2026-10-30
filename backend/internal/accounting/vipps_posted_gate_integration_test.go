package accounting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A charge counts as booked for a settlement's clearing only when its entry is
// posted: a charge on a draft or on a voided entry is not in the ledger, and
// clearing around it leaves the clearing account holding the fee and payout
// credits with no charge debit against them.

func (f *payoutSweepFixture) toDraft(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := f.club.Pool.Exec(context.Background(),
			`UPDATE journal_entries SET status = 'draft', posted_at = NULL, posted_by = NULL WHERE id = $1`, id); err != nil {
			t.Fatalf("flip %s to draft: %v", id, err)
		}
	}
}

func (f *payoutSweepFixture) feeEntry(t *testing.T, s sweepSettlement) string {
	t.Helper()
	var id string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM vipps_import_rows
		  WHERE club_id = $1 AND row_type = 'fee' AND booking_date = $2`, f.club.ID, s.booking).Scan(&id); err != nil {
		t.Fatalf("read fee entry: %v", err)
	}
	return id
}

// linkDraftCharge books a charge on a fresh draft DR 2900 / CR 3200.
func (f *payoutSweepFixture) linkDraftCharge(t *testing.T, s sweepSettlement, rowID string, cents int64) string {
	t.Helper()
	src := "vipps_import_rows"
	e, err := f.svc.CreateJournalEntry(context.Background(), CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID,
		EntryDate:      s.booking.Format("2006-01-02"),
		Description:    "Synthetic draft charge",
		Source:         "vipps",
		SourceID:       &rowID,
		SourceTable:    &src,
		CreatedBy:      f.club.Treasurer.ID,
		ClubID:         f.club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: vippsClearingAccountCode, Debit: float64(cents) / 100},
			{AccountCode: "3200", Credit: float64(cents) / 100},
		},
	})
	if err != nil {
		t.Fatalf("draft charge: %v", err)
	}
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE vipps_import_rows SET journal_entry_id = $1 WHERE id = $2`, e.ID, rowID); err != nil {
		t.Fatalf("link draft charge: %v", err)
	}
	return e.ID
}

func (f *payoutSweepFixture) backlog(t *testing.T, apply bool) *VippsBacklogResult {
	t.Helper()
	res, err := f.svc.PostVippsDraftBacklog(context.Background(), f.club.ID, f.club.PeriodID, f.club.Treasurer.ID, apply, nil)
	if err != nil {
		t.Fatalf("PostVippsDraftBacklog(apply=%v): %v", apply, err)
	}
	return res
}

func TestClearWaitsForChargesNotPosted(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3400101", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, s.charges[0])
	draft := f.linkDraftCharge(t, s, s.charges[1], 25000)
	before := f.entryCount(t)

	res, err := f.svc.ClearVippsPayout(ctx, f.club.ID, s.bankRow, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("clear with a draft charge: %v", err)
	}
	if res.Ready || res.UnreconciledCharges != 1 {
		t.Errorf("clear with a charge on a draft = %+v, want not ready with 1 charge waiting", res)
	}
	if f.bankEntry(t, s.bankRow) != nil || f.entryCount(t) != before {
		t.Errorf("clearing around a draft charge booked something")
	}

	// A charge left on a voided entry is not posted either.
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE vipps_import_rows SET journal_entry_id = NULL WHERE id = $1`, s.charges[1]); err != nil {
		t.Fatalf("unlink draft: %v", err)
	}
	if _, err := f.club.Pool.Exec(ctx, `DELETE FROM journal_entries WHERE id = $1`, draft); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	f.book(t, s.charges[1])
	if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, *f.chargeEntry(t, s.charges[1]), f.club.Treasurer.ID); err != nil {
		t.Fatalf("void: %v", err)
	}
	before = f.entryCount(t)
	res, err = f.svc.ClearVippsPayout(ctx, f.club.ID, s.bankRow, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("clear with a voided charge: %v", err)
	}
	if res.Ready || res.UnreconciledCharges != 1 || f.bankEntry(t, s.bankRow) != nil || f.entryCount(t) != before {
		t.Errorf("clear with a charge on a voided entry = %+v (entries +%d), want it to wait", res, f.entryCount(t)-before)
	}
	sweep, err := f.svc.ClearReadyVippsPayouts(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if w := settlementsOf(sweep.Waiting); !strings.Contains(w[s.number], "1 belastning") {
		t.Errorf("sweep waiting = %+v, want %s waiting on 1 charge", sweep.Waiting, s.number)
	}

	// Releasing the voided link and booking the charge again lets it clear,
	// with 2900 at zero.
	if err := f.unassign(s.charges[1]); err != nil {
		t.Fatalf("release the voided link: %v", err)
	}
	f.book(t, s.charges[1])
	f.clear(t, s)
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net = %.2f, want 0", got)
	}
}

// A settlement's fee and payout drafts are posted by the backlog only once
// every charge is posted: they stay drafts while a charge is unbooked, and a
// preview agrees with what the apply then does.
func TestBacklogPostsSettlementDraftsOnlyWhenChargesArePosted(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()

	// Settlement X: cleared, then its fee and payout turned back into drafts
	// (what a cutover leaves). A posted charge is unbooked freely, since the
	// ledger holds no clearing for it to leave behind.
	x := f.settlement(t, "3400201", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, x.charges...)
	f.clear(t, x)
	xFee, xBank := f.feeEntry(t, x), *f.bankEntry(t, x.bankRow)
	f.toDraft(t, xFee, xBank)
	if err := f.unassign(x.charges[1]); err != nil {
		t.Fatalf("unassign a posted charge whose payout is a draft: %v", err)
	}

	for _, apply := range []bool{false, true} {
		res := f.backlog(t, apply)
		if res.Posted != 0 || len(res.Refused) != 2 {
			t.Fatalf("backlog(apply=%v) = %+v, want both settlement drafts refused", apply, res)
		}
		for _, r := range res.Refused {
			if !strings.Contains(r.Reason, "not posted") || !strings.Contains(r.Reason, x.number) {
				t.Errorf("refusal %q does not say the settlement's charge is not posted", r.Reason)
			}
		}
		for _, id := range []string{xFee, xBank} {
			if got := f.entryStatusOf(t, id); got != "draft" {
				t.Errorf("settlement draft %s is %s after backlog(apply=%v), want draft", id, got, apply)
			}
		}
	}

	f.book(t, x.charges[1])
	res := f.backlog(t, true)
	if res.Posted != 2 || len(res.Refused) != 0 {
		t.Fatalf("backlog after booking the charge = %+v, want the two drafts posted", res)
	}
	for _, id := range []string{xFee, xBank} {
		if got := f.entryStatusOf(t, id); got != "posted" {
			t.Errorf("settlement draft %s is %s, want posted", id, got)
		}
	}
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net = %.2f, want 0", got)
	}

	// Settlement Y: a charge that is itself a draft the same run posts counts
	// as posted, in the preview as in the apply.
	y := f.settlement(t, "3400202", 3, []int64{30000, 10000}, 1000, true)
	f.book(t, y.charges...)
	f.clear(t, y)
	f.toDraft(t, f.feeEntry(t, y), *f.bankEntry(t, y.bankRow), *f.chargeEntry(t, y.charges[1]))
	preview := f.backlog(t, false)
	if preview.Posted != 3 || len(preview.Refused) != 0 {
		t.Fatalf("preview = %+v, want the charge draft and both settlement drafts posted", preview)
	}
	if got := f.entryStatusOf(t, *f.chargeEntry(t, y.charges[1])); got != "draft" {
		t.Fatalf("preview changed a draft to %s", got)
	}
	applied := f.backlog(t, true)
	if applied.Posted != 3 || len(applied.Refused) != 0 {
		t.Fatalf("apply = %+v, want 3 posted", applied)
	}
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net after the second settlement = %.2f, want 0", got)
	}
	_ = ctx
}

// A reclassification is one transaction: when the replacement cannot be
// posted, the charge keeps the draft it had.
func TestBacklogReclassFailureKeepsTheDraft(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	f.priceItem(t, "service", "Slipping synthetic", 160000, "once", "non_member")
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, CustomerName: "Walk Up"})
	draft := f.draftCharge(t, row, fallbackRevenueCode, 160000)

	for _, stmt := range []string{
		`CREATE FUNCTION refuse_auto_post() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.status = 'posted' AND NEW.description LIKE '%auto:%' THEN
		     RAISE EXCEPTION 'synthetic post failure';
		   END IF;
		   RETURN NEW;
		 END $$`,
		`CREATE TRIGGER refuse_auto_post BEFORE UPDATE ON journal_entries
		 FOR EACH ROW EXECUTE FUNCTION refuse_auto_post()`,
	} {
		f.exec(t, stmt)
	}
	res := f.backlog(t, true)
	if len(res.Reclassified) != 0 || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Reason, "keeps its draft") {
		t.Fatalf("apply = %+v, want the reclassification refused with the draft kept", res)
	}
	if got := f.entryStatus(t, draft); got != "draft" {
		t.Errorf("the draft is %s after a failed rebooking, want draft", got)
	}
	var linked *string
	if err := f.club.Pool.QueryRow(ctx, `SELECT journal_entry_id::text FROM vipps_import_rows WHERE id = $1`, row).Scan(&linked); err != nil ||
		linked == nil || *linked != draft {
		t.Errorf("charge is linked to %v after a failed rebooking (err %v), want its draft %s", linked, err, draft)
	}

	f.exec(t, `DROP TRIGGER refuse_auto_post ON journal_entries`)
	if again := f.backlog(t, true); len(again.Reclassified) != 1 {
		t.Fatalf("retry = %+v, want the reclassification applied", again)
	}
	if acct, status := f.bookedAccount(t, row); acct != "3300" || status != "posted" {
		t.Errorf("rebooked to %s (%s), want 3300 posted", acct, status)
	}
}

// The audit row of a rebooked charge names the entry it replaced.
func TestBacklogReclassAuditNamesThePriorEntry(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	f.priceItem(t, "service", "Slipping synthetic", 160000, "once", "non_member")
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, CustomerName: "Walk Up"})
	draft := f.draftCharge(t, row, fallbackRevenueCode, 160000)
	if res := f.backlog(t, true); len(res.Reclassified) != 1 {
		t.Fatalf("apply = %+v, want the charge rebooked", res)
	}
	var prior, entry string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT details->>'prior_journal_entry_id', details->>'journal_entry_id' FROM audit_log
		  WHERE club_id = $1 AND action = 'vipps_charge.auto_assigned' AND resource_id = $2`, f.club.ID, row).Scan(&prior, &entry); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if prior != draft || entry == "" || entry == draft {
		t.Errorf("audit prior = %q, new = %q, want the replaced draft %s and a new entry", prior, entry, draft)
	}
}

// settlementClearingBalance counts posted entries only: a draft payout is not
// in the ledger, and a voided original is left out with its reversal.
func TestSettlementClearingBalanceCountsPostedEntriesOnly(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3400301", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, s.charges...)
	f.clear(t, s)
	balance := func() float64 {
		t.Helper()
		b, err := f.svc.settlementClearingBalance(ctx, f.club.ID, s.number, sweepMSN, s.booking, s.bankRow)
		if err != nil {
			t.Fatalf("clearing balance: %v", err)
		}
		return b
	}
	if got := balance(); !floatNear(got, 0, 0.005) {
		t.Fatalf("balance of a cleared settlement = %.2f, want 0", got)
	}

	// A charge voided but still linked leaves the settlement short by it.
	if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, *f.chargeEntry(t, s.charges[1]), f.club.Treasurer.ID); err != nil {
		t.Fatalf("void: %v", err)
	}
	if got := balance(); !floatNear(got, -250, 0.005) {
		t.Errorf("balance with a voided charge = %.2f, want -250", got)
	}

	// A draft payout is not counted.
	f.toDraft(t, *f.bankEntry(t, s.bankRow))
	if got := balance(); !floatNear(got, 380, 0.005) {
		t.Errorf("balance with a draft payout = %.2f, want 380 (the booked charge less the fee)", got)
	}
}

// A clearing that fails partway leaves nothing behind: no entry, no fee row
// linked, no deposit booked.
func TestClearVippsPayoutIsAtomic(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3400401", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, s.charges...)
	if _, err := f.club.Pool.Exec(ctx, `UPDATE bank_imports SET bank_account_code = '9999' WHERE id = $1`, f.bankImport); err != nil {
		t.Fatalf("point the deposit at a missing account: %v", err)
	}
	before := f.entryCount(t)

	if _, err := f.svc.ClearVippsPayout(ctx, f.club.ID, s.bankRow, f.club.Treasurer.ID); err == nil {
		t.Fatal("clearing to a missing bank account succeeded")
	} else if errors.Is(err, ErrVippsPayoutRaced) {
		t.Fatalf("unexpected race error: %v", err)
	}
	if got := f.entryCount(t); got != before {
		t.Errorf("a failed clearing left %d entries behind", got-before)
	}
	var linkedFees, linkedPayouts int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE row_type = 'fee'), count(*) FILTER (WHERE row_type = 'payout')
		   FROM vipps_import_rows WHERE club_id = $1 AND journal_entry_id IS NOT NULL
		    AND row_type IN ('fee', 'payout')`, f.club.ID).Scan(&linkedFees, &linkedPayouts); err != nil {
		t.Fatalf("count linked rows: %v", err)
	}
	if linkedFees != 0 || linkedPayouts != 0 || f.bankEntry(t, s.bankRow) != nil {
		t.Errorf("a failed clearing left fee rows linked = %d, payout rows linked = %d, deposit booked = %v",
			linkedFees, linkedPayouts, f.bankEntry(t, s.bankRow) != nil)
	}
}
