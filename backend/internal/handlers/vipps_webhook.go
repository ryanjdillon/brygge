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
	log    zerolog.Logger
}

func NewVippsWebhookHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *VippsWebhookHandler {
	return &VippsWebhookHandler{
		db:  db,
		cfg: cfg,
		log: log.With().Str("handler", "vipps_webhook").Logger(),
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

	if evt.IdempotencyKey == "" {
		h.log.Warn().Str("event_name", evt.Name).Msg("webhook missing idempotency key — skipping")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Resolve club_id via the order reference (vipps_reference on orders table).
	// Fall back to the instance-level club when the reference is unrecognised
	// (e.g. QR payments not yet tied to an order).
	var clubID string
	err = h.db.QueryRow(ctx,
		`SELECT club_id FROM orders WHERE vipps_reference = $1 LIMIT 1`,
		evt.Reference,
	).Scan(&clubID)
	if err != nil {
		// Reference not found in orders yet — use the instance club.
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
		clubID, evt.IdempotencyKey, evt.Name, evt.Reference, json.RawMessage(raw),
	)
	if err != nil {
		h.log.Error().Err(err).Str("event_id", evt.IdempotencyKey).Msg("webhook: failed to persist event")
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
