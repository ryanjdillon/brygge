package accounting

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// txnRefusalCode is the code of a *StripeTxnRefusal, else "".
func txnRefusalCode(err error) string {
	var r *StripeTxnRefusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

func wantRefusal(t *testing.T, err error, code string) {
	t.Helper()
	if got := txnRefusalCode(err); got != code {
		t.Errorf("err = %v, want refusal %s", err, code)
	}
}

func assertLines(t *testing.T, c *fin.Club, entryID string, want map[string][2]float64) {
	t.Helper()
	lines := creditLines(t, c, entryID)
	if len(lines) != len(want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	for code, w := range want {
		if lines[code] != w {
			t.Errorf("account %s = %v, want %v (all: %v)", code, lines[code], w, lines)
		}
	}
}

// reviewRow stores a row in review with its reason.
func (f *stripeFixture) reviewRow(t *testing.T, id, category, reason string, amountOre, feeOre int64, orderID string) string {
	t.Helper()
	return f.insertBT(t, stripeBT{id: id, category: category, amountOre: amountOre, feeOre: feeOre,
		status: StripeTxnNeedsReview, reason: reason, orderID: orderID})
}

// A charge no attempt claims, assigned to its faktura by hand, books DR
// clearing / CR 1500 with the fee, records the faktura and settles it; the
// faktura then refuses a bank assign, and neither the booking pass nor a
// re-sync touches the row.
func TestAssignStripeTxnToInvoice(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.charge("txn_test_manual_inv", 600, 45000, "nok", nil, "")
	f.sync(t)
	var rowID string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id FROM stripe_balance_transactions WHERE club_id = $1 AND balance_transaction_id = 'txn_test_manual_inv'`,
		f.club.ID).Scan(&rowID); err != nil {
		t.Fatal(err)
	}
	if row := f.bt(t, rowID); row.status != StripeTxnNeedsReview || row.reason != StripeReviewNoAttempt {
		t.Fatalf("row = %+v, want in review for no_attempt", row)
	}

	res, err := f.svc.AssignStripeTxnToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, rowID, inv.ID)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	row := f.bt(t, rowID)
	if row.status != StripeTxnBooked || row.entry != res.JournalEntryID || row.reason != StripeReviewNoAttempt {
		t.Fatalf("row = %+v, want booked with the entry and its review reason kept", row)
	}
	// The fixture's fee is 45000/40 = 1125 øre.
	assertLines(t, f.club, res.JournalEntryID, map[string][2]float64{"1580": {450, 11.25}, "1500": {0, 450}, "7700": {11.25, 0}})

	var invoiceID, desc, status string
	var paid bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT t.invoice_id::text, e.description, e.status, i.payment_id IS NOT NULL
		   FROM stripe_balance_transactions t
		   JOIN journal_entries e ON e.id = t.journal_entry_id
		   JOIN invoices i ON i.id = t.invoice_id
		  WHERE t.id = $1`, rowID,
	).Scan(&invoiceID, &desc, &status, &paid); err != nil {
		t.Fatal(err)
	}
	if invoiceID != inv.ID || status != "posted" || !paid || desc != "Kortbetaling: faktura "+strconv.Itoa(inv.Number) {
		t.Errorf("invoice %s, entry %q %s, paid %v", invoiceID, desc, status, paid)
	}

	bankRow := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 45000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, bankRow, inv.ID); err == nil {
		t.Error("bank assign of the faktura the card paid succeeded")
	}

	if res := f.book(t); res.ChargesBooked != 0 || len(res.Errors) != 0 {
		t.Errorf("booking pass = %+v, want nothing", res)
	}
	f.sync(t)
	if row := f.bt(t, rowID); row.status != StripeTxnBooked || row.entry != res.JournalEntryID {
		t.Errorf("after re-sync row = %+v", row)
	}
	if n := f.entryCount(t); n != 1 {
		t.Errorf("%d stripe entries, want 1", n)
	}

	_, err = f.svc.AssignStripeTxnToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, rowID, inv.ID)
	wantRefusal(t, err, StripeTxnAlreadyBooked)
}

// A faktura assign is refused for a refund, another currency, a different
// amount, a settled faktura, a dismissed row and a closed period, with
// nothing booked.
func TestAssignStripeTxnToInvoiceRefusals(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	assign := func(rowID, invoiceID string) error {
		_, err := f.svc.AssignStripeTxnToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, rowID, invoiceID)
		return err
	}
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})

	refund := f.reviewRow(t, "txn_test_r_refund", "refund", StripeReviewRefundPending, -45000, 0, "")
	wantRefusal(t, assign(refund, inv.ID), StripeTxnNotAllowed)

	eur := f.reviewRow(t, "txn_test_r_eur", "charge", StripeReviewNoAttempt, 45000, 0, "")
	if _, err := f.club.Pool.Exec(ctx, `UPDATE stripe_balance_transactions SET currency = 'eur' WHERE id = $1`, eur); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, assign(eur, inv.ID), StripeTxnNotAllowed)

	notOpen := f.reviewRow(t, "txn_test_r_notopen", "charge", StripeReviewReceivableNotOpen, 45000, 0, "")
	wantRefusal(t, assign(notOpen, inv.ID), StripeTxnNotAllowed)

	off := f.reviewRow(t, "txn_test_r_off", "charge", StripeReviewAmountMismatch, 46200, 0, "")
	wantRefusal(t, assign(off, inv.ID), StripeTxnAmountMismatch)
	// A card charge takes exactly what was asked: even 50 øre is another payment.
	nearly := f.reviewRow(t, "txn_test_r_nearly", "charge", StripeReviewAmountMismatch, 44950, 0, "")
	wantRefusal(t, assign(nearly, inv.ID), StripeTxnAmountMismatch)
	near := f.reviewRow(t, "txn_test_r_near", "charge", StripeReviewAmountMismatch, 45000, 0, "")

	// An imported faktura whose receivable is not in the opening balance.
	imported := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Imported: true})
	wantRefusal(t, assign(near, imported.ID), StripeTxnInvoiceSettled)

	settled := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	bankRow := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 45000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, bankRow, settled.ID); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, assign(near, settled.ID), StripeTxnInvoiceSettled)

	if _, err := f.svc.DismissStripeTxn(ctx, f.club.ID, f.club.Treasurer.ID, near, "duplicate", ""); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, assign(near, inv.ID), StripeTxnNotAllowed)

	closed := f.reviewRow(t, "txn_test_r_closed", "charge", StripeReviewNoAttempt, 45000, 0, "")
	if _, err := f.club.Pool.Exec(ctx, `UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.club.PeriodID); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, assign(closed, inv.ID), StripeTxnPeriodClosed)

	if err := assign("00000000-0000-0000-0000-000000000001", inv.ID); !errors.Is(err, ErrStripeTxnNotFound) {
		t.Errorf("unknown row: %v", err)
	}
	if err := assign(closed, "00000000-0000-0000-0000-000000000001"); !errors.Is(err, ErrStripeInvoiceNotFound) {
		t.Errorf("unknown faktura: %v", err)
	}
	if n := f.entryCount(t); n != 0 {
		t.Errorf("%d stripe entries after refusals, want 0", n)
	}
}

// An account assign books a charge DR clearing / CR the account with the
// fee, and a negative adjustment DR the account / CR clearing with a
// returned fee reversed. A charge with an order booked to an account does
// not settle the order's faktura, and the booking pass leaves it alone.
func TestAssignStripeTxnToAccount(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	acct := func(rowID, code, note string) (*StripeTxnBookResult, error) {
		return f.svc.AssignStripeTxnToAccount(ctx, f.club.ID, f.club.Treasurer.ID, rowID, code, note)
	}

	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 30000})
	order := f.club.NewOrderForInvoice(t, inv)
	charge := f.reviewRow(t, "txn_test_a_charge", "charge", StripeReviewSplitMismatch, 31000, 775, order.ID)
	res, err := acct(charge, "3900", "Synthetic walk-up")
	if err != nil {
		t.Fatalf("assign charge: %v", err)
	}
	assertLines(t, f.club, res.JournalEntryID, map[string][2]float64{"1580": {310, 7.75}, "3900": {0, 310}, "7700": {7.75, 0}})
	var desc string
	if err := f.club.Pool.QueryRow(ctx, `SELECT description FROM journal_entries WHERE id = $1`, res.JournalEntryID).Scan(&desc); err != nil ||
		desc != "Kortbetaling — Synthetic walk-up" {
		t.Errorf("description = %q (%v)", desc, err)
	}
	if row := f.bt(t, charge); row.status != StripeTxnBooked || row.reason != StripeReviewSplitMismatch {
		t.Errorf("row = %+v, want booked with its reason", row)
	}
	if res := f.book(t); res.ChargesBooked != 0 || len(res.Errors) != 0 {
		t.Errorf("booking pass = %+v, want nothing", res)
	}
	// The order's faktura is still open: a bank payment of it books.
	bankRow := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 30000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, bankRow, inv.ID); err != nil {
		t.Errorf("bank assign of the order's faktura: %v", err)
	}

	adj := f.reviewRow(t, "txn_test_a_adj", "other_adjustment", StripeReviewUnsupportedCategory, -5000, -100, "")
	res, err = acct(adj, "7790", "")
	if err != nil {
		t.Fatalf("assign adjustment: %v", err)
	}
	assertLines(t, f.club, res.JournalEntryID, map[string][2]float64{"7790": {50, 0}, "1580": {1, 50}, "7700": {0, 1}})
	if err := f.club.Pool.QueryRow(ctx, `SELECT description FROM journal_entries WHERE id = $1`, res.JournalEntryID).Scan(&desc); err != nil ||
		desc != "Stripe-justering txn_test_a_adj" {
		t.Errorf("description = %q (%v)", desc, err)
	}

	// What stays on clearing is each row's net.
	var clearing float64
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT COALESCE(sum(jl.debit - jl.credit), 0) FROM journal_lines jl
		   JOIN accounts a ON a.id = jl.account_id
		   JOIN journal_entries e ON e.id = jl.journal_entry_id
		  WHERE e.club_id = $1 AND e.source = 'stripe' AND a.code = '1580'`, f.club.ID).Scan(&clearing); err != nil {
		t.Fatal(err)
	}
	if want := (31000.0 - 775 + (-5000) - (-100)) / 100; clearing != want {
		t.Errorf("clearing = %v, want the nets %v", clearing, want)
	}

	refund := f.reviewRow(t, "txn_test_a_refund", "refund", StripeReviewRefundPending, -5000, 0, "")
	_, err = acct(refund, "3900", "")
	wantRefusal(t, err, StripeTxnNotAllowed)
}

// The accounts a Stripe row may not be booked to are refused, and the list
// of assignable accounts leaves them out.
func TestAssignStripeTxnToAccountInvalidAccounts(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	row := f.reviewRow(t, "txn_test_inv_acct", "charge", StripeReviewNoAttempt, 10000, 0, "")
	f.club.NewBankImportOn(t, "1910")
	if _, err := f.club.Pool.Exec(ctx, `UPDATE accounts SET is_active = false WHERE club_id = $1 AND code = '3300'`, f.club.ID); err != nil {
		t.Fatal(err)
	}
	bad := []string{"", "1500", "1920", "1910", "2900", "1580", "9999", "3300"}
	for _, code := range bad {
		_, err := f.svc.AssignStripeTxnToAccount(ctx, f.club.ID, f.club.Treasurer.ID, row, code, "")
		if txnRefusalCode(err) != StripeTxnInvalidAccount {
			t.Errorf("account %q: err = %v, want invalid_account", code, err)
		}
	}
	if n := f.entryCount(t); n != 0 {
		t.Errorf("%d entries after refusals", n)
	}

	list, err := f.svc.ListStripeAssignableAccounts(ctx, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, a := range list {
		codes[a.Code] = true
	}
	for _, code := range bad {
		if codes[code] {
			t.Errorf("assignable accounts include %s", code)
		}
	}
	if !codes["3900"] || !codes["7790"] {
		t.Errorf("assignable accounts miss 3900 or 7790: %v", codes)
	}
}

// Dismissal keeps the review reason and the note, is idempotent, survives
// a re-sync and is taken back to review; a row without a reason goes back
// to unbooked, and one whose payout is cleared stays dismissed.
func TestDismissAndUndismissStripeTxn(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	actor := f.club.Treasurer.ID

	f.charge("txn_test_dismiss", 600, 12000, "nok", nil, "")
	f.sync(t)
	var rowID string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id FROM stripe_balance_transactions WHERE club_id = $1 AND balance_transaction_id = 'txn_test_dismiss'`,
		f.club.ID).Scan(&rowID); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.DismissStripeTxn(ctx, f.club.ID, actor, rowID, "internal_transfer", "")
	wantRefusal(t, err, StripeTxnInvalidReason)

	res, err := f.svc.DismissStripeTxn(ctx, f.club.ID, actor, rowID, "test_transaction", "Synthetic test card")
	if err != nil || res.Unchanged || res.Status != StripeTxnDismissed {
		t.Fatalf("dismiss = %+v, %v", res, err)
	}
	var stored, by string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT dismissed_reason, dismissed_by::text FROM stripe_balance_transactions WHERE id = $1 AND dismissed_at IS NOT NULL`,
		rowID).Scan(&stored, &by); err != nil || stored != "test_transaction: Synthetic test card" || by != actor {
		t.Errorf("dismissal = %q by %s (%v)", stored, by, err)
	}
	if row := f.bt(t, rowID); row.status != StripeTxnDismissed || row.reason != StripeReviewNoAttempt {
		t.Errorf("row = %+v", row)
	}
	if res, err := f.svc.DismissStripeTxn(ctx, f.club.ID, actor, rowID, "duplicate", ""); err != nil || !res.Unchanged {
		t.Errorf("second dismiss = %+v, %v", res, err)
	}
	f.sync(t)
	if row := f.bt(t, rowID); row.status != StripeTxnDismissed {
		t.Errorf("after re-sync row = %+v, want dismissed", row)
	}

	items, _, err := f.svc.ListStripeTransactions(ctx, f.club.ID, StripeTxnDismissed, 10, 0)
	if err != nil || len(items) != 1 || items[0].DismissedReason != "test_transaction" ||
		items[0].DismissedNote != "Synthetic test card" || len(items[0].AllowedActions) != 1 {
		t.Errorf("dismissed list = %+v (%v)", items, err)
	}

	res, err = f.svc.UndismissStripeTxn(ctx, f.club.ID, actor, rowID)
	if err != nil || res.Status != StripeTxnNeedsReview {
		t.Fatalf("undismiss = %+v, %v", res, err)
	}
	if row := f.bt(t, rowID); row.status != StripeTxnNeedsReview || row.reason != StripeReviewNoAttempt {
		t.Errorf("row = %+v, want back in review", row)
	}
	var cleared bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT dismissed_at IS NULL AND dismissed_by IS NULL AND dismissed_reason IS NULL FROM stripe_balance_transactions WHERE id = $1`,
		rowID).Scan(&cleared); err != nil || !cleared {
		t.Errorf("dismissal not cleared (%v)", err)
	}
	if res, err := f.svc.UndismissStripeTxn(ctx, f.club.ID, actor, rowID); err != nil || !res.Unchanged {
		t.Errorf("second undismiss = %+v, %v", res, err)
	}

	plain := f.insertBT(t, stripeBT{id: "txn_test_undismiss_plain", category: "charge", amountOre: 5000, status: StripeTxnDismissed})
	if res, err := f.svc.UndismissStripeTxn(ctx, f.club.ID, actor, plain); err != nil || res.Status != StripeTxnUnbooked {
		t.Errorf("undismiss without reason = %+v, %v", res, err)
	}

	// A cleared payout: any posted entry stands in for its clearing.
	fee := f.insertBT(t, stripeBT{id: "txn_test_cleared_fee", category: "fee", amountOre: -100})
	f.book(t)
	po := f.insertBT(t, stripeBT{id: "txn_test_cleared_member", category: "charge", amountOre: 5000,
		status: StripeTxnDismissed, reason: StripeReviewNoAttempt})
	if _, err := f.club.Pool.Exec(ctx,
		`WITH p AS (
		   INSERT INTO stripe_payouts (club_id, payout_id, amount_ore, currency, arrival_date, status, automatic, journal_entry_id)
		   VALUES ($1, 'po_test_cleared', 5000, 'nok', make_date($2, 6, 20), 'paid', true,
		           (SELECT journal_entry_id FROM stripe_balance_transactions WHERE id = $3))
		   RETURNING id)
		 UPDATE stripe_balance_transactions SET payout_id = (SELECT id FROM p) WHERE id = $4`,
		f.club.ID, f.club.Year, fee, po); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UndismissStripeTxn(ctx, f.club.ID, actor, po)
	wantRefusal(t, err, StripeTxnPayoutCleared)
	items, _, err = f.svc.ListStripeTransactions(ctx, f.club.ID, StripeTxnDismissed, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == po && (!it.PayoutCleared || len(it.AllowedActions) != 0 || it.PayoutID == nil || *it.PayoutID != "po_test_cleared") {
			t.Errorf("cleared member = %+v", it)
		}
	}

	booked := f.bt(t, fee)
	if booked.status != StripeTxnBooked {
		t.Fatalf("fee = %+v", booked)
	}
	_, err = f.svc.DismissStripeTxn(ctx, f.club.ID, actor, fee, "duplicate", "")
	wantRefusal(t, err, StripeTxnAlreadyBooked)
}

// The review list pages and carries each row's actions.
func TestListStripeTransactions(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	for i, reason := range []string{StripeReviewNoAttempt, StripeReviewReceivableNotOpen, StripeReviewCurrency} {
		f.insertBT(t, stripeBT{id: "txn_test_list_" + reason, category: "charge", amountOre: 1000, feeOre: 25,
			status: StripeTxnNeedsReview, reason: reason,
			created: time.Date(f.club.Year, 6, 10+i, 12, 0, 0, 0, time.UTC)})
	}
	f.insertBT(t, stripeBT{id: "txn_test_list_fee", category: "fee", amountOre: -100})

	page, more, err := f.svc.ListStripeTransactions(ctx, f.club.ID, StripeTxnNeedsReview, 2, 0)
	if err != nil || len(page) != 2 || !more {
		t.Fatalf("page 1 = %d rows, more %v (%v)", len(page), more, err)
	}
	if page[0].ReviewReason != StripeReviewCurrency || page[0].Amount != 10 || page[0].Fee != 0.25 || page[0].Net != 9.75 {
		t.Errorf("newest = %+v", page[0])
	}
	if got := page[0].AllowedActions; len(got) != 1 || got[0] != StripeActionDismiss {
		t.Errorf("currency actions = %v", got)
	}
	if got := page[1].AllowedActions; len(got) != 2 || got[0] != StripeActionAssignAccount {
		t.Errorf("receivable_not_open actions = %v", got)
	}
	page, more, err = f.svc.ListStripeTransactions(ctx, f.club.ID, StripeTxnNeedsReview, 2, 2)
	if err != nil || len(page) != 1 || more || page[0].ReviewReason != StripeReviewNoAttempt {
		t.Errorf("page 2 = %+v, more %v (%v)", page, more, err)
	}
	if _, _, err := f.svc.ListStripeTransactions(ctx, f.club.ID, StripeTxnUnbooked, 2, 0); err == nil {
		t.Error("unbooked status listed")
	}
}

// A manual assign racing booking passes, on a pool of two connections and
// with passes outside the club's slot (a second API process), books each
// row exactly once.
func TestStripeManualAssignConcurrentWithBookingPass(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(2))
	svc := NewService(pool, nil, zerolog.Nop())
	other := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &stripeFixture{svc: svc, club: club, src: newFakeStripeBalance(), now: time.Now().Unix()}

	var rows []string
	for i := range 4 {
		inv := club.NewInvoice(t, fin.InvoiceOpts{Cents: 20000})
		// Ready for the pass: the order adds up and its faktura is open.
		rows = append(rows, f.reviewRow(t, "txn_test_race_"+strconv.Itoa(i), "charge", StripeReviewSplitMismatch, 20000, 500,
			club.NewOrderForInvoice(t, inv).ID))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for _, id := range rows {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := svc.AssignStripeTxnToAccount(ctx, club.ID, club.Treasurer.ID, id, "3900", "")
			if err != nil && txnRefusalCode(err) != StripeTxnAlreadyBooked {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			res, err := other.bookStripeBalance(ctx, club.ID, club.Treasurer.ID)
			if err != nil {
				errs <- err
				return
			}
			for _, e := range res.Errors {
				errs <- errors.New(e)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("race: %v", err)
	}
	for _, id := range rows {
		var entries int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source_id = $2`, club.ID, id).Scan(&entries); err != nil {
			t.Fatal(err)
		}
		if row := f.bt(t, id); row.status != StripeTxnBooked || entries != 1 {
			t.Errorf("row %s = %+v with %d entries, want booked once", id, row, entries)
		}
	}
}

// A manual booking opens the fiscal year of its booking date when none
// exists, as a manual bank assign does; the booking pass never does.
func TestStripeManualAssignCreatesFiscalYear(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	next := f.club.Year + 1
	row := f.insertBT(t, stripeBT{id: "txn_test_next_year", category: "charge", amountOre: 10000,
		status: StripeTxnNeedsReview, reason: StripeReviewNoAttempt,
		created: time.Date(next, 2, 10, 12, 0, 0, 0, time.UTC)})
	res, err := f.svc.AssignStripeTxnToAccount(ctx, f.club.ID, f.club.Treasurer.ID, row, "3900", "")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	var year int
	var status string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT p.year, p.status FROM journal_entries e JOIN fiscal_periods p ON p.id = e.fiscal_period_id WHERE e.id = $1`,
		res.JournalEntryID).Scan(&year, &status); err != nil || year != next || status != "open" {
		t.Errorf("entry period = %d %s (%v), want a new open %d", year, status, err, next)
	}
}

// Only rows in review are dismissed: an unbooked charge waits for the
// booking pass.
func TestDismissUnbookedStripeTxnRefused(t *testing.T) {
	f := newStripeFixture(t)
	row := f.insertBT(t, stripeBT{id: "txn_test_unbooked", category: "charge", amountOre: 5000})
	_, err := f.svc.DismissStripeTxn(context.Background(), f.club.ID, f.club.Treasurer.ID, row, "duplicate", "")
	wantRefusal(t, err, StripeTxnNotAllowed)
	if got := f.bt(t, row); got.status != StripeTxnUnbooked {
		t.Errorf("row = %+v, want unbooked", got)
	}
}

// A row in review whose payout is cleared in the books is never booked:
// the clearing already took its net off the clearing account.
func TestStripeAssignRefusedOnClearedPayout(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	fee := f.insertBT(t, stripeBT{id: "txn_test_cp_fee", category: "fee", amountOre: -100})
	f.book(t)
	row := f.reviewRow(t, "txn_test_cp_row", "charge", StripeReviewNoAttempt, 45000, 0, "")
	if _, err := f.club.Pool.Exec(ctx,
		`WITH p AS (
		   INSERT INTO stripe_payouts (club_id, payout_id, amount_ore, currency, arrival_date, status, automatic, journal_entry_id)
		   VALUES ($1, 'po_test_cp', 45000, 'nok', make_date($2, 6, 20), 'paid', true,
		           (SELECT journal_entry_id FROM stripe_balance_transactions WHERE id = $3))
		   RETURNING id)
		 UPDATE stripe_balance_transactions SET payout_id = (SELECT id FROM p) WHERE id = $4`,
		f.club.ID, f.club.Year, fee, row); err != nil {
		t.Fatal(err)
	}
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	_, err := f.svc.AssignStripeTxnToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID)
	wantRefusal(t, err, StripeTxnPayoutCleared)
	_, err = f.svc.AssignStripeTxnToAccount(ctx, f.club.ID, f.club.Treasurer.ID, row, "3900", "")
	wantRefusal(t, err, StripeTxnPayoutCleared)
	items, _, err := f.svc.ListStripeTransactions(ctx, f.club.ID, StripeTxnNeedsReview, 10, 0)
	if err != nil || len(items) != 1 || !items[0].PayoutCleared || len(items[0].AllowedActions) != 1 ||
		items[0].AllowedActions[0] != StripeActionDismiss {
		t.Errorf("list = %+v (%v), want dismiss only", items, err)
	}
	if _, err := f.svc.DismissStripeTxn(ctx, f.club.ID, f.club.Treasurer.ID, row, "booked_manually", ""); err != nil {
		t.Errorf("dismiss: %v", err)
	}
}
