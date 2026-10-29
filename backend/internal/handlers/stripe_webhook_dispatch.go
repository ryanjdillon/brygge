package handlers

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/stripe"
)

// captureKeyPrefix starts the Idempotency-Key of an attempt's capture. Bump
// it whenever the capture call's parameters change.
const captureKeyPrefix = "capture2-"

// dispatch routes an already-persisted event to settlement, a payout event
// to the Stripe balance mirror, and a dispute event to payment_disputes.
// Charge refunds are only recorded. The error is a dispute that could not
// be stored, which makes the event be delivered again; every other outcome
// is handled and logged.
func (h *StripeWebhookHandler) dispatch(ctx context.Context, clubID string, evt *stripe.Event, dispute *disputeContext) error {
	switch evt.Type {
	case stripe.EventCheckoutSessionCompleted, stripe.EventCheckoutSessionAsyncPaymentSucceeded:
		h.onSessionCompleted(ctx, clubID, evt)
	case stripe.EventCheckoutSessionExpired, stripe.EventCheckoutSessionAsyncPaymentFailed:
		h.onSessionFailed(ctx, clubID, evt)
	case stripe.EventPaymentIntentCanceled:
		h.onIntentCanceled(ctx, clubID, evt)
	case stripe.EventPaymentIntentSucceeded:
		h.onIntentSucceeded(ctx, clubID, evt)
	case stripe.EventPayoutPaid, stripe.EventPayoutFailed, stripe.EventPayoutCanceled:
		h.onPayout(ctx, clubID, evt)
	case stripe.EventChargeDisputeCreated, stripe.EventChargeDisputeUpdated, stripe.EventChargeDisputeClosed,
		stripe.EventChargeDisputeFundsWithdrawn, stripe.EventChargeDisputeFundsReinstated:
		return h.onDispute(ctx, clubID, evt, dispute)
	}
	return nil
}

// payoutIngestTimeout bounds a payout ingest started by a webhook,
// including the wait for a sync of the same club that is already running.
const payoutIngestTimeout = 2 * time.Minute

// onPayout refreshes the payout in the Stripe balance mirror, linking the
// transactions it paid out or releasing them when it failed. It runs after
// the event is answered, since listing a payout's members takes several
// Stripe calls. It books nothing; a failure is logged and the nightly sync
// repairs it.
func (h *StripeWebhookHandler) onPayout(ctx context.Context, clubID string, evt *stripe.Event) {
	if h.ledger == nil || h.stripe == nil {
		return
	}
	var p stripe.Payout
	if err := json.Unmarshal(evt.Data.Object, &p); err != nil || p.ID == "" {
		h.log.Warn().Err(err).Str("event_id", evt.ID).Msg("webhook: undecodable payout")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), payoutIngestTimeout)
	h.payouts.Add(1)
	go func() {
		defer h.payouts.Done()
		defer cancel()
		res, err := h.ledger.IngestStripePayout(ctx, h.stripe, clubID, p.ID)
		if err != nil {
			h.log.Error().Err(err).Str("payout_id", p.ID).Msg("webhook payout: ingest failed")
			return
		}
		if len(res.Errors) > 0 {
			h.log.Warn().Strs("errors", res.Errors).Str("payout_id", p.ID).Msg("webhook payout: some transactions failed")
		}
	}()
}

// onSessionCompleted handles a paid Checkout Session. Sessions are created
// with manual capture, so completion is an authorization: it goes through
// the same capture-or-void decision as a Vipps AUTHORIZED, and a payment the
// order does not need is voided, never charged.
func (h *StripeWebhookHandler) onSessionCompleted(ctx context.Context, clubID string, evt *stripe.Event) {
	s, ok := h.decodeSession(evt)
	if !ok || !sessionAuthorized(s) {
		return
	}
	a, ok := h.orders.attemptByReference(ctx, clubID, stripe.ProviderName, s.ID)
	if !ok {
		return
	}
	// Without credentials Brygge can neither capture nor void; the attempt is
	// left as it is and the uncaptured authorization lapses at Stripe.
	if h.stripe == nil {
		return
	}
	if !sessionAmountMatches(a, s) {
		h.rejectAmountMismatch(ctx, a, s)
		return
	}
	switch d := h.orders.authorizeAttempt(ctx, a); d {
	case authSkip:
		return
	case authCancelOrderSettled, authCancelSibling:
		h.cancelStaleAuthorization(ctx, a, s.ID, d)
		return
	}
	h.capture(ctx, a, s)
}

// sessionAuthorized reports whether a completed session carries a payment
// to capture. With manual capture a completed card session reports
// payment_status "unpaid" (authorized, not yet taken), so only a session
// that needed no payment is excluded.
func sessionAuthorized(s *stripe.CheckoutSession) bool {
	return s.Status == stripe.SessionComplete &&
		s.PaymentStatus != "no_payment_required" &&
		s.PaymentIntent != nil && s.PaymentIntent.ID != ""
}

// sessionAmountMatches checks the session against what the attempt was
// created to charge, so a session altered at Stripe cannot settle an order
// for a different amount.
func sessionAmountMatches(a paymentAttempt, s *stripe.CheckoutSession) bool {
	return amountMatches(a, s.AmountTotal, s.Currency)
}

// amountMatches reports whether amountOre in currency is exactly what the
// attempt was created to charge.
func amountMatches(a paymentAttempt, amountOre int64, currency string) bool {
	return amountOre == int64(math.Round(a.Amount*100)) && strings.EqualFold(currency, a.Currency)
}

func (h *StripeWebhookHandler) rejectAmountMismatch(ctx context.Context, a paymentAttempt, s *stripe.CheckoutSession) {
	h.log.Error().Str("attempt_id", a.ID).Str("session_id", s.ID).
		Int64("session_amount", s.AmountTotal).Str("session_currency", s.Currency).
		Float64("attempt_amount", a.Amount).Str("attempt_currency", a.Currency).
		Msg("webhook completed: amount does not match the attempt, voiding")
	if err := h.canceller.CancelPayment(ctx, s.ID); err != nil {
		h.log.Error().Err(err).Str("session_id", s.ID).Msg("webhook completed: void of mismatched payment failed")
	}
	h.orders.markAttemptFailed(ctx, a)
}

// capture takes the money and settles the order. The idempotency key is per
// attempt, so a redelivered event cannot capture twice. It is versioned with
// the capture call's parameters: Stripe refuses a key reused with other
// parameters, so a new key keeps an event first handled by an earlier
// version from being refused (and the client captures again rather than
// reporting a failure if a key is refused anyway).
func (h *StripeWebhookHandler) capture(ctx context.Context, a paymentAttempt, s *stripe.CheckoutSession) {
	pi, err := h.stripe.CapturePaymentIntent(ctx, s.PaymentIntent.ID, captureKeyPrefix+a.ID)
	if err != nil {
		h.log.Error().Err(err).Str("session_id", s.ID).Msg("webhook completed: capture failed")
		h.afterFailedCapture(ctx, a, s.PaymentIntent.ID)
		return
	}
	var payerName func(context.Context) string
	var email string
	if cd := s.CustomerDetails; cd != nil {
		email = cd.Email
		if name := strings.TrimSpace(cd.Name); name != "" {
			payerName = func(context.Context) string { return name }
		}
	}
	h.orders.markAttemptCaptured(ctx, a, time.Now(), payerName)
	h.orders.backfillGuestEmail(ctx, a, email)
	brand, wallet := pi.CardBrandAndWallet()
	h.orders.recordAttemptCard(ctx, a.ID, brand, wallet)
}

// afterFailedCapture settles an attempt whose capture call failed by asking
// Stripe where the PaymentIntent stands. Stripe replays a key's first answer,
// errors included, so retrying the capture would only repeat the failure. A
// capture Stripe refused over its key (never attempted) does not get here:
// CapturePaymentIntent captures it under a new key instead, so a hold that
// should be taken is not voided below.
//   - Still authorized: void it, so the payer's card hold is released at once,
//     and mark the attempt failed; the payer starts a fresh attempt.
//   - Captured after all: settle it as captured.
//   - Unknown (lookup or void failed): release the attempt to created; the
//     hold lapses at Stripe, and payment_intent.succeeded or .canceled
//     settles it if Stripe moves it on.
func (h *StripeWebhookHandler) afterFailedCapture(ctx context.Context, a paymentAttempt, piID string) {
	pi, err := h.stripe.GetPaymentIntent(ctx, piID)
	if err != nil {
		h.log.Error().Err(err).Str("attempt_id", a.ID).Msg("webhook completed: payment intent lookup after failed capture failed")
		h.orders.releaseAuthorization(ctx, a)
		return
	}
	switch pi.Status {
	case stripe.PaymentIntentSucceeded:
		h.settleSucceededIntent(ctx, a, pi, time.Now())
		brand, wallet := pi.CardBrandAndWallet()
		h.orders.recordAttemptCard(ctx, a.ID, brand, wallet)
	case stripe.PaymentIntentRequiresCapture:
		if err := h.stripe.CancelPaymentIntent(ctx, piID); err != nil {
			h.log.Error().Err(err).Str("attempt_id", a.ID).Msg("webhook completed: void after failed capture failed")
			h.orders.releaseAuthorization(ctx, a)
			return
		}
		h.orders.markAttemptFailed(ctx, a)
	default:
		h.orders.releaseAuthorization(ctx, a)
	}
}

// cancelStaleAuthorization voids an authorization the order does not need;
// authorizeAttempt has already recorded the attempt as cancelled. If the
// void fails the authorization lapses at Stripe on its own; it is never
// captured either way.
func (h *StripeWebhookHandler) cancelStaleAuthorization(ctx context.Context, a paymentAttempt, sessionID string, d authDecision) {
	reason := "order already paid by another attempt"
	if d == authCancelSibling {
		reason = "another attempt on the order is already authorized or captured"
	}
	h.log.Warn().Str("order_id", a.OrderID).Str("session_id", sessionID).Str("reason", reason).
		Msg("webhook completed: cancelling, not capturing")
	if err := h.canceller.CancelPayment(ctx, sessionID); err != nil {
		h.log.Error().Err(err).Str("session_id", sessionID).Msg("webhook completed: cancel of stale authorization failed")
	}
}

func (h *StripeWebhookHandler) onSessionFailed(ctx context.Context, clubID string, evt *stripe.Event) {
	s, ok := h.decodeSession(evt)
	if !ok {
		return
	}
	if a, ok := h.orders.attemptByReference(ctx, clubID, stripe.ProviderName, s.ID); ok {
		h.failAttempt(ctx, a)
	}
}

func (h *StripeWebhookHandler) onIntentCanceled(ctx context.Context, clubID string, evt *stripe.Event) {
	if a, _, ok := h.intentAttempt(ctx, clubID, evt); ok {
		h.failAttempt(ctx, a)
	}
}

// onIntentSucceeded settles an attempt whose capture went through. Normally
// the capture call already did; this covers a capture whose answer was lost.
func (h *StripeWebhookHandler) onIntentSucceeded(ctx context.Context, clubID string, evt *stripe.Event) {
	a, pi, ok := h.intentAttempt(ctx, clubID, evt)
	if ok {
		h.settleSucceededIntent(ctx, a, pi, time.Unix(evt.Created, 0))
	}
}

// settleSucceededIntent pays the order only when the full amount was
// captured: a partial capture (made by hand in the Dashboard) does not
// settle what is owed and is left for the board to resolve.
func (h *StripeWebhookHandler) settleSucceededIntent(ctx context.Context, a paymentAttempt, pi *stripe.PaymentIntent, capturedAt time.Time) {
	if !amountMatches(a, pi.AmountReceived, pi.Currency) {
		h.log.Error().Str("attempt_id", a.ID).Str("payment_intent", pi.ID).
			Int64("amount_received", pi.AmountReceived).Str("currency", pi.Currency).
			Float64("attempt_amount", a.Amount).Str("attempt_currency", a.Currency).
			Msg("webhook: captured amount does not match the attempt, not marking paid")
		return
	}
	h.orders.markAttemptCaptured(ctx, a, capturedAt, nil)
}

// failAttempt marks an attempt failed unless Brygge already captured or
// voided it: the cancellation Brygge itself caused arrives as an event too,
// and must not fail an order another attempt is paying.
func (h *StripeWebhookHandler) failAttempt(ctx context.Context, a paymentAttempt) {
	if a.Status == "captured" || a.Status == "cancelled" {
		return
	}
	h.orders.markAttemptFailed(ctx, a)
}

// intentAttempt resolves a PaymentIntent event to its attempt through the
// attempt id Brygge put in the PaymentIntent's metadata.
func (h *StripeWebhookHandler) intentAttempt(ctx context.Context, clubID string, evt *stripe.Event) (paymentAttempt, *stripe.PaymentIntent, bool) {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(evt.Data.Object, &pi); err != nil {
		h.log.Warn().Err(err).Str("event_id", evt.ID).Msg("webhook: undecodable payment intent")
		return paymentAttempt{}, nil, false
	}
	a, ok := h.orders.attemptByID(ctx, clubID, stripe.ProviderName, pi.Metadata["attempt_id"])
	return a, &pi, ok
}

func (h *StripeWebhookHandler) decodeSession(evt *stripe.Event) (*stripe.CheckoutSession, bool) {
	var s stripe.CheckoutSession
	if err := json.Unmarshal(evt.Data.Object, &s); err != nil || s.ID == "" {
		h.log.Warn().Err(err).Str("event_id", evt.ID).Msg("webhook: undecodable checkout session")
		return nil, false
	}
	return &s, true
}
