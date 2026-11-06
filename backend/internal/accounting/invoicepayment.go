package accounting

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/membership"
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
	return linkInvoicePaymentIn(ctx, s.db, clubID, invoiceID, paidAt)
}

// txBeginner is a pool, or a transaction that opens a savepoint.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// errLinkRaced is a back-link that found the faktura already linked when it
// came to write: its payment row is rolled back and the link is a no-op.
var errLinkRaced = errors.New("faktura linked by another payment meanwhile")

// linkInvoicePaymentIn is linkInvoicePayment on q, for a transaction that
// holds row locks and must not touch the pool. The payment row, the back-link
// and the membership grant a join faktura carries are written in one
// transaction (a savepoint when q already is one), which locks the faktura
// first: of two concurrent links one writes and the other finds it linked, so
// no payment row is left without a faktura. The grant runs in a savepoint of
// its own and never fails the link (see membership.GrantForInvoiceInSavepoint).
func linkInvoicePaymentIn(ctx context.Context, q dbtx, clubID, invoiceID string, paidAt time.Time) error {
	b, ok := q.(txBeginner)
	if !ok {
		err := linkInvoicePaymentTx(ctx, q, clubID, invoiceID, paidAt)
		if errors.Is(err, errLinkRaced) {
			return nil
		}
		return err
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := linkInvoicePaymentTx(ctx, tx, clubID, invoiceID, paidAt); err != nil {
		if errors.Is(err, errLinkRaced) {
			return nil
		}
		return err
	}
	return tx.Commit(ctx)
}

func linkInvoicePaymentTx(ctx context.Context, q dbtx, clubID, invoiceID string, paidAt time.Time) error {
	var (
		purpose        *string
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
	err := q.QueryRow(ctx,
		`SELECT purpose, user_id, total_amount, category,
		        COALESCE(recipient_kind, 'private'),
		        recipient_org_name, recipient_org_number, recipient_org_address,
		        recipient_contact_person, recipient_email, member_name
		   FROM invoices
		  WHERE id = $1 AND club_id = $2 AND payment_id IS NULL
		  FOR UPDATE`,
		invoiceID, clubID,
	).Scan(&purpose, &userID, &amount, &category, &recipientKind,
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
		rid, rerr := resolveBillingRecipient(ctx, q, clubID, recipientKind,
			orgName, orgNumber, orgAddress, contactPerson, recipientEmail, memberName)
		if rerr != nil {
			return rerr
		}
		recipientID = &rid
	}

	var paymentID string
	if err := q.QueryRow(ctx,
		`INSERT INTO payments
		   (club_id, user_id, recipient_id, type, amount, status, paid_at, description)
		 VALUES ($1, $2, $3, $4::payment_type, $5, 'completed', $6, 'KID-avstemming')
		 RETURNING id`,
		clubID, userID, recipientID, paymentType, amount, paidAt,
	).Scan(&paymentID); err != nil {
		return err
	}

	tag, err := q.Exec(ctx,
		`UPDATE invoices SET payment_id = $1, recipient_id = COALESCE(recipient_id, $4)
		  WHERE id = $2 AND club_id = $3 AND payment_id IS NULL`,
		paymentID, invoiceID, clubID, recipientID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errLinkRaced
	}
	// A bank payment on a join faktura makes the applicant a member, in the
	// transaction that links it; a grant the integrity check withholds is
	// sent to the board.
	if purpose != nil && *purpose == membership.Purpose {
		if b, ok := q.(txBeginner); ok {
			membership.GrantForInvoiceInSavepoint(ctx, b, invoiceID, membership.SourceKID)
		}
	}
	return nil
}

// resolveBillingRecipient returns the id of the billing_recipients row for an
// organization / non-member invoice, creating it from the invoice snapshot if
// none exists yet. Orgs are keyed on (club, org_number|org_name);
// private non-members on (club, email|name) — so repeat invoices to the same
// payer reuse one recipient. See BRY-234.
func resolveBillingRecipient(
	ctx context.Context, q dbtx, clubID, recipientKind string,
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
		err := q.QueryRow(ctx,
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
	if err := q.QueryRow(ctx,
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
