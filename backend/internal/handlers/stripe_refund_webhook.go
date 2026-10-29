package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/stripe"
)

// Card refunds (charge.refunded and charge.refund.updated events). The
// webhook keeps payment_refunds up to date, whoever made the refund: a refund
// made with the refund button is found by its request key, one made in the
// Stripe Dashboard gets a row of its own. It writes nothing else. The refund
// is booked, and its credit note issued, from the refund's balance
// transaction by the Stripe booking pass (ACC-5).

// refundContext is the charge a refund event is about and the payment it
// belongs to.
type refundContext struct {
	chargeID string
	intentPayment
}

// resolveRefundEvent finds the payment a refund event is about, as a dispute
// event's is found: through the PaymentIntent and charge the event names.
func (h *StripeWebhookHandler) resolveRefundEvent(ctx context.Context, evt *stripe.Event) *refundContext {
	chargeID, piID, ok := stripe.RefundEventTarget(evt)
	if !ok {
		h.log.Warn().Str("event_id", evt.ID).Msg("webhook: refund event names no charge")
		return nil
	}
	return &refundContext{chargeID: chargeID, intentPayment: h.resolveIntentPayment(ctx, chargeID, piID, chargeID)}
}

// onRefund stores the state of the charge's refunds. Stripe is asked for the
// charge's refunds rather than trusting the event's payload, so an event
// delivered late or out of order still stores what the refunds are now. A
// payment that cannot be placed because Stripe could not be asked about it,
// or a refund list that cannot be read, is returned: the event is answered
// with an error and Stripe delivers it again.
func (h *StripeWebhookHandler) onRefund(ctx context.Context, clubID string, evt *stripe.Event, rc *refundContext) error {
	if rc == nil {
		return nil
	}
	if rc.foreign {
		h.log.Info().Str("charge_id", rc.chargeID).Msg("webhook refund: payment of another deployment, ignoring")
		return nil
	}
	if rc.unplaced() {
		return errors.New("refund's payment could not be looked up")
	}
	if rc.attemptID == "" {
		h.log.Info().Str("charge_id", rc.chargeID).Str("event_id", evt.ID).Msg("webhook refund: charge is not one of our payments, ignoring")
		return nil
	}
	if h.stripe == nil {
		return nil
	}
	refunds, err := h.stripe.ListRefundsByCharge(ctx, rc.chargeID)
	if err != nil {
		h.log.Error().Err(err).Str("charge_id", rc.chargeID).Msg("webhook refund: listing the charge's refunds failed")
		return fmt.Errorf("listing refunds: %w", err)
	}
	var failed error
	for _, r := range refunds {
		err := payments.UpsertProviderRefund(ctx, h.db, payments.ProviderRefund{
			ClubID: clubID, Provider: stripe.ProviderName, AttemptID: rc.attemptID,
			ProviderRefundID: r.ID, AmountOre: r.Amount, Currency: r.Currency,
			Status: r.BryggeStatus(), FailureReason: r.FailureReason,
			PaymentRef: string(r.PaymentIntent), ChargeRef: rc.chargeID,
			RequestKey: r.Metadata[stripe.RefundRequestKeyMetadata],
		})
		if err != nil {
			h.log.Error().Err(err).Str("refund_id", r.ID).Str("event_id", evt.ID).Msg("webhook refund: storing failed")
			failed = errors.Join(failed, err)
		}
	}
	return failed
}
