package finance

import (
	"fmt"
	"time"
)

// An invoice is "settled" in two different senses, and every caller must
// pick the right one rather than writing its own payment_id check:
//
//   - Settled for the member: the member owes nothing more. True once the
//     bank/Vipps reconciliation has linked a payment (payment_id), as soon as
//     the Vipps webhook has captured a pay-link payment (vipps_paid_at — the
//     settlement that sets payment_id lags by days), or when the invoice is
//     voided. Reminders, KID reissue, the member payables page and checkout
//     totals use this, so a member who just paid is never chased.
//   - Settled for the GL: a payment is linked (payment_id). Bookkeeping and
//     reconciliation use this, because vipps_paid_at alone has not cleared
//     the receivable yet.

// SettledForMemberSQL returns a SQL predicate that is true when the member
// owes nothing on the invoice. alias is the invoices table alias ("" for
// none).
func SettledForMemberSQL(alias string) string {
	p := col(alias)
	return fmt.Sprintf("(%[1]spayment_id IS NOT NULL OR %[1]svipps_paid_at IS NOT NULL OR %[1]sstatus = 'voided')", p)
}

// SettledForGLSQL returns a SQL predicate that is true when a payment has
// been linked to the invoice, i.e. the receivable is cleared.
func SettledForGLSQL(alias string) string {
	return fmt.Sprintf("(%spayment_id IS NOT NULL)", col(alias))
}

// SettledForMember is the Go form of SettledForMemberSQL for rows already
// loaded.
func SettledForMember(status string, paymentID *string, vippsPaidAt *time.Time) bool {
	return paymentID != nil || vippsPaidAt != nil || status == "voided"
}

func col(alias string) string {
	if alias == "" {
		return ""
	}
	return alias + "."
}
