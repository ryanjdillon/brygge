package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/stripe"
)

// Card disputes (charge.dispute.* events). The webhook keeps payment_disputes
// up to date and tells the treasurer about a new dispute; it writes nothing
// else. The loss and its reversal are booked from the dispute's balance
// transactions by the Stripe booking pass (ACC-4, ACC-5), and the order and
// faktura are never touched.

// disputeNoticeTimeout bounds the treasurer's email, which is sent while the
// event is being answered.
const disputeNoticeTimeout = 10 * time.Second

// stripeDashboardURL links to a page of the Stripe Dashboard, in test mode
// when the configured key is a test key.
func stripeDashboardURL(cfg *config.Config, path string) string {
	return stripe.DashboardURL(cfg.StripeSecretKey, path)
}

// disputeContext is what the webhook worked out about a dispute event before
// it stored the event: the dispute as the payload gave it and the payment it
// is about. clubID is empty when nothing tied the dispute to a club.
type disputeContext struct {
	dispute            stripe.Dispute
	clubID             string
	attemptID, orderID string
	foreign            bool
	// lookupFailed is set when the PaymentIntent could not be read (not
	// when it does not exist): the dispute may be another deployment's, or
	// ours with an attempt not yet found.
	lookupFailed bool
}

// resolveDispute finds the payment a dispute event is about. Stripe's dispute
// carries no metadata of Brygge's, so the attempt comes from the dispute's
// PaymentIntent (the attempt id and club slug Brygge put in its metadata),
// and failing that from the stored charge, by its charge id. A dispute on a
// payment another deployment sharing the Stripe account created is foreign
// and left alone.
func (h *StripeWebhookHandler) resolveDispute(ctx context.Context, evt *stripe.Event) *disputeContext {
	var d stripe.Dispute
	if err := json.Unmarshal(evt.Data.Object, &d); err != nil || d.ID == "" {
		h.log.Warn().Err(err).Str("event_id", evt.ID).Msg("webhook: undecodable dispute")
		return nil
	}
	dc := &disputeContext{dispute: d}

	if d.PaymentIntent != "" && h.stripe != nil {
		pi, err := h.stripe.GetPaymentIntent(ctx, d.PaymentIntent)
		var apiErr *stripe.APIError
		switch {
		case errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound:
			// Not in this Stripe account: nothing to learn from it.
		case err != nil:
			dc.lookupFailed = true
			h.log.Warn().Err(err).Str("dispute_id", d.ID).Msg("webhook dispute: payment intent lookup failed")
		default:
			// An attempt of ours settles it, whatever slug the payment
			// carries: a club's slug can be renamed.
			h.claimAttempt(ctx, dc, pi.Metadata["attempt_id"])
			if dc.attemptID == "" && foreignSlug(pi.Metadata["club_slug"], h.cfg.ClubSlug) {
				dc.foreign = true
				return dc
			}
		}
	}
	if dc.attemptID == "" {
		h.disputeFromCharge(ctx, dc)
	}
	return dc
}

// foreignSlug reports whether a club slug in a payment's metadata names a
// deployment other than this one.
func foreignSlug(metadataSlug, ours string) bool {
	return metadataSlug != "" && ours != "" && !strings.EqualFold(metadataSlug, ours)
}

// claimAttempt takes the attempt, its order and its club from an attempt id
// Brygge put in a payment's metadata, if it names one.
func (h *StripeWebhookHandler) claimAttempt(ctx context.Context, dc *disputeContext, attemptID string) {
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
			h.log.Warn().Err(err).Str("dispute_id", dc.dispute.ID).Msg("webhook dispute: attempt lookup failed")
		}
		return
	}
	dc.clubID, dc.attemptID, dc.orderID = club, attemptID, order
}

// disputeFromCharge ties a dispute to the stored charge it is about, which
// knows its attempt when the booking ingest could classify it.
func (h *StripeWebhookHandler) disputeFromCharge(ctx context.Context, dc *disputeContext) {
	d := dc.dispute
	if d.Charge == "" && d.PaymentIntent == "" {
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
		d.Charge, d.PaymentIntent,
	).Scan(&club, &attempt, &order)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			h.log.Warn().Err(err).Str("dispute_id", d.ID).Msg("webhook dispute: charge lookup failed")
		}
		return
	}
	dc.clubID = club
	if attempt != nil {
		dc.attemptID = *attempt
	}
	if order != nil {
		dc.orderID = *order
	}
}

// onDispute stores a dispute event and tells the club's treasurer when
// payments.UpsertDispute says a phase of the dispute is new. A failure to
// store is returned, and so is a dispute that cannot be placed because
// Stripe could not be asked about its payment (it may be another
// deployment's, or ours with its refund block missing): the event is
// answered with an error and Stripe delivers it again. The email is best
// effort and never fails the event.
func (h *StripeWebhookHandler) onDispute(ctx context.Context, clubID string, evt *stripe.Event, dc *disputeContext) error {
	if dc == nil || dc.dispute.ID == "" {
		return nil
	}
	if dc.foreign {
		h.log.Info().Str("dispute_id", dc.dispute.ID).Msg("webhook dispute: payment of another deployment, ignoring")
		return nil
	}
	if dc.lookupFailed && dc.attemptID == "" {
		return errors.New("dispute's payment could not be looked up")
	}
	d := dc.dispute
	var due *time.Time
	if d.EvidenceDueBy > 0 {
		t := time.Unix(d.EvidenceDueBy, 0).UTC()
		due = &t
	}
	rec := payments.Dispute{
		ClubID: clubID, Provider: stripe.ProviderName, ProviderDisputeID: d.ID,
		AttemptID: dc.attemptID, OrderID: dc.orderID,
		ChargeRef: d.Charge, PaymentRef: d.PaymentIntent,
		AmountOre: d.Amount, Currency: d.Currency, Reason: d.Reason, Status: d.Status, EvidenceDueBy: due,
		ChargeRefundable: d.ChargeRefundable,
	}
	notify, err := payments.UpsertDispute(ctx, h.db, rec)
	if err != nil {
		h.log.Error().Err(err).Str("dispute_id", d.ID).Str("event_id", evt.ID).Msg("webhook dispute: storing failed")
		return err
	}
	if notify {
		h.notifyTreasurer(ctx, rec)
	}
	return nil
}

// notifyTreasurer emails the club's treasurer that a card payment is
// disputed. It has no effect without an address or a mail transport, and a
// failure is only logged.
func (h *StripeWebhookHandler) notifyTreasurer(ctx context.Context, d payments.Dispute) {
	log := h.log.With().Str("dispute_id", d.ProviderDisputeID).Logger()
	if h.mailer == nil {
		log.Info().Msg("webhook dispute: no mail transport, treasurer not told")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), disputeNoticeTimeout)
	defer cancel()

	var clubName, to string
	if err := h.db.QueryRow(ctx,
		`SELECT name, COALESCE(treasurer_email, '') FROM clubs WHERE id = $1`, d.ClubID,
	).Scan(&clubName, &to); err != nil {
		log.Warn().Err(err).Msg("webhook dispute: reading the club failed, treasurer not told")
		return
	}
	to = strings.TrimSpace(to)
	if to == "" {
		log.Info().Msg("webhook dispute: club has no treasurer email, treasurer not told")
		return
	}
	notice := email.DisputeNotice{
		ClubName:      clubName,
		AmountOre:     d.AmountOre,
		Reference:     h.disputeReference(ctx, d.OrderID),
		Reason:        d.Reason,
		EvidenceDueBy: d.EvidenceDueBy,
		DashboardURL:  stripeDashboardURL(h.cfg, disputeDashboardPath(d.ProviderDisputeID)),
	}
	if err := h.mailer.Send(ctx, to, email.DisputeSubject(d.EvidenceDueBy), email.DisputeBody(notice)); err != nil {
		log.Warn().Err(err).Msg("webhook dispute: email to the treasurer failed")
	}
}

// disputeDashboardPath is the Dashboard page of a dispute, or the list of
// disputes when the id is not one Stripe could have sent.
func disputeDashboardPath(id string) string {
	if !stripe.ValidDisputeID(id) {
		return "/disputes"
	}
	return "/disputes/" + url.PathEscape(id)
}

// disputeReference says what the disputed payment was for: the fakturas the
// order settles, else the order. Empty when the payment has no order.
func (h *StripeWebhookHandler) disputeReference(ctx context.Context, orderID string) string {
	if orderID == "" {
		return ""
	}
	rows, err := h.db.Query(ctx,
		`SELECT i.invoice_number FROM order_sources os
		   JOIN invoices i ON i.id = os.invoice_id
		  WHERE os.order_id = $1 ORDER BY i.invoice_number`, orderID)
	if err == nil {
		numbers, err := pgx.CollectRows(rows, pgx.RowTo[int])
		if err == nil && len(numbers) > 0 {
			parts := make([]string, len(numbers))
			for i, n := range numbers {
				parts[i] = fmt.Sprint(n)
			}
			if len(parts) == 1 {
				return "faktura " + parts[0]
			}
			return "faktura " + strings.Join(parts, ", ")
		}
	}
	if len(orderID) > 8 {
		orderID = orderID[:8]
	}
	return "ordre " + orderID
}
