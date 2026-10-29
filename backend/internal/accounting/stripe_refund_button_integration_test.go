package accounting

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/payments"
)

// buttonRefunder is the refund provider the button reaches: it accepts a
// refund and names it as the Stripe balance lists it (re_<balance txn id>).
type buttonRefunder struct{ refundID string }

func (buttonRefunder) Name() string { return "stripe" }
func (buttonRefunder) CreatePayment(context.Context, payments.Request) (payments.Result, error) {
	return payments.Result{}, nil
}
func (b buttonRefunder) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	return payments.RefundResult{ProviderRefundID: b.refundID, Status: payments.RefundSucceeded, PaymentRef: "pi_button", ChargeRef: "ch_button"}, nil
}
func (buttonRefunder) FindRefund(context.Context, string, string) (payments.RefundResult, bool, error) {
	return payments.RefundResult{}, false, nil
}

// requestViaButton runs the refund button's service for the charge's attempt.
func (f *stripeFixture) requestViaButton(t *testing.T, charge, refundBT string, amountOre int64, outcome string) (key string) {
	t.Helper()
	reg := payments.NewRegistry(buttonRefunder{refundID: "re_" + refundBT})
	svc := payments.NewRefundService(f.club.Pool, reg, zerolog.Nop())
	key = uuid.NewString()
	out, err := svc.Request(context.Background(), payments.RefundInput{
		ClubID: f.club.ID, OrderID: f.orderOf(t, charge), AttemptID: f.attemptOf(t, charge), AmountOre: amountOre,
		Reason: "Synthetic button refund", Outcome: outcome, RequestKey: key, ActorID: f.club.Treasurer.ID,
	})
	if err != nil || out.Refund.Status != payments.RefundSucceeded {
		t.Fatalf("button refund: %+v, %v", out, err)
	}
	return key
}

// paymentRefundRows are the refunds of the club as the button and the pass
// left them: one row each, never a second made for the same refund.
func (f *stripeFixture) paymentRefundRows(t *testing.T) []struct {
	providerID, reason, outcome string
	byPerson                    bool
} {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT COALESCE(provider_refund_id, ''), reason, COALESCE(outcome, ''), created_by IS NOT NULL
		   FROM payment_refunds WHERE club_id = $1 ORDER BY created_at`, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []struct {
		providerID, reason, outcome string
		byPerson                    bool
	}
	for rows.Next() {
		var r struct {
			providerID, reason, outcome string
			byPerson                    bool
		}
		if err := rows.Scan(&r.providerID, &r.reason, &r.outcome, &r.byPerson); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// A full refund started with the button and "Krev inn på nytt": the booking
// pass finds the button's row by the request key Stripe carries on the
// refund, honours its outcome (no credit note, the faktura reopens) and does
// not make a second row for it.
func TestRefundButtonRecollectBookedWithChosenOutcome(t *testing.T) {
	f := newStripeFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_bb_charge", inv)
	key := f.requestViaButton(t, charge, "txn_test_bb_refund", 45000, payments.RefundOutcomeRecollect)

	f.refundTxn("txn_test_bb_refund", 300, 45000, "txn_test_bb_charge", map[string]string{"refund_request_key": key})
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}

	docs := f.refundDocs(t, f.rowOf(t, "txn_test_bb_refund"))
	if len(docs) != 1 || docs[0].kind != "recollect" || docs[0].creditInvoiceID != "" {
		t.Fatalf("documents = %+v, want one recollect and no credit note", docs)
	}
	if s := f.invoice(t, inv.ID); s.paid || s.credited || !s.owed {
		t.Errorf("faktura = %+v, want an ordinary open faktura", s)
	}
	rows := f.paymentRefundRows(t)
	if len(rows) != 1 || rows[0].providerID != "re_txn_test_bb_refund" || rows[0].outcome != "recollect" ||
		rows[0].reason != "Synthetic button refund" || !rows[0].byPerson {
		t.Errorf("payment_refunds = %+v, want the button's row, matched and untouched", rows)
	}
}

// A partial refund started with the button is booked as a credit note for
// its share, and the refund's outcome stays unset (credit).
func TestRefundButtonPartialBookedAsCreditNote(t *testing.T) {
	f := newStripeFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_bp_charge", inv)
	key := f.requestViaButton(t, charge, "txn_test_bp_refund", 9000, "")

	f.refundTxn("txn_test_bp_refund", 300, 9000, "txn_test_bp_charge", map[string]string{"refund_request_key": key})
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	docs := f.refundDocs(t, f.rowOf(t, "txn_test_bp_refund"))
	if len(docs) != 1 || docs[0].kind != "credit" || docs[0].invoiceID != inv.ID || docs[0].amountOre != 9000 {
		t.Fatalf("documents = %+v, want one 90 kr credit note", docs)
	}
	if rows := f.paymentRefundRows(t); len(rows) != 1 || rows[0].outcome != "" || !rows[0].byPerson {
		t.Errorf("payment_refunds = %+v", rows)
	}
}

// A full refund with "Krediter" completes the payment: credit note, order
// refunded, and the button offers nothing more.
func TestRefundButtonFullCreditBooked(t *testing.T) {
	f := newStripeFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_bf_charge", inv)
	order := f.orderOf(t, charge)
	key := f.requestViaButton(t, charge, "txn_test_bf_refund", 45000, payments.RefundOutcomeCredit)

	f.refundTxn("txn_test_bf_refund", 300, 45000, "txn_test_bf_charge", map[string]string{"refund_request_key": key})
	f.sync(t)
	if res := f.book(t); res.RefundsBooked != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	docs := f.refundDocs(t, f.rowOf(t, "txn_test_bf_refund"))
	if len(docs) != 1 || docs[0].kind != "credit" || docs[0].amountOre != 45000 {
		t.Fatalf("documents = %+v, want one 450 kr credit note", docs)
	}
	if st := f.orderStatus(t, order); st != "refunded" {
		t.Errorf("order = %s, want refunded", st)
	}
	svc := payments.NewRefundService(f.club.Pool, payments.NewRegistry(buttonRefunder{}), zerolog.Nop())
	refundable, err := svc.RefundableOrders(context.Background(), f.club.ID, []string{order})
	if err != nil || refundable[order] {
		t.Errorf("refundable after a full refund = %v, %v", refundable, err)
	}
}
