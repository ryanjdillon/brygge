package accounting

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// refundTxn adds a refund of the charge stored as chargeBT to the fake
// balance, with the metadata Stripe carries on the refund.
func (f *stripeFixture) refundTxn(id string, ageSec, amountOre int64, chargeBT string, meta map[string]string) {
	f.other(id, ageSec, -amountOre, stripe.CategoryRefund,
		&stripe.BalanceSource{ID: "re_" + id, Object: "refund", Charge: stripe.ObjectID("ch_" + chargeBT), Metadata: meta})
}

// refundFailure adds the failure of the refund stored as refundBT.
func (f *stripeFixture) refundFailure(id string, ageSec, amountOre int64, refundBT string) {
	f.other(id, ageSec, amountOre, stripe.CategoryRefundFailure,
		&stripe.BalanceSource{ID: "re_" + refundBT, Object: "refund"})
}

func (f *stripeFixture) rowOf(t *testing.T, btID string) string {
	t.Helper()
	var id string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT id FROM stripe_balance_transactions WHERE club_id = $1 AND balance_transaction_id = $2`,
		f.club.ID, btID).Scan(&id); err != nil {
		t.Fatalf("row %s: %v", btID, err)
	}
	return id
}

type storedRefundDoc struct {
	kind, invoiceID, creditInvoiceID, detached string
	amountOre                                  int64
}

func (f *stripeFixture) refundDocs(t *testing.T, rowID string) []storedRefundDoc {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT kind, COALESCE(invoice_id::text, ''), COALESCE(credit_invoice_id::text, ''),
		        COALESCE(detached_order_id::text, ''), amount_ore
		   FROM refund_documents WHERE refund_txn_id = $1 ORDER BY amount_ore DESC, id`, rowID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []storedRefundDoc
	for rows.Next() {
		var d storedRefundDoc
		if err := rows.Scan(&d.kind, &d.invoiceID, &d.creditInvoiceID, &d.detached, &d.amountOre); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// invoiceState is what a faktura reads as for the refund tests.
type invoiceState struct {
	paid, online, credited, owed, booked bool
	total                                float64
	issueDate, sentAt                    string
}

func (f *stripeFixture) invoice(t *testing.T, id string) invoiceState {
	t.Helper()
	var s invoiceState
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT i.payment_id IS NOT NULL, i.online_paid_at IS NOT NULL, `+finance.CreditedSQL("i")+`,
		        `+finance.OwedByMemberSQL("i")+`, `+invoiceAlreadyBookedSQL("i")+`,
		        i.total_amount, i.issue_date::text, COALESCE(i.sent_at::text, '')
		   FROM invoices i WHERE i.id = $1`, id,
	).Scan(&s.paid, &s.online, &s.credited, &s.owed, &s.booked, &s.total, &s.issueDate, &s.sentAt); err != nil {
		t.Fatalf("invoice %s: %v", id, err)
	}
	return s
}

// noteLines is a document's lines by category ("" for none), in kroner.
func (f *stripeFixture) noteLines(t *testing.T, id string) map[string]float64 {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT COALESCE(category, ''), line_total FROM invoice_lines WHERE invoice_id = $1`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var c string
		var v float64
		if err := rows.Scan(&c, &v); err != nil {
			t.Fatal(err)
		}
		out[c] += v
	}
	return out
}

func (f *stripeFixture) orderStatus(t *testing.T, orderID string) string {
	t.Helper()
	var s string
	if err := f.club.Pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1`, orderID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *stripeFixture) refunded(t *testing.T, rowID string) (ore int64, recollected bool) {
	t.Helper()
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT refunded_ore, recollected_at IS NOT NULL FROM stripe_balance_transactions WHERE id = $1`, rowID,
	).Scan(&ore, &recollected); err != nil {
		t.Fatal(err)
	}
	return ore, recollected
}

// balance is an account's debit less credit over every Stripe and faktura
// entry of the club.
func (f *stripeFixture) balance(t *testing.T, code string) float64 {
	t.Helper()
	var v float64
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(jl.debit) - sum(jl.credit), 0) FROM journal_lines jl
		   JOIN journal_entries je ON je.id = jl.journal_entry_id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1 AND je.status IN ('posted', 'voided') AND a.code = $2`, f.club.ID, code,
	).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *stripeFixture) attemptOf(t *testing.T, rowID string) string {
	t.Helper()
	var a string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT attempt_id::text FROM stripe_balance_transactions WHERE id = $1`, rowID).Scan(&a); err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *stripeFixture) orderOf(t *testing.T, rowID string) string {
	t.Helper()
	var o string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT order_id::text FROM stripe_balance_transactions WHERE id = $1`, rowID).Scan(&o); err != nil {
		t.Fatal(err)
	}
	return o
}

// requestRefund stores what the refund button leaves before Stripe answers:
// a requested payment_refunds row with its key and outcome.
func (f *stripeFixture) requestRefund(t *testing.T, attemptID string, amountOre int64, outcome string) string {
	t.Helper()
	key := uuid.NewString()
	var out *string
	if outcome != "" {
		out = &outcome
	}
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, reason, created_by, request_key, outcome)
		 VALUES ($1, $2, 'stripe', $3::bigint / 100.0, 'Synthetic request', $4, $5, $6)`,
		f.club.ID, attemptID, amountOre, f.club.Treasurer.ID, key, out); err != nil {
		t.Fatal(err)
	}
	return key
}

// twoLineInvoice is a 450 kr faktura: membership 300 and a slip fee 150.
func (f *stripeFixture) twoLineInvoice(t *testing.T) fin.Invoice {
	t.Helper()
	return f.club.NewInvoice(t, fin.InvoiceOpts{Lines: []fin.Line{
		{Category: "membership", Cents: 30000}, {Category: "slip_fee", Cents: 15000},
	}})
}

// A partial card refund of a faktura issues a credit note for the refunded
// part, its lines scaled from the faktura's and dated the refund's booking
// date, books it and the refund (DR 1500 / CR clearing), and queues it for
// delivery; the faktura stays paid and is not credited. A second pass and
// a re-sync change nothing. With no request in Brygge (a Dashboard refund),
// the pass records the refund.
func TestRefundPartialIssuesCreditNote(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_rp_charge", inv)
	f.refundTxn("txn_test_rp_refund", 300, 9000, "txn_test_rp_charge", nil)
	f.sync(t)

	res := f.book(t)
	if res.RefundsBooked != 1 || len(res.Errors) != 0 || len(res.NeedsReview) != 0 {
		t.Fatalf("result = %+v, want the refund booked", res)
	}
	refund := f.rowOf(t, "txn_test_rp_refund")
	row := f.bt(t, refund)
	if row.status != StripeTxnBooked || row.reason != "" {
		t.Fatalf("refund row = %+v, want booked", row)
	}
	lines := creditLines(t, f.club, row.entry)
	if lines["1500"] != [2]float64{90, 0} || lines["1580"] != [2]float64{0, 90} || len(lines) != 2 {
		t.Errorf("refund entry = %v, want DR 1500 / CR 1580 90", lines)
	}
	docs := f.refundDocs(t, refund)
	if len(docs) != 1 || docs[0].kind != "credit" || docs[0].invoiceID != inv.ID || docs[0].amountOre != 9000 {
		t.Fatalf("documents = %+v, want one 90 kr credit note for the faktura", docs)
	}
	note := docs[0].creditInvoiceID
	if got := f.noteLines(t, note); got["membership"] != -60 || got["slip_fee"] != -30 || len(got) != 2 {
		t.Errorf("credit note lines = %v, want membership -60 and slip fee -30", got)
	}
	ns := f.invoice(t, note)
	wantDate := stripeBookingDate(time.Unix(f.now-300, 0), time.Local).Format("2006-01-02")
	if ns.total != -90 || ns.issueDate != wantDate || ns.sentAt == "" {
		t.Errorf("credit note = %+v, want -90 issued and sent on %s", ns, wantDate)
	}
	var noteEntries, deliveries int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM journal_entries WHERE source = 'invoice_sync' AND source_id = $1::text AND status = 'posted'),
		        (SELECT count(*) FROM deliveries WHERE invoice_id = $1::uuid AND kind = 'invoice_refund_document')`, note,
	).Scan(&noteEntries, &deliveries); err != nil {
		t.Fatal(err)
	}
	if noteEntries != 1 || deliveries != 1 {
		t.Errorf("credit note entries %d deliveries %d, want one of each", noteEntries, deliveries)
	}
	if s := f.invoice(t, inv.ID); !s.paid || s.credited || s.owed {
		t.Errorf("faktura = %+v, want still paid and not credited", s)
	}
	if ore, _ := f.refunded(t, charge); ore != 9000 {
		t.Errorf("refunded_ore = %d, want 9000", ore)
	}
	var reason, status string
	var createdBy *string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT reason, status, created_by::text FROM payment_refunds WHERE provider_refund_id = 're_txn_test_rp_refund'`,
	).Scan(&reason, &status, &createdBy); err != nil || reason != "Stripe Dashboard" || status != "succeeded" || createdBy != nil {
		t.Errorf("payment_refunds = %q %q %v (%v), want a Dashboard row with no person", reason, status, createdBy, err)
	}

	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 0 || len(res.Errors) != 0 {
		t.Errorf("second pass = %+v, want nothing", res)
	}
	if n := len(f.refundDocs(t, refund)); n != 1 {
		t.Errorf("documents after a second pass = %d", n)
	}
}

// A credit note copies the faktura net of exemptions: an exempt item and
// its exemption line drop out, so the note credits only what was paid for.
func TestRefundCreditNoteNetsExemptions(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.twoLineInvoice(t)
	if _, err := f.club.Pool.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, account_id,
		                            price_item_id, category, exemption_role)
		 VALUES ($1, 'Fritak', 1, -150, -150, (SELECT id FROM accounts WHERE club_id = $2 AND code = '7790'),
		         $3, 'slip_fee', 'chair')`,
		inv.ID, f.club.ID, f.club.PriceItem(t, "slip_fee")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.club.Pool.Exec(ctx, `UPDATE invoices SET total_amount = 300 WHERE id = $1`, inv.ID); err != nil {
		t.Fatal(err)
	}
	inv.TotalCents = 30000
	f.passBookedCharge(t, "txn_test_ex_charge", inv)
	f.refundTxn("txn_test_ex_refund", 300, 15000, "txn_test_ex_charge", nil)
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 {
		t.Fatalf("result = %+v", res)
	}
	docs := f.refundDocs(t, f.rowOf(t, "txn_test_ex_refund"))
	if len(docs) != 1 {
		t.Fatalf("documents = %+v", docs)
	}
	if got := f.noteLines(t, docs[0].creditInvoiceID); got["membership"] != -150 || len(got) != 1 {
		t.Errorf("credit note lines = %v, want membership -150 only", got)
	}
}

// Partial refunds adding up to the charge credit the faktura in full
// (finance.CreditedSQL) to the øre, the order turns refunded, and a
// replayed capture cannot mark the faktura again.
func TestRefundPartialsSumToFull(t *testing.T) {
	f := newStripeFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_pf_charge", inv)
	order := f.orderOf(t, charge)
	f.refundTxn("txn_test_pf_r1", 400, 10001, "txn_test_pf_charge", nil)
	f.refundTxn("txn_test_pf_r2", 300, 34999, "txn_test_pf_charge", nil)
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 2 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want both refunds booked", res)
	}
	var sum int64
	for _, id := range []string{"txn_test_pf_r1", "txn_test_pf_r2"} {
		for _, d := range f.refundDocs(t, f.rowOf(t, id)) {
			sum += d.amountOre
		}
	}
	s := f.invoice(t, inv.ID)
	if sum != 45000 || !s.credited || s.owed {
		t.Errorf("credited %d øre, faktura %+v; want 45000 and credited", sum, s)
	}
	if st := f.orderStatus(t, order); st != "refunded" {
		t.Errorf("order = %s, want refunded", st)
	}
	if ore, _ := f.refunded(t, charge); ore != 45000 {
		t.Errorf("refunded_ore = %d", ore)
	}
	// What the refunds took off clearing is what the charge put there;
	// only Stripe's fee is gone.
	if got := f.balance(t, "1580"); got != -float64(45000/40)/100 {
		t.Errorf("clearing = %v, want only the charge's fee taken off", got)
	}
}

// A multi-faktura order is divided by share, a partial first and then the
// rest, and each faktura ends credited in full.
func TestRefundMultiFakturaOrder(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	a := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 30000, Lines: []fin.Line{{Category: "membership", Cents: 30000}}})
	b := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 15000, Lines: []fin.Line{{Category: "slip_fee", Cents: 15000}}, Member: &fin.Member{ID: a.MemberID}})
	order := f.club.NewOrderForInvoices(t, a, b)
	markPaidByStripe(t, f.club, a.ID)
	markPaidByStripe(t, f.club, b.ID)
	var attemptID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, currency, created_at)
		 VALUES ($1, $2, 'stripe', 'cs_test_multi', 'captured', 450, 'NOK', now() - interval '1 hour') RETURNING id`,
		f.club.ID, order.ID).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	f.charge("txn_test_mf_charge", 600, 45000, "nok", map[string]string{"attempt_id": attemptID}, "pi_test_mf")
	f.refundTxn("txn_test_mf_r1", 400, 9000, "txn_test_mf_charge", nil)
	f.sync(t)
	if res := f.book(t); res.ChargesBooked != 1 || res.RefundsBooked != 1 {
		t.Fatalf("result = %+v", res)
	}
	docs := f.refundDocs(t, f.rowOf(t, "txn_test_mf_r1"))
	if len(docs) != 2 || docs[0].amountOre != 6000 || docs[0].invoiceID != a.ID || docs[1].amountOre != 3000 {
		t.Fatalf("partial documents = %+v, want 60 and 30", docs)
	}
	f.refundTxn("txn_test_mf_r2", 300, 36000, "txn_test_mf_charge", nil)
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 {
		t.Fatalf("result = %+v", res)
	}
	for _, inv := range []fin.Invoice{a, b} {
		if s := f.invoice(t, inv.ID); !s.credited {
			t.Errorf("faktura %d not credited after a full refund", inv.Number)
		}
	}
	if st := f.orderStatus(t, order.ID); st != "refunded" {
		t.Errorf("order = %s", st)
	}
}

// A full refund the treasurer asked to collect again issues no credit
// note: the faktura loses its payment and online marker, is detached from
// the refunded order and reads as open (a bank KID payment may book it);
// a new order can be minted for it and a card payment on it books.
func TestRefundRecollectReopensFaktura(t *testing.T) {
	f := newStripeFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_rc_charge", inv)
	order := f.orderOf(t, charge)
	key := f.requestRefund(t, f.attemptOf(t, charge), 45000, "recollect")
	f.refundTxn("txn_test_rc_refund", 300, 45000, "txn_test_rc_charge", map[string]string{"refund_request_key": key})
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	refund := f.rowOf(t, "txn_test_rc_refund")
	docs := f.refundDocs(t, refund)
	if len(docs) != 1 || docs[0].kind != "recollect" || docs[0].creditInvoiceID != "" || docs[0].detached != order {
		t.Fatalf("documents = %+v, want one recollect detached from the order", docs)
	}
	s := f.invoice(t, inv.ID)
	if s.paid || s.online || s.credited || !s.owed || s.booked {
		t.Errorf("faktura = %+v, want an ordinary open faktura", s)
	}
	if _, recollected := f.refunded(t, charge); !recollected {
		t.Error("charge not marked recollected")
	}
	if st := f.orderStatus(t, order); st != "refunded" {
		t.Errorf("order = %s, want refunded", st)
	}
	var key2 *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT provider_refund_id FROM payment_refunds WHERE request_key = $1`, key).Scan(&key2); err != nil ||
		key2 == nil || *key2 != "re_txn_test_rc_refund" {
		t.Errorf("request matched by key: provider id %v (%v)", key2, err)
	}

	// The faktura takes a fresh card payment through a new order.
	f.passBookedCharge(t, "txn_test_rc_again", inv)
	if s := f.invoice(t, inv.ID); !s.paid {
		t.Errorf("faktura not paid by the new card payment: %+v", s)
	}
}

// Collecting again is refused after a partial refund was credited: the
// full refund is held as refund_split and nothing of it is kept.
func TestRefundRecollectRefusedAfterPartial(t *testing.T) {
	f := newStripeFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_rr_charge", inv)
	f.refundTxn("txn_test_rr_r1", 400, 5000, "txn_test_rr_charge", nil)
	f.sync(t)
	f.book(t)
	key := f.requestRefund(t, f.attemptOf(t, charge), 40000, "recollect")
	f.refundTxn("txn_test_rr_r2", 300, 40000, "txn_test_rr_charge", map[string]string{"refund_request_key": key})
	f.sync(t)
	res := f.book(t)
	if res.NeedsReview[StripeReviewRefundSplit] != 1 {
		t.Fatalf("result = %+v, want the full refund held as refund_split", res)
	}
	r2 := f.rowOf(t, "txn_test_rr_r2")
	if row := f.bt(t, r2); row.status != StripeTxnNeedsReview || row.reason != StripeReviewRefundSplit || row.entry != "" {
		t.Errorf("row = %+v", row)
	}
	if docs := f.refundDocs(t, r2); len(docs) != 0 {
		t.Errorf("documents kept for a held refund: %+v", docs)
	}
	if s := f.invoice(t, inv.ID); !s.paid {
		t.Errorf("faktura reopened by a refused recollect: %+v", s)
	}
	if ore, _ := f.refunded(t, charge); ore != 5000 {
		t.Errorf("refunded_ore = %d, want only the partial", ore)
	}
}

// A guest's catalogue order with no faktura gets one credit note for the
// order, to the guest's name; with no email it is queued for its PDF only.
func TestRefundCatalogueOrderGuest(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	orderID := catalogOrder(t, f.club, map[string]float64{"guest": 300})
	if _, err := f.club.Pool.Exec(ctx, `UPDATE orders SET guest_name = 'Synthetic Guest', guest_email = '' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	var attemptID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, currency, created_at)
		 VALUES ($1, $2, 'stripe', 'cs_test_guest', 'captured', 300, 'NOK', now() - interval '1 hour') RETURNING id`,
		f.club.ID, orderID).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	f.charge("txn_test_g_charge", 600, 30000, "nok", map[string]string{"attempt_id": attemptID}, "pi_test_g")
	f.refundTxn("txn_test_g_refund", 300, 30000, "txn_test_g_charge", nil)
	f.sync(t)
	if res := f.book(t); res.ChargesBooked != 1 || res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	docs := f.refundDocs(t, f.rowOf(t, "txn_test_g_refund"))
	if len(docs) != 1 || docs[0].invoiceID != "" || docs[0].amountOre != 30000 {
		t.Fatalf("documents = %+v, want one order credit note", docs)
	}
	var name, email string
	var user *string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT i.member_name, d.email, i.user_id::text FROM invoices i JOIN deliveries d ON d.invoice_id = i.id WHERE i.id = $1`,
		docs[0].creditInvoiceID).Scan(&name, &email, &user); err != nil {
		t.Fatal(err)
	}
	if name != "Synthetic Guest" || email != "" || user != nil {
		t.Errorf("credit note to %q / %q / %v, want the guest with no email", name, email, user)
	}
	if st := f.orderStatus(t, orderID); st != "refunded" {
		t.Errorf("order = %s", st)
	}
}

// A charge booked to an account by hand is refunded against that account,
// with no document.
func TestRefundOfAccountAssignedCharge(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	f.charge("txn_test_ac_charge", 600, 20000, "nok", nil, "")
	f.sync(t)
	charge := f.rowOf(t, "txn_test_ac_charge")
	if _, err := f.svc.AssignStripeTxnToAccount(ctx, f.club.ID, f.club.Treasurer.ID, charge, "3900", "Synthetic"); err != nil {
		t.Fatal(err)
	}
	f.refundTxn("txn_test_ac_refund", 300, 5000, "txn_test_ac_charge", nil)
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 {
		t.Fatalf("result = %+v", res)
	}
	refund := f.rowOf(t, "txn_test_ac_refund")
	if lines := creditLines(t, f.club, f.bt(t, refund).entry); lines["3900"] != [2]float64{50, 0} || lines["1580"] != [2]float64{0, 50} {
		t.Errorf("refund entry = %v, want DR 3900 / CR 1580 50", lines)
	}
	if docs := f.refundDocs(t, refund); len(docs) != 0 {
		t.Errorf("documents = %+v, want none", docs)
	}

	// Unassigning it is allowed and gives the charge its amount back; a
	// rebook books it again.
	if _, err := f.unassign(refund); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if ore, _ := f.refunded(t, charge); ore != 0 {
		t.Errorf("refunded_ore after unassign = %d", ore)
	}
	if res, err := f.svc.RebookStripeTxn(ctx, f.club.ID, f.club.Treasurer.ID, refund); err != nil || res.Outcome != StripeRebookBooked {
		t.Fatalf("rebook = %+v, %v", res, err)
	}
	if ore, _ := f.refunded(t, charge); ore != 5000 {
		t.Errorf("refunded_ore after rebook = %d", ore)
	}
}

// A duplicate capture (a charge Brygge could not tie to an open faktura)
// refunded in full books as a pair through the suspense account; a partial
// refund of such a charge waits.
func TestRefundDuplicateCapturePair(t *testing.T) {
	f := newStripeFixture(t)
	f.charge("txn_test_dup_charge", 600, 45000, "nok", nil, "")
	f.charge("txn_test_dup_other", 600, 20000, "nok", nil, "")
	f.refundTxn("txn_test_dup_refund", 300, 45000, "txn_test_dup_charge", nil)
	f.refundTxn("txn_test_dup_part", 300, 5000, "txn_test_dup_other", nil)
	f.sync(t)
	res := f.book(t)
	if res.RefundsBooked != 1 || res.NeedsReview[StripeReviewRefundChargeUnbooked] != 1 {
		t.Fatalf("result = %+v, want the pair booked and the partial held", res)
	}
	charge := f.bt(t, f.rowOf(t, "txn_test_dup_charge"))
	if charge.status != StripeTxnBooked || charge.reason != StripeReviewNoAttempt {
		t.Errorf("charge = %+v, want booked, keeping its review reason", charge)
	}
	if lines := creditLines(t, f.club, charge.entry); lines["2900"] != [2]float64{0, 450} || lines["1580"] != [2]float64{450, 11.25} {
		t.Errorf("charge entry = %v, want DR 1580 / CR 2900 with the fee", lines)
	}
	refund := f.bt(t, f.rowOf(t, "txn_test_dup_refund"))
	if lines := creditLines(t, f.club, refund.entry); lines["2900"] != [2]float64{450, 0} || lines["1580"] != [2]float64{0, 450} {
		t.Errorf("refund entry = %v, want DR 2900 / CR 1580", lines)
	}
	if got := f.balance(t, "2900"); got != 0 {
		t.Errorf("suspense = %v, want 0", got)
	}
	if _, err := f.unassign(f.rowOf(t, "txn_test_dup_charge")); !isRefusal(err, StripeTxnRefundBooked) {
		t.Errorf("unassign of the refunded charge = %v, want refund_booked", err)
	}
}

// A refund that arrives before its charge waits as refund_unlinked; once
// the charge is listed, one pass books the charge, then the refund.
func TestRefundUnlinkedThenChargeArrives(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.twoLineInvoice(t)
	order := f.club.NewOrderForInvoice(t, inv)
	markPaidByStripe(t, f.club, inv.ID)
	var attemptID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, currency, created_at)
		 VALUES ($1, $2, 'stripe', 'cs_test_late', 'captured', 450, 'NOK', now() - interval '1 hour') RETURNING id`,
		f.club.ID, order.ID).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	f.refundTxn("txn_test_late_refund", 300, 45000, "txn_test_late_charge", nil)
	f.sync(t)
	if res := f.book(t); res.NeedsReview[StripeReviewRefundUnlinked] != 1 {
		t.Fatalf("result = %+v, want the refund unlinked", res)
	}
	f.charge("txn_test_late_charge", 600, 45000, "nok", map[string]string{"attempt_id": attemptID}, "pi_test_late")
	f.sync(t)
	res := f.book(t)
	if res.ChargesBooked != 1 || res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want the charge and then the refund booked", res)
	}
	if s := f.invoice(t, inv.ID); !s.credited || !s.paid {
		t.Errorf("faktura = %+v, want paid and credited", s)
	}
}

// A refund failure of a credited refund mirrors the refund's entry and
// issues a new debit faktura for the money that came back, settled by it;
// the credit note stands and the order is paid again.
func TestRefundFailureOfCreditedRefund(t *testing.T) {
	f := newStripeFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_fc_charge", inv)
	order := f.orderOf(t, charge)
	f.refundTxn("txn_test_fc_refund", 400, 45000, "txn_test_fc_charge", nil)
	f.sync(t)
	f.book(t)
	note := f.refundDocs(t, f.rowOf(t, "txn_test_fc_refund"))[0].creditInvoiceID
	f.refundFailure("txn_test_fc_fail", 300, 45000, "txn_test_fc_refund")
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	fail := f.rowOf(t, "txn_test_fc_fail")
	if lines := creditLines(t, f.club, f.bt(t, fail).entry); lines["1580"] != [2]float64{450, 0} || lines["1500"] != [2]float64{0, 450} {
		t.Errorf("failure entry = %v, want DR 1580 / CR 1500", lines)
	}
	docs := f.refundDocs(t, fail)
	if len(docs) != 1 || docs[0].kind != "refail_invoice" || docs[0].invoiceID != inv.ID {
		t.Fatalf("documents = %+v, want one debit faktura", docs)
	}
	debit := f.invoice(t, docs[0].creditInvoiceID)
	if debit.total != 450 || !debit.paid || debit.owed {
		t.Errorf("debit faktura = %+v, want 450 settled", debit)
	}
	if got := f.noteLines(t, docs[0].creditInvoiceID); got["membership"] != 300 || got["slip_fee"] != 150 {
		t.Errorf("debit faktura lines = %v", got)
	}
	var noteStatus string
	if err := f.club.Pool.QueryRow(context.Background(), `SELECT status FROM invoices WHERE id = $1`, note).Scan(&noteStatus); err != nil || noteStatus != "open" {
		t.Errorf("credit note status = %q (%v), want it to stand", noteStatus, err)
	}
	if ore, _ := f.refunded(t, charge); ore != 0 {
		t.Errorf("refunded_ore = %d, want 0", ore)
	}
	if st := f.orderStatus(t, order); st != "paid" {
		t.Errorf("order = %s, want paid again", st)
	}
	if _, err := f.unassign(fail); !isRefusal(err, StripeTxnCreditNoteIssued) {
		t.Errorf("unassign of the failure = %v, want credit_note_issued", err)
	}
}

// A refund failure of a refund that reopened the faktura settles it again
// while it is still open; once the faktura was paid another way, the
// failure waits for a person.
func TestRefundFailureOfRecollect(t *testing.T) {
	for _, settledSince := range []bool{false, true} {
		t.Run(fmt.Sprintf("settled since %v", settledSince), func(t *testing.T) {
			f := newStripeFixture(t)
			ctx := context.Background()
			inv := f.twoLineInvoice(t)
			charge, _ := f.passBookedCharge(t, "txn_test_fr_charge", inv)
			order := f.orderOf(t, charge)
			key := f.requestRefund(t, f.attemptOf(t, charge), 45000, "recollect")
			f.refundTxn("txn_test_fr_refund", 400, 45000, "txn_test_fr_charge", map[string]string{"refund_request_key": key})
			f.sync(t)
			f.book(t)
			if settledSince {
				if _, err := f.club.Pool.Exec(ctx,
					`WITH p AS (INSERT INTO payments (club_id, user_id, type, amount, status, paid_at)
					            VALUES ($1, $2, 'dues', 450, 'completed', now()) RETURNING id)
					 UPDATE invoices SET payment_id = (SELECT id FROM p) WHERE id = $3`,
					f.club.ID, inv.MemberID, inv.ID); err != nil {
					t.Fatal(err)
				}
			}
			f.refundFailure("txn_test_fr_fail", 300, 45000, "txn_test_fr_refund")
			f.sync(t)
			res := f.book(t)
			fail := f.bt(t, f.rowOf(t, "txn_test_fr_fail"))
			if settledSince {
				if res.NeedsReview[StripeReviewRefundFailureSettled] != 1 || fail.reason != StripeReviewRefundFailureSettled {
					t.Fatalf("result = %+v, row %+v, want refund_failure_settled", res, fail)
				}
				return
			}
			if res.RefundsBooked != 1 || fail.status != StripeTxnBooked {
				t.Fatalf("result = %+v, row %+v", res, fail)
			}
			s := f.invoice(t, inv.ID)
			if !s.paid || !s.online || s.owed {
				t.Errorf("faktura = %+v, want settled again", s)
			}
			if _, recollected := f.refunded(t, charge); recollected {
				t.Error("charge still recollected")
			}
			if st := f.orderStatus(t, order); st != "paid" {
				t.Errorf("order = %s, want paid", st)
			}
			if got := f.orderLink(t, inv.ID); got != order {
				t.Errorf("faktura order link = %s, want the original %s", got, order)
			}
			// The charge settles the faktura again, so unassigning it
			// reopens it.
			if _, err := f.unassign(charge); err != nil {
				t.Fatalf("unassign charge: %v", err)
			}
			if p := f.invoicePayment(t, inv.ID); p != "" {
				t.Errorf("payment_id = %s after unassigning the charge, want it cleared", p)
			}
		})
	}
}

// A refund failure of a duplicate pair only mirrors the refund.
func TestRefundFailureOfDuplicatePair(t *testing.T) {
	f := newStripeFixture(t)
	f.charge("txn_test_fd_charge", 600, 45000, "nok", nil, "")
	f.refundTxn("txn_test_fd_refund", 400, 45000, "txn_test_fd_charge", nil)
	f.refundFailure("txn_test_fd_fail", 300, 45000, "txn_test_fd_refund")
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 2 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if lines := creditLines(t, f.club, f.bt(t, f.rowOf(t, "txn_test_fd_fail")).entry); lines["1580"] != [2]float64{450, 0} || lines["2900"] != [2]float64{0, 450} {
		t.Errorf("failure entry = %v, want DR 1580 / CR 2900", lines)
	}
	if got := f.balance(t, "2900"); got != -450 {
		t.Errorf("suspense = %v, want the returned 450 held", got)
	}
}

// A refund that issued credit notes is never undone, nor is the charge it
// refunded; a refund that reopened a faktura is undone while the faktura
// is still open, which settles it again.
func TestUnassignRefundRules(t *testing.T) {
	f := newStripeFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_ur_charge", inv)
	f.refundTxn("txn_test_ur_refund", 300, 9000, "txn_test_ur_charge", nil)
	f.sync(t)
	f.book(t)
	if _, err := f.unassign(f.rowOf(t, "txn_test_ur_refund")); !isRefusal(err, StripeTxnCreditNoteIssued) {
		t.Errorf("unassign of a credited refund = %v, want credit_note_issued", err)
	}
	if _, err := f.unassign(charge); !isRefusal(err, StripeTxnRefundBooked) {
		t.Errorf("unassign of the refunded charge = %v, want refund_booked", err)
	}

	inv2 := f.twoLineInvoice(t)
	charge2, _ := f.passBookedCharge(t, "txn_test_ur_charge2", inv2)
	key := f.requestRefund(t, f.attemptOf(t, charge2), 45000, "recollect")
	f.refundTxn("txn_test_ur_rc", 300, 45000, "txn_test_ur_charge2", map[string]string{"refund_request_key": key})
	f.sync(t)
	f.book(t)
	rc := f.rowOf(t, "txn_test_ur_rc")
	res, err := f.unassign(rc)
	if err != nil || len(res.InvoiceIDs) != 1 {
		t.Fatalf("unassign of a recollect = %+v, %v", res, err)
	}
	if s := f.invoice(t, inv2.ID); !s.paid || !s.online {
		t.Errorf("faktura = %+v, want settled again", s)
	}
	if ore, recollected := f.refunded(t, charge2); ore != 0 || recollected {
		t.Errorf("charge refunded %d recollected %v, want neither", ore, recollected)
	}
	if docs := f.refundDocs(t, rc); len(docs) != 0 {
		t.Errorf("documents left after the unassign: %+v", docs)
	}
	if got, want := f.orderLink(t, inv2.ID), f.orderOf(t, charge2); got != want {
		t.Errorf("faktura order link = %s, want the original %s", got, want)
	}
	// A later refund of the same charge finds its fakturas again.
	f.refundTxn("txn_test_ur_later", 200, 4500, "txn_test_ur_charge2", nil)
	f.sync(t)
	if res := f.book(t); res.RefundsBooked < 1 || len(res.Errors) != 0 {
		t.Fatalf("later refund: %+v", res)
	}
	if docs := f.refundDocs(t, f.rowOf(t, "txn_test_ur_later")); len(docs) != 1 || docs[0].invoiceID != inv2.ID {
		t.Errorf("later refund documents = %+v, want one for the faktura", docs)
	}
}

func isRefusal(err error, code string) bool {
	var r *StripeTxnRefusal
	return errors.As(err, &r) && r.Code == code
}

// Rows stored before refunds were booked are migrated: refunds and
// disputes held as refund_pending_bry446 become refund_legacy (a dismissed
// one keeps its state), other adjustments unsupported_category, and refund
// failures stored as other adjustments get their category.
func TestRefundLegacyMigration(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	refund := f.insertBT(t, stripeBT{id: "txn_test_lg_refund", category: "refund", amountOre: -100,
		status: StripeTxnNeedsReview, reason: StripeReviewDisputePending})
	dispute := f.insertBT(t, stripeBT{id: "txn_test_lg_dispute", category: "dispute", amountOre: -100,
		status: StripeTxnNeedsReview, reason: StripeReviewDisputePending})
	dismissed := f.insertBT(t, stripeBT{id: "txn_test_lg_dismissed", category: "refund", amountOre: -100,
		status: StripeTxnDismissed, reason: StripeReviewDisputePending})
	adj := f.insertBT(t, stripeBT{id: "txn_test_lg_adj", category: "other_adjustment", amountOre: -100,
		status: StripeTxnNeedsReview, reason: StripeReviewDisputePending})
	failure := f.insertBT(t, stripeBT{id: "txn_test_lg_fail", category: "other_adjustment", stripeTyp: "refund_failure",
		amountOre: 100, status: StripeTxnNeedsReview, reason: StripeReviewUnsupportedCategory})

	sql, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), "000119_stripe_refunds.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.club.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("re-running the migration: %v", err)
	}
	want := map[string]storedBT{
		refund:    {status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy},
		dispute:   {status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy},
		dismissed: {status: StripeTxnDismissed, reason: StripeReviewRefundLegacy},
		adj:       {status: StripeTxnNeedsReview, reason: StripeReviewUnsupportedCategory},
		failure:   {status: StripeTxnNeedsReview, reason: StripeReviewRefundLegacy},
	}
	for id, w := range want {
		if got := f.bt(t, id); got != w {
			t.Errorf("row %s = %+v, want %+v", id, got, w)
		}
	}
	var category string
	if err := f.club.Pool.QueryRow(ctx, `SELECT reporting_category FROM stripe_balance_transactions WHERE id = $1`, failure).Scan(&category); err != nil || category != "refund_failure" {
		t.Errorf("failure category = %q (%v)", category, err)
	}
}

// Fakturas created at once by several paths (the booking pass issuing a
// credit note, a treasurer creating fakturas) get distinct numbers, on a
// pool so small that a numbering path waiting for a second connection
// under its lock would hang.
func TestInvoiceNumberingConcurrent(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(2))
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &stripeFixture{svc: svc, club: club, src: newFakeStripeBalance(), now: time.Now().Unix()}
	inv := f.twoLineInvoice(t)
	f.passBookedCharge(t, "txn_test_num_charge", inv)
	f.refundTxn("txn_test_num_refund", 300, 45000, "txn_test_num_charge", nil)
	f.sync(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := pool.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback(ctx)
			if _, err := CreateInvoiceTx(ctx, tx, club.ID, InvoiceDraft{
				DueDate: time.Now(), MemberName: "Synthetic",
				Lines: []InvoiceDraftLine{{Description: fmt.Sprintf("Synthetic %d", i), Quantity: 1, UnitPrice: 10}},
			}); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit(ctx)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := svc.BookStripeBalance(ctx, club.ID, club.Treasurer.ID)
		if err == nil && res.RefundsBooked != 1 {
			err = fmt.Errorf("booking pass = %+v", res)
		}
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n, distinct int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), count(DISTINCT invoice_number) FROM invoices WHERE club_id = $1`, club.ID,
	).Scan(&n, &distinct); err != nil {
		t.Fatal(err)
	}
	if n != 8 || distinct != n {
		t.Errorf("%d fakturas with %d distinct numbers, want 8 distinct", n, distinct)
	}
}

// The booking pass, an unassign and the payout sweep run at once on a
// small pool: none deadlocks, and the refund books once.
func TestRefundPassConcurrentWithUnassignAndSweep(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(2))
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &stripeFixture{svc: svc, club: club, src: newFakeStripeBalance(), now: time.Now().Unix()}
	inv := f.twoLineInvoice(t)
	f.passBookedCharge(t, "txn_test_cc_charge", inv)
	other := f.twoLineInvoice(t)
	otherCharge, _ := f.passBookedCharge(t, "txn_test_cc_other", other)
	f.refundTxn("txn_test_cc_refund", 300, 9000, "txn_test_cc_charge", nil)
	f.sync(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	wg.Add(3)
	go func() {
		defer wg.Done()
		_, err := svc.BookStripeBalance(ctx, club.ID, club.Treasurer.ID)
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := svc.UnassignStripeTxn(ctx, club.ID, club.Treasurer.ID, otherCharge)
		if errors.Is(err, ErrStripeSyncBusy) {
			err = nil
		}
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := svc.ClearStripePayouts(ctx, club.ID, club.Treasurer.ID)
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if res := f.book(t); res.RefundsBooked > 1 {
		t.Errorf("refund booked again: %+v", res)
	}
	if docs := f.refundDocs(t, f.rowOf(t, "txn_test_cc_refund")); len(docs) != 1 {
		t.Errorf("documents = %+v, want one credit note", docs)
	}
}

// orderLink is the order a faktura is linked to ("" for none).
func (f *stripeFixture) orderLink(t *testing.T, invoiceID string) string {
	t.Helper()
	var o *string
	err := f.club.Pool.QueryRow(context.Background(),
		`SELECT order_id::text FROM order_sources WHERE invoice_id = $1`, invoiceID).Scan(&o)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	if o == nil {
		return ""
	}
	return *o
}

// A refund made in the Dashboard of a charge no attempt claims, which a
// person assigned to a faktura, credits that faktura: the refund has no
// request to match and the charge no attempt, so no payment_refunds row is
// made, and the order (none) is left alone.
func TestRefundOfFakturaAssignedByHand(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.twoLineInvoice(t)
	f.charge("txn_test_hand_charge", 600, 45000, "nok", nil, "")
	f.sync(t)
	charge := f.rowOf(t, "txn_test_hand_charge")
	if _, err := f.svc.AssignStripeTxnToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, charge, inv.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	f.refundTxn("txn_test_hand_refund", 300, 9000, "txn_test_hand_charge", nil)
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	refund := f.rowOf(t, "txn_test_hand_refund")
	docs := f.refundDocs(t, refund)
	if len(docs) != 1 || docs[0].kind != "credit" || docs[0].invoiceID != inv.ID || docs[0].amountOre != 9000 {
		t.Fatalf("documents = %+v, want one credit note for the faktura", docs)
	}
	var requests int
	if err := f.club.Pool.QueryRow(ctx, `SELECT count(*) FROM payment_refunds WHERE club_id = $1`, f.club.ID).Scan(&requests); err != nil || requests != 0 {
		t.Errorf("payment_refunds = %d (%v), want none for a charge with no attempt", requests, err)
	}
	if s := f.invoice(t, inv.ID); !s.paid || s.credited {
		t.Errorf("faktura = %+v, want paid, not yet credited", s)
	}

	// The rest completes it: the faktura reads credited.
	f.refundTxn("txn_test_hand_refund2", 200, 36000, "txn_test_hand_charge", nil)
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 {
		t.Fatalf("second refund: %+v", res)
	}
	if s := f.invoice(t, inv.ID); !s.credited {
		t.Errorf("faktura = %+v, want credited", s)
	}
}

// Collecting again on a charge assigned to a faktura by hand reopens the
// faktura but touches only the link to the refunded order: the faktura's
// own pay-link order stays, and the attempt's order is not refunded.
func TestRefundRecollectOfHandAssignedChargeKeepsOtherOrder(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.twoLineInvoice(t)
	own := f.club.NewOrderForInvoice(t, inv)
	attemptID, attemptOrder := f.attempt(t, 500, "NOK")
	f.charge("txn_test_hr_charge", 600, 45000, "nok", map[string]string{"attempt_id": attemptID}, "pi_test_hr")
	f.sync(t)
	charge := f.rowOf(t, "txn_test_hr_charge")
	if row := f.bt(t, charge); row.reason != StripeReviewAmountMismatch {
		t.Fatalf("charge = %+v, want amount_mismatch", row)
	}
	if _, err := f.svc.AssignStripeTxnToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, charge, inv.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	key := f.requestRefund(t, attemptID, 45000, "recollect")
	f.refundTxn("txn_test_hr_refund", 300, 45000, "txn_test_hr_charge", map[string]string{"refund_request_key": key})
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	docs := f.refundDocs(t, f.rowOf(t, "txn_test_hr_refund"))
	if len(docs) != 1 || docs[0].kind != "recollect" || docs[0].detached != "" {
		t.Fatalf("documents = %+v, want a recollect that detached nothing", docs)
	}
	if got := f.orderLink(t, inv.ID); got != own.ID {
		t.Errorf("faktura order link = %s, want its own order %s", got, own.ID)
	}
	if st := f.orderStatus(t, attemptOrder); st == "refunded" {
		t.Error("the attempt's order was refunded for a charge assigned by hand")
	}
	if s := f.invoice(t, inv.ID); s.paid || !s.owed {
		t.Errorf("faktura = %+v, want open", s)
	}
}

// A refund held and then booked to an account by hand never counted in
// refunded_ore, so its failure (booked by the pass) neither lowers it nor,
// when unassigned, raises it.
func TestRefundFailureUnassignKeepsRefundedOre(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_fu_charge", inv)
	f.refundTxn("txn_test_fu_r1", 400, 5000, "txn_test_fu_charge", nil)
	f.sync(t)
	f.book(t)
	key := f.requestRefund(t, f.attemptOf(t, charge), 40000, "recollect")
	f.refundTxn("txn_test_fu_r2", 300, 40000, "txn_test_fu_charge", map[string]string{"refund_request_key": key})
	f.sync(t)
	if res := f.book(t); res.NeedsReview[StripeReviewRefundSplit] != 1 {
		t.Fatalf("result = %+v, want refund_split", res)
	}
	r2 := f.rowOf(t, "txn_test_fu_r2")
	if _, err := f.svc.AssignStripeTxnToAccount(ctx, f.club.ID, f.club.Treasurer.ID, r2, "3900", "Synthetic"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	f.refundFailure("txn_test_fu_fail", 200, 40000, "txn_test_fu_r2")
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("failure: %+v", res)
	}
	if ore, _ := f.refunded(t, charge); ore != 5000 {
		t.Fatalf("refunded_ore after the failure = %d, want 5000", ore)
	}
	if _, err := f.unassign(f.rowOf(t, "txn_test_fu_fail")); err != nil {
		t.Fatalf("unassign failure: %v", err)
	}
	if ore, _ := f.refunded(t, charge); ore != 5000 {
		t.Errorf("refunded_ore after unassigning the failure = %d, want 5000", ore)
	}
}
