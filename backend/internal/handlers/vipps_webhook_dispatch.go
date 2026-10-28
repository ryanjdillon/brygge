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
// onCaptured. An authorization the order does not need (it is paid, or another
// attempt is already authorized or captured) is voided instead, so the payer
// is not charged twice.
func (h *VippsWebhookHandler) onAuthorized(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	a, ok := h.orders.attemptByReference(ctx, clubID, vipps.ProviderName, evt.Reference)
	if !ok {
		return // not one of our checkout orders
	}
	// Without credentials Brygge can neither capture nor void, and without an
	// amount it cannot capture; the attempt is left as it is and an uncaptured
	// reservation lapses at Vipps.
	if h.vipps == nil || evt.Amount == nil {
		return
	}
	switch d := h.orders.authorizeAttempt(ctx, a); d {
	case authSkip:
		return
	case authCancelOrderSettled, authCancelSibling:
		h.cancelStaleAuthorization(ctx, a, evt.Reference, d)
		return
	}
	if err := h.vipps.CapturePayment(ctx, "capture-"+evt.Reference, evt.Reference, evt.Amount.Value); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook authorized: capture failed")
		h.orders.releaseAuthorization(ctx, a)
	}
}

// cancelStaleAuthorization voids an authorization the order does not need.
// If the void fails the reservation lapses at Vipps on its own; it is never
// captured either way.
func (h *VippsWebhookHandler) cancelStaleAuthorization(ctx context.Context, a paymentAttempt, reference string, d authDecision) {
	reason := "order already paid by another attempt"
	if d == authCancelSibling {
		reason = "another attempt on the order is already authorized or captured"
	}
	h.log.Warn().Str("order_id", a.OrderID).Str("reference", reference).Str("reason", reason).
		Msg("webhook authorized: cancelling, not capturing")
	if h.canceller == nil {
		return
	}
	if err := h.canceller.CancelPayment(ctx, reference); err != nil {
		h.log.Error().Err(err).Str("reference", reference).Msg("webhook authorized: cancel of stale authorization failed")
		return
	}
	h.orders.markAttemptCancelled(ctx, a)
}

// onCaptured settles the checkout order (merch, harbor QR, or a faktura pay
// link) the captured payment belongs to.
func (h *VippsWebhookHandler) onCaptured(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	a, ok := h.orders.attemptByReference(ctx, clubID, vipps.ProviderName, evt.Reference)
	if !ok {
		return
	}
	// Only guests are asked to share their name, so only a guest order has
	// one to look up.
	var payerName func(context.Context) string
	if a.Guest {
		payerName = func(ctx context.Context) string { return h.payerName(ctx, evt.Reference) }
	}
	h.orders.markAttemptCaptured(ctx, a, evt.Timestamp, payerName)
}

// payerName returns the name the payer consented to share via the Vipps
// profile, or "" when there is none.
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
	a, ok := h.orders.attemptByReference(ctx, clubID, vipps.ProviderName, evt.Reference)
	if !ok {
		return
	}
	h.orders.markAttemptFailed(ctx, a)
}
