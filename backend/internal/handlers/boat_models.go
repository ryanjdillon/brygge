package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

type BoatModelsHandler struct {
	db  *pgxpool.Pool
	log zerolog.Logger
}

func NewBoatModelsHandler(db *pgxpool.Pool, log zerolog.Logger) *BoatModelsHandler {
	return &BoatModelsHandler{
		db:  db,
		log: log.With().Str("handler", "boat_models").Logger(),
	}
}

type boatModel struct {
	ID           string    `json:"id"`
	Manufacturer string    `json:"manufacturer"`
	Model        string    `json:"model"`
	YearFrom     *int      `json:"year_from,omitempty"`
	YearTo       *int      `json:"year_to,omitempty"`
	LengthM      *float64  `json:"length_m,omitempty"`
	BeamM        *float64  `json:"beam_m,omitempty"`
	DraftM       *float64  `json:"draft_m,omitempty"`
	WeightKg     *float64  `json:"weight_kg,omitempty"`
	BoatType     string    `json:"boat_type"`
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
}

func (h *BoatModelsHandler) HandleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		JSON(w, http.StatusOK, []boatModel{})
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, manufacturer, model, year_from, year_to,
		        length_m, beam_m, draft_m, weight_kg, boat_type, source, created_at
		 FROM boat_models
		 WHERE manufacturer ILIKE $1 OR model ILIKE $1
		    OR to_tsvector('simple', manufacturer || ' ' || model) @@ to_tsquery('simple', regexp_replace(trim($2), '\s+', ':* & ', 'g') || ':*')
		 ORDER BY manufacturer, model
		 LIMIT 20`,
		q+"%", q,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to search boat models")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	models := make([]boatModel, 0)
	for rows.Next() {
		var m boatModel
		if err := rows.Scan(
			&m.ID, &m.Manufacturer, &m.Model, &m.YearFrom, &m.YearTo,
			&m.LengthM, &m.BeamM, &m.DraftM, &m.WeightKg,
			&m.BoatType, &m.Source, &m.CreatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan boat model")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		models = append(models, m)
	}

	JSON(w, http.StatusOK, models)
}

type unconfirmedBoat struct {
	ID                 string   `json:"id"`
	UserID             string   `json:"user_id"`
	OwnerName          string   `json:"owner_name"`
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Manufacturer       string   `json:"manufacturer"`
	Model              string   `json:"model"`
	LengthM            *float64 `json:"length_m,omitempty"`
	BeamM              *float64 `json:"beam_m,omitempty"`
	DraftM             *float64 `json:"draft_m,omitempty"`
	WeightKg           *float64 `json:"weight_kg,omitempty"`
	RegistrationNumber string   `json:"registration_number"`
	BoatModelID        *string  `json:"boat_model_id,omitempty"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
}

func (h *BoatModelsHandler) HandleListUnconfirmed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT b.id, b.user_id, u.full_name, b.name, b.type, b.manufacturer, b.model,
		        b.length_m, b.beam_m, b.draft_m, b.weight_kg,
		        b.registration_number, b.boat_model_id,
		        b.created_at, b.updated_at
		 FROM boats b
		 JOIN users u ON u.id = b.user_id
		 WHERE b.club_id = $1 AND b.measurements_confirmed = false
		 ORDER BY b.updated_at DESC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list unconfirmed boats")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	boats := make([]unconfirmedBoat, 0)
	for rows.Next() {
		var b unconfirmedBoat
		var createdAt, updatedAt time.Time
		if err := rows.Scan(
			&b.ID, &b.UserID, &b.OwnerName, &b.Name, &b.Type, &b.Manufacturer, &b.Model,
			&b.LengthM, &b.BeamM, &b.DraftM, &b.WeightKg,
			&b.RegistrationNumber, &b.BoatModelID,
			&createdAt, &updatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan unconfirmed boat")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		b.CreatedAt = createdAt.Format(time.RFC3339)
		b.UpdatedAt = updatedAt.Format(time.RFC3339)
		boats = append(boats, b)
	}

	JSON(w, http.StatusOK, boats)
}

type adminConfirmBoatRequest struct {
	LengthM     *float64 `json:"length_m,omitempty"`
	BeamM       *float64 `json:"beam_m,omitempty"`
	DraftM      *float64 `json:"draft_m,omitempty"`
	WeightKg    *float64 `json:"weight_kg,omitempty"`
	AddToModels bool     `json:"add_to_models"`
}

func (h *BoatModelsHandler) HandleConfirmBoat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	boatID := chi.URLParam(r, "boatID")
	if boatID == "" {
		Error(w, http.StatusBadRequest, "missing boat ID")
		return
	}

	var req adminConfirmBoatRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			Error(w, http.StatusBadRequest, "invalid request body")
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

	// Apply optional dimension adjustments
	if req.LengthM != nil {
		_, _ = tx.Exec(ctx, `UPDATE boats SET length_m = $2 WHERE id = $1`, boatID, *req.LengthM)
	}
	if req.BeamM != nil {
		_, _ = tx.Exec(ctx, `UPDATE boats SET beam_m = $2 WHERE id = $1`, boatID, *req.BeamM)
	}
	if req.DraftM != nil {
		_, _ = tx.Exec(ctx, `UPDATE boats SET draft_m = $2 WHERE id = $1`, boatID, *req.DraftM)
	}
	if req.WeightKg != nil {
		_, _ = tx.Exec(ctx, `UPDATE boats SET weight_kg = $2 WHERE id = $1`, boatID, *req.WeightKg)
	}

	tag, err := tx.Exec(ctx,
		`UPDATE boats
		 SET measurements_confirmed = true, confirmed_by = $2, confirmed_at = now(), updated_at = now()
		 WHERE id = $1 AND club_id = $3`,
		boatID, claims.UserID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to confirm boat")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "boat not found")
		return
	}

	if req.AddToModels {
		var manufacturer, model, boatType string
		var lengthM, beamM, draftM, weightKg *float64
		_ = tx.QueryRow(ctx,
			`SELECT manufacturer, model, type, length_m, beam_m, draft_m, weight_kg FROM boats WHERE id = $1`,
			boatID,
		).Scan(&manufacturer, &model, &boatType, &lengthM, &beamM, &draftM, &weightKg)

		if manufacturer != "" && model != "" {
			_, _ = tx.Exec(ctx,
				`INSERT INTO boat_models (manufacturer, model, length_m, beam_m, draft_m, weight_kg, boat_type, source)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, 'club-confirmed')
				 ON CONFLICT DO NOTHING`,
				manufacturer, model, lengthM, beamM, draftM, weightKg, boatType,
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit confirm transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "confirm_boat", "boat", boatID,
		nil, map[string]any{"confirmed": true, "add_to_models": req.AddToModels},
	); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}
