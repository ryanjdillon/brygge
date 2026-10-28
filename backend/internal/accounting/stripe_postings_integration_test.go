package accounting

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// stripeBT describes a stripe_balance_transactions row stored as ingest
// would leave it.
type stripeBT struct {
	id        string
	category  string
	stripeTyp string
	amountOre int64
	feeOre    int64
	created   time.Time
	orderID   string
	status    string
	reason    string
}

// insertBT stores bt for the fixture's club and returns its row id.
func (f *stripeFixture) insertBT(t *testing.T, bt stripeBT) string {
	t.Helper()
	if bt.status == "" {
		bt.status = StripeTxnUnbooked
	}
	if bt.created.IsZero() {
		bt.created = time.Date(f.club.Year, 6, 15, 10, 0, 0, 0, time.UTC)
	}
	var orderID, reason *string
	if bt.orderID != "" {
		orderID = &bt.orderID
	}
	if bt.reason != "" {
		reason = &bt.reason
	}
	var id string
	if err := f.club.Pool.QueryRow(context.Background(),
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, stripe_type, amount_ore, fee_ore, net_ore,
		      currency, created_at, order_id, status, review_reason)
		 VALUES ($1, $2, $3, $4, $5::bigint, $6::bigint, $5::bigint - $6::bigint, 'nok', $7, $8, $9, $10) RETURNING id`,
		f.club.ID, bt.id, bt.category, bt.stripeTyp, bt.amountOre, bt.feeOre, bt.created, orderID, bt.status, reason,
	).Scan(&id); err != nil {
		t.Fatalf("insert bt %s: %v", bt.id, err)
	}
	return id
}

type storedBT struct {
	status, reason, entry string
}

func (f *stripeFixture) bt(t *testing.T, rowID string) storedBT {
	t.Helper()
	var s storedBT
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(review_reason, ''), COALESCE(journal_entry_id::text, '')
		   FROM stripe_balance_transactions WHERE id = $1`, rowID,
	).Scan(&s.status, &s.reason, &s.entry); err != nil {
		t.Fatalf("bt %s: %v", rowID, err)
	}
	return s
}

func (f *stripeFixture) book(t *testing.T) *StripeBookResult {
	t.Helper()
	res, err := f.svc.BookStripeBalance(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("BookStripeBalance: %v", err)
	}
	return res
}

func (f *stripeFixture) entryCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source = 'stripe'`, f.club.ID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// markPaidByStripe is what the Stripe webhook leaves on a faktura it
// settled: the online-paid marker, no payment yet.
func markPaidByStripe(t *testing.T, c *fin.Club, invoiceID string) {
	t.Helper()
	if _, err := c.Pool.Exec(context.Background(),
		`UPDATE invoices SET online_paid_at = now(), online_paid_provider = 'stripe' WHERE id = $1`, invoiceID,
	); err != nil {
		t.Fatal(err)
	}
}

// catalogOrder seeds a QR checkout order with one line per category.
func catalogOrder(t *testing.T, c *fin.Club, lines map[string]float64) string {
	t.Helper()
	ctx := context.Background()
	var total float64
	for _, v := range lines {
		total += v
	}
	var orderID string
	if err := c.Pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, token, status) VALUES ($1, $2, $3, 'paid') RETURNING id`,
		c.ID, total, "tok-"+testutil.RandomHex(8),
	).Scan(&orderID); err != nil {
		t.Fatalf("order: %v", err)
	}
	for cat, v := range lines {
		if _, err := c.Pool.Exec(ctx,
			`INSERT INTO order_lines (order_id, price_item_id, name, quantity, unit_price, total_price)
			 VALUES ($1, $2, $3, 1, $4, $4)`, orderID, c.PriceItem(t, cat), "Synthetic "+cat, v,
		); err != nil {
			t.Fatalf("order line: %v", err)
		}
	}
	return orderID
}

// A faktura paid by card, ingested through the sync, books DR clearing /
// CR 1500 with Stripe's fee in the same entry, settles the faktura and
// marks the row booked; a second sync and booking change nothing.
func TestBookStripeChargeSettlesFaktura(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := f.club.NewOrderForInvoice(t, inv)
	markPaidByStripe(t, f.club, inv.ID)
	var attemptID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, currency, created_at)
		 VALUES ($1, $2, 'stripe', 'cs_test_faktura', 'captured', 450, 'NOK', now() - interval '1 hour') RETURNING id`,
		f.club.ID, order.ID,
	).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	f.charge("txn_test_faktura", 600, 45000, "nok", map[string]string{"attempt_id": attemptID}, "pi_test_faktura")
	f.sync(t)

	res := f.book(t)
	if res.ChargesBooked != 1 || res.FeesBooked != 0 || len(res.Errors) != 0 || len(res.Deferred) != 0 || len(res.NeedsReview) != 0 {
		t.Fatalf("result = %+v, want one charge booked", res)
	}

	var rowID string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id FROM stripe_balance_transactions WHERE club_id = $1 AND balance_transaction_id = 'txn_test_faktura'`,
		f.club.ID).Scan(&rowID); err != nil {
		t.Fatal(err)
	}
	row := f.bt(t, rowID)
	if row.status != StripeTxnBooked || row.entry == "" {
		t.Fatalf("row = %+v, want booked with an entry", row)
	}
	// Fee is 45000/40 = 1125 øre.
	lines := creditLines(t, f.club, row.entry)
	want := map[string][2]float64{"1580": {450, 11.25}, "1500": {0, 450}, "7700": {11.25, 0}}
	if len(lines) != len(want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	for code, w := range want {
		if lines[code] != w {
			t.Errorf("account %s = %v, want %v (all: %v)", code, lines[code], w, lines)
		}
	}

	var status, source, sourceTable, sourceID, createdBy, entryDate string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT status, source, source_table, source_id, created_by::text, entry_date::text
		   FROM journal_entries WHERE id = $1`, row.entry,
	).Scan(&status, &source, &sourceTable, &sourceID, &createdBy, &entryDate); err != nil {
		t.Fatal(err)
	}
	wantDate := stripeBookingDate(time.Unix(f.now-600, 0), time.Local).Format("2006-01-02")
	if status != "posted" || source != "stripe" || sourceTable != "stripe_balance_transactions" ||
		sourceID != rowID || createdBy != f.club.Treasurer.ID || entryDate != wantDate {
		t.Errorf("entry = %s %s %s %s %s %s, want posted stripe on the row by the treasurer on %s",
			status, source, sourceTable, sourceID, createdBy, entryDate, wantDate)
	}

	var paid bool
	if err := f.club.Pool.QueryRow(ctx, `SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, inv.ID).Scan(&paid); err != nil || !paid {
		t.Errorf("faktura not settled (paid=%v, err=%v)", paid, err)
	}
	var orderStatus string
	if err := f.club.Pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, order.ID).Scan(&orderStatus); err != nil {
		t.Fatal(err)
	}

	f.sync(t)
	again := f.book(t)
	if again.ChargesBooked != 0 || again.FeesBooked != 0 || len(again.Errors) != 0 {
		t.Errorf("re-run = %+v, want nothing booked", again)
	}
	if n := f.entryCount(t); n != 1 {
		t.Errorf("stripe entries = %d after a re-run, want 1", n)
	}
	var stillOrderStatus string
	if err := f.club.Pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, order.ID).Scan(&stillOrderStatus); err != nil {
		t.Fatal(err)
	}
	if stillOrderStatus != orderStatus {
		t.Errorf("booking changed the order status %q → %q", orderStatus, stillOrderStatus)
	}
}

// A QR checkout's catalog lines credit their category revenue accounts,
// and a charge with no fee books no fee lines.
func TestBookStripeChargeCatalogSplit(t *testing.T) {
	f := newStripeFixture(t)
	orderID := catalogOrder(t, f.club, map[string]float64{"guest": 400, "service": 120})
	rowID := f.insertBT(t, stripeBT{id: "txn_test_qr", category: "charge", amountOre: 52000, orderID: orderID})

	if res := f.book(t); res.ChargesBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want one charge booked", res)
	}
	row := f.bt(t, rowID)
	lines := creditLines(t, f.club, row.entry)
	want := map[string][2]float64{"1580": {520, 0}, "3200": {0, 400}, "3300": {0, 120}}
	if len(lines) != len(want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	for code, w := range want {
		if lines[code] != w {
			t.Errorf("account %s = %v, want %v", code, lines[code], w)
		}
	}
	var desc string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT description FROM journal_entries WHERE id = $1`, row.entry).Scan(&desc); err != nil {
		t.Fatal(err)
	}
	if desc != "Kortbetaling ordre "+orderID[:8] {
		t.Errorf("description = %q", desc)
	}
}

// A fee transaction books DR fee / CR clearing for what Stripe took, and
// the reverse for a fee it returned.
func TestBookStripeFee(t *testing.T) {
	f := newStripeFixture(t)
	charged := f.insertBT(t, stripeBT{id: "txn_test_fee", category: "fee", stripeTyp: "stripe_fee", amountOre: -300})
	returned := f.insertBT(t, stripeBT{id: "txn_test_fee_back", category: "fee", stripeTyp: "stripe_fee", amountOre: 150,
		created: time.Date(f.club.Year, 6, 16, 10, 0, 0, 0, time.UTC)})

	res := f.book(t)
	if res.FeesBooked != 2 || res.ChargesBooked != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want two fees booked", res)
	}
	if lines := creditLines(t, f.club, f.bt(t, charged).entry); lines["7700"] != [2]float64{3, 0} || lines["1580"] != [2]float64{0, 3} {
		t.Errorf("fee = %v, want DR 7700 / CR 1580 3.00", lines)
	}
	if lines := creditLines(t, f.club, f.bt(t, returned).entry); lines["1580"] != [2]float64{1.5, 0} || lines["7700"] != [2]float64{0, 1.5} {
		t.Errorf("returned fee = %v, want DR 1580 / CR 7700 1.50", lines)
	}
	var desc string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT description FROM journal_entries WHERE id = $1`, f.bt(t, charged).entry).Scan(&desc); err != nil {
		t.Fatal(err)
	}
	if desc != "Stripe-gebyr stripe_fee" {
		t.Errorf("description = %q", desc)
	}
	if again := f.book(t); again.FeesBooked != 0 || f.entryCount(t) != 2 {
		t.Errorf("re-run = %+v, %d entries; want nothing new", again, f.entryCount(t))
	}
}

// A row whose fiscal period is closed stays unbooked, is reported as
// deferred and is booked once the period is reopened.
func TestBookStripeClosedPeriodDefers(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	var periodID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'closed') RETURNING id`,
		f.club.ID, f.club.Year-1).Scan(&periodID); err != nil {
		t.Fatal(err)
	}
	rowID := f.insertBT(t, stripeBT{id: "txn_test_old_fee", category: "fee", amountOre: -500,
		created: time.Date(f.club.Year-1, 7, 1, 12, 0, 0, 0, time.UTC)})

	res := f.book(t)
	if res.FeesBooked != 0 || len(res.Deferred) != 1 || res.Deferred[0].BalanceTransactionID != "txn_test_old_fee" ||
		res.Deferred[0].Reason != StripeDeferPeriodClosed || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want the fee deferred", res)
	}
	if row := f.bt(t, rowID); row.status != StripeTxnUnbooked || row.entry != "" {
		t.Errorf("row = %+v, want it left unbooked", row)
	}
	var drafts int
	if err := f.club.Pool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&drafts); err != nil || drafts != 0 {
		t.Errorf("entries = %d (err %v), want none", drafts, err)
	}

	if _, err := f.club.Pool.Exec(ctx, `UPDATE fiscal_periods SET status = 'open' WHERE id = $1`, periodID); err != nil {
		t.Fatal(err)
	}
	if res := f.book(t); res.FeesBooked != 1 || len(res.Deferred) != 0 {
		t.Errorf("after reopening = %+v, want the fee booked", res)
	}
	if row := f.bt(t, rowID); row.status != StripeTxnBooked {
		t.Errorf("row = %+v, want booked", row)
	}
}

// A charge whose order may not be booked is held for review with a reason
// and no entry; a re-sync leaves it there, and the booking pass books it
// once the reason is gone.
func TestBookStripeChargeGuards(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()

	paid := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 30000, Paid: true})
	paidRow := f.insertBT(t, stripeBT{id: "txn_test_paid", category: "charge", amountOre: 30000,
		orderID: f.club.NewOrderForInvoice(t, paid).ID})

	imported := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 20000, Imported: true,
		IssueDate: time.Date(f.club.Year, 1, 10, 0, 0, 0, 0, time.UTC)})
	importedOrder := f.club.NewOrderForInvoice(t, imported)
	importedRow := f.insertBT(t, stripeBT{id: "txn_test_imported", category: "charge", amountOre: 20000, orderID: importedOrder.ID})

	short := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	mismatchRow := f.insertBT(t, stripeBT{id: "txn_test_mismatch", category: "charge", amountOre: 40000,
		orderID: f.club.NewOrderForInvoice(t, short).ID})

	res := f.book(t)
	if res.ChargesBooked != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want nothing booked", res)
	}
	want := map[string]string{
		paidRow:     StripeReviewReceivableNotOpen,
		importedRow: StripeReviewImportedNoOpeningBalance,
		mismatchRow: StripeReviewSplitMismatch,
	}
	for id, reason := range want {
		if row := f.bt(t, id); row.status != StripeTxnNeedsReview || row.reason != reason || row.entry != "" {
			t.Errorf("row %s = %+v, want needs_review %s", id, row, reason)
		}
		if res.NeedsReview[reason] != 1 {
			t.Errorf("needs_review[%s] = %d, want 1", reason, res.NeedsReview[reason])
		}
	}
	if n := f.entryCount(t); n != 0 {
		t.Errorf("%d entries, want none", n)
	}

	// Ingest never reclassifies a row the booking pass holds for review.
	var attemptID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, currency, created_at)
		 VALUES ($1, $2, 'stripe', 'cs_test_imported', 'captured', 200, 'NOK', now() - interval '1 hour') RETURNING id`,
		f.club.ID, importedOrder.ID).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	f.charge("txn_test_imported", 600, 20000, "nok", map[string]string{"attempt_id": attemptID}, "pi_test_imported")
	f.sync(t)
	if row := f.bt(t, importedRow); row.status != StripeTxnNeedsReview || row.reason != StripeReviewImportedNoOpeningBalance {
		t.Errorf("after a re-sync the row is %+v, want it still held for review", row)
	}

	// The opening balance carries the imported faktura's receivable; the
	// next pass books the charge.
	ob, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID, EntryDate: time.Date(f.club.Year, 1, 31, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
		Description: "Synthetic opening balance", Source: "opening_balance", CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
		Lines: []CreateJournalLineInput{{AccountCode: "1500", Debit: 200}, {AccountCode: "2050", Credit: 200}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, ob.ID, f.club.Treasurer.ID); err != nil {
		t.Fatal(err)
	}
	res = f.book(t)
	if res.ChargesBooked != 1 || res.NeedsReview[StripeReviewImportedNoOpeningBalance] != 0 {
		t.Errorf("retry = %+v, want the imported faktura's charge booked", res)
	}
	if row := f.bt(t, importedRow); row.status != StripeTxnBooked || row.reason != "" {
		t.Errorf("row = %+v, want booked with no review reason", row)
	}
	if row := f.bt(t, paidRow); row.status != StripeTxnNeedsReview {
		t.Errorf("paid-faktura row = %+v, want it still in review", row)
	}
}

// Concurrent booking passes, on a pool of two connections and including
// passes that bypass the club's in-process slot (a second API process),
// book each row exactly once.
func TestBookStripeConcurrentOnSmallPool(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(2))
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &stripeFixture{svc: svc, club: club, src: newFakeStripeBalance(), now: time.Now().Unix()}

	inv := club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	charge := f.insertBT(t, stripeBT{id: "txn_test_c1", category: "charge", amountOre: 45000, feeOre: 1125,
		orderID: club.NewOrderForInvoice(t, inv).ID})
	catalog := f.insertBT(t, stripeBT{id: "txn_test_c2", category: "charge", amountOre: 12000,
		orderID: catalogOrder(t, club, map[string]float64{"service": 120})})
	fee := f.insertBT(t, stripeBT{id: "txn_test_f1", category: "fee", amountOre: -300})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan *StripeBookResult, 6)
	errs := make(chan error, 6)
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var res *StripeBookResult
			var err error
			if i%2 == 0 {
				res, err = svc.BookStripeBalance(ctx, club.ID, club.Treasurer.ID)
			} else {
				res, err = svc.bookStripeBalance(ctx, club.ID, club.Treasurer.ID)
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
		t.Fatalf("booking: %v", err)
	}
	charges, fees := 0, 0
	for res := range results {
		if len(res.Errors) != 0 {
			t.Errorf("errors: %v", res.Errors)
		}
		charges += res.ChargesBooked
		fees += res.FeesBooked
	}
	if charges != 2 || fees != 1 {
		t.Errorf("booked %d charges and %d fees in total, want 2 and 1", charges, fees)
	}
	if n := f.entryCount(t); n != 3 {
		t.Errorf("%d stripe entries, want 3", n)
	}
	for _, id := range []string{charge, catalog, fee} {
		if row := f.bt(t, id); row.status != StripeTxnBooked {
			t.Errorf("row %s = %+v, want booked", id, row)
		}
	}
	var payments int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payments WHERE club_id = $1`, club.ID).Scan(&payments); err != nil || payments != 1 {
		t.Errorf("payments = %d (err %v), want one", payments, err)
	}
}

// Bank sync and a bank import run the booking pass as the acting user; with
// nothing to book they report nothing.
func TestBankPathsBookStripe(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()

	res, err := f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.StripeBooking != nil {
		t.Errorf("stripe_booking = %+v with nothing to book, want nil", res.StripeBooking)
	}

	f.insertBT(t, stripeBT{id: "txn_test_sync_fee", category: "fee", amountOre: -300})
	res, err = f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.StripeBooking == nil || res.StripeBooking.FeesBooked != 1 {
		t.Errorf("stripe_booking = %+v, want the fee booked", res.StripeBooking)
	}

	f.insertBT(t, stripeBT{id: "txn_test_import_fee", category: "fee", amountOre: -200})
	imp, err := f.svc.ImportBankRows(ctx, f.club.ID, f.club.NewBankImport(t), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if imp.StripeBooking == nil || imp.StripeBooking.FeesBooked != 1 {
		t.Errorf("import stripe_booking = %+v, want the fee booked", imp.StripeBooking)
	}

	// While a Stripe sync holds the club's slot the pass stands aside.
	f.insertBT(t, stripeBT{id: "txn_test_busy_fee", category: "fee", amountOre: -100})
	release, ok := f.svc.stripeSync.tryAcquire(f.club.ID)
	if !ok {
		t.Fatal("slot busy")
	}
	res, err = f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	release()
	if err != nil {
		t.Fatal(err)
	}
	if res.StripeBooking == nil || !res.StripeBooking.Busy || res.StripeBooking.FeesBooked != 0 {
		t.Errorf("stripe_booking = %+v, want busy", res.StripeBooking)
	}
}

func TestStripeBookingDate(t *testing.T) {
	oslo := time.FixedZone("CEST", 2*3600)
	cases := []struct {
		ts   time.Time
		want string
	}{
		{time.Date(2026, 6, 30, 22, 30, 0, 0, time.UTC), "2026-07-01"},
		{time.Date(2026, 6, 30, 21, 59, 0, 0, time.UTC), "2026-06-30"},
		{time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC), "2027-01-01"},
	}
	for _, c := range cases {
		if got := stripeBookingDate(c.ts, oslo).Format("2006-01-02"); got != c.want {
			t.Errorf("stripeBookingDate(%s) = %s, want %s", c.ts, got, c.want)
		}
	}
}

// A booking date with no fiscal period is deferred as no_period; Stripe
// booking never creates the year. A locked period defers like a closed one.
func TestBookStripeMissingAndLockedPeriods(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	missing := f.insertBT(t, stripeBT{id: "txn_test_no_period", category: "fee", amountOre: -400,
		created: time.Date(f.club.Year+1, 2, 1, 12, 0, 0, 0, time.UTC)})
	var lockedID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'locked') RETURNING id`,
		f.club.ID, f.club.Year-2).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	locked := f.insertBT(t, stripeBT{id: "txn_test_locked", category: "fee", amountOre: -100,
		created: time.Date(f.club.Year-2, 3, 1, 12, 0, 0, 0, time.UTC)})

	res := f.book(t)
	reasons := map[string]string{}
	for _, d := range res.Deferred {
		reasons[d.BalanceTransactionID] = d.Reason
	}
	if reasons["txn_test_no_period"] != StripeDeferNoPeriod || reasons["txn_test_locked"] != StripeDeferPeriodClosed ||
		res.FeesBooked != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want no_period and period_closed deferrals", res)
	}
	for _, id := range []string{missing, locked} {
		if row := f.bt(t, id); row.status != StripeTxnUnbooked || row.entry != "" {
			t.Errorf("row %s = %+v, want unbooked", id, row)
		}
	}
	var periods int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FROM fiscal_periods WHERE club_id = $1 AND year = $2`, f.club.ID, f.club.Year+1).Scan(&periods); err != nil {
		t.Fatal(err)
	}
	if periods != 0 {
		t.Errorf("booking created %d fiscal period(s) for %d", periods, f.club.Year+1)
	}
}

// A card payment for an order of two fakturas credits 1500 for each and
// back-links both.
func TestBookStripeChargeMultiFakturaOrder(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	member := f.club.NewMember(t, "member")
	a := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 30000, Member: &member})
	b := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 15000, Member: &member})
	order := f.club.NewOrderForInvoices(t, a, b)
	rowID := f.insertBT(t, stripeBT{id: "txn_test_multi", category: "charge", amountOre: 45000, feeOre: 1125, orderID: order.ID})

	if res := f.book(t); res.ChargesBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want one charge booked", res)
	}
	lines := creditLines(t, f.club, f.bt(t, rowID).entry)
	if lines["1500"] != [2]float64{0, 450} || lines["1580"] != [2]float64{450, 11.25} || lines["7700"] != [2]float64{11.25, 0} {
		t.Errorf("lines = %v", lines)
	}
	var credits int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FROM journal_lines jl JOIN accounts ac ON ac.id = jl.account_id
		  WHERE jl.journal_entry_id = $1 AND ac.code = '1500' AND jl.credit > 0`, f.bt(t, rowID).entry).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 2 {
		t.Errorf("%d receivable credit lines, want one per faktura", credits)
	}
	for _, inv := range []fin.Invoice{a, b} {
		var paid bool
		if err := f.club.Pool.QueryRow(ctx, `SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, inv.ID).Scan(&paid); err != nil || !paid {
			t.Errorf("faktura %d not back-linked (paid=%v, err=%v)", inv.Number, paid, err)
		}
	}
}

// A charge held for split_mismatch is booked once its order is corrected.
func TestBookStripeSplitMismatchBookedAfterCorrection(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	orderID := catalogOrder(t, f.club, map[string]float64{"service": 300})
	rowID := f.insertBT(t, stripeBT{id: "txn_test_fix", category: "charge", amountOre: 25000, orderID: orderID})

	if res := f.book(t); res.NeedsReview[StripeReviewSplitMismatch] != 1 {
		t.Fatalf("result = %+v, want split_mismatch", res)
	}
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE order_lines SET unit_price = 250, total_price = 250 WHERE order_id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	if res := f.book(t); res.ChargesBooked != 1 || len(res.NeedsReview) != 0 {
		t.Fatalf("after correcting the order = %+v, want it booked", res)
	}
	row := f.bt(t, rowID)
	if row.status != StripeTxnBooked || row.reason != "" {
		t.Errorf("row = %+v, want booked", row)
	}
	if lines := creditLines(t, f.club, row.entry); lines["3300"] != [2]float64{0, 250} || lines["1580"] != [2]float64{250, 0} {
		t.Errorf("lines = %v", lines)
	}
}

// A faktura whose order charge is booked, Stripe or Vipps, refuses a bank
// assign even while its payment back-link is missing.
func TestOrderChargeBlocksSecondAssign(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	unlink := func(invoiceID string) {
		t.Helper()
		if _, err := f.club.Pool.Exec(ctx, `UPDATE invoices SET payment_id = NULL WHERE id = $1`, invoiceID); err != nil {
			t.Fatal(err)
		}
	}
	assign := func(inv fin.Invoice) error {
		t.Helper()
		row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{
			Date: time.Date(f.club.Year, 6, 20, 0, 0, 0, 0, time.UTC), Cents: inv.TotalCents})
		_, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID)
		return err
	}

	card := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.insertBT(t, stripeBT{id: "txn_test_block", category: "charge", amountOre: 45000,
		orderID: f.club.NewOrderForInvoice(t, card).ID})
	if res := f.book(t); res.ChargesBooked != 1 {
		t.Fatalf("result = %+v", res)
	}
	unlink(card.ID)
	if err := assign(card); !errors.Is(err, errInvoiceHasBookedRow) {
		t.Errorf("bank assign after a Stripe charge: %v, want errInvoiceHasBookedRow", err)
	}

	vipps := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 30000})
	order := f.club.NewOrderForInvoice(t, vipps)
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, OrderID: order.VippsReference})
	if res, err := f.svc.AutoReconcileOrderCharges(ctx, f.club.ID, f.club.Treasurer.ID); err != nil || res.Booked != 1 {
		t.Fatalf("vipps order charge: %+v %v", res, err)
	}
	unlink(vipps.ID)
	if err := assign(vipps); !errors.Is(err, errInvoiceHasBookedRow) {
		t.Errorf("bank assign after a Vipps order charge: %v, want errInvoiceHasBookedRow", err)
	}

	// A faktura no order charge settles still takes the bank payment.
	plain := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 12000})
	if err := assign(plain); err != nil {
		t.Errorf("bank assign of an unbooked faktura: %v", err)
	}
}
