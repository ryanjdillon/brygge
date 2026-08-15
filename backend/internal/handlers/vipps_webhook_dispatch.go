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

// onAuthorized captures an authorized invoice payment. The sales unit uses the
// Reserve-Capture model, so an authorized faktura payment must be captured to
// actually take the funds; capture is idempotent (stable key) so webhook
// retries won't double-capture. A successful capture emits CAPTURED, which
// marks the invoice paid in onCaptured.
func (h *VippsWebhookHandler) onAuthorized(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	if _, ok := h.invoiceByReference(ctx, clubID, evt.Reference); !ok {
		return // not an invoice payment (e.g. a checkout order)
	}
	if h.vipps == nil || evt.Amount == nil {
		return
	}
	if err := h.vipps.CapturePayment(ctx, "capture-"+evt.Reference, evt.Reference, evt.Amount.Value); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook authorized: capture failed")
	}
}

func (h *VippsWebhookHandler) onCaptured(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	// Checkout / QR orders.
	if _, err := h.db.Exec(ctx,
		`UPDATE orders SET status = 'paid', paid_at = now()
		 WHERE club_id = $1 AND vipps_reference = $2 AND status = 'pending'`,
		clubID, evt.Reference,
	); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook captured: order update failed")
	}

	// Faktura payment links (BRY-8): record a soft "Vipps payment received"
	// marker only — deliberately NOT payment_id, and no GL entry. During the
	// CSV overlap the settlement import stays the single source of the paid
	// status and the journal posting, and it matches only invoices with
	// payment_id IS NULL; setting payment_id here would orphan that
	// reconciliation. The marker just drives a realtime "mottatt, venter
	// oppgjør" indicator until settlement clears it. API-primary GL ownership
	// is BRY-256, gated on BRY-4 (Report API settlements).
	paidAt := evt.Timestamp
	if paidAt.IsZero() {
		paidAt = time.Now()
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE invoices SET vipps_paid_at = $3
		 WHERE club_id = $1 AND vipps_payment_reference = $2 AND vipps_paid_at IS NULL`,
		clubID, evt.Reference, paidAt,
	); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook captured: invoice marker failed")
	}
}

func (h *VippsWebhookHandler) onTerminated(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	if _, err := h.db.Exec(ctx,
		`UPDATE orders SET status = 'cancelled'
		 WHERE club_id = $1 AND vipps_reference = $2 AND status = 'pending'`,
		clubID, evt.Reference,
	); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook terminated: order update failed")
	}
}

// invoiceByReference returns the invoice id whose stored Vipps payment
// reference matches, and whether one was found.
func (h *VippsWebhookHandler) invoiceByReference(ctx context.Context, clubID, reference string) (string, bool) {
	var invoiceID string
	if err := h.db.QueryRow(ctx,
		`SELECT id FROM invoices WHERE club_id = $1 AND vipps_payment_reference = $2 LIMIT 1`,
		clubID, reference,
	).Scan(&invoiceID); err != nil {
		return "", false
	}
	return invoiceID, true
}
