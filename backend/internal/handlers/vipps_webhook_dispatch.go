package handlers

import (
	"context"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// dispatch routes an already-persisted event to the appropriate business logic.
// All operations here are best-effort: the event row is already persisted and
// Vipps redelivers the event.
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

// onCaptured settles the checkout order (merch, harbor QR, or a faktura pay
// link) the captured payment belongs to.
func (h *VippsWebhookHandler) onCaptured(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	orderID, ok := h.orderByReference(ctx, clubID, evt.Reference)
	if !ok {
		return
	}
	h.orders.markOrderPaid(ctx, orderID, evt.Timestamp, func(ctx context.Context) string {
		return h.payerName(ctx, evt.Reference)
	})
}

// payerName returns the name the payer consented to share via the Vipps
// profile (requested for guest orders only), or "" when there is none.
func (h *VippsWebhookHandler) payerName(ctx context.Context, reference string) string {
	if h.vipps == nil {
		return ""
	}
	pd, err := h.vipps.GetPayment(ctx, reference)
	if err != nil {
		h.log.Debug().Err(err).Str("reference", reference).Msg("webhook: get payment for guest name failed")
		return ""
	}
	return pd.UserDetails.PayerName()
}

func (h *VippsWebhookHandler) onTerminated(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	orderID, ok := h.orderByReference(ctx, clubID, evt.Reference)
	if !ok {
		return
	}
	h.orders.markOrderFailed(ctx, orderID)
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
