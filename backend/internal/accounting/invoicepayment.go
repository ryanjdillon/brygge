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
//   - Skips org-recipient invoices (invoices.user_id IS NULL) because
//     payments.user_id is NOT NULL. Those need a separate mechanism
//     and are out of scope here.
//   - Skips invoices that already have a payment_id (no-op idempotent).
//   - Maps invoice.category onto payment_type using the categories that
//     overlap; everything else falls through to `dues` which is the
//     least-surprising default for membership-style billing.
func (s *Service) linkInvoicePayment(ctx context.Context, clubID, invoiceID string, paidAt time.Time) error {
	var userID *string
	var amount float64
	var category *string
	err := s.db.QueryRow(ctx,
		`SELECT user_id, total_amount, category
		   FROM invoices
		  WHERE id = $1 AND club_id = $2 AND payment_id IS NULL`,
		invoiceID, clubID,
	).Scan(&userID, &amount, &category)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if userID == nil {
		// Org-recipient invoices need a different backfill path;
		// payments.user_id is NOT NULL and lifting that constraint
		// is a larger schema change.
		return nil
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

	var paymentID string
	if err := s.db.QueryRow(ctx,
		`INSERT INTO payments
		   (club_id, user_id, type, amount, status, paid_at, description)
		 VALUES ($1, $2, $3::payment_type, $4, 'completed', $5, 'KID-avstemming')
		 RETURNING id`,
		clubID, *userID, paymentType, amount, paidAt,
	).Scan(&paymentID); err != nil {
		return err
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE invoices SET payment_id = $1
		  WHERE id = $2 AND club_id = $3 AND payment_id IS NULL`,
		paymentID, invoiceID, clubID,
	); err != nil {
		return err
	}
	return nil
}
