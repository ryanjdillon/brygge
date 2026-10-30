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
// to the Stripe balance mirror, a dispute event to payment_disputes and a
// refund event to payment_refunds. The error is a dispute or refund that
// could not be stored, an attempt that could not be looked up, or a settlement
// decision or write that failed in the database, which makes the event be
// delivered again; every other outcome (including a provider call that
// failed) is handled and logged.
func (h *StripeWebhookHandler) dispatch(ctx context.Context, clubID string, evt *stripe.Event, dispute *disputeContext, refund *refundContext) error {
	switch evt.Type {
	case stripe.EventCheckoutSessionCompleted, stripe.EventCheckoutSessionAsyncPaymentSucceeded:
		return h.onSessionCompleted(ctx, clubID, evt)
	case stripe.EventCheckoutSessionExpired, stripe.EventCheckoutSessionAsyncPaymentFailed:
		return h.onSessionFailed(ctx, clubID, evt)
	case stripe.EventPaymentIntentCanceled:
		return h.onIntentCanceled(ctx, clubID, evt)
	case stripe.EventPaymentIntentSucceeded:
		return h.onIntentSucceeded(ctx, clubID, evt)
	case stripe.EventPayoutPaid, stripe.EventPayoutFailed, stripe.EventPayoutCanceled:
		h.onPayout(ctx, clubID, evt)
	case stripe.EventChargeDisputeCreated, stripe.EventChargeDisputeUpdated, stripe.EventChargeDisputeClosed,
		stripe.EventChargeDisputeFundsWithdrawn, stripe.EventChargeDisputeFundsReinstated:
		return h.onDispute(ctx, clubID, evt, dispute)
	case stripe.EventChargeRefunded, stripe.EventChargeRefundUpdated:
		return h.onRefund(ctx, clubID, evt, refund)
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
func (h *StripeWebhookHandler) onSessionCompleted(ctx context.Context, clubID string, evt *stripe.Event) error {
	s, ok := h.decodeSession(evt)
	if !ok || !sessionAuthorized(s) {
		return nil
	}
	a, ok, err := h.sessionAttempt(ctx, clubID, evt, s)
	if err != nil || !ok {
		return err
	}
	// Without credentials Brygge can neither capture nor void; the attempt is
	// left as it is and the uncaptured authorization lapses at Stripe.
	if h.stripe == nil {
		return nil
	}
	if !sessionAmountMatches(a, s) {
		return h.rejectAmountMismatch(ctx, a, s)
	}
	d, err := h.orders.authorizeAttempt(ctx, a)
	if err != nil {
		return err
	}
	switch d {
	case authSkip:
		return nil
	case authCancelOrderSettled, authCancelSibling:
		h.cancelStaleAuthorization(ctx, a, s.ID, d)
		return nil
	}
	return h.capture(ctx, a, s)
}

// sessionAttempt resolves a Checkout Session event to its attempt by the
// session id. A failed lookup is logged and returned so the webhook answers
// 5xx.
func (h *StripeWebhookHandler) sessionAttempt(ctx context.Context, clubID string, evt *stripe.Event, s *stripe.CheckoutSession) (paymentAttempt, bool, error) {
	a, ok, err := h.orders.attemptByReference(ctx, clubID, stripe.ProviderName, s.ID)
	if err != nil {
		h.log.Error().Err(err).Str("event_id", evt.ID).Str("session_id", s.ID).Msg("webhook: attempt lookup failed")
	}
	return a, ok, err
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

func (h *StripeWebhookHandler) rejectAmountMismatch(ctx context.Context, a paymentAttempt, s *stripe.CheckoutSession) error {
	h.log.Error().Str("attempt_id", a.ID).Str("session_id", s.ID).
		Int64("session_amount", s.AmountTotal).Str("session_currency", s.Currency).
		Float64("attempt_amount", a.Amount).Str("attempt_currency", a.Currency).
		Msg("webhook completed: amount does not match the attempt, voiding")
	if err := h.canceller.CancelPayment(ctx, s.ID); err != nil {
		h.log.Error().Err(err).Str("session_id", s.ID).Msg("webhook completed: void of mismatched payment failed")
	}
	wctx, cancel := settleContext(ctx)
	defer cancel()
	return h.orders.markAttemptFailed(wctx, a)
}

// capture takes the money and settles the order. The idempotency key is per
// attempt, so a redelivered event cannot capture twice. It is versioned with
// the capture call's parameters: Stripe refuses a key reused with other
// parameters, so a new key keeps an event first handled by an earlier
// version from being refused (and the client captures again rather than
// reporting a failure if a key is refused anyway).
//
// The error is a settlement write that failed after the capture went through.
// The webhook then answers 5xx and the redelivery runs the same decision (the
// attempt is still authorized) and the same capture call: Stripe answers it
// from the key's first use, or, past the key's lifetime, the PaymentIntent is
// found already succeeded, so the money is taken once and the writes finish.
func (h *StripeWebhookHandler) capture(ctx context.Context, a paymentAttempt, s *stripe.CheckoutSession) error {
	pi, err := h.stripe.CapturePaymentIntent(ctx, s.PaymentIntent.ID, captureKeyPrefix+a.ID)
	if err != nil {
		h.log.Error().Err(err).Str("session_id", s.ID).Msg("webhook completed: capture failed")
		return h.afterFailedCapture(ctx, a, s.PaymentIntent.ID)
	}
	var payerName func(context.Context) string
	var email string
	if cd := s.CustomerDetails; cd != nil {
		email = cd.Email
		if name := strings.TrimSpace(cd.Name); name != "" {
			payerName = func(context.Context) string { return name }
		}
	}
	// The money is taken: record it even if the request's context has ended.
	wctx, cancel := settleContext(ctx)
	defer cancel()
	if err := h.orders.markAttemptCaptured(wctx, a, time.Now(), payerName); err != nil {
		return err
	}
	h.orders.backfillGuestEmail(wctx, a, email)
	brand, wallet := pi.CardBrandAndWallet()
	h.orders.recordAttemptCard(wctx, a.ID, brand, wallet)
	return nil
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
//
// The error is the database write that records the outcome failing; a
// provider call that fails is one of the outcomes above, not an error.
func (h *StripeWebhookHandler) afterFailedCapture(ctx context.Context, a paymentAttempt, piID string) error {
	// The provider calls keep the request's context; the writes that record
	// their outcome do not, as for a successful capture. Each gets its own
	// bounded context, started after the provider call it follows.
	release := func() error {
		wctx, cancel := settleContext(ctx)
		defer cancel()
		return h.orders.releaseAuthorization(wctx, a)
	}
	pi, err := h.stripe.GetPaymentIntent(ctx, piID)
	if err != nil {
		h.log.Error().Err(err).Str("attempt_id", a.ID).Msg("webhook completed: payment intent lookup after failed capture failed")
		return release()
	}
	switch pi.Status {
	case stripe.PaymentIntentSucceeded:
		wctx, cancel := settleContext(ctx)
		defer cancel()
		if err := h.settleSucceededIntent(wctx, a, pi, time.Now()); err != nil {
			return err
		}
		brand, wallet := pi.CardBrandAndWallet()
		h.orders.recordAttemptCard(wctx, a.ID, brand, wallet)
		return nil
	case stripe.PaymentIntentRequiresCapture:
		if err := h.stripe.CancelPaymentIntent(ctx, piID); err != nil {
			h.log.Error().Err(err).Str("attempt_id", a.ID).Msg("webhook completed: void after failed capture failed")
			return release()
		}
		wctx, cancel := settleContext(ctx)
		defer cancel()
		return h.orders.markAttemptFailed(wctx, a)
	default:
		return release()
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

func (h *StripeWebhookHandler) onSessionFailed(ctx context.Context, clubID string, evt *stripe.Event) error {
	s, ok := h.decodeSession(evt)
	if !ok {
		return nil
	}
	a, ok, err := h.sessionAttempt(ctx, clubID, evt, s)
	if err != nil || !ok {
		return err
	}
	return h.failAttempt(ctx, a)
}

func (h *StripeWebhookHandler) onIntentCanceled(ctx context.Context, clubID string, evt *stripe.Event) error {
	a, _, ok, err := h.intentAttempt(ctx, clubID, evt)
	if err != nil || !ok {
		return err
	}
	return h.failAttempt(ctx, a)
}

// onIntentSucceeded settles an attempt whose capture went through. Normally
// the capture call already did; this covers a capture whose answer was lost.
func (h *StripeWebhookHandler) onIntentSucceeded(ctx context.Context, clubID string, evt *stripe.Event) error {
	a, pi, ok, err := h.intentAttempt(ctx, clubID, evt)
	if err != nil || !ok {
		return err
	}
	return h.settleSucceededIntent(ctx, a, pi, time.Unix(evt.Created, 0))
}

// settleSucceededIntent pays the order only when the full amount was
// captured: a partial capture (made by hand in the Dashboard) does not
// settle what is owed and is left for the board to resolve (not an error: a
// redelivery would read the same amount).
func (h *StripeWebhookHandler) settleSucceededIntent(ctx context.Context, a paymentAttempt, pi *stripe.PaymentIntent, capturedAt time.Time) error {
	if !amountMatches(a, pi.AmountReceived, pi.Currency) {
		h.log.Error().Str("attempt_id", a.ID).Str("payment_intent", pi.ID).
			Int64("amount_received", pi.AmountReceived).Str("currency", pi.Currency).
			Float64("attempt_amount", a.Amount).Str("attempt_currency", a.Currency).
			Msg("webhook: captured amount does not match the attempt, not marking paid")
		return nil
	}
	return h.orders.markAttemptCaptured(ctx, a, capturedAt, nil)
}

// failAttempt marks an attempt failed unless Brygge already captured or
// voided it: the cancellation Brygge itself caused arrives as an event too,
// and must not fail an order another attempt is paying.
func (h *StripeWebhookHandler) failAttempt(ctx context.Context, a paymentAttempt) error {
	if a.Status == "captured" || a.Status == "cancelled" {
		return nil
	}
	return h.orders.markAttemptFailed(ctx, a)
}

// intentAttempt resolves a PaymentIntent event to its attempt through the
// attempt id Brygge put in the PaymentIntent's metadata. A failed lookup is
// logged and returned so the webhook answers 5xx.
func (h *StripeWebhookHandler) intentAttempt(ctx context.Context, clubID string, evt *stripe.Event) (paymentAttempt, *stripe.PaymentIntent, bool, error) {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(evt.Data.Object, &pi); err != nil {
		h.log.Warn().Err(err).Str("event_id", evt.ID).Msg("webhook: undecodable payment intent")
		return paymentAttempt{}, nil, false, nil
	}
	a, ok, err := h.orders.attemptByID(ctx, clubID, stripe.ProviderName, pi.Metadata["attempt_id"])
	if err != nil {
		h.log.Error().Err(err).Str("event_id", evt.ID).Msg("webhook: attempt lookup failed")
	}
	return a, &pi, ok, err
}

func (h *StripeWebhookHandler) decodeSession(evt *stripe.Event) (*stripe.CheckoutSession, bool) {
	var s stripe.CheckoutSession
	if err := json.Unmarshal(evt.Data.Object, &s); err != nil || s.ID == "" {
		h.log.Warn().Err(err).Str("event_id", evt.ID).Msg("webhook: undecodable checkout session")
		return nil, false
	}
	return &s, true
}
