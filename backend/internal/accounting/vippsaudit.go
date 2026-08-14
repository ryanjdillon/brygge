package accounting

import (
	"context"
	"sort"
	"time"
)

// UnlinkedVippsPayment is a Vipps belastning charge that looks like it paid a
// specific member's still-open, unpaid faktura that was never marked paid —
// the pre-purring safety net (BRY-223). It surfaces when the Vipps customer
// resolves to a member (via the same cascade the reconciler trusts) and that
// member has exactly one open invoice matching the payment amount.
//
// AlreadyReconciled distinguishes the two ways a payment goes unlinked:
//   - false: the charge is still unreconciled (journal_entry_id IS NULL) and
//     is also visible in the Tildel queue — assign it there.
//   - true: the charge was already booked (to revenue/fallback, because the
//     name didn't resolve at import time) yet the faktura is still unpaid — a
//     CONFIRMED miss that never appears in Tildel. These are the dangerous
//     ones: the member will wrongly receive a purring.
type UnlinkedVippsPayment struct {
	VippsRowID        string
	CustomerName      string
	Message           string
	Amount            float64
	BookingDate       time.Time
	SettlementNumber  string
	AlreadyReconciled bool

	InvoiceID     string
	InvoiceNumber int
	InvoiceAmount float64
	IssueDate     time.Time

	MemberID   string
	MemberName string
}

// AuditUnlinkedVippsPayments scans every Vipps belastning charge for the club
// and returns those that look like a paid-but-unmarked faktura. Read-only: it
// writes nothing, resolving each charge with resolveCustomerToMember +
// findOpenInvoiceForMember (the exact logic the reconciler uses), so it is
// safe to run before sending purringer. Confirmed misses (AlreadyReconciled)
// are returned first.
//
// Cost is O(charges × members) DB round-trips because it reuses the per-charge
// resolver; acceptable for an occasional single-club audit, and worth more
// than a bespoke query that could drift from the reconciler's matching rules.
func (s *Service) AuditUnlinkedVippsPayments(ctx context.Context, clubID string) ([]UnlinkedVippsPayment, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id,
		        COALESCE(customer_name, ''),
		        COALESCE(message, ''),
		        amount,
		        COALESCE(booking_date, tx_at::date, CURRENT_DATE),
		        COALESCE(settlement_number, ''),
		        journal_entry_id IS NOT NULL
		   FROM vipps_import_rows
		  WHERE club_id = $1
		    AND row_type = $2
		    AND dismissed_at IS NULL
		    AND amount > 0`,
		clubID, string(VippsRowBelastning),
	)
	if err != nil {
		return nil, err
	}

	type charge struct {
		id, customerName, message, settlement string
		amount                                float64
		bookingDate                           time.Time
		reconciled                            bool
	}
	// Drain the cursor before resolving, so we do not hold it open across the
	// per-charge lookups (which each issue their own queries).
	var charges []charge
	for rows.Next() {
		var c charge
		if err := rows.Scan(&c.id, &c.customerName, &c.message, &c.amount, &c.bookingDate, &c.settlement, &c.reconciled); err != nil {
			rows.Close()
			return nil, err
		}
		charges = append(charges, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []UnlinkedVippsPayment
	for _, c := range charges {
		memberID, err := s.resolveCustomerToMember(ctx, clubID, c.customerName, c.message)
		if err != nil {
			return nil, err
		}
		if memberID == "" {
			continue
		}
		invoiceID := s.findOpenInvoiceForMember(ctx, clubID, memberID, c.amount)
		if invoiceID == "" {
			continue
		}

		var (
			invNo  int
			invAmt float64
			issued time.Time
			member string
		)
		if err := s.db.QueryRow(ctx,
			`SELECT i.invoice_number, i.total_amount, i.issue_date,
			        COALESCE(NULLIF(u.full_name, ''),
			                 NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''),
			                 '')
			   FROM invoices i
			   JOIN users u ON u.id = i.user_id
			  WHERE i.id = $1`,
			invoiceID,
		).Scan(&invNo, &invAmt, &issued, &member); err != nil {
			return nil, err
		}

		out = append(out, UnlinkedVippsPayment{
			VippsRowID:        c.id,
			CustomerName:      c.customerName,
			Message:           c.message,
			Amount:            c.amount,
			BookingDate:       c.bookingDate,
			SettlementNumber:  c.settlement,
			AlreadyReconciled: c.reconciled,
			InvoiceID:         invoiceID,
			InvoiceNumber:     invNo,
			InvoiceAmount:     invAmt,
			IssueDate:         issued,
			MemberID:          memberID,
			MemberName:        member,
		})
	}

	// Confirmed misses (already booked to the wrong account, invisible in the
	// Tildel queue) first — those are the ones that will wrongly trigger a
	// purring; then most-recent charge first.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AlreadyReconciled != out[j].AlreadyReconciled {
			return out[i].AlreadyReconciled
		}
		return out[i].BookingDate.After(out[j].BookingDate)
	})
	return out, nil
}
