package accounting

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// passBookedCharge ingests a card payment of the faktura's order and books
// it through the booking pass, returning the row id and its entry.
func (f *stripeFixture) passBookedCharge(t *testing.T, btID string, inv fin.Invoice) (rowID, entryID string) {
	t.Helper()
	ctx := context.Background()
	order := f.club.NewOrderForInvoice(t, inv)
	markPaidByStripe(t, f.club, inv.ID)
	var attemptID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, currency, created_at)
		 VALUES ($1, $2, 'stripe', $3, 'captured', $4, 'NOK', now() - interval '1 hour') RETURNING id`,
		f.club.ID, order.ID, "cs_test_"+btID, float64(inv.TotalCents)/100,
	).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	f.charge(btID, 600, inv.TotalCents, "nok", map[string]string{"attempt_id": attemptID}, "pi_"+btID)
	f.sync(t)
	if res := f.book(t); res.ChargesBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("booking pass = %+v, want the charge booked", res)
	}
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id, journal_entry_id::text FROM stripe_balance_transactions WHERE club_id = $1 AND balance_transaction_id = $2`,
		f.club.ID, btID).Scan(&rowID, &entryID); err != nil {
		t.Fatal(err)
	}
	return rowID, entryID
}

func (f *stripeFixture) unassign(rowID string) (*StripeTxnUnassignResult, error) {
	return f.svc.UnassignStripeTxn(context.Background(), f.club.ID, f.club.Treasurer.ID, rowID)
}

func (f *stripeFixture) entryStatus(t *testing.T, entryID string) string {
	t.Helper()
	var s string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT status FROM journal_entries WHERE id = $1`, entryID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// invoicePayment is a faktura's payment_id, "" when unpaid.
func (f *stripeFixture) invoicePayment(t *testing.T, invoiceID string) string {
	t.Helper()
	var p *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT payment_id::text FROM invoices WHERE id = $1`, invoiceID).Scan(&p); err != nil {
		t.Fatal(err)
	}
	if p == nil {
		return ""
	}
	return *p
}

func (f *stripeFixture) reversals(t *testing.T, entryID string) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries o JOIN journal_entries r
		    ON r.club_id = o.club_id AND r.description = 'Reversering av bilag #' || o.entry_number || ': ' || o.description
		 WHERE o.id = $1`, entryID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *stripeFixture) item(t *testing.T, status, rowID string) StripeTxnItem {
	t.Helper()
	items, _, err := f.svc.ListStripeTransactions(context.Background(), f.club.ID, status, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == rowID {
			return it
		}
	}
	t.Fatalf("row %s not listed as %s", rowID, status)
	return StripeTxnItem{}
}

// A charge the booking pass booked from its order is unassigned: its entry
// is voided with a reversal, the faktura loses its payment (but keeps the
// online-paid marker) and the row is held for the person. Neither the
// booking pass nor a re-sync touches the held row; a rebook books it again
// with a new entry and pays the faktura again.
func TestUnassignStripeChargeBookedByPass(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	rowID, entryID := f.passBookedCharge(t, "txn_test_unassign_pass", inv)
	paymentID := f.invoicePayment(t, inv.ID)
	if paymentID == "" {
		t.Fatal("faktura not paid by the booking")
	}
	booked := f.item(t, StripeTxnBooked, rowID)
	if !slices.Equal(booked.AllowedActions, []string{StripeActionUnassign}) || booked.JournalEntryID == nil ||
		*booked.JournalEntryID != entryID || booked.JournalEntryNumber == nil || booked.JournalEntryStatus != "posted" ||
		!slices.Equal(booked.InvoiceNumbers, []int{inv.Number}) {
		t.Errorf("booked item = %+v", booked)
	}

	res, err := f.unassign(rowID)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if res.VoidedJournalEntryID != entryID || res.ReversalEntryID == "" || res.AlreadyVoided ||
		!slices.Equal(res.InvoiceIDs, []string{inv.ID}) || res.ReviewReason != StripeReviewUnassigned {
		t.Errorf("result = %+v", res)
	}
	if s := f.entryStatus(t, entryID); s != "voided" {
		t.Errorf("entry %s, want voided", s)
	}
	for _, code := range []string{"1580", "1500", "7700"} {
		if b := f.accountBalance(t, code); b != 0 {
			t.Errorf("account %s nets %v after the void, want 0", code, b)
		}
	}
	if p := f.invoicePayment(t, inv.ID); p != "" {
		t.Errorf("faktura still paid by %s", p)
	}
	var payments int
	var onlinePaid bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM payments WHERE id = $1), online_paid_at IS NOT NULL FROM invoices WHERE id = $2`,
		paymentID, inv.ID).Scan(&payments, &onlinePaid); err != nil {
		t.Fatal(err)
	}
	if payments != 0 || !onlinePaid {
		t.Errorf("payment rows %d, online paid %v; want the payment gone and the marker kept", payments, onlinePaid)
	}
	if row := f.bt(t, rowID); row.status != StripeTxnNeedsReview || row.reason != StripeReviewUnassigned || row.entry != "" {
		t.Fatalf("row = %+v, want held as unassigned", row)
	}

	if res := f.book(t); res.ChargesBooked != 0 || len(res.NeedsReview) != 0 || len(res.Errors) != 0 {
		t.Errorf("booking pass = %+v, want the held row left alone", res)
	}
	f.sync(t)
	if row := f.bt(t, rowID); row.status != StripeTxnNeedsReview || row.reason != StripeReviewUnassigned {
		t.Errorf("after re-sync row = %+v, want still held", row)
	}
	held := f.item(t, StripeTxnNeedsReview, rowID)
	if !slices.Equal(held.AllowedActions, []string{StripeActionRebook, StripeActionAssignInvoice, StripeActionAssignAccount, StripeActionDismiss}) {
		t.Errorf("held actions = %v", held.AllowedActions)
	}
	_, err = f.unassign(rowID)
	wantRefusal(t, err, StripeTxnNotAllowed)

	rb, err := f.svc.RebookStripeTxn(ctx, f.club.ID, f.club.Treasurer.ID, rowID)
	if err != nil {
		t.Fatalf("rebook: %v", err)
	}
	if rb.Outcome != StripeRebookBooked || rb.Status != StripeTxnBooked || rb.JournalEntryID == "" || rb.JournalEntryID == entryID {
		t.Fatalf("rebook = %+v, want booked with a new entry", rb)
	}
	if row := f.bt(t, rowID); row.status != StripeTxnBooked || row.reason != "" || row.entry != rb.JournalEntryID {
		t.Errorf("row = %+v, want booked by the pass", row)
	}
	if f.invoicePayment(t, inv.ID) == "" {
		t.Error("faktura not paid again by the rebook")
	}
	if n := f.entryCount(t); n != 2 {
		t.Errorf("%d stripe entries, want the voided one and the new one", n)
	}
	_, err = f.svc.RebookStripeTxn(ctx, f.club.ID, f.club.Treasurer.ID, rowID)
	wantRefusal(t, err, StripeTxnAlreadyBooked)
}

// A faktura assign and an account assign are unassigned too. Only the
// faktura in the row's invoice_id is reopened: a row with an order that a
// person booked to an account never settled the order's faktura, so a bank
// payment of it stays. A held row can be assigned again.
func TestUnassignStripeManualAssigns(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	actor := f.club.Treasurer.ID

	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 30000})
	row := f.reviewRow(t, "txn_test_un_inv", "charge", StripeReviewNoAttempt, 30000, 750, "")
	booked, err := f.svc.AssignStripeTxnToInvoice(ctx, f.club.ID, actor, row, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.item(t, StripeTxnBooked, row).InvoiceNumbers; !slices.Equal(got, []int{inv.Number}) {
		t.Errorf("invoice numbers = %v", got)
	}
	res, err := f.unassign(row)
	if err != nil {
		t.Fatalf("unassign faktura assign: %v", err)
	}
	if res.VoidedJournalEntryID != booked.JournalEntryID || !slices.Equal(res.InvoiceIDs, []string{inv.ID}) {
		t.Errorf("result = %+v", res)
	}
	if f.invoicePayment(t, inv.ID) != "" {
		t.Error("faktura still paid")
	}
	var invoiceID *string
	if err := f.club.Pool.QueryRow(ctx, `SELECT invoice_id::text FROM stripe_balance_transactions WHERE id = $1`, row).Scan(&invoiceID); err != nil || invoiceID != nil {
		t.Errorf("invoice_id = %v (%v), want cleared", invoiceID, err)
	}
	if got := f.item(t, StripeTxnNeedsReview, row).AllowedActions; !slices.Equal(got,
		[]string{StripeActionAssignInvoice, StripeActionAssignAccount, StripeActionDismiss}) {
		t.Errorf("held actions without an order = %v", got)
	}
	_, err = f.svc.RebookStripeTxn(ctx, f.club.ID, actor, row)
	wantRefusal(t, err, StripeTxnNotAllowed)
	again, err := f.svc.AssignStripeTxnToInvoice(ctx, f.club.ID, actor, row, inv.ID)
	if err != nil {
		t.Fatalf("assign again: %v", err)
	}
	if r := f.bt(t, row); r.status != StripeTxnBooked || r.reason != StripeReviewUnassigned || r.entry != again.JournalEntryID {
		t.Errorf("re-assigned row = %+v, want booked by hand", r)
	}
	if f.invoicePayment(t, inv.ID) == "" {
		t.Error("faktura not paid again")
	}

	// The order's faktura is paid by the bank; the card charge went to 3900.
	orderInv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 20000})
	order := f.club.NewOrderForInvoice(t, orderInv)
	acctRow := f.reviewRow(t, "txn_test_un_acct", "charge", StripeReviewSplitMismatch, 21000, 0, order.ID)
	if _, err := f.svc.AssignStripeTxnToAccount(ctx, f.club.ID, actor, acctRow, "3900", ""); err != nil {
		t.Fatal(err)
	}
	bankRow := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 20000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, actor, bankRow, orderInv.ID); err != nil {
		t.Fatal(err)
	}
	bankPayment := f.invoicePayment(t, orderInv.ID)
	if got := f.item(t, StripeTxnBooked, acctRow).InvoiceNumbers; len(got) != 0 {
		t.Errorf("account booking lists fakturas %v", got)
	}
	res, err = f.unassign(acctRow)
	if err != nil {
		t.Fatalf("unassign account assign: %v", err)
	}
	if len(res.InvoiceIDs) != 0 || f.invoicePayment(t, orderInv.ID) != bankPayment || bankPayment == "" {
		t.Errorf("result %+v; the bank payment of the order's faktura must stay", res)
	}
	if b := f.accountBalance(t, "3900"); b != 0 {
		t.Errorf("3900 nets %v, want 0 after the void", b)
	}
}

// A fee the pass booked is unassigned and held: the pass does not book it
// again until the person rebooks it.
func TestUnassignAndRebookStripeFee(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	fee := f.insertBT(t, stripeBT{id: "txn_test_un_fee", category: "fee", stripeTyp: "stripe_fee", amountOre: -250})
	if res := f.book(t); res.FeesBooked != 1 {
		t.Fatalf("booking pass = %+v", res)
	}
	entry := f.bt(t, fee).entry
	if _, err := f.unassign(fee); err != nil {
		t.Fatalf("unassign fee: %v", err)
	}
	if b := f.accountBalance(t, "7700"); b != 0 {
		t.Errorf("7700 nets %v, want 0", b)
	}
	if res := f.book(t); res.FeesBooked != 0 {
		t.Errorf("booking pass booked the held fee: %+v", res)
	}
	if got := f.item(t, StripeTxnNeedsReview, fee).AllowedActions; !slices.Equal(got,
		[]string{StripeActionRebook, StripeActionAssignAccount, StripeActionDismiss}) {
		t.Errorf("held fee actions = %v", got)
	}
	rb, err := f.svc.RebookStripeTxn(ctx, f.club.ID, f.club.Treasurer.ID, fee)
	if err != nil || rb.Outcome != StripeRebookBooked || rb.JournalEntryID == entry {
		t.Fatalf("rebook = %+v, %v", rb, err)
	}
	if b := f.accountBalance(t, "7700"); b != 2.5 {
		t.Errorf("7700 = %v after the rebook, want 2.5", b)
	}
}

// Refusals change nothing: a row not booked, a payout row, a row whose
// payout is cleared, and an entry in a closed period.
func TestUnassignStripeTxnRefusals(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()

	review := f.reviewRow(t, "txn_test_unr_review", "charge", StripeReviewNoAttempt, 1000, 0, "")
	_, err := f.unassign(review)
	wantRefusal(t, err, StripeTxnNotAllowed)
	payout := f.insertBT(t, stripeBT{id: "txn_test_unr_po", category: "payout", amountOre: -1000})
	_, err = f.unassign(payout)
	wantRefusal(t, err, StripeTxnUseBankUnassign)
	if _, err := f.unassign("00000000-0000-0000-0000-000000000001"); !errors.Is(err, ErrStripeTxnNotFound) {
		t.Errorf("unknown row: %v", err)
	}

	cleared := f.insertBT(t, stripeBT{id: "txn_test_unr_cleared", category: "fee", amountOre: -100})
	closed := f.insertBT(t, stripeBT{id: "txn_test_unr_closed", category: "fee", amountOre: -300})
	f.book(t)
	if _, err := f.club.Pool.Exec(ctx,
		`WITH p AS (
		   INSERT INTO stripe_payouts (club_id, payout_id, amount_ore, currency, arrival_date, status, automatic, journal_entry_id)
		   VALUES ($1, 'po_test_unr', 100, 'nok', make_date($2, 6, 20), 'paid', true,
		           (SELECT journal_entry_id FROM stripe_balance_transactions WHERE id = $3))
		   RETURNING id)
		 UPDATE stripe_balance_transactions SET payout_id = (SELECT id FROM p) WHERE id = $3`,
		f.club.ID, f.club.Year, cleared); err != nil {
		t.Fatal(err)
	}
	if got := f.item(t, StripeTxnBooked, cleared).AllowedActions; len(got) != 0 {
		t.Errorf("cleared member actions = %v", got)
	}
	_, err = f.unassign(cleared)
	wantRefusal(t, err, StripeTxnPayoutCleared)

	if _, err := f.club.Pool.Exec(ctx, `UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.club.PeriodID); err != nil {
		t.Fatal(err)
	}
	_, err = f.unassign(closed)
	wantRefusal(t, err, StripeTxnPeriodClosed)
	for _, id := range []string{cleared, closed} {
		if r := f.bt(t, id); r.status != StripeTxnBooked || f.entryStatus(t, r.entry) != "posted" {
			t.Errorf("row %s = %+v after a refusal, want booked and posted", id, r)
		}
	}
}

// An entry voided before the row was unassigned (in the ledger, before it
// refused Stripe entries) is not reversed again; the row and faktura are
// still released.
func TestUnassignStripeTxnAlreadyVoided(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 12500, Lines: []fin.Line{{Category: "membership", Cents: 12500}}})
	rowID, entryID := f.passBookedCharge(t, "txn_test_un_voided", inv)
	if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, entryID, f.club.Treasurer.ID); err != nil {
		t.Fatal(err)
	}
	res, err := f.unassign(rowID)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !res.AlreadyVoided || res.ReversalEntryID != "" || f.reversals(t, entryID) != 1 {
		t.Errorf("result = %+v with %d reversals, want the void tolerated", res, f.reversals(t, entryID))
	}
	if f.invoicePayment(t, inv.ID) != "" {
		t.Error("faktura still paid")
	}
	if row := f.bt(t, rowID); row.status != StripeTxnNeedsReview || row.reason != StripeReviewUnassigned {
		t.Errorf("row = %+v", row)
	}
}

// The ledger refuses to void an entry booked from a Stripe row; ordinary
// entries are still voided there.
func TestVoidLedgerEntryRefusesStripeEntries(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	fee := f.insertBT(t, stripeBT{id: "txn_test_ledger_fee", category: "fee", amountOre: -100})
	f.book(t)
	entry := f.bt(t, fee).entry
	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, entry, f.club.Treasurer.ID); !errors.Is(err, ErrStripeSourcedEntry) {
		t.Errorf("ledger void of a Stripe entry: %v, want ErrStripeSourcedEntry", err)
	}
	if s := f.entryStatus(t, entry); s != "posted" {
		t.Errorf("entry %s, want posted", s)
	}

	manual, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID, EntryDate: strconv.Itoa(f.club.Year) + "-06-15", Description: "Synthetic manual entry",
		Source: "manual", CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
		Lines: []CreateJournalLineInput{{AccountCode: "1920", Debit: 10}, {AccountCode: "3900", Credit: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, manual.ID, f.club.Treasurer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, manual.ID, f.club.Treasurer.ID); err != nil {
		t.Errorf("ledger void of a manual entry: %v", err)
	}
	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, "00000000-0000-0000-0000-000000000001", f.club.Treasurer.ID); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("unknown entry: %v", err)
	}
}

// A payout's members are unassigned only once the payout clearing is
// undone from its bank deposit (UnassignBankRow, which the ledger refusal
// does not touch: the clearing entry is sourced to the bank row). The held
// member then keeps the payout waiting.
func TestUnassignStripeMemberAfterBankUnassign(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	order := catalogOrder(t, f.club, map[string]float64{"service": 120})
	created := time.Date(f.club.Year, 6, 8, 10, 0, 0, 0, time.UTC)
	po := f.insertPayout(t, payoutSpec{id: "po_test_un_member", amountOre: 11700, arrival: f.day(6, 10)})
	charge := f.member(t, po, stripeBT{id: "txn_test_un_member", category: "charge", amountOre: 12000, feeOre: 300, orderID: order, created: created})
	payoutBT := f.insertBT(t, stripeBT{id: "txn_test_un_member_po", category: "payout", amountOre: -11700, created: created.AddDate(0, 0, 1)})
	if _, err := f.club.Pool.Exec(ctx, `UPDATE stripe_balance_transactions SET payout_id = $2 WHERE id = $1`, payoutBT, po); err != nil {
		t.Fatal(err)
	}
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 11), 11700, "STRIPE PAYMENTS PAYOUT")
	f.book(t)
	if res := f.sweep(t); sweepCounts(res) != [4]int{1, 0, 0, 0} {
		t.Fatalf("sweep = %+v, want the payout cleared", res)
	}
	clearing := bankRowEntry(t, f.club, row)
	var sourceTable string
	if err := f.club.Pool.QueryRow(ctx, `SELECT source_table FROM journal_entries WHERE id = $1`, clearing).Scan(&sourceTable); err != nil ||
		sourceTable != "bank_import_rows" {
		t.Fatalf("clearing source_table = %q (%v)", sourceTable, err)
	}

	_, err := f.unassign(charge)
	wantRefusal(t, err, StripeTxnPayoutCleared)
	_, err = f.unassign(payoutBT)
	wantRefusal(t, err, StripeTxnUseBankUnassign)

	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("bank unassign: %v", err)
	}
	if _, err := f.unassign(charge); err != nil {
		t.Fatalf("unassign after the bank unassign: %v", err)
	}
	res := f.sweep(t)
	if sweepCounts(res) != [4]int{0, 1, 0, 0} {
		t.Errorf("sweep = %+v, want the payout waiting on the held charge", res)
	}
	if b := f.accountBalance(t, "1580"); b != 0 {
		t.Errorf("1580 nets %v, want 0 with everything undone", b)
	}
}

// Unassigns racing booking passes, on a pool of two connections and with
// passes outside the club's slot (a second API process), void each entry
// once and leave every row held; no pass books a held row again.
func TestUnassignStripeConcurrentWithBookingPass(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(2))
	svc := NewService(pool, nil, zerolog.Nop())
	other := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &stripeFixture{svc: svc, club: club, src: newFakeStripeBalance(), now: time.Now().Unix()}

	var rows, invoices []string
	for i := range 4 {
		inv := club.NewInvoice(t, fin.InvoiceOpts{Cents: 20000})
		invoices = append(invoices, inv.ID)
		rows = append(rows, f.insertBT(t, stripeBT{id: "txn_test_unrace_" + strconv.Itoa(i), category: "charge",
			amountOre: 20000, feeOre: 500, orderID: club.NewOrderForInvoice(t, inv).ID}))
	}
	if res := f.book(t); res.ChargesBooked != 4 {
		t.Fatalf("booking pass = %+v", res)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for _, id := range rows {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := svc.UnassignStripeTxn(ctx, club.ID, club.Treasurer.ID, id); err != nil {
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
	for i, id := range rows {
		var entries, voided int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*), count(*) FILTER (WHERE status = 'voided') FROM journal_entries WHERE club_id = $1 AND source_id = $2`,
			club.ID, id).Scan(&entries, &voided); err != nil {
			t.Fatal(err)
		}
		if row := f.bt(t, id); row.status != StripeTxnNeedsReview || row.reason != StripeReviewUnassigned || entries != 1 || voided != 1 {
			t.Errorf("row %s = %+v with %d entries (%d voided), want held with its one entry voided", id, row, entries, voided)
		}
		if f.invoicePayment(t, invoices[i]) != "" {
			t.Errorf("faktura %s still paid", invoices[i])
		}
	}
}

// postedManualEntry posts a balanced synthetic entry in the club's period.
func (f *stripeFixture) postedManualEntry(t *testing.T, desc string) string {
	t.Helper()
	ctx := context.Background()
	e, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID, EntryDate: strconv.Itoa(f.club.Year) + "-06-15", Description: desc,
		Source: "manual", CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
		Lines: []CreateJournalLineInput{{AccountCode: "1920", Debit: 10}, {AccountCode: "3900", Credit: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, e.ID, f.club.Treasurer.ID); err != nil {
		t.Fatal(err)
	}
	return e.ID
}

// The ledger refuses to void an entry a bank row or a Stripe payout points
// at (undone from Tildel), and still voids one a Vipps charge points at
// (an order-booked Vipps charge is undone that way).
func TestVoidLedgerEntryRefusesBankLinkedEntries(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	void := func(id string) error {
		_, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, id, f.club.Treasurer.ID)
		return err
	}

	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 15000})
	bankRow := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 15000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, bankRow, inv.ID); err != nil {
		t.Fatal(err)
	}
	bankEntry := bankRowEntry(t, f.club, bankRow)
	if err := void(bankEntry); !errors.Is(err, ErrBankLinkedEntry) {
		t.Errorf("void of a bank row's entry: %v, want ErrBankLinkedEntry", err)
	}
	if s := f.entryStatus(t, bankEntry); s != "posted" {
		t.Errorf("bank entry %s, want posted", s)
	}

	payoutEntry := f.postedManualEntry(t, "Synthetic payout clearing")
	f.insertPayout(t, payoutSpec{id: "po_test_ledger", amountOre: 1000, arrival: f.day(6, 10)})
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE stripe_payouts SET journal_entry_id = $2 WHERE club_id = $1 AND payout_id = 'po_test_ledger'`,
		f.club.ID, payoutEntry); err != nil {
		t.Fatal(err)
	}
	if err := void(payoutEntry); !errors.Is(err, ErrBankLinkedEntry) {
		t.Errorf("void of a Stripe payout's entry: %v, want ErrBankLinkedEntry", err)
	}

	vippsEntry := f.postedManualEntry(t, "Synthetic Vipps order charge")
	vippsRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 1000})
	if _, err := f.club.Pool.Exec(ctx, `UPDATE vipps_import_rows SET journal_entry_id = $2 WHERE id = $1`, vippsRow, vippsEntry); err != nil {
		t.Fatal(err)
	}
	if err := void(vippsEntry); err != nil {
		t.Errorf("void of a Vipps charge's entry: %v, want it voided", err)
	}
}

// A held charge with an order, booked by hand to another faktura, is
// unassigned as a manual booking: only the faktura in invoice_id reopens,
// and the order's faktura (paid by the bank meanwhile) keeps its payment.
func TestUnassignHeldChargeAssignedToAnotherFaktura(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	actor := f.club.Treasurer.ID
	orderInv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 18000, Lines: []fin.Line{{Category: "membership", Cents: 18000}}})
	rowID, _ := f.passBookedCharge(t, "txn_test_un_other", orderInv)
	if _, err := f.unassign(rowID); err != nil {
		t.Fatal(err)
	}

	other := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 18000})
	if _, err := f.svc.AssignStripeTxnToInvoice(ctx, f.club.ID, actor, rowID, other.ID); err != nil {
		t.Fatalf("assign to another faktura: %v", err)
	}
	bankRow := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 18000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, actor, bankRow, orderInv.ID); err != nil {
		t.Fatalf("bank payment of the order's faktura: %v", err)
	}
	bankPayment := f.invoicePayment(t, orderInv.ID)
	if got := f.item(t, StripeTxnBooked, rowID).InvoiceNumbers; !slices.Equal(got, []int{other.Number}) {
		t.Errorf("invoice numbers = %v, want only %d", got, other.Number)
	}

	res, err := f.unassign(rowID)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !slices.Equal(res.InvoiceIDs, []string{other.ID}) {
		t.Errorf("reopened %v, want only %s", res.InvoiceIDs, other.ID)
	}
	if f.invoicePayment(t, other.ID) != "" {
		t.Error("the hand-assigned faktura is still paid")
	}
	if p := f.invoicePayment(t, orderInv.ID); p != bankPayment || p == "" {
		t.Errorf("order faktura payment = %q, want the bank payment %q kept", p, bankPayment)
	}
}
