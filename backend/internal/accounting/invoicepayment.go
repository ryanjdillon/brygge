package accounting

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// linkInvoicePayment creates a `payments` row for the supplied invoice
// and back-links it via `invoices.payment_id`. Called immediately
// after a bank-side KID (or invoice-number) auto-match posts a
// journal entry — without this back-link the GL is balanced but the
// invoice keeps reading as unpaid on the dashboard and faktura list.
// See DIL-363 for context.
//
// Behavior:
//   - Member invoices (invoices.user_id set) get a member payment.
//   - Organization / non-member invoices (user_id NULL) resolve-or-create
//     a billing_recipients row from the invoice's recipient snapshot and
//     get a recipient payment (payments.recipient_id) — so an org invoice
//     can be marked paid instead of sitting falsely overdue. See BRY-234.
//   - Skips invoices that already have a payment_id (no-op idempotent).
//   - Maps invoice.category onto payment_type using the categories that
//     overlap; everything else falls through to `dues` which is the
//     least-surprising default for membership-style billing.
func (s *Service) linkInvoicePayment(ctx context.Context, clubID, invoiceID string, paidAt time.Time) error {
	var (
		userID         *string
		amount         float64
		category       *string
		recipientKind  string
		orgName        *string
		orgNumber      *string
		orgAddress     *string
		contactPerson  *string
		recipientEmail *string
		memberName     *string
	)
	err := s.db.QueryRow(ctx,
		`SELECT user_id, total_amount, category,
		        COALESCE(recipient_kind, 'private'),
		        recipient_org_name, recipient_org_number, recipient_org_address,
		        recipient_contact_person, recipient_email, member_name
		   FROM invoices
		  WHERE id = $1 AND club_id = $2 AND payment_id IS NULL`,
		invoiceID, clubID,
	).Scan(&userID, &amount, &category, &recipientKind,
		&orgName, &orgNumber, &orgAddress, &contactPerson, &recipientEmail, &memberName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	// Map price_items.category → payment_type enum. Categories without a
	// direct enum match fall back to the closest semantic equivalent.
	paymentType := "dues"
	if category != nil {
		switch *category {
		case "harbor_membership":
			paymentType = "harbor_membership"
		case "slip_fee":
			paymentType = "slip_fee"
		case "guest", "seasonal_rental", "motorhome", "room_hire":
			paymentType = "booking"
		case "service", "electricity", "other":
			paymentType = "merchandise"
		}
	}

	// Resolve the payer side: a member (user_id) or an external billing
	// recipient (org / non-member). Exactly one is set on the payment row.
	var recipientID *string
	if userID == nil {
		rid, rerr := s.resolveBillingRecipient(ctx, clubID, recipientKind,
			orgName, orgNumber, orgAddress, contactPerson, recipientEmail, memberName)
		if rerr != nil {
			return rerr
		}
		recipientID = &rid
	}

	var paymentID string
	if err := s.db.QueryRow(ctx,
		`INSERT INTO payments
		   (club_id, user_id, recipient_id, type, amount, status, paid_at, description)
		 VALUES ($1, $2, $3, $4::payment_type, $5, 'completed', $6, 'KID-avstemming')
		 RETURNING id`,
		clubID, userID, recipientID, paymentType, amount, paidAt,
	).Scan(&paymentID); err != nil {
		return err
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE invoices SET payment_id = $1, recipient_id = COALESCE(recipient_id, $4)
		  WHERE id = $2 AND club_id = $3 AND payment_id IS NULL`,
		paymentID, invoiceID, clubID, recipientID,
	); err != nil {
		return err
	}
	return nil
}

// MarkInvoicePaidFromVipps marks an invoice paid from an inbound Vipps
// ePayment webhook: it creates the payments back-link (idempotent) so the
// invoice reads as paid, but posts NO journal entry. For Vipps the GL posting
// is owned by the settlement/clearing reconciliation path (BRY-256); posting
// here as well would double-count the same money.
func (s *Service) MarkInvoicePaidFromVipps(ctx context.Context, clubID, invoiceID string, paidAt time.Time) error {
	return s.linkInvoicePayment(ctx, clubID, invoiceID, paidAt)
}

// resolveBillingRecipient returns the id of the billing_recipients row for an
// organization / non-member invoice, creating it from the invoice snapshot if
// none exists yet. Orgs are keyed on (club, org_number|org_name);
// private non-members on (club, email|name) — so repeat invoices to the same
// payer reuse one recipient. See BRY-234.
func (s *Service) resolveBillingRecipient(
	ctx context.Context, clubID, recipientKind string,
	orgName, orgNumber, orgAddress, contactPerson, email, memberName *string,
) (string, error) {
	kind := "organization"
	if recipientKind != "organization" {
		kind = "private_nonmember"
	}

	// For a private non-member the person's name lives in member_name (the
	// invoice snapshot); store it as the contact_person of the recipient.
	if kind == "private_nonmember" && (contactPerson == nil || *contactPerson == "") {
		contactPerson = memberName
	}

	// Dedup key: the strongest stable identifier we have for this payer.
	var key *string
	switch kind {
	case "organization":
		key = firstNonEmptyPtr(orgNumber, orgName)
	default:
		key = firstNonEmptyPtr(email, contactPerson)
	}

	if key != nil {
		var existing string
		err := s.db.QueryRow(ctx,
			`SELECT id FROM billing_recipients
			  WHERE club_id = $1 AND kind = $2
			    AND COALESCE(NULLIF(org_number, ''), NULLIF(org_name, ''),
			                 NULLIF(email, ''), NULLIF(contact_person, '')) = $3
			  LIMIT 1`,
			clubID, kind, *key,
		).Scan(&existing)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}

	var id string
	if err := s.db.QueryRow(ctx,
		`INSERT INTO billing_recipients
		   (club_id, kind, org_name, org_number, org_address, contact_person, email)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		clubID, kind, orgName, orgNumber, orgAddress, contactPerson, email,
	).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// firstNonEmpty returns the first non-nil, non-blank string pointer.
func firstNonEmptyPtr(vals ...*string) *string {
	for _, v := range vals {
		if v != nil && *v != "" {
			return v
		}
	}
	return nil
}
