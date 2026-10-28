package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/stripe"
)

// StripeWebhookHandler receives Checkout and PaymentIntent events from
// Stripe. The endpoint carries no session; each request is authenticated by
// the Stripe-Signature HMAC over the body.
type StripeWebhookHandler struct {
	db     *pgxpool.Pool
	cfg    *config.Config
	stripe *stripe.Client // nil when the instance has no Stripe credentials
	// canceller voids authorizations that must not be captured; nil without
	// Stripe credentials.
	canceller payments.Canceller
	orders    orderSettlement
	log       zerolog.Logger
}

// NewStripeWebhookHandler returns the webhook handler; client is nil when
// Stripe is not configured, and then every event is still verified and
// stored but none is acted on.
func NewStripeWebhookHandler(db *pgxpool.Pool, cfg *config.Config, client *stripe.Client, log zerolog.Logger) *StripeWebhookHandler {
	log = log.With().Str("handler", "stripe_webhook").Logger()
	h := &StripeWebhookHandler{
		db:     db,
		cfg:    cfg,
		stripe: client,
		orders: orderSettlement{db: db, log: log},
		log:    log,
	}
	if client != nil {
		h.canceller = stripe.NewPaymentProvider(client, cfg.ClubSlug)
	}
	return h
}

// stripeEventObject is what every event object Brygge records shares: its
// id, its kind, the PaymentIntent it belongs to (charges and disputes) and
// the metadata Brygge set (sessions and PaymentIntents).
type stripeEventObject struct {
	ID            string                   `json:"id"`
	Object        string                   `json:"object"`
	PaymentIntent *stripe.PaymentIntentRef `json:"payment_intent"`
	Metadata      map[string]string        `json:"metadata"`
}

// eventRefs returns the reference an event is stored under (the session id
// for sessions, otherwise the PaymentIntent id) and the attempt id Brygge put
// in its metadata, if any.
func eventRefs(evt *stripe.Event) (reference, attemptID string) {
	var obj stripeEventObject
	if err := json.Unmarshal(evt.Data.Object, &obj); err != nil {
		return "", ""
	}
	reference = obj.ID
	if obj.Object != "checkout.session" && obj.PaymentIntent != nil && obj.PaymentIntent.ID != "" {
		reference = obj.PaymentIntent.ID
	}
	return reference, obj.Metadata["attempt_id"]
}

// Handle is the POST /api/v1/stripe/webhooks handler. Every verified event is
// stored once and then dispatched, also when it was already stored: all
// settlement writes are guarded, so a redelivery is a no-op, and a dispatch
// that failed part-way gets another chance. Only a failure to store answers
// 5xx, so Stripe retries; anything Brygge does not act on is 200.
func (h *StripeWebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	evt, raw, err := stripe.ParseAndVerify(r, h.cfg.StripeWebhookSecret)
	if err != nil {
		h.log.Warn().Err(err).Msg("webhook rejected")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	reference, attemptID := eventRefs(evt)
	clubID := h.resolveClub(ctx, reference, attemptID)
	if clubID == "" {
		h.log.Warn().Str("event_id", evt.ID).Str("reference", reference).Msg("webhook: could not resolve club_id — dropping")
		w.WriteHeader(http.StatusOK)
		return
	}

	if _, err := h.db.Exec(ctx,
		`INSERT INTO payment_webhook_events (club_id, provider, event_id, event_name, reference, payload)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (club_id, provider, event_id) DO NOTHING`,
		clubID, stripe.ProviderName, evt.ID, evt.Type, reference, json.RawMessage(raw),
	); err != nil {
		h.log.Error().Err(err).Str("event_id", evt.ID).Msg("webhook: failed to persist event")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.log.Info().
		Str("event_type", evt.Type).
		Str("event_id", evt.ID).
		Str("reference", reference).
		Str("club_id", clubID).
		Msg("stripe webhook received")

	h.dispatch(ctx, clubID, evt)

	w.WriteHeader(http.StatusOK)
}

// resolveClub finds the club of the attempt the event belongs to, by
// reference or by the attempt id in its metadata, falling back to the
// instance club for events about payments Brygge did not create.
func (h *StripeWebhookHandler) resolveClub(ctx context.Context, reference, attemptID string) string {
	var clubID string
	if reference != "" {
		if err := h.db.QueryRow(ctx,
			`SELECT club_id FROM payment_attempts WHERE provider = $1 AND provider_reference = $2`,
			stripe.ProviderName, reference,
		).Scan(&clubID); err == nil {
			return clubID
		}
	}
	if _, err := uuid.Parse(attemptID); err == nil {
		if err := h.db.QueryRow(ctx,
			`SELECT club_id FROM payment_attempts WHERE provider = $1 AND id = $2`,
			stripe.ProviderName, attemptID,
		).Scan(&clubID); err == nil {
			return clubID
		}
	}
	if err := h.db.QueryRow(ctx,
		`SELECT id FROM clubs WHERE slug = $1 LIMIT 1`, h.cfg.ClubSlug,
	).Scan(&clubID); err != nil {
		return ""
	}
	return clubID
}
