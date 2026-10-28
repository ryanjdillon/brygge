package accounting

import (
	"slices"
	"testing"
)

func TestStripeAllowedActions(t *testing.T) {
	const (
		inv  = StripeActionAssignInvoice
		acct = StripeActionAssignAccount
		dis  = StripeActionDismiss
		und  = StripeActionUndismiss
	)
	review := func(category, reason string) stripeTxnState {
		return stripeTxnState{category: category, status: StripeTxnNeedsReview, reviewReason: reason, currency: "nok"}
	}
	eur := review("charge", StripeReviewNoAttempt)
	eur.currency = "eur"
	booked := review("charge", StripeReviewNoAttempt)
	booked.booked = true
	dismissed := stripeTxnState{category: "charge", status: StripeTxnDismissed, reviewReason: StripeReviewNoAttempt, currency: "nok"}
	dismissedCleared := dismissed
	dismissedCleared.payoutCleared = true
	reviewCleared := review("charge", StripeReviewNoAttempt)
	reviewCleared.payoutCleared = true

	tests := []struct {
		name string
		st   stripeTxnState
		want []string
	}{
		{"no attempt", review("charge", StripeReviewNoAttempt), []string{inv, acct, dis}},
		{"amount mismatch", review("charge", StripeReviewAmountMismatch), []string{inv, acct, dis}},
		{"split mismatch", review("charge", StripeReviewSplitMismatch), []string{inv, acct, dis}},
		{"receivable not open", review("charge", StripeReviewReceivableNotOpen), []string{acct, dis}},
		{"imported without opening balance", review("charge", StripeReviewImportedNoOpeningBalance), []string{acct, dis}},
		{"currency reason", review("charge", StripeReviewCurrency), []string{dis}},
		{"other currency", eur, []string{dis}},
		{"refund", review("refund", StripeReviewRefundPending), []string{dis}},
		{"dispute", review("dispute", StripeReviewRefundPending), []string{dis}},
		{"unsupported category", review("other_adjustment", StripeReviewUnsupportedCategory), []string{acct, dis}},
		{"fee in review", review("fee", StripeReviewNoAttempt), []string{}},
		{"unbooked charge", stripeTxnState{category: "charge", status: StripeTxnUnbooked, currency: "nok"}, []string{}},
		{"booked", booked, []string{}},
		{"dismissed", dismissed, []string{und}},
		{"dismissed, payout cleared", dismissedCleared, []string{}},
		{"in review, payout cleared", reviewCleared, []string{dis}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripeAllowedActions(tt.st); !slices.Equal(got, tt.want) {
				t.Errorf("stripeAllowedActions = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplitStripeDismissal(t *testing.T) {
	tests := []struct{ stored, reason, note string }{
		{"duplicate", "duplicate", ""},
		{"booked_manually: bilag 12: rettet", "booked_manually", "bilag 12: rettet"},
	}
	for _, tt := range tests {
		if r, n := splitStripeDismissal(tt.stored); r != tt.reason || n != tt.note {
			t.Errorf("splitStripeDismissal(%q) = %q, %q; want %q, %q", tt.stored, r, n, tt.reason, tt.note)
		}
	}
}

func TestIsValidStripeDismissReason(t *testing.T) {
	for _, r := range []string{"booked_manually", "test_transaction", "unidentifiable", "refund_or_credit", "duplicate"} {
		if !IsValidStripeDismissReason(r) {
			t.Errorf("%s refused", r)
		}
	}
	for _, r := range []string{"", "internal_transfer", "bounced", "superseded"} {
		if IsValidStripeDismissReason(r) {
			t.Errorf("%q accepted", r)
		}
	}
}
