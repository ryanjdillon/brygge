package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/stripe"
)

// intentPayment is the Brygge payment a Stripe event about a charge names
// (disputes and refunds carry no metadata of Brygge's, only the PaymentIntent
// and charge). clubID is empty when nothing tied the event to a club.
type intentPayment struct {
	clubID             string
	attemptID, orderID string
	foreign            bool
	// lookupFailed is set when the PaymentIntent could not be read (not
	// when it does not exist): the payment may be another deployment's, or
	// ours with an attempt not yet found.
	lookupFailed bool
}

// unplaced reports a payment the webhook cannot place and must not drop:
// Stripe could not be asked about it, so it may be ours. The event is
// answered with an error and Stripe delivers it again.
func (p intentPayment) unplaced() bool { return p.lookupFailed && p.attemptID == "" }

// resolveIntentPayment finds the payment an event is about. The attempt comes
// from the PaymentIntent (the attempt id and club slug Brygge put in its
// metadata), and failing that from the stored charge, by its charge id. A
// payment another deployment sharing the Stripe account created is foreign
// and left alone. objectID names the event's object in the log.
func (h *StripeWebhookHandler) resolveIntentPayment(ctx context.Context, objectID, paymentIntentID, chargeID string) intentPayment {
	var p intentPayment
	if paymentIntentID != "" && h.stripe != nil {
		pi, err := h.stripe.GetPaymentIntent(ctx, paymentIntentID)
		var apiErr *stripe.APIError
		switch {
		case errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound:
			// Not in this Stripe account: nothing to learn from it.
		case err != nil:
			p.lookupFailed = true
			h.log.Warn().Err(err).Str("object_id", objectID).Msg("webhook: payment intent lookup failed")
		default:
			// An attempt of ours settles it, whatever slug the payment
			// carries: a club's slug can be renamed.
			h.claimAttempt(ctx, &p, objectID, pi.Metadata["attempt_id"])
			if p.attemptID == "" && foreignSlug(pi.Metadata["club_slug"], h.cfg.ClubSlug) {
				p.foreign = true
				return p
			}
		}
	}
	if p.attemptID == "" {
		h.paymentFromCharge(ctx, &p, objectID, chargeID, paymentIntentID)
	}
	return p
}

// foreignSlug reports whether a club slug in a payment's metadata names a
// deployment other than this one.
func foreignSlug(metadataSlug, ours string) bool {
	return metadataSlug != "" && ours != "" && !strings.EqualFold(metadataSlug, ours)
}

// claimAttempt takes the attempt, its order and its club from an attempt id
// Brygge put in a payment's metadata, if it names one.
func (h *StripeWebhookHandler) claimAttempt(ctx context.Context, p *intentPayment, objectID, attemptID string) {
	if _, err := uuid.Parse(attemptID); err != nil {
		return
	}
	var club, order string
	err := h.db.QueryRow(ctx,
		`SELECT club_id::text, order_id::text FROM payment_attempts WHERE provider = $1 AND id = $2`,
		stripe.ProviderName, attemptID,
	).Scan(&club, &order)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			h.log.Warn().Err(err).Str("object_id", objectID).Msg("webhook: attempt lookup failed")
		}
		return
	}
	p.clubID, p.attemptID, p.orderID = club, attemptID, order
}

// paymentFromCharge ties an event to the stored charge it is about, which
// knows its attempt when the booking ingest could classify it.
func (h *StripeWebhookHandler) paymentFromCharge(ctx context.Context, p *intentPayment, objectID, chargeID, paymentIntentID string) {
	if chargeID == "" && paymentIntentID == "" {
		return
	}
	var club string
	var attempt, order *string
	err := h.db.QueryRow(ctx,
		`SELECT club_id::text, attempt_id::text, order_id::text
		   FROM stripe_balance_transactions
		  WHERE reporting_category = 'charge'
		    AND ((charge_id <> '' AND charge_id = $1) OR (payment_intent_id <> '' AND payment_intent_id = $2))
		  ORDER BY created_at, id LIMIT 1`,
		chargeID, paymentIntentID,
	).Scan(&club, &attempt, &order)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			h.log.Warn().Err(err).Str("object_id", objectID).Msg("webhook: charge lookup failed")
		}
		return
	}
	p.clubID = club
	if attempt != nil {
		p.attemptID = *attempt
	}
	if order != nil {
		p.orderID = *order
	}
}
