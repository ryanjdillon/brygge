package accounting

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// payoutSpec describes a stripe_payouts row as ingest would leave it.
type payoutSpec struct {
	id        string
	amountOre int64
	arrival   time.Time
	manual    bool
	status    string
	unlisted  bool
}

// insertPayout stores a payout for the fixture's club and returns its row id.
func (f *stripeFixture) insertPayout(t *testing.T, p payoutSpec) string {
	t.Helper()
	if p.status == "" {
		p.status = "paid"
	}
	var id string
	if err := f.club.Pool.QueryRow(context.Background(),
		`INSERT INTO stripe_payouts (club_id, payout_id, amount_ore, currency, arrival_date, status, automatic, members_listed_at)
		 VALUES ($1, $2, $3, 'nok', $4, $5, $6, CASE WHEN $7 THEN NULL ELSE now() END) RETURNING id`,
		f.club.ID, p.id, p.amountOre, p.arrival, p.status, !p.manual, p.unlisted,
	).Scan(&id); err != nil {
		t.Fatalf("insert payout %s: %v", p.id, err)
	}
	return id
}

// member stores bt as a member of the payout.
func (f *stripeFixture) member(t *testing.T, payoutRow string, bt stripeBT) string {
	t.Helper()
	id := f.insertBT(t, bt)
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE stripe_balance_transactions SET payout_id = $2 WHERE id = $1`, id, payoutRow); err != nil {
		t.Fatal(err)
	}
	return id
}

// stripeDeposit seeds an incoming bank row with the given text.
func (f *stripeFixture) stripeDeposit(t *testing.T, imp string, day time.Time, cents int64, desc string) string {
	t.Helper()
	return f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: day, Cents: cents, Description: desc})
}

// bankRowEntry is a bank row's journal entry id, "" when unbooked.
func bankRowEntry(t *testing.T, c *fin.Club, rowID string) string {
	t.Helper()
	var entry *string
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1`, rowID).Scan(&entry); err != nil {
		t.Fatalf("bank row: %v", err)
	}
	if entry == nil {
		return ""
	}
	return *entry
}

func (f *stripeFixture) sweep(t *testing.T) *StripePayoutSweepResult {
	t.Helper()
	res, err := f.svc.ClearStripePayouts(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("ClearStripePayouts: %v", err)
	}
	return res
}

func (f *stripeFixture) day(month time.Month, d int) time.Time {
	return time.Date(f.club.Year, month, d, 0, 0, 0, 0, time.UTC)
}

// clearingEntries counts the club's payout clearing entries.
func (f *stripeFixture) clearingEntries(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries
		  WHERE club_id = $1 AND source = 'stripe' AND source_table = 'bank_import_rows'`, f.club.ID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// accountBalance counts posted and voided entries, as reports do, so a
// void and its reversal net to zero.
func (f *stripeFixture) accountBalance(t *testing.T, code string) float64 {
	t.Helper()
	var b float64
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(jl.debit - jl.credit), 0) FROM journal_lines jl
		   JOIN accounts a ON a.id = jl.account_id
		   JOIN journal_entries je ON je.id = jl.journal_entry_id
		  WHERE a.club_id = $1 AND a.code = $2 AND je.status IN ('posted', 'voided')`, f.club.ID, code,
	).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

func sweepCounts(r *StripePayoutSweepResult) [4]int {
	if r == nil {
		return [4]int{}
	}
	return [4]int{len(r.Cleared), len(r.Waiting), len(r.Skipped), len(r.Failed)}
}

// A payout clears only once every member is booked: the bank entry is
// DR 1920 / CR 1580 on the bank date, posted and sourced to the deposit,
// and the clearing account nets to zero over the members and the clearing.
// Bank sync runs the sweep after booking, and a re-run changes nothing.
func TestStripePayoutClearsOnceMembersBooked(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	order := catalogOrder(t, f.club, map[string]float64{"service": 120})
	created := time.Date(f.club.Year, 6, 8, 10, 0, 0, 0, time.UTC)
	po := f.insertPayout(t, payoutSpec{id: "po_test_clear", amountOre: 11500, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_charge", category: "charge", amountOre: 12000, feeOre: 300, orderID: order, created: created})
	f.member(t, po, stripeBT{id: "txn_test_fee", category: "fee", stripeTyp: "stripe_fee", amountOre: -200, created: created})
	payoutBT := f.insertBT(t, stripeBT{id: "txn_test_po", category: "payout", amountOre: -11500, created: created.AddDate(0, 0, 1)})
	if _, err := f.club.Pool.Exec(ctx, `UPDATE stripe_balance_transactions SET payout_id = $2 WHERE id = $1`, payoutBT, po); err != nil {
		t.Fatal(err)
	}
	imp := f.club.NewBankImport(t)
	row := f.stripeDeposit(t, imp, f.day(6, 11), 11500, "STRIPE PAYMENTS PAYOUT")

	res := f.sweep(t)
	if sweepCounts(res) != [4]int{0, 1, 0, 0} || res.Waiting[0].BankRowID != row ||
		res.Waiting[0].Reason != "1 kortbetaling venter på bokføring, 1 Stripe-gebyr venter på bokføring" {
		t.Fatalf("before booking = %+v, want the payout waiting on its charge and fee", res)
	}
	listed, err := f.svc.ListUnmatchedBankRows(ctx, f.club.ID, "", "", 0, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ProviderPayout == nil || *listed[0].ProviderPayout != ProviderPayoutStripe ||
		listed[0].ProviderPayoutReason != res.Waiting[0].Reason || listed[0].ProviderPayoutByHand {
		t.Errorf("Tildel row = %+v, want a Stripe payout with the waiting reason", listed)
	}

	sync, err := f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sync.StripeBooking == nil || sync.StripeBooking.ChargesBooked != 1 || sync.StripeBooking.FeesBooked != 1 {
		t.Fatalf("stripe_booking = %+v, want the charge and the fee booked", sync.StripeBooking)
	}
	if sweepCounts(sync.StripePayouts) != [4]int{1, 0, 0, 0} || sync.StripePayouts.Cleared[0].PayoutID != "po_test_clear" {
		t.Fatalf("stripe_payouts = %+v, want the payout cleared", sync.StripePayouts)
	}
	entry := sync.StripePayouts.Cleared[0].EntryID
	if got := bankRowEntry(t, f.club, row); got != entry || entry == "" {
		t.Fatalf("bank row entry = %q, want %q", got, entry)
	}

	lines := creditLines(t, f.club, entry)
	if len(lines) != 2 || lines["1920"] != [2]float64{115, 0} || lines["1580"] != [2]float64{0, 115} {
		t.Errorf("lines = %v, want DR 1920 / CR 1580 115", lines)
	}
	var status, source, sourceTable, sourceID, createdBy, entryDate, desc string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT status, source, source_table, source_id, created_by::text, entry_date::text, description
		   FROM journal_entries WHERE id = $1`, entry,
	).Scan(&status, &source, &sourceTable, &sourceID, &createdBy, &entryDate, &desc); err != nil {
		t.Fatal(err)
	}
	wantDesc := "Stripe-utbetaling (ankomst 10.06." + f.day(6, 10).Format("2006") + ")"
	if status != "posted" || source != "stripe" || sourceTable != "bank_import_rows" || sourceID != row ||
		createdBy != f.club.Treasurer.ID || entryDate != f.day(6, 11).Format("2006-01-02") || desc != wantDesc {
		t.Errorf("entry = %s %s %s %s %s %s %q", status, source, sourceTable, sourceID, createdBy, entryDate, desc)
	}
	var autoMatched bool
	var payoutRow, payoutEntry string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT b.auto_matched, p.bank_row_id::text, p.journal_entry_id::text
		   FROM bank_import_rows b, stripe_payouts p WHERE b.id = $1 AND p.id = $2`, row, po,
	).Scan(&autoMatched, &payoutRow, &payoutEntry); err != nil {
		t.Fatal(err)
	}
	if !autoMatched || payoutRow != row || payoutEntry != entry {
		t.Errorf("links: auto_matched %v, payout bank row %s entry %s", autoMatched, payoutRow, payoutEntry)
	}
	if bt := f.bt(t, payoutBT); bt.status != StripeTxnBooked || bt.entry != entry {
		t.Errorf("payout transaction = %+v, want booked with the clearing entry", bt)
	}
	if b := f.accountBalance(t, "1580"); !floatNear(b, 0, 0.001) {
		t.Errorf("1580 balance = %v, want 0", b)
	}

	again, err := f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.StripePayouts != nil || f.clearingEntries(t) != 1 {
		t.Errorf("re-run: stripe_payouts = %+v, %d clearing entries; want nil and 1", again.StripePayouts, f.clearingEntries(t))
	}
}

// A refund among the members keeps the payout waiting with a reason; once
// it is dismissed the payout clears.
func TestStripePayoutWaitsForRefund(t *testing.T) {
	f := newStripeFixture(t)
	po := f.insertPayout(t, payoutSpec{id: "po_test_refund", amountOre: 5000, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_c", category: "charge", amountOre: 6000, status: StripeTxnBooked})
	refund := f.member(t, po, stripeBT{id: "txn_test_r", category: "refund", amountOre: -1000,
		status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy})
	f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 10), 5000, "Stripe")

	res := f.sweep(t)
	if sweepCounts(res) != [4]int{0, 1, 0, 0} || !strings.Contains(res.Waiting[0].Reason, "1 refusjon til gjennomgang") {
		t.Fatalf("result = %+v, want waiting on the refund", res)
	}
	if n := f.clearingEntries(t); n != 0 {
		t.Fatalf("%d clearing entries, want none", n)
	}
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE stripe_balance_transactions SET status = 'dismissed', dismissed_at = now() WHERE id = $1`, refund); err != nil {
		t.Fatal(err)
	}
	if res := f.sweep(t); sweepCounts(res) != [4]int{1, 0, 0, 0} {
		t.Errorf("after dismissing the refund = %+v, want cleared", res)
	}
}

// A manual payout is never cleared, a failed one is ignored (its deposit is
// a Stripe deposit no payout explains), and a payout whose members were
// not listed waits.
func TestStripePayoutManualFailedAndUnlisted(t *testing.T) {
	f := newStripeFixture(t)
	imp := f.club.NewBankImport(t)
	f.insertPayout(t, payoutSpec{id: "po_test_manual", amountOre: 5000, arrival: f.day(6, 10), manual: true})
	manualRow := f.stripeDeposit(t, imp, f.day(6, 10), 5000, "STRIPE")
	f.insertPayout(t, payoutSpec{id: "po_test_failed", amountOre: 7000, arrival: f.day(6, 10), status: "failed"})
	failedRow := f.stripeDeposit(t, imp, f.day(6, 10), 7000, "STRIPE")
	f.insertPayout(t, payoutSpec{id: "po_test_pending", amountOre: 8000, arrival: f.day(6, 10), status: "in_transit"})
	f.insertPayout(t, payoutSpec{id: "po_test_unlisted", amountOre: 9000, arrival: f.day(6, 10), unlisted: true})
	f.stripeDeposit(t, imp, f.day(6, 10), 9000, "STRIPE")

	res := f.sweep(t)
	if sweepCounts(res) != [4]int{0, 1, 2, 0} {
		t.Fatalf("result = %+v, want the unlisted payout waiting, the manual one and the failed deposit skipped", res)
	}
	if res.Waiting[0].PayoutID != "po_test_unlisted" || res.Waiting[0].Reason != stripeReasonNotListed {
		t.Errorf("waiting = %+v", res.Waiting)
	}
	skipped := map[string]StripePayoutSweepItem{}
	for _, it := range res.Skipped {
		skipped[it.BankRowID] = it
	}
	if it := skipped[manualRow]; it.PayoutID != "po_test_manual" || it.Reason != stripeReasonManual {
		t.Errorf("manual payout = %+v, want skipped as manual", it)
	}
	if it := skipped[failedRow]; it.PayoutID != "" || it.Reason != stripeReasonNoPayout {
		t.Errorf("failed payout's deposit = %+v, want skipped with no payout", it)
	}
	if n := f.clearingEntries(t); n != 0 {
		t.Errorf("%d clearing entries, want none", n)
	}
}

// Two payouts one deposit could be, or two deposits one payout could be,
// are left to a person.
func TestStripePayoutAmbiguousMatches(t *testing.T) {
	f := newStripeFixture(t)
	imp := f.club.NewBankImport(t)
	for _, p := range []struct {
		id      string
		arrival time.Time
	}{{"po_test_a", f.day(6, 10)}, {"po_test_b", f.day(6, 11)}} {
		po := f.insertPayout(t, payoutSpec{id: p.id, amountOre: 4000, arrival: p.arrival})
		f.member(t, po, stripeBT{id: "txn_" + p.id, category: "charge", amountOre: 4000, status: StripeTxnBooked})
	}
	f.stripeDeposit(t, imp, f.day(6, 11), 4000, "STRIPE")

	po := f.insertPayout(t, payoutSpec{id: "po_test_c", amountOre: 9000, arrival: f.day(6, 20)})
	f.member(t, po, stripeBT{id: "txn_test_c", category: "charge", amountOre: 9000, status: StripeTxnBooked})
	f.stripeDeposit(t, imp, f.day(6, 20), 9000, "STRIPE")
	f.stripeDeposit(t, imp, f.day(6, 21), 9000, "STRIPE")

	res := f.sweep(t)
	if sweepCounts(res) != [4]int{0, 0, 3, 0} {
		t.Fatalf("result = %+v, want all three payouts skipped", res)
	}
	for _, it := range res.Skipped {
		want := stripeReasonManyPayouts
		if it.PayoutID == "po_test_c" {
			want = stripeReasonManyRows
		}
		if it.Reason != want {
			t.Errorf("%s: reason %q, want %q", it.PayoutID, it.Reason, want)
		}
	}
	if n := f.clearingEntries(t); n != 0 {
		t.Errorf("%d clearing entries, want none", n)
	}
}

// A deposit without the descriptor is no match; a club's own descriptor
// replaces the default.
func TestStripePayoutDescriptor(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	imp := f.club.NewBankImport(t)
	po := f.insertPayout(t, payoutSpec{id: "po_test_desc", amountOre: 3000, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_desc", category: "charge", amountOre: 3000, status: StripeTxnBooked})
	f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.day(6, 10), Cents: 3000, Description: "Overføring", Counterpart: "Ola Testmann"})

	res := f.sweep(t)
	if sweepCounts(res) != [4]int{0, 1, 0, 0} || !strings.HasPrefix(res.Waiting[0].Reason, "venter på bankinnbetalingen: 30,00 kr") {
		t.Fatalf("result = %+v, want waiting for its deposit", res)
	}

	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE clubs SET stripe_payout_descriptor = '^UTBETALING KLUBB' WHERE id = $1`, f.club.ID); err != nil {
		t.Fatal(err)
	}
	f.stripeDeposit(t, imp, f.day(6, 5), 1234, "stripe payout")
	custom := f.stripeDeposit(t, imp, f.day(6, 11), 3000, "UTBETALING KLUBB 42")
	res = f.sweep(t)
	if sweepCounts(res) != [4]int{1, 0, 0, 0} || res.Cleared[0].BankRowID != custom {
		t.Errorf("custom descriptor = %+v, want the matching deposit cleared and the default text ignored", res)
	}
}

// The bank date's period must exist and be open; the sweep never creates
// one.
func TestStripePayoutPeriods(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	if _, err := f.club.Pool.Exec(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'closed')`, f.club.ID, f.club.Year-1); err != nil {
		t.Fatal(err)
	}
	imp := f.club.NewBankImport(t)
	closed := time.Date(f.club.Year-1, 12, 30, 0, 0, 0, 0, time.UTC)
	po := f.insertPayout(t, payoutSpec{id: "po_test_closed", amountOre: 2000, arrival: closed})
	f.member(t, po, stripeBT{id: "txn_test_closed", category: "charge", amountOre: 2000, status: StripeTxnBooked})
	f.stripeDeposit(t, imp, closed.AddDate(0, 0, 1), 2000, "STRIPE")
	missing := time.Date(f.club.Year+1, 1, 5, 0, 0, 0, 0, time.UTC)
	po = f.insertPayout(t, payoutSpec{id: "po_test_missing", amountOre: 2500, arrival: missing})
	f.member(t, po, stripeBT{id: "txn_test_missing", category: "charge", amountOre: 2500, status: StripeTxnBooked})
	f.stripeDeposit(t, imp, missing, 2500, "STRIPE")

	periods := func() int {
		var n int
		if err := f.club.Pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := periods()
	res := f.sweep(t)
	if sweepCounts(res) != [4]int{0, 2, 0, 0} {
		t.Fatalf("result = %+v, want both waiting", res)
	}
	reasons := map[string]string{}
	for _, it := range res.Waiting {
		reasons[it.PayoutID] = it.Reason
	}
	if !strings.Contains(reasons["po_test_closed"], "er lukket") || !strings.HasPrefix(reasons["po_test_missing"], "ingen regnskapsperiode") {
		t.Errorf("reasons = %v", reasons)
	}
	if periods() != before || f.clearingEntries(t) != 0 {
		t.Errorf("periods %d → %d, %d clearing entries; want no period created and no entry", before, periods(), f.clearingEntries(t))
	}
	var drafts int
	if err := f.club.Pool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&drafts); err != nil || drafts != 0 {
		t.Errorf("entries = %d (%v), want none", drafts, err)
	}
}

// Concurrent sweeps, on a pool of two connections and including sweeps
// that bypass the club's slot (a second API process), clear a payout once.
func TestStripePayoutConcurrentOnSmallPool(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(2))
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &stripeFixture{svc: svc, club: club, src: newFakeStripeBalance(), now: time.Now().Unix()}
	po := f.insertPayout(t, payoutSpec{id: "po_test_race", amountOre: 4200, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_race", category: "charge", amountOre: 4200, status: StripeTxnBooked})
	f.stripeDeposit(t, club.NewBankImport(t), f.day(6, 10), 4200, "STRIPE")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan *StripePayoutSweepResult, 6)
	errs := make(chan error, 6)
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var res *StripePayoutSweepResult
			var err error
			if i%2 == 0 {
				res, err = svc.ClearStripePayouts(ctx, club.ID, club.Treasurer.ID)
			} else {
				res, err = svc.clearStripePayouts(ctx, club.ID, club.Treasurer.ID)
			}
			if err != nil {
				errs <- err
				return
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("sweep: %v", err)
	}
	cleared := 0
	for res := range results {
		cleared += len(res.Cleared)
		if len(res.Failed) != 0 {
			t.Errorf("failed: %+v", res.Failed)
		}
	}
	if cleared != 1 || f.clearingEntries(t) != 1 {
		t.Errorf("cleared %d times with %d entries, want once", cleared, f.clearingEntries(t))
	}
}

// Bank import runs the sweep after booking; the Stripe sync endpoint path
// (TrySyncAndBookStripeBalance) reports it too.
func TestBankImportClearsStripePayout(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	po := f.insertPayout(t, payoutSpec{id: "po_test_import", amountOre: 7700, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_import", category: "fee", amountOre: 7700,
		created: time.Date(f.club.Year, 6, 8, 10, 0, 0, 0, time.UTC)})
	imp, err := f.svc.ImportBankRows(ctx, f.club.ID, f.club.NewBankImport(t), "", []BankRow{
		{Date: f.day(6, 12), Description: "STRIPE PAYOUT", Amount: 77, Reference: "SYNSTRIPE1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if imp.StripeBooking == nil || imp.StripeBooking.FeesBooked != 1 || sweepCounts(imp.StripePayouts) != [4]int{1, 0, 0, 0} {
		t.Errorf("import = booking %+v, payouts %+v; want the fee booked and the payout cleared", imp.StripeBooking, imp.StripePayouts)
	}

	_, _, payouts, err := f.svc.TrySyncAndBookStripeBalance(ctx, f.src, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if payouts == nil || sweepCounts(payouts) != [4]int{} {
		t.Errorf("sync payouts = %+v, want an empty sweep", payouts)
	}
}

func TestMatchStripePayoutWindow(t *testing.T) {
	arrival := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		offset int
		match  bool
	}{{-2, false}, {-1, true}, {0, true}, {3, true}, {4, false}} {
		plan := matchStripePayouts(
			[]stripePayoutCand{{id: "p", payoutID: "po_test", amountOre: 100, arrival: arrival, automatic: true, listed: true}},
			[]stripeDeposit{{id: "r", date: arrival.AddDate(0, 0, c.offset), cents: 100}})
		got := plan.payouts[0].state == stripePlanReady
		if got != c.match || (len(plan.orphans) == 0) != c.match {
			t.Errorf("offset %d: ready %v orphans %d, want match %v", c.offset, got, len(plan.orphans), c.match)
		}
	}
	plan := matchStripePayouts(
		[]stripePayoutCand{{id: "p", payoutID: "po_test", amountOre: 100, arrival: arrival, automatic: true}},
		[]stripeDeposit{{id: "r", date: arrival, cents: 101}})
	if plan.payouts[0].state != stripePlanWaiting || len(plan.orphans) != 1 {
		t.Errorf("an øre off: %+v", plan)
	}
}

// Unassigning a cleared deposit voids the clearing and releases the payout
// and its payout row, so the next sweep clears it again with a new entry
// and the clearing account still nets to zero.
func TestStripePayoutReclearsAfterUnassign(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	po := f.insertPayout(t, payoutSpec{id: "po_test_undo", amountOre: 6000, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_undo_fee", category: "fee", amountOre: 6000,
		created: time.Date(f.club.Year, 6, 8, 10, 0, 0, 0, time.UTC)})
	payoutBT := f.insertBT(t, stripeBT{id: "txn_test_undo_po", category: "payout", amountOre: -6000})
	if _, err := f.club.Pool.Exec(ctx, `UPDATE stripe_balance_transactions SET payout_id = $2 WHERE id = $1`, payoutBT, po); err != nil {
		t.Fatal(err)
	}
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 10), 6000, "STRIPE")
	f.book(t)

	first := f.sweep(t)
	if sweepCounts(first) != [4]int{1, 0, 0, 0} {
		t.Fatalf("first sweep = %+v, want cleared", first)
	}
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	var linked bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT journal_entry_id IS NOT NULL OR bank_row_id IS NOT NULL FROM stripe_payouts WHERE id = $1`, po,
	).Scan(&linked); err != nil || linked {
		t.Fatalf("payout still linked after unassign (err %v)", err)
	}
	if bt := f.bt(t, payoutBT); bt.status != StripeTxnUnbooked || bt.entry != "" {
		t.Errorf("payout transaction = %+v, want unbooked", bt)
	}

	second := f.sweep(t)
	if sweepCounts(second) != [4]int{1, 0, 0, 0} || second.Cleared[0].EntryID == first.Cleared[0].EntryID {
		t.Fatalf("second sweep = %+v, want cleared again with a new entry", second)
	}
	if got := bankRowEntry(t, f.club, row); got != second.Cleared[0].EntryID {
		t.Errorf("bank row entry = %q, want the new one", got)
	}
	if b := f.accountBalance(t, "1580"); !floatNear(b, 0, 0.001) {
		t.Errorf("1580 balance = %v, want 0", b)
	}
}

// Members that are all booked but do not add up to the payout leave it to
// a person, with no entry.
func TestStripePayoutMembersMustSumToPayout(t *testing.T) {
	f := newStripeFixture(t)
	po := f.insertPayout(t, payoutSpec{id: "po_test_sum", amountOre: 5000, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_sum", category: "charge", amountOre: 4000, status: StripeTxnBooked})
	f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 10), 5000, "STRIPE")

	res := f.sweep(t)
	if sweepCounts(res) != [4]int{0, 0, 1, 0} ||
		res.Skipped[0].Reason != "Stripe-transaksjonene i utbetalingen summerer til 40,00 kr, ikke 50,00 kr – avstem manuelt" {
		t.Fatalf("result = %+v, want skipped on the sum", res)
	}
	if n := f.clearingEntries(t); n != 0 {
		t.Errorf("%d clearing entries, want none", n)
	}
}
