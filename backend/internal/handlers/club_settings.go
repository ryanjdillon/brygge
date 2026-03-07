package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type ClubSettingsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewClubSettingsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *ClubSettingsHandler {
	return &ClubSettingsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "club_settings").Logger(),
	}
}

type clubSetting struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type updateSettingsRequest struct {
	Settings map[string]json.RawMessage `json:"settings"`
}

func (h *ClubSettingsHandler) HandleGetBookingSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT key, value, updated_at FROM club_settings WHERE club_id = $1 ORDER BY key`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list club settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	settings := make(map[string]json.RawMessage)
	for rows.Next() {
		var s clubSetting
		if err := rows.Scan(&s.Key, &s.Value, &s.UpdatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan setting")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		settings[s.Key] = s.Value
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, settings)
}

func (h *ClubSettingsHandler) HandleUpdateBookingSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req updateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Settings) == 0 {
		Error(w, http.StatusBadRequest, "settings map is required")
		return
	}

	allowedKeys := map[string]bool{
		"hoist_slot_duration_minutes": true,
		"hoist_open_hour":             true,
		"hoist_close_hour":            true,
		"hoist_max_consecutive_slots": true,
		"slip_share_rebate_pct":       true,
		"season_summer_start":         true,
		"season_summer_end":           true,
		"season_winter_start":         true,
		"season_winter_end":           true,
	}

	for key := range req.Settings {
		if !allowedKeys[key] {
			Error(w, http.StatusBadRequest, "unknown setting key: "+key)
			return
		}
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	for key, value := range req.Settings {
		_, err := tx.Exec(ctx,
			`INSERT INTO club_settings (club_id, key, value, updated_at)
			 VALUES ($1, $2, $3, now())
			 ON CONFLICT (club_id, key) DO UPDATE SET value = $3, updated_at = now()`,
			claims.ClubID, key, value,
		)
		if err != nil {
			h.log.Error().Err(err).Str("key", key).Msg("failed to upsert setting")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "update_booking_settings", "club_settings", claims.ClubID, nil, req.Settings); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
