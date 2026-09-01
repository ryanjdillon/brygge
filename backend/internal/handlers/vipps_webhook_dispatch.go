package handlers

import (
	"context"
	"time"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// dispatch routes an already-persisted event to the appropriate business logic.
// All operations here are best-effort — the webhook has already been ack'd 200.
func (h *VippsWebhookHandler) dispatch(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	switch evt.Name {
	case vipps.EventPaymentAuthorized:
		h.onAuthorized(ctx, clubID, evt)
	case vipps.EventPaymentCaptured:
		h.onCaptured(ctx, clubID, evt)
	case vipps.EventPaymentCancelled, vipps.EventPaymentAborted, vipps.EventPaymentExpired, vipps.EventPaymentTerminated:
		h.onTerminated(ctx, clubID, evt)
	}
}

// onAuthorized captures an authorized checkout-order payment. The sales unit
// uses the Reserve-Capture model, so an authorized payment must be captured to
// actually take the funds; capture is idempotent (stable key) so webhook retries
// won't double-capture. A successful capture emits CAPTURED, handled in
// onCaptured.
func (h *VippsWebhookHandler) onAuthorized(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	if _, ok := h.orderByReference(ctx, clubID, evt.Reference); !ok {
		return // not one of our checkout orders
	}
	if h.vipps == nil || evt.Amount == nil {
		return
	}
	if err := h.vipps.CapturePayment(ctx, "capture-"+evt.Reference, evt.Reference, evt.Amount.Value); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook authorized: capture failed")
	}
}

func (h *VippsWebhookHandler) onCaptured(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	// Mark the checkout order paid (merch, harbor QR, or a faktura pay link).
	if _, err := h.db.Exec(ctx,
		`UPDATE orders SET status = 'paid', paid_at = now(), updated_at = now()
		 WHERE club_id = $1 AND vipps_reference = $2 AND status = 'pending'`,
		clubID, evt.Reference,
	); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook captured: order update failed")
	}

	// Backfill the payer's name onto guest orders (QR / walk-up) from the Vipps
	// profile the payer consented to share (BRY-272), so the harbor overview
	// shows who paid instead of "Gjest".
	h.backfillGuestName(ctx, clubID, evt.Reference)

	// Faktura payment links (BRY-8): for every invoice this order settles, record
	// a soft "Vipps payment received" marker only — deliberately NOT payment_id,
	// and no GL entry. During the CSV overlap the settlement import stays the
	// single source of the paid status and the journal posting, and it matches
	// only invoices with payment_id IS NULL; setting payment_id here would orphan
	// that reconciliation. The marker just drives a realtime "mottatt, venter
	// oppgjør" indicator until settlement clears it. API-primary GL ownership is
	// BRY-256, gated on BRY-4 (Report API settlements).
	paidAt := evt.Timestamp
	if paidAt.IsZero() {
		paidAt = time.Now()
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE invoices SET vipps_paid_at = $3
		 WHERE vipps_paid_at IS NULL
		   AND id IN (
		     SELECT os.invoice_id FROM order_sources os
		     JOIN orders o ON o.id = os.order_id
		     WHERE o.club_id = $1 AND o.vipps_reference = $2
		   )`,
		clubID, evt.Reference, paidAt,
	); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook captured: invoice marker failed")
	}
}

// backfillGuestName sets a guest order's payer name from the Vipps profile
// (BRY-272). No-op unless the payer consented to share their name and the order
// is a guest order (user_id IS NULL) with no name yet — never overwrites a
// linked member or a name the payer typed on the checkout page.
func (h *VippsWebhookHandler) backfillGuestName(ctx context.Context, clubID, reference string) {
	if h.vipps == nil {
		return
	}
	pd, err := h.vipps.GetPayment(ctx, reference)
	if err != nil {
		h.log.Debug().Err(err).Str("reference", reference).Msg("webhook: get payment for guest name failed")
		return
	}
	name := pd.UserDetails.PayerName()
	if name == "" {
		return
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE orders SET guest_name = $3
		   WHERE club_id = $1 AND vipps_reference = $2
		     AND user_id IS NULL AND COALESCE(guest_name, '') = ''`,
		clubID, reference, name,
	); err != nil {
		h.log.Error().Err(err).Str("reference", reference).Msg("webhook: backfill guest name failed")
	}
}

func (h *VippsWebhookHandler) onTerminated(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	// 'failed' is the terminal-negative state in the order_status enum (there is
	// no 'cancelled' value). An abandoned/aborted attempt can be retried via a
	// fresh mint on the same durable order.
	if _, err := h.db.Exec(ctx,
		`UPDATE orders SET status = 'failed', updated_at = now()
		 WHERE club_id = $1 AND vipps_reference = $2 AND status = 'pending'`,
		clubID, evt.Reference,
	); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook terminated: order update failed")
	}
}

// orderByReference returns the checkout order id whose stored Vipps reference
// matches, and whether one was found.
func (h *VippsWebhookHandler) orderByReference(ctx context.Context, clubID, reference string) (string, bool) {
	var orderID string
	if err := h.db.QueryRow(ctx,
		`SELECT id FROM orders WHERE club_id = $1 AND vipps_reference = $2 LIMIT 1`,
		clubID, reference,
	).Scan(&orderID); err != nil {
		return "", false
	}
	return orderID, true
}
