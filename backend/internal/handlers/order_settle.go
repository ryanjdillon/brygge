package handlers

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// orderSettlement records a payment provider's verdict on a checkout order.
// It is the only code that marks an order paid or failed, and it is called
// only from a provider's signature-verified webhook (see the "Only the payment
// provider can mark an order paid" invariant). Every write is guarded so a
// redelivered or out-of-order event is a no-op. The writes are best-effort and
// each runs on its own, so one failing does not undo the others: the event is
// already persisted and the provider redelivers it.
//
// It works per order, not per payment attempt; tracking several attempts on
// one order is the provider-neutral data model's job.
type orderSettlement struct {
	db  *pgxpool.Pool
	log zerolog.Logger
}

// markOrderPaid settles an order whose payment the provider has captured.
// capturedAt is the provider's capture time (zero: now) and is what the
// faktura marker records; the order's own paid_at is when Brygge learned of
// it. payerName (nil: none) returns the name the provider shared, if any, to
// name a guest order that has none yet. It is called only after the status
// update, so a slow or failing provider lookup cannot hold up marking the
// order paid.
func (s orderSettlement) markOrderPaid(ctx context.Context, orderID string, capturedAt time.Time, payerName func(context.Context) string) {
	// Only a pending order moves, so a replayed capture cannot touch paid_at
	// and an order already failed or refunded keeps its state.
	if _, err := s.db.Exec(ctx,
		`UPDATE orders SET status = 'paid', paid_at = now(), updated_at = now()
		 WHERE id = $1 AND status = 'pending'`,
		orderID,
	); err != nil {
		s.log.Error().Err(err).Str("order_id", orderID).Msg("order paid: order update failed")
	}

	// Guest orders (QR / walk-up) show the payer instead of "Gjest" in the
	// harbor overview. Never overwrites a linked member or a name the payer
	// typed on the checkout page.
	if name := callPayerName(ctx, payerName); name != "" {
		if _, err := s.db.Exec(ctx,
			`UPDATE orders SET guest_name = $2
			  WHERE id = $1 AND user_id IS NULL AND COALESCE(guest_name, '') = ''`,
			orderID, name,
		); err != nil {
			s.log.Error().Err(err).Str("order_id", orderID).Msg("order paid: backfill guest name failed")
		}
	}

	// For every faktura the order settles, record a soft "payment received"
	// marker only — deliberately NOT payment_id, and no GL entry. The
	// settlement import stays the single source of the paid status and the
	// journal posting, and it matches only invoices with payment_id IS NULL;
	// setting payment_id here would orphan that reconciliation. The marker
	// drives the "mottatt, venter oppgjør" indicator until settlement clears
	// it. API-primary GL ownership is BRY-256, gated on BRY-4.
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE invoices SET vipps_paid_at = $2
		 WHERE vipps_paid_at IS NULL
		   AND id IN (SELECT invoice_id FROM order_sources WHERE order_id = $1)`,
		orderID, capturedAt,
	); err != nil {
		s.log.Error().Err(err).Str("order_id", orderID).Msg("order paid: invoice marker failed")
	}
}

func callPayerName(ctx context.Context, payerName func(context.Context) string) string {
	if payerName == nil {
		return ""
	}
	return payerName(ctx)
}

// markOrderFailed records an abandoned, expired or refused payment. 'failed'
// is the terminal-negative order_status (there is no 'cancelled'); the payer
// can retry with a fresh payment on the same durable order.
func (s orderSettlement) markOrderFailed(ctx context.Context, orderID string) {
	if _, err := s.db.Exec(ctx,
		`UPDATE orders SET status = 'failed', updated_at = now()
		 WHERE id = $1 AND status = 'pending'`,
		orderID,
	); err != nil {
		s.log.Error().Err(err).Str("order_id", orderID).Msg("order failed: order update failed")
	}
}
