package accounting

import (
	"context"
	"testing"
	"time"
)

func (f *stripeFixture) reissue(t *testing.T, invoiceID string) *ReissuedInvoice {
	t.Helper()
	today := time.Now()
	res, err := f.svc.ReissueAfterRefund(context.Background(), f.club.ID, invoiceID, today, today.AddDate(0, 0, 21))
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	return res
}

func (f *stripeFixture) invoiceCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM invoices WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A refund that credited a faktura fails after "Ny faktura" collected it
// again: a debit faktura for the returned money would bill the payer twice,
// so the failure waits for a person. When the new faktura was voided, the
// failure is booked as it is without one.
func TestRefundFailureAfterReissue(t *testing.T) {
	for _, voided := range []bool{false, true} {
		name := "new faktura stands"
		if voided {
			name = "new faktura voided"
		}
		t.Run(name, func(t *testing.T) {
			f := newStripeFixture(t)
			inv := f.twoLineInvoice(t)
			charge, _ := f.passBookedCharge(t, "txn_test_ra_charge", inv)
			f.refundTxn("txn_test_ra_refund", 400, 45000, "txn_test_ra_charge", nil)
			f.sync(t)
			f.book(t)
			_ = charge

			again := f.reissue(t, inv.ID)
			if voided {
				if _, err := f.club.Pool.Exec(context.Background(),
					`UPDATE invoices SET status = 'voided' WHERE id = $1`, again.ID); err != nil {
					t.Fatal(err)
				}
			}
			before := f.invoiceCount(t)

			f.refundFailure("txn_test_ra_fail", 300, 45000, "txn_test_ra_refund")
			f.sync(t)
			res := f.book(t)
			fail := f.bt(t, f.rowOf(t, "txn_test_ra_fail"))

			if voided {
				if res.RefundsBooked != 1 || fail.status != StripeTxnBooked {
					t.Fatalf("result = %+v, row %+v, want the failure booked", res, fail)
				}
				if docs := f.refundDocs(t, f.rowOf(t, "txn_test_ra_fail")); len(docs) != 1 || docs[0].kind != "refail_invoice" {
					t.Errorf("documents = %+v, want the debit faktura", docs)
				}
				return
			}
			if res.NeedsReview[StripeReviewRefundFailureReissued] != 1 || fail.status != StripeTxnNeedsReview ||
				fail.reason != StripeReviewRefundFailureReissued || fail.entry != "" {
				t.Fatalf("result = %+v, row %+v, want held as refund_failure_reissued with no entry", res, fail)
			}
			if docs := f.refundDocs(t, f.rowOf(t, "txn_test_ra_fail")); len(docs) != 0 {
				t.Errorf("documents = %+v, want none written", docs)
			}
			if n := f.invoiceCount(t); n != before {
				t.Errorf("fakturas = %d, want %d: no debit faktura for money billed again", n, before)
			}
			// Only the new faktura is owed; the credited one stays settled.
			if s := f.invoice(t, again.ID); !s.owed {
				t.Errorf("new faktura = %+v, want owed", s)
			}
			if s := f.invoice(t, inv.ID); s.owed {
				t.Errorf("credited faktura = %+v, want settled", s)
			}
			// A second pass leaves it held, not retried.
			if res := f.book(t); res.RefundsBooked != 0 {
				t.Errorf("second pass = %+v, want nothing booked", res)
			}
		})
	}
}

func TestReissueEligibleRule(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		total, credited, repl int64
		want                  bool
	}{
		{"credited in full", 45000, 45000, 0, true},
		{"credited in part", 45000, 9000, 0, false},
		{"not credited", 45000, 0, 0, false},
		{"credit note", -45000, 0, 0, false},
		// Credited, billed again by a failed refund, credited once more:
		// the net is the total, but the failed refund's re-billing makes it
		// unsafe, in the list and the endpoint alike.
		{"450 credited, 450 re-billed, 450 credited", 45000, 90000, 45000, false},
		{"credited in full, re-billed", 45000, 45000, 45000, false},
	} {
		if got := ReissueEligible(tc.total, tc.credited, tc.repl); got != tc.want {
			t.Errorf("%s: ReissueEligible = %v, want %v", tc.name, got, tc.want)
		}
	}
}
