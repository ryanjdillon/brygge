package handlers

import (
	"context"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// dispatch routes an already-persisted event to the appropriate business logic.
// The error is a failed attempt lookup, or a settlement decision or write that
// failed in the database, which makes Vipps deliver the event again; the
// event row is already persisted and every settlement write is guarded, so the
// redelivery finishes what this one left. A reference that is not one of ours,
// and a provider call that fails, are not errors.
func (h *VippsWebhookHandler) dispatch(ctx context.Context, clubID string, evt *vipps.WebhookEvent) error {
	switch evt.Name {
	case vipps.EventPaymentAuthorized:
		return h.onAuthorized(ctx, clubID, evt)
	case vipps.EventPaymentCaptured:
		return h.onCaptured(ctx, clubID, evt)
	case vipps.EventPaymentCancelled, vipps.EventPaymentAborted, vipps.EventPaymentExpired, vipps.EventPaymentTerminated:
		return h.onTerminated(ctx, clubID, evt)
	}
	return nil
}

// lookupAttempt resolves a Vipps event's reference to its attempt. A failed
// lookup is logged and returned so the webhook answers 5xx.
func (h *VippsWebhookHandler) lookupAttempt(ctx context.Context, clubID string, evt *vipps.WebhookEvent) (paymentAttempt, bool, error) {
	a, ok, err := h.orders.attemptByReference(ctx, clubID, vipps.ProviderName, evt.Reference)
	if err != nil {
		h.log.Error().Err(err).Str("event_name", evt.Name).Str("reference", evt.Reference).
			Msg("webhook: attempt lookup failed")
	}
	return a, ok, err
}

// onAuthorized captures an authorized checkout-order payment. The sales unit
// uses the Reserve-Capture model, so an authorized payment must be captured to
// actually take the funds; capture is idempotent (stable key) so webhook retries
// won't double-capture. A successful capture emits CAPTURED, handled in
// onCaptured. An authorization the order does not need (it is paid, or another
// attempt is already authorized or captured) is voided instead, so the payer
// is not charged twice.
func (h *VippsWebhookHandler) onAuthorized(ctx context.Context, clubID string, evt *vipps.WebhookEvent) error {
	a, ok, err := h.lookupAttempt(ctx, clubID, evt)
	if err != nil || !ok {
		return err // an error, or not one of our checkout orders
	}
	// Without credentials Brygge can neither capture nor void, and without an
	// amount it cannot capture; the attempt is left as it is and an uncaptured
	// reservation lapses at Vipps.
	if h.vipps == nil || evt.Amount == nil {
		return nil
	}
	d, err := h.orders.authorizeAttempt(ctx, a)
	if err != nil {
		return err
	}
	switch d {
	case authSkip:
		return nil
	case authCancelOrderSettled, authCancelSibling:
		h.cancelStaleAuthorization(ctx, a, evt.Reference, d)
		return nil
	}
	if err := h.vipps.CapturePayment(ctx, "capture-"+evt.Reference, evt.Reference, evt.Amount.Value); err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook authorized: capture failed")
		// Recording the outcome of a provider call must not depend on the
		// request still being alive.
		wctx, cancel := settleContext(ctx)
		defer cancel()
		return h.orders.releaseAuthorization(wctx, a)
	}
	return nil
}

// cancelStaleAuthorization voids an authorization the order does not need;
// authorizeAttempt has already recorded the attempt as cancelled. If the void
// fails the reservation lapses at Vipps on its own; it is never captured
// either way.
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
	}
}

// onCaptured settles the checkout order (merch, harbor QR, or a faktura pay
// link) the captured payment belongs to. Vipps settles only here, never in
// onAuthorized, so a settlement write that fails after the capture answers 5xx
// and Vipps sends the CAPTURED event again; nothing is captured a second time.
func (h *VippsWebhookHandler) onCaptured(ctx context.Context, clubID string, evt *vipps.WebhookEvent) error {
	a, ok, err := h.lookupAttempt(ctx, clubID, evt)
	if err != nil || !ok {
		return err
	}
	// Only guests are asked to share their name, so only a guest order has
	// one to look up.
	var payerName func(context.Context) string
	if a.Guest {
		payerName = func(ctx context.Context) string { return h.payerName(ctx, evt.Reference) }
	}
	return h.orders.markAttemptCaptured(ctx, a, evt.Timestamp, payerName)
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

func (h *VippsWebhookHandler) onTerminated(ctx context.Context, clubID string, evt *vipps.WebhookEvent) error {
	a, ok, err := h.lookupAttempt(ctx, clubID, evt)
	if err != nil || !ok {
		return err
	}
	return h.orders.markAttemptFailed(ctx, a)
}
