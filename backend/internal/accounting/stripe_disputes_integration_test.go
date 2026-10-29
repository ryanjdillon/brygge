package accounting

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// disputeTxn adds a dispute (negative amount: money withdrawn) or dispute
// reversal (positive) of the charge stored as chargeBT to the fake balance.
// A positive feeOre is Stripe's dispute fee, a negative one a fee returned.
func (f *stripeFixture) disputeTxn(id string, ageSec, amountOre, feeOre int64, category, chargeBT string) {
	f.src.add(stripe.BalanceTransaction{
		ID: id, Amount: amountOre, Fee: feeOre, Net: amountOre - feeOre, Currency: "nok",
		Created: f.now - ageSec, Type: "adjustment", ReportingCategory: category,
		Source: &stripe.BalanceSource{ID: "du_" + id, Object: "dispute", Charge: stripe.ObjectID("ch_" + chargeBT),
			PaymentIntent: stripe.ObjectID("pi_" + chargeBT)},
	})
}

func (f *stripeFixture) originOf(t *testing.T, rowID string) string {
	t.Helper()
	var o *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT origin_txn_id::text FROM stripe_balance_transactions WHERE id = $1`, rowID).Scan(&o); err != nil {
		t.Fatal(err)
	}
	if o == nil {
		return ""
	}
	return *o
}

// A lost dispute books DR the dispute account / CR clearing for the money
// withdrawn, with the dispute fee in the same entry, and links its charge;
// the faktura stays paid and its order untouched. A win books the mirror,
// returning the fee Stripe gave back. A second pass changes nothing.
func TestBookStripeDisputeLostAndWon(t *testing.T) {
	f := newStripeFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_dp_charge", inv)
	order := f.orderOf(t, charge)
	before := f.invoice(t, inv.ID)

	f.disputeTxn("txn_test_dp_lost", 300, -45000, 15000, stripe.CategoryDispute, "txn_test_dp_charge")
	f.disputeTxn("txn_test_dp_won", 200, 45000, -15000, stripe.CategoryDisputeReversal, "txn_test_dp_charge")
	if res := f.sync(t); len(res.Errors) != 0 || res.NeedsReview[StripeReviewCurrency] != 0 {
		t.Fatalf("sync = %+v", res)
	}
	lost, won := f.rowOf(t, "txn_test_dp_lost"), f.rowOf(t, "txn_test_dp_won")
	if f.bt(t, lost).status != StripeTxnUnbooked || f.bt(t, won).status != StripeTxnUnbooked {
		t.Fatalf("ingested as %+v and %+v, want both unbooked", f.bt(t, lost), f.bt(t, won))
	}

	res := f.book(t)
	if res.DisputesBooked != 2 || len(res.Errors) != 0 || len(res.NeedsReview) != 0 {
		t.Fatalf("pass = %+v, want both disputes booked", res)
	}
	if got := creditLines(t, f.club, f.bt(t, lost).entry); got["7830"] != [2]float64{450, 0} ||
		got["7700"] != [2]float64{150, 0} || got["1580"] != [2]float64{0, 600} || len(got) != 3 {
		t.Errorf("lost dispute = %v, want DR 7830 450, DR 7700 150 / CR 1580 600", got)
	}
	if got := creditLines(t, f.club, f.bt(t, won).entry); got["7830"] != [2]float64{0, 450} ||
		got["7700"] != [2]float64{0, 150} || got["1580"] != [2]float64{600, 0} || len(got) != 3 {
		t.Errorf("won dispute = %v, want DR 1580 600 / CR 7830 450, CR 7700 150", got)
	}
	if f.originOf(t, lost) != charge || f.originOf(t, won) != charge {
		t.Errorf("origins = %q, %q, want the charge %s", f.originOf(t, lost), f.originOf(t, won), charge)
	}
	if got := f.balance(t, "7830"); got != 0 {
		t.Errorf("dispute account balance = %v after the win, want 0", got)
	}
	if after := f.invoice(t, inv.ID); after != before {
		t.Errorf("faktura = %+v, want it untouched (%+v)", after, before)
	}
	if got := f.orderStatus(t, order); got == "refunded" {
		t.Errorf("order status = %q, want it untouched", got)
	}
	var refunded int64
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT refunded_ore FROM stripe_balance_transactions WHERE id = $1`, charge).Scan(&refunded); err != nil || refunded != 0 {
		t.Errorf("charge refunded_ore = %d (%v), want 0", refunded, err)
	}

	entries := f.entryCount(t)
	if again := f.book(t); again.DisputesBooked != 0 || f.entryCount(t) != entries {
		t.Errorf("second pass = %+v, entries %d -> %d", again, entries, f.entryCount(t))
	}
	f.sync(t)
	if f.bt(t, lost).status != StripeTxnBooked {
		t.Errorf("a re-sync changed the booked dispute: %+v", f.bt(t, lost))
	}
}

// A dispute books without a charge in Brygge, and without the fee line when
// Stripe took none; a reversal that returns no fee books the principal only.
func TestBookStripeDisputeNeedsNoCharge(t *testing.T) {
	f := newStripeFixture(t)
	f.disputeTxn("txn_test_dp_nocharge", 300, -12000, 0, stripe.CategoryDispute, "txn_test_unknown")
	f.disputeTxn("txn_test_dp_nocharge_win", 200, 12000, 0, stripe.CategoryDisputeReversal, "txn_test_unknown")
	f.sync(t)

	if res := f.book(t); res.DisputesBooked != 2 || len(res.Errors) != 0 {
		t.Fatalf("pass = %+v, want both booked", res)
	}
	lost := f.rowOf(t, "txn_test_dp_nocharge")
	if got := creditLines(t, f.club, f.bt(t, lost).entry); got["7830"] != [2]float64{120, 0} || got["1580"] != [2]float64{0, 120} || len(got) != 2 {
		t.Errorf("dispute = %v, want DR 7830 / CR 1580 120", got)
	}
	if f.originOf(t, lost) != "" {
		t.Errorf("origin = %q, want none", f.originOf(t, lost))
	}
	won := f.rowOf(t, "txn_test_dp_nocharge_win")
	if got := creditLines(t, f.club, f.bt(t, won).entry); got["1580"] != [2]float64{120, 0} || got["7830"] != [2]float64{0, 120} || len(got) != 2 {
		t.Errorf("reversal = %v, want DR 1580 / CR 7830 120", got)
	}
}

// Without a usable dispute account the dispute is held and retried by every
// pass (and left alone by a re-sync) until the account is there. The
// account is clubs.stripe_dispute_account, else 7830.
func TestBookStripeDisputeAccountMissing(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.club.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE accounts SET is_active = false WHERE club_id = $1 AND code = '7830'`, f.club.ID)
	f.disputeTxn("txn_test_dp_noacct", 300, -9000, 1500, stripe.CategoryDispute, "txn_test_x")
	f.sync(t)
	row := f.rowOf(t, "txn_test_dp_noacct")

	res := f.book(t)
	if res.DisputesBooked != 0 || res.NeedsReview[StripeReviewDisputeAccountMissing] != 1 {
		t.Fatalf("pass = %+v, want the dispute held for its account", res)
	}
	if got := f.bt(t, row); got.status != StripeTxnNeedsReview || got.reason != StripeReviewDisputeAccountMissing || got.entry != "" {
		t.Fatalf("row = %+v", got)
	}
	f.sync(t)
	if got := f.bt(t, row); got.reason != StripeReviewDisputeAccountMissing {
		t.Errorf("a re-sync reclassified the held row: %+v", got)
	}
	if again := f.book(t); again.NeedsReview[StripeReviewDisputeAccountMissing] != 1 {
		t.Errorf("the held dispute was not retried: %+v", again)
	}
	if got := stripeAllowedActions(stripeTxnState{category: "dispute", status: StripeTxnNeedsReview,
		reviewReason: StripeReviewDisputeAccountMissing, currency: "nok"}); len(got) != 1 || got[0] != StripeActionDismiss {
		t.Errorf("actions = %v, want dismiss only", got)
	}

	// The club's own account is used when set.
	exec(`UPDATE clubs SET stripe_dispute_account = '7790' WHERE id = $1`, f.club.ID)
	if res := f.book(t); res.DisputesBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("pass with a custom account = %+v", res)
	}
	if got := creditLines(t, f.club, f.bt(t, row).entry); got["7790"] != [2]float64{90, 0} || got["7830"] != ([2]float64{}) {
		t.Errorf("entry = %v, want the loss on 7790", got)
	}

	// A configured account that does not exist holds the next one.
	exec(`UPDATE clubs SET stripe_dispute_account = '7999' WHERE id = $1`, f.club.ID)
	f.disputeTxn("txn_test_dp_noacct2", 100, -5000, 0, stripe.CategoryDispute, "txn_test_y")
	f.sync(t)
	if res := f.book(t); res.NeedsReview[StripeReviewDisputeAccountMissing] != 1 {
		t.Fatalf("pass = %+v, want the second dispute held", res)
	}

	// Back to the default, with the account active again: the held one books.
	exec(`UPDATE clubs SET stripe_dispute_account = NULL WHERE id = $1`, f.club.ID)
	exec(`UPDATE accounts SET is_active = true WHERE club_id = $1 AND code = '7830'`, f.club.ID)
	second := f.rowOf(t, "txn_test_dp_noacct2")
	if res := f.book(t); res.DisputesBooked != 1 || f.bt(t, second).status != StripeTxnBooked {
		t.Fatalf("pass = %+v, row %+v, want it booked", res, f.bt(t, second))
	}
	if got := creditLines(t, f.club, f.bt(t, second).entry); got["7830"] != [2]float64{50, 0} {
		t.Errorf("entry = %v, want DR 7830 50", got)
	}
}

// A dispute in another currency is never booked.
func TestStripeDisputeOtherCurrencyStaysInReview(t *testing.T) {
	f := newStripeFixture(t)
	f.src.add(stripe.BalanceTransaction{
		ID: "txn_test_dp_eur", Amount: -5000, Net: -5000, Currency: "eur", Created: f.now - 100, ReportingCategory: stripe.CategoryDispute,
		Source: &stripe.BalanceSource{ID: "du_eur", Object: "dispute", Charge: "ch_eur"},
	})
	f.sync(t)
	row := f.rowOf(t, "txn_test_dp_eur")
	if got := f.bt(t, row); got.status != StripeTxnNeedsReview || got.reason != StripeReviewCurrency {
		t.Fatalf("row = %+v, want currency review", got)
	}
	if res := f.book(t); res.DisputesBooked != 0 || f.bt(t, row).entry != "" {
		t.Errorf("pass = %+v, row %+v", res, f.bt(t, row))
	}
}

// A booked dispute is undone with its entry voided and the row held; the
// person hands it back to the pass, which books it again.
func TestUnassignAndRebookStripeDispute(t *testing.T) {
	f := newStripeFixture(t)
	f.disputeTxn("txn_test_dp_un", 300, -9000, 1500, stripe.CategoryDispute, "txn_test_x")
	f.sync(t)
	row := f.rowOf(t, "txn_test_dp_un")
	f.book(t)
	first := f.bt(t, row).entry
	if first == "" {
		t.Fatal("dispute not booked")
	}
	if got := f.item(t, StripeTxnBooked, row).AllowedActions; len(got) != 1 || got[0] != StripeActionUnassign {
		t.Fatalf("booked actions = %v, want unassign", got)
	}

	res, err := f.unassign(row)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if res.ReviewReason != StripeReviewUnassigned || f.entryStatus(t, first) != "voided" {
		t.Errorf("unassign = %+v, entry %s", res, f.entryStatus(t, first))
	}
	got := f.item(t, StripeTxnNeedsReview, row).AllowedActions
	if len(got) != 3 || got[0] != StripeActionRebook {
		t.Fatalf("held actions = %v, want rebook, assign_account, dismiss", got)
	}
	if again := f.book(t); again.DisputesBooked != 0 {
		t.Fatalf("the pass retried a row a person holds: %+v", again)
	}

	rb, err := f.svc.RebookStripeTxn(context.Background(), f.club.ID, f.club.Treasurer.ID, row)
	if err != nil || rb.Outcome != StripeRebookBooked {
		t.Fatalf("rebook = %+v, %v", rb, err)
	}
	if now := f.bt(t, row); now.status != StripeTxnBooked || now.entry == first {
		t.Errorf("row = %+v, want a new entry", now)
	}
	if got := f.balance(t, "7830"); got != 90 {
		t.Errorf("dispute account = %v, want the loss booked once (90)", got)
	}
}

// Disputes held as refund_legacy by migration 000119 are handed back with a
// rebook; migration 000120 hands the ones that are safe back by itself.
func TestStripeDisputeLegacyRows(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()

	legacy := f.insertBT(t, stripeBT{id: "txn_test_lg_dp", category: "dispute", amountOre: -3000, feeOre: 1500,
		status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy})
	if got := f.item(t, StripeTxnNeedsReview, legacy).AllowedActions; len(got) != 2 || got[0] != StripeActionRebook || got[1] != StripeActionDismiss {
		t.Fatalf("legacy dispute actions = %v, want rebook, dismiss", got)
	}
	rb, err := f.svc.RebookStripeTxn(ctx, f.club.ID, f.club.Treasurer.ID, legacy)
	if err != nil || rb.Outcome != StripeRebookBooked {
		t.Fatalf("rebook = %+v, %v", rb, err)
	}

	reversal := f.insertBT(t, stripeBT{id: "txn_test_lg_rev", category: "dispute_reversal", amountOre: 3000,
		status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy})
	dismissed := f.insertBT(t, stripeBT{id: "txn_test_lg_dismissed", category: "dispute", amountOre: -3000,
		status: StripeTxnDismissed, reason: StripeReviewRefundLegacy})
	refund := f.insertBT(t, stripeBT{id: "txn_test_lg_refund", category: "refund", amountOre: -3000,
		status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy})
	pending := f.insertBT(t, stripeBT{id: "txn_test_lg_pending", category: "dispute", amountOre: -3000,
		status: StripeTxnNeedsReview, reason: "refund_pending_bry446"})
	cleared := f.insertBT(t, stripeBT{id: "txn_test_lg_cleared", category: "dispute", amountOre: -3000,
		status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy})
	pendingCleared := f.insertBT(t, stripeBT{id: "txn_test_lg_pending_cleared", category: "dispute", amountOre: -3000,
		status: StripeTxnNeedsReview, reason: "refund_pending_bry446"})
	foreign := f.insertBT(t, stripeBT{id: "txn_test_lg_eur", category: "dispute", amountOre: -3000,
		status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy})
	if _, err := f.club.Pool.Exec(ctx, `UPDATE stripe_balance_transactions SET currency = 'eur' WHERE id = $1`, foreign); err != nil {
		t.Fatal(err)
	}
	var payoutID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO stripe_payouts (club_id, payout_id, amount_ore, currency, arrival_date, status, automatic, journal_entry_id)
		 VALUES ($1, 'po_test_lg', 1000, 'nok', current_date, 'paid', true, $2) RETURNING id`,
		f.club.ID, f.postedManualEntry(t, "Synthetic payout clearing")).Scan(&payoutID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.club.Pool.Exec(ctx, `UPDATE stripe_balance_transactions SET payout_id = $2 WHERE id IN ($1, $3)`, cleared, payoutID, pendingCleared); err != nil {
		t.Fatal(err)
	}

	sql, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), "000120_stripe_disputes.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.club.Pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("running the migration: %v", err)
		}
	}
	want := map[string]storedBT{
		reversal:  {status: StripeTxnUnbooked},
		pending:   {status: StripeTxnUnbooked},
		dismissed: {status: StripeTxnDismissed, reason: StripeReviewRefundLegacy},
		refund:    {status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy},
		cleared:   {status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy},
		// Left held so that a re-sync cannot reclassify it to unbooked.
		pendingCleared: {status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy},
		foreign:        {status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy},
	}
	for id, w := range want {
		if got := f.bt(t, id); got != w {
			t.Errorf("row %s = %+v, want %+v", id, got, w)
		}
	}
	if res := f.book(t); res.DisputesBooked != 2 || len(res.Errors) != 0 {
		t.Errorf("pass = %+v, want the two reclassified disputes booked", res)
	}
}

// Account 7830 is in the default chart and is added to the charts of clubs
// seeded without it, once; a club with no chart gets nothing.
func TestDisputeAccountMigrationSeedsCharts(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	bare := testutil.SeedClub(t, f.club.Pool)
	count := func(clubID string) (n int) {
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT count(*) FROM accounts WHERE club_id = $1 AND code = '7830' AND is_active AND account_type = 'expense'`,
			clubID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(f.club.ID) != 1 {
		t.Fatal("a freshly seeded chart lacks 7830")
	}
	if _, err := f.club.Pool.Exec(ctx, `DELETE FROM accounts WHERE club_id = $1 AND code = '7830'`, f.club.ID); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), "000120_stripe_disputes.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.club.Pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("running the migration: %v", err)
		}
	}
	if count(f.club.ID) != 1 || count(bare) != 0 {
		t.Errorf("7830 rows: seeded club %d, club without a chart %d, want 1 and 0", count(f.club.ID), count(bare))
	}
}

func TestStripeDisputeBookingDate(t *testing.T) {
	// The dispute row is dated by the day Stripe withdrew the money, like
	// every other balance row.
	f := newStripeFixture(t)
	row := f.insertBT(t, stripeBT{id: "txn_test_dp_date", category: "dispute", amountOre: -4000,
		created: time.Date(f.club.Year, 3, 2, 10, 0, 0, 0, time.UTC)})
	if res := f.book(t); res.DisputesBooked != 1 {
		t.Fatalf("pass = %+v", res)
	}
	var date string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT entry_date::text FROM journal_entries WHERE id = $1`, f.bt(t, row).entry).Scan(&date); err != nil {
		t.Fatal(err)
	}
	if date != time.Date(f.club.Year, 3, 2, 0, 0, 0, 0, time.UTC).Format("2006-01-02") {
		t.Errorf("entry dated %s", date)
	}
}

// The entry side follows the sign of the amount. A row that moved money the
// wrong way for its category is held for a person (not retried), and a row
// that moved no money is marked booked without an entry, once, with no error.
func TestBookStripeDisputeDirectionAndNoMoney(t *testing.T) {
	f := newStripeFixture(t)
	wrong := f.insertBT(t, stripeBT{id: "txn_test_dp_wrong", category: "dispute", amountOre: 5000})
	wrongRev := f.insertBT(t, stripeBT{id: "txn_test_dp_wrong_rev", category: "dispute_reversal", amountOre: -5000})
	nothing := f.insertBT(t, stripeBT{id: "txn_test_dp_nothing", category: "dispute", amountOre: 0})
	nothingRev := f.insertBT(t, stripeBT{id: "txn_test_dp_nothing_rev", category: "dispute_reversal", amountOre: 0})
	feeOnly := f.insertBT(t, stripeBT{id: "txn_test_dp_feeonly", category: "dispute", amountOre: 0, feeOre: 1500})

	res := f.book(t)
	if len(res.Errors) != 0 || res.DisputesBooked != 3 || res.NeedsReview[StripeReviewDisputeDirection] != 2 {
		t.Fatalf("pass = %+v, want 3 booked (two of them without money) and 2 held as dispute_direction", res)
	}
	for _, id := range []string{wrong, wrongRev} {
		if got := f.bt(t, id); got.status != StripeTxnNeedsReview || got.reason != StripeReviewDisputeDirection || got.entry != "" {
			t.Errorf("row %s = %+v", id, got)
		}
	}
	for _, id := range []string{nothing, nothingRev} {
		if got := f.bt(t, id); got.status != StripeTxnBooked || got.reason != "" || got.entry != "" {
			t.Errorf("row %s = %+v, want booked with no entry", id, got)
		}
	}
	if got := creditLines(t, f.club, f.bt(t, feeOnly).entry); got["7700"] != [2]float64{15, 0} || got["1580"] != [2]float64{0, 15} || len(got) != 2 {
		t.Errorf("fee-only dispute = %v, want the fee alone", got)
	}
	// Nothing retries them; a re-sync leaves them; a person may book or dismiss.
	if again := f.book(t); len(again.Errors) != 0 || again.DisputesBooked != 0 || len(again.NeedsReview) != 0 {
		t.Errorf("second pass = %+v, want nothing to do", again)
	}
	if got := f.item(t, StripeTxnNeedsReview, wrong).AllowedActions; len(got) != 2 || got[0] != StripeActionAssignAccount || got[1] != StripeActionDismiss {
		t.Errorf("actions = %v, want assign_account, dismiss", got)
	}
}

// A rebooked dispute is reported as booked.
func TestStripeRebookOutcomeCountsDisputes(t *testing.T) {
	out, _ := stripeRebookOutcome(stripeBookOutcome{kind: stripeOutcomeDispute}, nil)
	if out != StripeRebookBooked {
		t.Errorf("outcome = %q, want booked", out)
	}
}
