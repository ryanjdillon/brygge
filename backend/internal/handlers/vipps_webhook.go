package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// VippsWebhookHandler receives ePayment lifecycle events from Vipps.
// The endpoint carries no session; each request is authenticated by verifying
// the Vipps HMAC-SHA256 signature over the body against the registration secret.
type VippsWebhookHandler struct {
	db    *pgxpool.Pool
	cfg   *config.Config
	vipps *vipps.Client // nil when the instance has no Vipps credentials
	// canceller voids authorizations that must not be captured; nil without
	// Vipps credentials.
	canceller payments.Canceller
	orders    orderSettlement
	// failures counts the 5xx answers by event name.
	failures *payments.WebhookFailures
	log      zerolog.Logger
}

func NewVippsWebhookHandler(db *pgxpool.Pool, cfg *config.Config, vippsClient *vipps.Client, log zerolog.Logger) *VippsWebhookHandler {
	log = log.With().Str("handler", "vipps_webhook").Logger()
	h := &VippsWebhookHandler{
		db:     db,
		cfg:    cfg,
		vipps:  vippsClient,
		orders: orderSettlement{db: db, log: log},
		log:    log,

		failures: payments.NewWebhookFailures(vipps.ProviderName, vipps.WebhookEventNames),
	}
	if vippsClient != nil {
		h.canceller = vipps.NewPaymentProvider(vippsClient)
	}
	return h
}

// Handle is the POST /api/v1/vipps/webhooks handler.
func (h *VippsWebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	evt, raw, err := vipps.ParseAndVerify(r, h.cfg.VippsWebhookSecret)
	if err != nil {
		h.log.Warn().Err(err).Msg("webhook rejected")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// idempotencyKey is optional per Vipps ("some webhooks may not supply this
	// value"), so fall back to pspReference — which is unique per operation —
	// as the dedup key. Only drop when neither is present.
	eventID := evt.IdempotencyKey
	if eventID == "" {
		eventID = evt.PSPReference
	}
	if eventID == "" {
		h.log.Warn().Str("event_name", evt.Name).Str("reference", evt.Reference).
			Msg("webhook missing idempotencyKey and pspReference — skipping")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Resolve club_id from the reference: a checkout-order payment attempt
	// (checkout/QR/faktura pay link) or an invoice (BRY-8 faktura payment
	// link). Fall back to the instance-level club when the reference is
	// unrecognised.
	clubID, err := h.resolveClub(ctx, evt.Reference)
	if err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook: club lookup failed")
		h.internalError(w, r, evt.Name)
		return
	}
	if clubID == "" {
		h.log.Warn().Str("reference", evt.Reference).Msg("webhook: could not resolve club_id — dropping")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Idempotent insert — ignore on conflict so replayed events are safe.
	_, err = h.db.Exec(ctx,
		`INSERT INTO vipps_webhook_events (club_id, event_id, event_name, reference, payload)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (club_id, event_id) DO NOTHING`,
		clubID, eventID, evt.Name, evt.Reference, json.RawMessage(raw),
	)
	if err != nil {
		h.log.Error().Err(err).Str("event_id", eventID).Msg("webhook: failed to persist event")
		h.internalError(w, r, evt.Name)
		return
	}

	h.log.Info().
		Str("event_name", evt.Name).
		Str("reference", evt.Reference).
		Str("club_id", clubID).
		Msg("vipps webhook received")

	if err := h.dispatch(ctx, clubID, evt); err != nil {
		h.internalError(w, r, evt.Name)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// internalError answers 5xx, so Vipps delivers the event again, and counts it.
func (h *VippsWebhookHandler) internalError(w http.ResponseWriter, r *http.Request, eventName string) {
	h.failures.Record(r.Context(), eventName)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// resolveClub finds the club of the attempt or invoice the reference belongs
// to, falling back to the instance club for a reference that is neither. A
// failed lookup is an error, not a fall-through: the event would otherwise be
// stored and dispatched under the wrong club and never reach its attempt.
// The result is "" when not even the instance club exists.
func (h *VippsWebhookHandler) resolveClub(ctx context.Context, reference string) (string, error) {
	return firstClub(ctx, h.db, []clubLookup{
		{`SELECT club_id FROM payment_attempts WHERE provider = $1 AND provider_reference = $2`, []any{vipps.ProviderName, reference}},
		{`SELECT club_id FROM invoices WHERE vipps_payment_reference = $1 LIMIT 1`, []any{reference}},
		{`SELECT id FROM clubs WHERE slug = $1 LIMIT 1`, []any{h.cfg.ClubSlug}},
	})
}
