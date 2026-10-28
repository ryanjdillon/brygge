package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func (f *stripeFixture) clearManually(t *testing.T, payoutRow, bankRow string) (*StripePayoutClearResult, error) {
	t.Helper()
	return f.svc.ClearStripePayoutManually(context.Background(), f.club.ID, f.club.Treasurer.ID, payoutRow, bankRow)
}

// refusalCode is the StripeClearRefusal code of err, "" for anything else.
func refusalCode(err error) string {
	var r *StripeClearRefusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

// A manual payout, deposited outside the sweep's date window, is cleared by
// a person: DR 1920 / CR 1580 on the bank date, posted and sourced to the
// deposit, the payout and its own payout row booked with it, the deposit
// not marked as auto-matched. Stripe lists no members of a manual payout,
// so no clearing account remainder is reported.
func TestManualClearOfManualPayout(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	po := f.insertPayout(t, payoutSpec{id: "po_test_hand", amountOre: 5000, arrival: f.day(6, 10), manual: true, unlisted: true})
	payoutBT := f.insertBT(t, stripeBT{id: "txn_test_hand_po", category: "payout", amountOre: -5000,
		created: time.Date(f.club.Year, 6, 9, 10, 0, 0, 0, time.UTC)})
	if _, err := f.club.Pool.Exec(ctx, `UPDATE stripe_balance_transactions SET payout_id = $2 WHERE id = $1`, payoutBT, po); err != nil {
		t.Fatal(err)
	}
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 18), 5000, "STRIPE PAYOUT")

	if res := f.sweep(t); sweepCounts(res) != [4]int{0, 0, 2, 0} {
		t.Fatalf("sweep = %+v, want the manual payout and its deposit left to a person", res)
	}
	res, err := f.clearManually(t, po, row)
	if err != nil {
		t.Fatalf("ClearStripePayoutManually: %v", err)
	}
	if res.PayoutID != "po_test_hand" || res.BankRowID != row || res.PayoutRowID != po || res.JournalEntryID == "" ||
		res.MembersCount != 0 || res.MembersNetOre != 0 || res.ClearingBalanceOre != nil {
		t.Fatalf("result = %+v, want the payout cleared with no remainder reported", res)
	}
	if got := bankRowEntry(t, f.club, row); got != res.JournalEntryID {
		t.Errorf("bank row entry = %q, want %q", got, res.JournalEntryID)
	}
	lines := creditLines(t, f.club, res.JournalEntryID)
	if len(lines) != 2 || lines["1920"] != [2]float64{50, 0} || lines["1580"] != [2]float64{0, 50} {
		t.Errorf("lines = %v, want DR 1920 / CR 1580 50", lines)
	}
	var status, source, sourceID, entryDate string
	var autoMatched bool
	var payoutEntry *string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT je.status, je.source, je.source_id, je.entry_date::text, b.auto_matched, p.journal_entry_id::text
		   FROM journal_entries je, bank_import_rows b, stripe_payouts p
		  WHERE je.id = $1 AND b.id = $2 AND p.id = $3`, res.JournalEntryID, row, po,
	).Scan(&status, &source, &sourceID, &entryDate, &autoMatched, &payoutEntry); err != nil {
		t.Fatal(err)
	}
	if status != "posted" || source != "stripe" || sourceID != row || entryDate != f.day(6, 18).Format("2006-01-02") ||
		autoMatched || payoutEntry == nil || *payoutEntry != res.JournalEntryID {
		t.Errorf("entry %s %s %s %s, auto_matched %v, payout entry %v", status, source, sourceID, entryDate, autoMatched, payoutEntry)
	}
	if bt := f.bt(t, payoutBT); bt.status != StripeTxnBooked || bt.entry != res.JournalEntryID {
		t.Errorf("payout transaction = %+v, want booked with the clearing entry", bt)
	}

	// Clearing it again books nothing and says the payout is cleared.
	if _, err := f.clearManually(t, po, row); refusalCode(err) != StripeClearPayoutNotClearable {
		t.Errorf("second clear: %v, want %s", err, StripeClearPayoutNotClearable)
	}
	if n := f.clearingEntries(t); n != 1 {
		t.Errorf("%d clearing entries, want 1", n)
	}
	if res := f.sweep(t); sweepCounts(res) != [4]int{} {
		t.Errorf("sweep after the clearing = %+v, want nothing to look at", res)
	}
}

// The BRY-460 case: the funds of a failed payout were carried into a later
// one, so its members do not add up to it and the sweep leaves it to a
// person. The person clears it anyway and is told the remainder on the
// clearing account.
func TestManualClearReportsClearingRemainder(t *testing.T) {
	f := newStripeFixture(t)
	po := f.insertPayout(t, payoutSpec{id: "po_test_carried", amountOre: 5000, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_carried_fee", category: "fee", amountOre: 3000,
		created: time.Date(f.club.Year, 6, 8, 10, 0, 0, 0, time.UTC)})
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 10), 5000, "STRIPE")
	if res := f.book(t); res.FeesBooked != 1 {
		t.Fatalf("booking = %+v, want the member booked", res)
	}
	if res := f.sweep(t); sweepCounts(res) != [4]int{0, 0, 1, 0} {
		t.Fatalf("sweep = %+v, want the payout skipped on its sum", res)
	}

	res, err := f.clearManually(t, po, row)
	if err != nil {
		t.Fatalf("ClearStripePayoutManually: %v", err)
	}
	if res.MembersCount != 1 || res.MembersNetOre != 3000 || res.ClearingBalanceOre == nil || *res.ClearingBalanceOre != -2000 {
		t.Errorf("result = %+v, want members net 30 kr and a remainder of -20 kr", res)
	}
	if b := f.accountBalance(t, "1580"); !floatNear(b, -20, 0.001) {
		t.Errorf("1580 balance = %v, want -20", b)
	}
}

// What a person may not clear is refused with a code and nothing booked.
func TestManualClearRefusals(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	if _, err := f.club.Pool.Exec(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'closed')`, f.club.ID, f.club.Year-1); err != nil {
		t.Fatal(err)
	}
	imp := f.club.NewBankImport(t)
	payout := func(id string, ore int64, spec payoutSpec) string {
		spec.id, spec.amountOre = id, ore
		if spec.arrival.IsZero() {
			spec.arrival = f.day(6, 10)
		}
		po := f.insertPayout(t, spec)
		f.member(t, po, stripeBT{id: "txn_" + id, category: "charge", amountOre: ore, status: StripeTxnBooked})
		return po
	}

	open := f.insertPayout(t, payoutSpec{id: "po_test_open", amountOre: 4000, arrival: f.day(6, 10)})
	f.member(t, open, stripeBT{id: "txn_test_open_c", category: "charge", amountOre: 3000})
	f.member(t, open, stripeBT{id: "txn_test_open_f", category: "fee", amountOre: 1000})
	closedDay := time.Date(f.club.Year-1, 12, 30, 0, 0, 0, 0, time.UTC)
	missingDay := time.Date(f.club.Year+1, 1, 5, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		payout string
		row    string
		code   string
	}{
		{"members open", open, f.stripeDeposit(t, imp, f.day(6, 10), 4000, "STRIPE"), StripeClearMembersOpen},
		{"amount", payout("po_test_amount", 5000, payoutSpec{}), f.stripeDeposit(t, imp, f.day(6, 10), 5001, "STRIPE"), StripeClearAmountMismatch},
		{"no descriptor", payout("po_test_plain", 6000, payoutSpec{}),
			f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.day(6, 10), Cents: 6000, Counterpart: "Ola Testmann"}), StripeClearNotStripeDeposit},
		{"vipps settlement", payout("po_test_vipps", 6100, payoutSpec{}),
			f.stripeDeposit(t, imp, f.day(6, 10), 6100, "Utb. 3000100 Vippsnr 548005"), StripeClearNotStripeDeposit},
		{"failed payout", payout("po_test_failed", 6200, payoutSpec{status: "failed"}),
			f.stripeDeposit(t, imp, f.day(6, 10), 6200, "STRIPE"), StripeClearPayoutNotClearable},
		{"closed period", payout("po_test_closed", 6300, payoutSpec{arrival: closedDay}),
			f.stripeDeposit(t, imp, closedDay, 6300, "STRIPE"), StripeClearPeriodClosed},
		{"no period", payout("po_test_missing", 6400, payoutSpec{arrival: missingDay}),
			f.stripeDeposit(t, imp, missingDay, 6400, "STRIPE"), StripeClearNoPeriod},
	}
	dismissedPO := payout("po_test_dismissed", 6500, payoutSpec{})
	dismissedRow := f.stripeDeposit(t, imp, f.day(6, 10), 6500, "STRIPE")
	if err := f.svc.DismissBankRow(ctx, f.club.ID, f.club.Treasurer.ID, dismissedRow, "internal_transfer"); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, struct {
		name   string
		payout string
		row    string
		code   string
	}{"dismissed row", dismissedPO, dismissedRow, StripeClearBankRowNotEligible})

	periods := func() int {
		var n int
		if err := f.club.Pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := periods()
	for _, c := range cases {
		_, err := f.clearManually(t, c.payout, c.row)
		if got := refusalCode(err); got != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
		if c.code == StripeClearMembersOpen {
			var r *StripeClearRefusal
			if errors.As(err, &r) && (r.OpenMembers != 2 || r.Reason == "") {
				t.Errorf("%s: refusal %+v, want 2 open members and a reason", c.name, r)
			}
		}
	}
	if _, err := f.clearManually(t, "00000000-0000-0000-0000-000000000001", dismissedRow); !errors.Is(err, ErrStripePayoutNotFound) {
		t.Errorf("unknown payout: %v", err)
	}
	if _, err := f.clearManually(t, open, "00000000-0000-0000-0000-000000000001"); !errors.Is(err, ErrBankRowNotFound) {
		t.Errorf("unknown row: %v", err)
	}
	var entries int
	if err := f.club.Pool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 || periods() != before {
		t.Errorf("%d entries, periods %d → %d; want no entry and no period created", entries, before, periods())
	}
}

// While a Stripe sync or booking holds the club's slot, a manual clearing
// answers busy instead of waiting.
func TestManualClearBusy(t *testing.T) {
	f := newStripeFixture(t)
	saved := manualClearSlotWait
	manualClearSlotWait = 200 * time.Millisecond
	t.Cleanup(func() { manualClearSlotWait = saved })
	po := f.insertPayout(t, payoutSpec{id: "po_test_busy", amountOre: 2000, arrival: f.day(6, 10), manual: true})
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 10), 2000, "STRIPE")
	release, ok := f.svc.stripeSync.tryAcquire(f.club.ID)
	if !ok {
		t.Fatal("slot taken")
	}
	_, err := f.clearManually(t, po, row)
	if !errors.Is(err, ErrStripeSyncBusy) {
		release()
		t.Fatalf("while busy: %v, want ErrStripeSyncBusy", err)
	}

	// A holder that lets go within the wait is waited for.
	go func() {
		time.Sleep(50 * time.Millisecond)
		release()
	}()
	if _, err := f.clearManually(t, po, row); err != nil {
		t.Errorf("slot released during the wait: %v", err)
	}
}

// An automatic payout whose members Stripe has not listed would pass "every
// member booked" with none; it is refused until Synk Stripe lists them.
func TestManualClearRefusesUnlistedAutomaticPayout(t *testing.T) {
	f := newStripeFixture(t)
	po := f.insertPayout(t, payoutSpec{id: "po_test_unlisted_auto", amountOre: 2000, arrival: f.day(6, 10), unlisted: true})
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 10), 2000, "STRIPE")
	if _, err := f.clearManually(t, po, row); refusalCode(err) != StripeClearMembersNotListed {
		t.Fatalf("unlisted automatic payout: %v, want %s", err, StripeClearMembersNotListed)
	}
	if n := f.clearingEntries(t); n != 0 {
		t.Errorf("%d clearing entries, want none", n)
	}
}

// A manual payout has no members, so it clears only once every balance row
// that could have funded it (linked to no payout, available by its
// arrival) is booked or dismissed. A row after the arrival does not count.
func TestManualClearOfManualPayoutWaitsForOpenBalance(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	po := f.insertPayout(t, payoutSpec{id: "po_test_funded", amountOre: 4000, arrival: f.day(6, 10), manual: true, unlisted: true})
	f.insertBT(t, stripeBT{id: "txn_test_funding_fee", category: "fee", amountOre: 4000,
		created: time.Date(f.club.Year, 6, 5, 10, 0, 0, 0, time.UTC)})
	f.insertBT(t, stripeBT{id: "txn_test_later_refund", category: "refund", amountOre: -500,
		status: StripeTxnNeedsReview, reason: StripeReviewRefundPending,
		created: time.Date(f.club.Year, 6, 20, 10, 0, 0, 0, time.UTC)})
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 10), 4000, "STRIPE")

	listed, err := f.svc.ListUnclearedStripePayouts(ctx, f.club.ID)
	if err != nil || len(listed) != 1 || listed[0].OpenBalance != 1 {
		t.Fatalf("list = %+v (%v), want open_balance 1", listed, err)
	}
	_, err = f.clearManually(t, po, row)
	var r *StripeClearRefusal
	if !errors.As(err, &r) || r.Code != StripeClearBalanceOpen || r.OpenBalance != 1 || r.Reason == "" {
		t.Fatalf("with an open charge: %v, want %s with 1 open row", err, StripeClearBalanceOpen)
	}
	if res := f.book(t); res.FeesBooked != 1 {
		t.Fatalf("booking = %+v, want the fee booked", res)
	}
	res, err := f.clearManually(t, po, row)
	if err != nil {
		t.Fatalf("after booking: %v", err)
	}
	if res.JournalEntryID == "" || bankRowEntry(t, f.club, row) != res.JournalEntryID {
		t.Errorf("result = %+v, want the deposit booked", res)
	}
	if b := f.accountBalance(t, "1580"); !floatNear(b, 0, 0.001) {
		t.Errorf("1580 balance = %v, want 0", b)
	}
}

// Unassigning a manually cleared deposit releases the payout like an
// automatic clearing's, and it can be cleared again.
func TestManualClearUnassignAndClearAgain(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	po := f.insertPayout(t, payoutSpec{id: "po_test_again", amountOre: 3000, arrival: f.day(6, 10), manual: true, unlisted: true})
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 11), 3000, "STRIPE")
	first, err := f.clearManually(t, po, row)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	listed, err := f.svc.ListUnclearedStripePayouts(ctx, f.club.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != po {
		t.Fatalf("uncleared after unassign = %+v (%v), want the payout back", listed, err)
	}
	second, err := f.clearManually(t, po, row)
	if err != nil || second.JournalEntryID == first.JournalEntryID {
		t.Fatalf("second clear = %+v (%v), want a new entry", second, err)
	}
	if b := f.accountBalance(t, "1920"); !floatNear(b, 30, 0.001) {
		t.Errorf("1920 balance = %v, want 30 (the void nets the first clearing out)", b)
	}
}

// The picker's list: uncleared paid NOK payouts with their members.
func TestListUnclearedStripePayouts(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	po := f.insertPayout(t, payoutSpec{id: "po_test_list", amountOre: 4500, arrival: f.day(6, 12)})
	f.member(t, po, stripeBT{id: "txn_test_list_c", category: "charge", amountOre: 5000, status: StripeTxnBooked})
	f.member(t, po, stripeBT{id: "txn_test_list_f", category: "fee", amountOre: -300})
	f.insertPayout(t, payoutSpec{id: "po_test_list_manual", amountOre: 1000, arrival: f.day(6, 1), manual: true, unlisted: true})
	f.insertBT(t, stripeBT{id: "txn_test_list_unlinked", category: "fee", amountOre: -100,
		created: time.Date(f.club.Year, 5, 20, 10, 0, 0, 0, time.UTC)})
	f.insertPayout(t, payoutSpec{id: "po_test_list_failed", amountOre: 1000, arrival: f.day(6, 1), status: "failed"})
	f.insertPayout(t, payoutSpec{id: "po_test_list_transit", amountOre: 1000, arrival: f.day(6, 1), status: "in_transit"})

	got, err := f.svc.ListUnclearedStripePayouts(ctx, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].PayoutID != "po_test_list" || got[1].PayoutID != "po_test_list_manual" {
		t.Fatalf("list = %+v, want the paid payouts, newest first", got)
	}
	if p := got[0]; p.ID != po || p.Amount != 45 || p.AmountOre != 4500 || p.ArrivalDate != f.day(6, 12).Format("2006-01-02") ||
		!p.Automatic || !p.MembersListed || p.MembersCount != 2 || p.OpenMembers != 1 || p.MembersNetOre != 4700 {
		t.Errorf("automatic payout = %+v", p)
	}
	if got[0].OpenBalance != 0 {
		t.Errorf("automatic payout open_balance = %d, want 0", got[0].OpenBalance)
	}
	if p := got[1]; p.Automatic || p.MembersListed || p.MembersCount != 0 || p.OpenMembers != 0 || p.OpenBalance != 1 {
		t.Errorf("manual payout = %+v", p)
	}
}
