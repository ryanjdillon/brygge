package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// VippsWebhookHandler receives ePayment lifecycle events from Vipps.
// The endpoint carries no session; each request is authenticated by verifying
// the Vipps HMAC-SHA256 signature over the body against the registration secret.
type VippsWebhookHandler struct {
	db     *pgxpool.Pool
	cfg    *config.Config
	vipps  *vipps.Client // nil when the instance has no Vipps credentials
	orders orderSettlement
	log    zerolog.Logger
}

func NewVippsWebhookHandler(db *pgxpool.Pool, cfg *config.Config, vippsClient *vipps.Client, log zerolog.Logger) *VippsWebhookHandler {
	log = log.With().Str("handler", "vipps_webhook").Logger()
	return &VippsWebhookHandler{
		db:     db,
		cfg:    cfg,
		vipps:  vippsClient,
		orders: orderSettlement{db: db, log: log},
		log:    log,
	}
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

	// Resolve club_id from the reference: an order (checkout/QR) or an invoice
	// (BRY-8 faktura payment link). Fall back to the instance-level club when
	// the reference is unrecognised.
	var clubID string
	err = h.db.QueryRow(ctx,
		`SELECT club_id FROM orders WHERE vipps_reference = $1 LIMIT 1`,
		evt.Reference,
	).Scan(&clubID)
	if err != nil {
		err = h.db.QueryRow(ctx,
			`SELECT club_id FROM invoices WHERE vipps_payment_reference = $1 LIMIT 1`,
			evt.Reference,
		).Scan(&clubID)
	}
	if err != nil {
		// Reference matched neither — use the instance club.
		_ = h.db.QueryRow(ctx,
			`SELECT id FROM clubs WHERE slug = $1 LIMIT 1`,
			h.cfg.ClubSlug,
		).Scan(&clubID)
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
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.log.Info().
		Str("event_name", evt.Name).
		Str("reference", evt.Reference).
		Str("club_id", clubID).
		Msg("vipps webhook received")

	h.dispatch(ctx, clubID, evt)

	w.WriteHeader(http.StatusOK)
}
