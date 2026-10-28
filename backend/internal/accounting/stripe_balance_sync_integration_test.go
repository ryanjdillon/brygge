package accounting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// fakeStripeBalance is an in-memory Stripe balance: the transaction list,
// payouts with their members, and PaymentIntents for the metadata fallback.
type fakeStripeBalance struct {
	mu       sync.Mutex
	pageSize int
	txns     []stripe.BalanceTransaction
	payouts  map[string]*stripe.Payout
	members  map[string][]string
	intents  map[string]*stripe.PaymentIntent

	intentCalls     int
	listCalls       int
	payoutListCalls int
	// failPage makes the balance list fail for any page after the first
	// while set.
	failPage bool
}

func newFakeStripeBalance() *fakeStripeBalance {
	return &fakeStripeBalance{
		pageSize: 2,
		payouts:  map[string]*stripe.Payout{},
		members:  map[string][]string{},
		intents:  map[string]*stripe.PaymentIntent{},
	}
}

func (f *fakeStripeBalance) add(bt stripe.BalanceTransaction) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txns = append(f.txns, bt)
}

func (f *fakeStripeBalance) byID(id string) (stripe.BalanceTransaction, bool) {
	for _, bt := range f.txns {
		if bt.ID == id {
			return bt, true
		}
	}
	return stripe.BalanceTransaction{}, false
}

// page returns the transactions after startingAfter, newest first as
// Stripe lists them.
func (f *fakeStripeBalance) page(all []stripe.BalanceTransaction, startingAfter string) *stripe.BalanceTransactionPage {
	sort.SliceStable(all, func(i, j int) bool { return all[i].Created > all[j].Created })
	start := 0
	if startingAfter != "" {
		for i, bt := range all {
			if bt.ID == startingAfter {
				start = i + 1
			}
		}
	}
	end := min(start+f.pageSize, len(all))
	return &stripe.BalanceTransactionPage{Data: all[start:end], HasMore: end < len(all)}
}

func (f *fakeStripeBalance) ListBalanceTransactions(_ context.Context, since time.Time, after string) (*stripe.BalanceTransactionPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.failPage && after != "" {
		return nil, fmt.Errorf("synthetic outage")
	}
	var all []stripe.BalanceTransaction
	for _, bt := range f.txns {
		if bt.Created >= since.Unix() {
			all = append(all, bt)
		}
	}
	return f.page(all, after), nil
}

func (f *fakeStripeBalance) ListPayoutTransactions(_ context.Context, payoutID, after string) (*stripe.BalanceTransactionPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payoutListCalls++
	if p := f.payouts[payoutID]; p == nil || !p.Automatic {
		return nil, fmt.Errorf("payout %s cannot be listed", payoutID)
	}
	var all []stripe.BalanceTransaction
	for _, id := range f.members[payoutID] {
		if bt, ok := f.byID(id); ok {
			all = append(all, bt)
		}
	}
	return f.page(all, after), nil
}

func (f *fakeStripeBalance) GetPayout(_ context.Context, id string) (*stripe.Payout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.payouts[id]
	if !ok {
		return nil, fmt.Errorf("no such payout %s", id)
	}
	cp := *p
	return &cp, nil
}

func (f *fakeStripeBalance) GetPaymentIntent(_ context.Context, id string) (*stripe.PaymentIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.intentCalls++
	pi, ok := f.intents[id]
	if !ok {
		return nil, fmt.Errorf("no such payment intent %s", id)
	}
	return pi, nil
}

func (f *fakeStripeBalance) setPayoutStatus(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payouts[id].Status = status
}

// stripeFixture is a club with Stripe payment attempts and a fake balance.
type stripeFixture struct {
	svc  *Service
	club *fin.Club
	src  *fakeStripeBalance
	now  int64
}

func newStripeFixture(t *testing.T) *stripeFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	return &stripeFixture{svc: svc, club: club, src: newFakeStripeBalance(), now: time.Now().Unix()}
}

// attempt seeds an order and a Stripe payment attempt of amountKr, created
// an hour ago.
func (f *stripeFixture) attempt(t *testing.T, amountKr float64, currency string) (attemptID, orderID string) {
	t.Helper()
	ctx := context.Background()
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, currency, token) VALUES ($1, $2, $3, $4) RETURNING id`,
		f.club.ID, amountKr, currency, "tok-"+testutil.RandomHex(12),
	).Scan(&orderID); err != nil {
		t.Fatalf("order: %v", err)
	}
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, currency, created_at)
		 VALUES ($1, $2, 'stripe', $3, 'captured', $4, $5, now() - interval '1 hour') RETURNING id`,
		f.club.ID, orderID, "cs_test_"+testutil.RandomHex(6), amountKr, currency,
	).Scan(&attemptID); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	return attemptID, orderID
}

func (f *stripeFixture) charge(id string, ageSec, amountOre int64, currency string, metadata map[string]string, piID string) {
	f.src.add(stripe.BalanceTransaction{
		ID: id, Amount: amountOre, Fee: amountOre / 40, Net: amountOre - amountOre/40, Currency: currency,
		Created: f.now - ageSec, AvailableOn: f.now + 2*86400, Type: "charge", ReportingCategory: stripe.CategoryCharge,
		FeeDetails: []stripe.FeeDetail{{Amount: amountOre / 40, Currency: currency, Description: "Stripe processing fees", Type: "stripe_fee"}},
		Source:     &stripe.BalanceSource{ID: "ch_" + id, Object: "charge", PaymentIntent: stripe.ObjectID(piID), Metadata: metadata},
	})
}

func (f *stripeFixture) other(id string, ageSec, amountOre int64, category string, source *stripe.BalanceSource) {
	f.src.add(stripe.BalanceTransaction{
		ID: id, Amount: amountOre, Net: amountOre, Currency: "nok",
		Created: f.now - ageSec, ReportingCategory: category, Source: source,
	})
}

func (f *stripeFixture) payout(id string, ageSec, amountOre int64, automatic bool, status string, members ...string) {
	f.src.mu.Lock()
	f.src.payouts[id] = &stripe.Payout{ID: id, Amount: amountOre, Currency: "nok", ArrivalDate: f.now + 86400,
		Status: status, Automatic: automatic, StatementDescriptor: "SYNTHETIC CLUB"}
	f.src.members[id] = members
	f.src.mu.Unlock()
	f.other("txn_"+id, ageSec, -amountOre, stripe.CategoryPayout,
		&stripe.BalanceSource{ID: id, Object: "payout", Amount: amountOre, Status: status, Automatic: automatic})
}

type storedTxn struct {
	Category, Status, Reason, Attempt, Order, Payout, Charge, PI string
	Amount, Fee, Net                                             int64
}

func (f *stripeFixture) txns(t *testing.T) map[string]storedTxn {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT t.balance_transaction_id, t.reporting_category, t.status, COALESCE(t.review_reason, ''),
		        COALESCE(t.attempt_id::text, ''), COALESCE(t.order_id::text, ''), COALESCE(p.payout_id, ''),
		        t.charge_id, t.payment_intent_id, t.amount_ore, t.fee_ore, t.net_ore
		   FROM stripe_balance_transactions t
		   LEFT JOIN stripe_payouts p ON p.id = t.payout_id
		  WHERE t.club_id = $1`, f.club.ID)
	if err != nil {
		t.Fatalf("txns: %v", err)
	}
	defer rows.Close()
	out := map[string]storedTxn{}
	for rows.Next() {
		var id string
		var s storedTxn
		if err := rows.Scan(&id, &s.Category, &s.Status, &s.Reason, &s.Attempt, &s.Order, &s.Payout,
			&s.Charge, &s.PI, &s.Amount, &s.Fee, &s.Net); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = s
	}
	return out
}

func (f *stripeFixture) payoutRow(t *testing.T, id string) (status string, automatic bool) {
	t.Helper()
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT status, automatic FROM stripe_payouts WHERE club_id = $1 AND payout_id = $2`, f.club.ID, id,
	).Scan(&status, &automatic); err != nil {
		t.Fatalf("payout %s: %v", id, err)
	}
	return status, automatic
}

func (f *stripeFixture) sync(t *testing.T) StripeSyncResult {
	t.Helper()
	res, err := f.svc.SyncStripeBalance(context.Background(), f.src, f.club.ID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return res
}

func TestSyncStripeBalanceClassifiesAndLinks(t *testing.T) {
	f := newStripeFixture(t)
	a1, o1 := f.attempt(t, 400, "NOK")
	a2, o2 := f.attempt(t, 250, "NOK")
	a3, _ := f.attempt(t, 100, "NOK")
	a4, _ := f.attempt(t, 120, "NOK")
	a5, o5 := f.attempt(t, 75.5, "NOK")

	f.charge("txn_test_1", 300, 40000, "nok", map[string]string{"attempt_id": a1}, "pi_test_1")
	// No metadata on the charge: the attempt comes from its PaymentIntent.
	f.charge("txn_test_2", 290, 25000, "nok", nil, "pi_test_2")
	f.src.intents["pi_test_2"] = &stripe.PaymentIntent{ID: "pi_test_2", Metadata: map[string]string{"attempt_id": a2}}
	f.charge("txn_test_3", 280, 9900, "nok", map[string]string{"attempt_id": a3}, "pi_test_3")
	f.charge("txn_test_4", 270, 12000, "eur", map[string]string{"attempt_id": a4}, "pi_test_4")
	f.charge("txn_test_5", 260, 5000, "nok", map[string]string{"attempt_id": "00000000-0000-0000-0000-00000000dead"}, "")
	f.other("txn_test_6", 250, -300, stripe.CategoryFee, nil)
	f.other("txn_test_7", 240, -10000, stripe.CategoryRefund,
		&stripe.BalanceSource{ID: "re_test_1", Object: "refund", Charge: "ch_txn_test_1", PaymentIntent: "pi_test_1"})
	f.other("txn_test_8", 230, -25000, stripe.CategoryDispute,
		&stripe.BalanceSource{ID: "du_test_1", Object: "dispute", Charge: "ch_txn_test_2"})
	f.other("txn_test_9", 220, -100, "transfer", nil)
	// Older than the first-sync window; only reachable through its payout.
	f.charge("txn_test_10", 40*86400, 7550, "nok", map[string]string{"attempt_id": a5}, "pi_test_10")
	f.payout("po_test_1", 100, 48000, true, stripe.PayoutPaid, "txn_test_1", "txn_test_6", "txn_test_10", "txn_po_test_1")
	f.payout("po_test_2", 90, 20000, false, stripe.PayoutInTransit)

	res := f.sync(t)

	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if res.Fetched != 11 || res.Inserted != 12 {
		t.Errorf("fetched %d inserted %d, want 11 and 12", res.Fetched, res.Inserted)
	}
	wantReview := map[string]int{
		StripeReviewAmountMismatch: 1, StripeReviewCurrency: 1, StripeReviewNoAttempt: 1,
		StripeReviewRefundPending: 2, StripeReviewUnsupportedCategory: 1,
	}
	if fmt.Sprint(res.NeedsReview) != fmt.Sprint(wantReview) {
		t.Errorf("needs_review = %v, want %v", res.NeedsReview, wantReview)
	}
	if res.PayoutsSeen != 2 || res.PayoutsLinked != 1 || res.MembersLinked != 3 {
		t.Errorf("payouts seen %d linked %d members %d, want 2, 1, 3", res.PayoutsSeen, res.PayoutsLinked, res.MembersLinked)
	}

	got := f.txns(t)
	want := map[string]storedTxn{
		"txn_test_1":    {Category: "charge", Status: "unbooked", Attempt: a1, Order: o1, Payout: "po_test_1", Charge: "ch_txn_test_1", PI: "pi_test_1", Amount: 40000, Fee: 1000, Net: 39000},
		"txn_test_2":    {Category: "charge", Status: "unbooked", Attempt: a2, Order: o2, Charge: "ch_txn_test_2", PI: "pi_test_2", Amount: 25000, Fee: 625, Net: 24375},
		"txn_test_3":    {Category: "charge", Status: "needs_review", Reason: "amount_mismatch", Attempt: a3, Charge: "ch_txn_test_3", PI: "pi_test_3", Amount: 9900, Fee: 247, Net: 9653},
		"txn_test_4":    {Category: "charge", Status: "needs_review", Reason: "currency", Attempt: a4, Charge: "ch_txn_test_4", PI: "pi_test_4", Amount: 12000, Fee: 300, Net: 11700},
		"txn_test_5":    {Category: "charge", Status: "needs_review", Reason: "no_attempt", Charge: "ch_txn_test_5", Amount: 5000, Fee: 125, Net: 4875},
		"txn_test_6":    {Category: "fee", Status: "unbooked", Payout: "po_test_1", Amount: -300, Net: -300},
		"txn_test_7":    {Category: "refund", Status: "needs_review", Reason: "refund_pending_bry446", Charge: "ch_txn_test_1", PI: "pi_test_1", Amount: -10000, Net: -10000},
		"txn_test_8":    {Category: "dispute", Status: "needs_review", Reason: "refund_pending_bry446", Charge: "ch_txn_test_2", Amount: -25000, Net: -25000},
		"txn_test_9":    {Category: "other_adjustment", Status: "needs_review", Reason: "unsupported_category", Amount: -100, Net: -100},
		"txn_test_10":   {Category: "charge", Status: "unbooked", Attempt: a5, Order: o5, Payout: "po_test_1", Charge: "ch_txn_test_10", PI: "pi_test_10", Amount: 7550, Fee: 188, Net: 7362},
		"txn_po_test_1": {Category: "payout", Status: "unbooked", Payout: "po_test_1", Amount: -48000, Net: -48000},
		"txn_po_test_2": {Category: "payout", Status: "unbooked", Payout: "po_test_2", Amount: -20000, Net: -20000},
	}
	for id, w := range want {
		g, ok := got[id]
		if !ok {
			t.Errorf("%s not stored", id)
			continue
		}
		// Attempt-linked rows carry the attempt's order; the mismatch rows
		// are checked on the attempt alone.
		if w.Order == "" && w.Attempt != "" {
			g.Order = ""
		}
		if g != w {
			t.Errorf("%s =\n  %+v\nwant\n  %+v", id, g, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("stored %d rows, want %d", len(got), len(want))
	}
	var stripeType, feeType string
	var feeAmount int64
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT stripe_type, fee_details->0->>'type', (fee_details->0->>'amount')::bigint
		   FROM stripe_balance_transactions WHERE club_id = $1 AND balance_transaction_id = 'txn_test_1'`, f.club.ID,
	).Scan(&stripeType, &feeType, &feeAmount); err != nil {
		t.Fatal(err)
	}
	if stripeType != "charge" || feeType != "stripe_fee" || feeAmount != 1000 {
		t.Errorf("txn_test_1 type %q, fee detail %q %d; want charge, stripe_fee 1000", stripeType, feeType, feeAmount)
	}
	var emptyDetails string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT fee_details::text FROM stripe_balance_transactions WHERE club_id = $1 AND balance_transaction_id = 'txn_test_6'`, f.club.ID,
	).Scan(&emptyDetails); err != nil {
		t.Fatal(err)
	}
	if emptyDetails != "[]" {
		t.Errorf("fee_details without a breakdown = %s, want []", emptyDetails)
	}
	if st, auto := f.payoutRow(t, "po_test_1"); st != "paid" || !auto {
		t.Errorf("po_test_1 = %s automatic=%v", st, auto)
	}
	if st, auto := f.payoutRow(t, "po_test_2"); st != "in_transit" || auto {
		t.Errorf("po_test_2 = %s automatic=%v", st, auto)
	}

	// A dismissed row stays dismissed on the next sync.
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE stripe_balance_transactions
		    SET status = 'dismissed', dismissed_at = now(), dismissed_reason = 'synthetic'
		  WHERE club_id = $1 AND balance_transaction_id = 'txn_test_5'`, f.club.ID); err != nil {
		t.Fatal(err)
	}
	got["txn_test_5"] = storedTxn{Category: "charge", Status: "dismissed", Reason: "no_attempt", Charge: "ch_txn_test_5", Amount: 5000, Fee: 125, Net: 4875}
	intentCalls := f.src.intentCalls

	again := f.sync(t)

	if again.Inserted != 0 || len(again.NeedsReview) != 0 || len(again.Errors) != 0 {
		t.Errorf("re-sync inserted %d, needs_review %v, errors %v; want nothing new", again.Inserted, again.NeedsReview, again.Errors)
	}
	if again.MembersLinked != 0 {
		t.Errorf("re-sync linked %d members again", again.MembersLinked)
	}
	if f.src.intentCalls != intentCalls {
		t.Errorf("re-sync looked up %d PaymentIntents for rows already classified", f.src.intentCalls-intentCalls)
	}
	after := f.txns(t)
	for id, g := range got {
		if after[id] != g {
			t.Errorf("re-sync changed %s:\n  %+v\nto\n  %+v", id, g, after[id])
		}
	}
	if len(after) != len(got) {
		t.Errorf("re-sync stored %d rows, want %d", len(after), len(got))
	}
}

func TestSyncStripeBalanceReclassifiesReviewRows(t *testing.T) {
	f := newStripeFixture(t)
	a1, o1 := f.attempt(t, 300, "NOK")
	f.charge("txn_test_1", 100, 30000, "nok", nil, "pi_test_1")

	// The PaymentIntent lookup fails: the charge waits for review.
	res := f.sync(t)
	if len(res.Errors) != 1 || res.NeedsReview[StripeReviewNoAttempt] != 1 {
		t.Fatalf("first sync = %+v, want one lookup error and one no_attempt", res)
	}

	f.src.intents["pi_test_1"] = &stripe.PaymentIntent{ID: "pi_test_1", Metadata: map[string]string{"attempt_id": a1}}
	res = f.sync(t)
	if len(res.Errors) != 0 || len(res.NeedsReview) != 0 {
		t.Errorf("second sync = %+v", res)
	}
	got := f.txns(t)["txn_test_1"]
	if got.Status != "unbooked" || got.Reason != "" || got.Attempt != a1 || got.Order != o1 {
		t.Errorf("after lookup succeeded: %+v", got)
	}
}

func TestStripePayoutFailureReleasesMembers(t *testing.T) {
	f := newStripeFixture(t)
	a1, _ := f.attempt(t, 200, "NOK")
	f.charge("txn_test_1", 300, 20000, "nok", map[string]string{"attempt_id": a1}, "pi_test_1")
	f.payout("po_test_1", 200, 19500, true, stripe.PayoutInTransit, "txn_test_1", "txn_po_test_1")

	res := f.sync(t)
	if res.MembersLinked != 1 || len(res.Errors) != 0 {
		t.Fatalf("first sync = %+v", res)
	}
	if got := f.txns(t)["txn_test_1"]; got.Payout != "po_test_1" {
		t.Fatalf("member not linked: %+v", got)
	}

	// The bank rejects the payout: Stripe marks it failed and returns the
	// money to the balance with a reversing transaction.
	f.src.setPayoutStatus("po_test_1", stripe.PayoutFailed)
	f.other("txn_test_rev", 50, 19500, stripe.CategoryPayoutReversal,
		&stripe.BalanceSource{ID: "po_test_1", Object: "payout", Status: stripe.PayoutFailed, Automatic: true})

	res = f.sync(t)
	if len(res.Errors) != 0 {
		t.Fatalf("second sync errors: %v", res.Errors)
	}
	if st, _ := f.payoutRow(t, "po_test_1"); st != "failed" {
		t.Errorf("payout status = %s, want failed", st)
	}
	got := f.txns(t)
	if got["txn_test_1"].Payout != "" {
		t.Errorf("member still on the failed payout: %+v", got["txn_test_1"])
	}
	if got["txn_po_test_1"].Payout != "po_test_1" || got["txn_test_rev"].Payout != "po_test_1" {
		t.Errorf("payout and reversal rows should keep the payout: %+v / %+v", got["txn_po_test_1"], got["txn_test_rev"])
	}

	// A stale answer saying in_transit does not revive it.
	f.src.setPayoutStatus("po_test_1", stripe.PayoutInTransit)
	if _, err := f.svc.IngestStripePayout(context.Background(), f.src, f.club.ID, "po_test_1"); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if st, _ := f.payoutRow(t, "po_test_1"); st != "failed" {
		t.Errorf("payout status = %s after stale answer, want failed", st)
	}
	if got := f.txns(t)["txn_test_1"]; got.Payout != "" {
		t.Errorf("stale answer re-linked the member: %+v", got)
	}
}

func TestStripeSyncCursor(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()

	before := time.Now()
	since, err := f.svc.stripeSyncCursor(ctx, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d := before.Sub(since); d < stripeFirstSyncLookback-time.Minute || d > stripeFirstSyncLookback+time.Minute {
		t.Errorf("no attempts: cursor %v before now, want %v", d, stripeFirstSyncLookback)
	}

	f.attempt(t, 100, "NOK")
	since, err = f.svc.stripeSyncCursor(ctx, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(since); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("first attempt an hour ago: cursor %v before now", d)
	}

	f.other("txn_test_1", 0, -300, stripe.CategoryFee, nil)
	f.sync(t)
	since, err = f.svc.stripeSyncCursor(ctx, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Unix(f.now, 0).Add(-stripeCursorOverlap); !since.Equal(want) {
		t.Errorf("cursor = %v, want %v", since, want)
	}
}

// A listing that fails part-way stores what it read but does not move the
// watermark, so the next sync reads the older pages it missed. A payout
// ingest in between does not move it either.
func TestStripeSyncResumesAfterFailedPage(t *testing.T) {
	f := newStripeFixture(t)
	a1, _ := f.attempt(t, 100, "NOK")
	for i := range 5 {
		f.other(fmt.Sprintf("txn_test_%d", i), int64(600-100*i), -100, stripe.CategoryFee, nil)
	}
	// Older than the attempt; reached only through its payout.
	f.charge("txn_test_old", 7*86400, 10000, "nok", map[string]string{"attempt_id": a1}, "pi_test_old")
	f.src.failPage = true

	_, err := f.svc.SyncStripeBalance(context.Background(), f.src, f.club.ID)
	if err == nil {
		t.Fatal("sync with a failing page succeeded")
	}
	if n := len(f.txns(t)); n != 2 {
		t.Fatalf("stored %d rows from the first page, want 2", n)
	}

	f.payout("po_test_1", 50, 9900, true, stripe.PayoutPaid, "txn_test_old", "txn_po_test_1")
	if _, err := f.svc.IngestStripePayout(context.Background(), f.src, f.club.ID, "po_test_1"); err != nil {
		t.Fatalf("payout ingest: %v", err)
	}
	since, err := f.svc.stripeSyncCursor(context.Background(), f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(since); d < 59*time.Minute {
		t.Errorf("cursor moved to %v ago after a failed sync and a payout ingest; want the first attempt", d)
	}

	f.src.failPage = false
	res := f.sync(t)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	got := f.txns(t)
	for i := range 5 {
		if _, ok := got[fmt.Sprintf("txn_test_%d", i)]; !ok {
			t.Errorf("txn_test_%d never stored", i)
		}
	}
	if len(got) != 7 {
		t.Errorf("stored %d rows, want 7", len(got))
	}
	var watermark time.Time
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT listed_through FROM stripe_sync_state WHERE club_id = $1`, f.club.ID).Scan(&watermark); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	if want := time.Unix(f.now-50, 0); !watermark.Equal(want) {
		t.Errorf("watermark = %v, want the newest listed transaction %v", watermark, want)
	}
}

// An automatic payout's members are listed once; a paid payout that paid
// out nothing is not listed again on every sync.
func TestStripePayoutMembersListedOnce(t *testing.T) {
	f := newStripeFixture(t)
	f.payout("po_test_1", 100, 0, true, stripe.PayoutPaid)
	f.sync(t)
	var listedAt *time.Time
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT members_listed_at FROM stripe_payouts WHERE club_id = $1 AND payout_id = 'po_test_1'`, f.club.ID,
	).Scan(&listedAt); err != nil {
		t.Fatal(err)
	}
	if listedAt == nil {
		t.Fatal("members_listed_at not set after a complete listing")
	}
	calls := f.src.payoutListCalls

	// Outside the overlap window the payout is reached only through the
	// refresh query, which must not pick it up again.
	f.src.mu.Lock()
	f.src.txns = nil
	f.src.mu.Unlock()
	f.sync(t)
	f.sync(t)
	if f.src.payoutListCalls != calls {
		t.Errorf("payout listed %d more times after its membership was known", f.src.payoutListCalls-calls)
	}
}

// Only one Stripe ingest runs per club: the try variant refuses while one
// runs, the waiting variant gives up when its context ends.
func TestStripeSyncSingleFlight(t *testing.T) {
	f := newStripeFixture(t)
	release, ok := f.svc.stripeSync.tryAcquire(f.club.ID)
	if !ok {
		t.Fatal("slot not free")
	}
	if _, err := f.svc.TrySyncStripeBalance(context.Background(), f.src, f.club.ID); !errors.Is(err, ErrStripeSyncBusy) {
		t.Errorf("try sync while busy: %v, want ErrStripeSyncBusy", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.svc.IngestStripePayout(ctx, f.src, f.club.ID, "po_test_1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("payout ingest while busy: %v, want deadline exceeded", err)
	}
	other := fin.NewClub(t, f.club.Pool)
	if _, err := f.svc.TrySyncStripeBalance(context.Background(), f.src, other.ID); err != nil {
		t.Errorf("another club's sync: %v", err)
	}
	release()
	if _, err := f.svc.TrySyncStripeBalance(context.Background(), f.src, f.club.ID); err != nil {
		t.Errorf("sync after release: %v", err)
	}
}

func (f *stripeFixture) attemptCard(t *testing.T, id string) (brand, wallet string) {
	t.Helper()
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(card_brand, ''), COALESCE(card_wallet, '') FROM payment_attempts WHERE id = $1`, id,
	).Scan(&brand, &wallet); err != nil {
		t.Fatal(err)
	}
	return brand, wallet
}

func cardDetails(brand, wallet string) *stripe.PaymentMethodDetails {
	d := &stripe.PaymentMethodDetails{Type: "card", Card: &stripe.CardDetails{Brand: brand}}
	if wallet != "" {
		d.Card.Wallet = &stripe.CardWallet{Type: wallet}
	}
	return d
}

func TestStripeSyncRecordsCard(t *testing.T) {
	f := newStripeFixture(t)
	a1, _ := f.attempt(t, 100, "NOK")
	a2, _ := f.attempt(t, 100, "NOK")
	a3, _ := f.attempt(t, 100, "NOK")
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE payment_attempts SET card_brand = 'amex' WHERE id = $1`, a2); err != nil {
		t.Fatal(err)
	}

	f.charge("txn_card_1", 300, 10000, "nok", map[string]string{"attempt_id": a1}, "pi_card_1")
	f.charge("txn_card_2", 200, 10000, "nok", map[string]string{"attempt_id": a2}, "pi_card_2")
	f.charge("txn_card_3", 100, 10000, "nok", map[string]string{"attempt_id": a3}, "pi_card_3")
	f.charge("txn_card_4", 150, 10000, "nok", map[string]string{"attempt_id": "00000000-0000-0000-0000-00000000beef"}, "")
	f.src.mu.Lock()
	f.src.txns[0].Source.PaymentMethodDetails = cardDetails("Visa", "apple_pay")
	f.src.txns[1].Source.PaymentMethodDetails = cardDetails("visa", "")
	f.src.txns[3].Source.PaymentMethodDetails = cardDetails("visa", "")
	f.src.mu.Unlock()

	if res := f.sync(t); len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if b, w := f.attemptCard(t, a1); b != "visa" || w != "apple_pay" {
		t.Errorf("a1 card = %q/%q, want visa/apple_pay", b, w)
	}
	if b, w := f.attemptCard(t, a2); b != "amex" || w != "" {
		t.Errorf("a2 card = %q/%q, want the recorded amex kept", b, w)
	}
	if b, _ := f.attemptCard(t, a3); b != "" {
		t.Errorf("a3 card = %q before its charge carried details", b)
	}

	// A row already stored and classified is filled when it is listed again.
	f.src.mu.Lock()
	f.src.txns[2].Source.PaymentMethodDetails = cardDetails("mastercard", "google_pay")
	f.src.mu.Unlock()
	if res := f.sync(t); len(res.Errors) != 0 || res.Inserted != 0 {
		t.Fatalf("re-sync = %+v", res)
	}
	if b, w := f.attemptCard(t, a3); b != "mastercard" || w != "google_pay" {
		t.Errorf("a3 card = %q/%q, want mastercard/google_pay", b, w)
	}
	if st := f.txns(t)["txn_card_3"].Status; st != "unbooked" {
		t.Errorf("txn_card_3 status = %q, want unbooked", st)
	}
}
