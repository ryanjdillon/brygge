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

// OpenReceivableSQL returns a SQL predicate that is true when the invoice's
// receivable can still be cleared by a payment: a debt that is not voided,
// not credited and has no payment linked. Unlike OwedByMemberSQL it ignores
// vipps_paid_at, because the Vipps charge that set it is exactly the
// payment that clears the receivable (ePayment order reconciliation).
// Automatic matching of *other* money (bank KID, invoice number, payer name)
// must use OwedByMemberSQL instead, so a second payment for a faktura already
// captured by Vipps is left for a human rather than booked twice.
func OpenReceivableSQL(alias string) string {
	p := col(alias)
	return fmt.Sprintf("(%s AND %sstatus <> 'voided' AND %spayment_id IS NULL AND NOT %s)",
		IsDebtSQL(alias), p, p, CreditedSQL(alias))
}

// SettledForGLSQL returns a SQL predicate that is true when a payment has
// been linked to the invoice, i.e. the receivable is cleared.
func SettledForGLSQL(alias string) string {
	return fmt.Sprintf("(%spayment_id IS NOT NULL)", col(alias))
}

// ImportedReceivableOpenSQL returns a SQL predicate that is true when the
// invoice's receivable exists in Brygge's GL, so a payment or credit note may
// credit 1500 for it (BRY-386). A Brygge faktura always qualifies. An
// imported one (import_source set) was never booked by Brygge, so it
// qualifies only when either
//
//   - a posted opening balance (source 'opening_balance') in the faktura's
//     club debits 1500 and is dated on or after the faktura's issue date (it
//     can only carry receivables that existed by then) and on or before
//     bookingDateExpr, the date the payment or credit note is booked; or
//   - the faktura has its own posted invoice_sync entry.
//
// bookingDateExpr is a SQL date expression such as "$3::date",
// "CURRENT_DATE" or a subquery. The account code is the receivables account
// (accounting's receivablesAccountCode); finance can't import accounting.
func ImportedReceivableOpenSQL(invAlias, bookingDateExpr string) string {
	r := ref(invAlias)
	return fmt.Sprintf(`(%[1]simport_source IS NULL
	 OR EXISTS (SELECT 1 FROM journal_entries ob_je
	              JOIN journal_lines ob_jl ON ob_jl.journal_entry_id = ob_je.id
	              JOIN accounts ob_a ON ob_a.id = ob_jl.account_id
	             WHERE ob_je.club_id = %[1]sclub_id AND ob_je.source = 'opening_balance'
	               AND ob_je.status = 'posted' AND ob_a.code = '1500' AND ob_jl.debit > 0
	               AND ob_je.entry_date >= %[1]sissue_date AND ob_je.entry_date <= (%[2]s))
	 OR EXISTS (SELECT 1 FROM journal_entries is_je
	             WHERE is_je.club_id = %[1]sclub_id AND is_je.source = 'invoice_sync'
	               AND is_je.source_id = %[1]sid::text AND is_je.status = 'posted'))`,
		r, bookingDateExpr)
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
