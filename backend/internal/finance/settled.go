package finance

import (
	"fmt"
)

// An invoice is "settled" in two different senses, and every caller must
// pick the right one rather than writing its own payment_id check:
//
//   - Settled for the member: the member owes nothing more. True once the
//     bank/Vipps reconciliation has linked a payment (payment_id), as soon as
//     the Vipps webhook has captured a pay-link payment (vipps_paid_at — the
//     settlement that sets payment_id lags by days), when the invoice is
//     voided, or when a live credit note credits it (credits_invoice_id).
//     Reminders, KID reissue, the member payables page and checkout totals
//     use this, so a member who just paid is never chased.
//   - Settled for the GL: a payment is linked (payment_id). Bookkeeping and
//     reconciliation use this, because vipps_paid_at alone has not cleared
//     the receivable yet.
//
// A credit note (negative total) is never a debt: it is money the club owes
// back, so "is the member chased for this?" is OwedByMemberSQL, which also
// requires the invoice to be a debt.

// SettledForMemberSQL returns a SQL predicate that is true when the member
// owes nothing on the invoice. alias is the invoices table alias ("" for an
// unaliased invoices table).
func SettledForMemberSQL(alias string) string {
	p := col(alias)
	return fmt.Sprintf("(%[1]spayment_id IS NOT NULL OR %[1]svipps_paid_at IS NOT NULL OR %[1]sstatus = 'voided' OR %[2]s)",
		p, CreditedSQL(alias))
}

// CreditedSQL returns a SQL predicate that is true when a live (non-voided)
// credit note credits the invoice.
func CreditedSQL(alias string) string {
	return fmt.Sprintf("EXISTS (SELECT 1 FROM invoices cn WHERE cn.credits_invoice_id = %sid AND cn.status <> 'voided')", ref(alias))
}

// IsDebtSQL returns a SQL predicate that is true for an invoice the member
// can owe money on, i.e. not a credit note.
func IsDebtSQL(alias string) string {
	return fmt.Sprintf("(%stotal_amount > 0)", col(alias))
}

// OwedByMemberSQL returns a SQL predicate that is true when the member still
// owes money on the invoice: it is a debt and not settled for the member.
// Overdue lists, reminders and the payables page use this.
func OwedByMemberSQL(alias string) string {
	return fmt.Sprintf("(%s AND NOT %s)", IsDebtSQL(alias), SettledForMemberSQL(alias))
}

// SettledForGLSQL returns a SQL predicate that is true when a payment has
// been linked to the invoice, i.e. the receivable is cleared.
func SettledForGLSQL(alias string) string {
	return fmt.Sprintf("(%spayment_id IS NOT NULL)", col(alias))
}

func col(alias string) string {
	if alias == "" {
		return ""
	}
	return alias + "."
}

// ref qualifies a column for use inside a correlated subquery, where an
// unaliased outer table must be named explicitly.
func ref(alias string) string {
	if alias == "" {
		return "invoices."
	}
	return alias + "."
}
