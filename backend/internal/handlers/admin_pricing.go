package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type AdminPricingHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewAdminPricingHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *AdminPricingHandler {
	return &AdminPricingHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "admin_pricing").Logger(),
	}
}

func (h *AdminPricingHandler) HandleGetPricing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var configJSON json.RawMessage
	err := h.db.QueryRow(ctx,
		`SELECT COALESCE(config->'pricing', '{}')
		 FROM clubs WHERE slug = $1`,
		h.config.ClubSlug,
	).Scan(&configJSON)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "club not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query pricing config")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"pricing":`))
	w.Write(configJSON)
	w.Write([]byte(`}`))
}

func (h *AdminPricingHandler) HandleUpdatePricing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var pricing json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&pricing); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var clubID string
	var oldConfig json.RawMessage
	err = tx.QueryRow(ctx,
		`SELECT id, COALESCE(config->'pricing', '{}')
		 FROM clubs WHERE slug = $1 FOR UPDATE`,
		claims.ClubID,
	).Scan(&clubID, &oldConfig)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "club not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query club config")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, err = tx.Exec(ctx,
		`UPDATE clubs SET config = jsonb_set(COALESCE(config, '{}'), '{pricing}', $1), updated_at = now()
		 WHERE id = $2`,
		pricing, clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update pricing config")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, user_id, action, entity_type, entity_id, old_data, new_data)
		 VALUES ($1, $2, 'update_pricing', 'club', $1, $3, $4)`,
		clubID, claims.UserID, oldConfig, pricing,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to write audit log")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info().Str("actor", claims.UserID).Msg("pricing updated")

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
