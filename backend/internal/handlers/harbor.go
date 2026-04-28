package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type HarborHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewHarborHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *HarborHandler {
	return &HarborHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "harbor").Logger(),
	}
}

// geometry is a GeoJSON Geometry stored as raw JSON. We don't unmarshal
// because we just pass it through to the client and validate at the DB
// layer (CHECK constraints on slips.location and dock_fingers.geometry).
type rawJSON = json.RawMessage

type feature struct {
	Type       string                 `json:"type"`
	ID         string                 `json:"id,omitempty"`
	Geometry   rawJSON                `json:"geometry"`
	Properties map[string]interface{} `json:"properties"`
}

type featureCollection struct {
	Type     string    `json:"type"`
	Mode     string    `json:"mode"`
	Features []feature `json:"features"`
}

// HandleGetLayout returns the harbor layout as a GeoJSON FeatureCollection.
// Detail level scales with caller role:
//   - anonymous: counts/positions only (no occupant or boat)
//   - member: occupant last name + boat summary (length only)
//   - admin/board/harbor_master: full owner contact + boat detail
func (h *HarborHandler) HandleGetLayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)

	var clubID string
	if claims != nil && claims.ClubID != "" {
		clubID = claims.ClubID
	} else {
		if err := h.db.QueryRow(ctx,
			`SELECT id FROM clubs WHERE slug = $1`, h.config.ClubSlug,
		).Scan(&clubID); err != nil {
			h.log.Error().Err(err).Msg("failed to resolve club id")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	mode := "public"
	canSeeDetail := false
	if claims != nil {
		mode = "member"
		for _, role := range claims.Roles {
			if role == "admin" || role == "board" || role == "harbor_master" {
				mode = "admin"
				canSeeDetail = true
				break
			}
		}
	}

	features := []feature{}

	// Dock fingers (LineStrings)
	fingerRows, err := h.db.Query(ctx,
		`SELECT id, geometry, position
		   FROM dock_fingers
		  WHERE club_id = $1
		  ORDER BY position, id`, clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query dock fingers")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	for fingerRows.Next() {
		var id string
		var geom rawJSON
		var position int
		if err := fingerRows.Scan(&id, &geom, &position); err != nil {
			fingerRows.Close()
			h.log.Error().Err(err).Msg("failed to scan dock finger")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		features = append(features, feature{
			Type:     "Feature",
			ID:       id,
			Geometry: geom,
			Properties: map[string]interface{}{
				"kind":     "finger",
				"position": position,
			},
		})
	}
	fingerRows.Close()

	// Slips (Points). Includes unplaced slips with null geometry so the
	// editor can list them in a queue for placement.
	slipRows, err := h.db.Query(ctx,
		`SELECT s.id, s.number, s.section, s.status,
		        s.length_m, s.width_m, s.location,
		        sa.assignment_type,
		        sa.user_id, u.first_name, u.last_name, u.email,
		        b.id, b.name, b.length_m, b.beam_m
		   FROM slips s
		   LEFT JOIN slip_assignments sa
		     ON sa.slip_id = s.id AND sa.released_at IS NULL
		   LEFT JOIN users u ON u.id = sa.user_id
		   LEFT JOIN boats b ON b.id = sa.boat_id
		  WHERE s.club_id = $1
		  ORDER BY s.section, s.number`, clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query slips")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	for slipRows.Next() {
		var id, number, section, status string
		var lengthM, widthM *float64
		var location *rawJSON
		var assignmentType, userID, firstName, lastName, email *string
		var boatID, boatName *string
		var boatLength, boatBeam *float64

		if err := slipRows.Scan(
			&id, &number, &section, &status,
			&lengthM, &widthM, &location,
			&assignmentType,
			&userID, &firstName, &lastName, &email,
			&boatID, &boatName, &boatLength, &boatBeam,
		); err != nil {
			slipRows.Close()
			h.log.Error().Err(err).Msg("failed to scan slip")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}

		props := map[string]interface{}{
			"kind":    "slip",
			"number":  number,
			"section": section,
			"status":  status,
		}
		if lengthM != nil {
			props["length_m"] = *lengthM
		}
		if widthM != nil {
			props["width_m"] = *widthM
		}
		if assignmentType != nil {
			props["assignment_type"] = *assignmentType
		}

		if mode != "public" && lastName != nil && *lastName != "" {
			props["occupant_last_name"] = *lastName
		}
		if canSeeDetail {
			if userID != nil {
				props["occupant_id"] = *userID
			}
			if firstName != nil || lastName != nil {
				full := strings.TrimSpace(deref(firstName) + " " + deref(lastName))
				if full != "" {
					props["occupant_name"] = full
				}
			}
			if email != nil {
				props["occupant_email"] = *email
			}
			if boatID != nil {
				props["boat_id"] = *boatID
			}
			if boatName != nil && *boatName != "" {
				props["boat_name"] = *boatName
			}
			if boatLength != nil {
				props["boat_length_m"] = *boatLength
			}
			if boatBeam != nil {
				props["boat_beam_m"] = *boatBeam
			}
		}

		var geom rawJSON
		if location != nil {
			geom = *location
		} else {
			geom = json.RawMessage("null")
		}

		features = append(features, feature{
			Type:       "Feature",
			ID:         id,
			Geometry:   geom,
			Properties: props,
		})
	}
	slipRows.Close()

	resp := featureCollection{
		Type:     "FeatureCollection",
		Mode:     mode,
		Features: features,
	}
	w.Header().Set("Cache-Control", "private, max-age=15")
	JSON(w, http.StatusOK, resp)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// HandlePutLayout accepts a GeoJSON FeatureCollection of fingers and
// slips (plus a `deleted_finger_ids` top-level array) and persists in
// a single transaction. Slip features only update the slip's location;
// slip rows themselves are managed via the existing admin slips API.
func (h *HarborHandler) HandlePutLayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	type putRequest struct {
		Type              string    `json:"type"`
		Features          []feature `json:"features"`
		DeletedFingerIDs  []string  `json:"deleted_finger_ids"`
	}
	var req putRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tx, err := h.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin tx")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Delete fingers explicitly listed.
	for _, id := range req.DeletedFingerIDs {
		if _, err := tx.Exec(ctx,
			`DELETE FROM dock_fingers WHERE id = $1 AND club_id = $2`,
			id, claims.ClubID,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to delete dock finger")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	for _, f := range req.Features {
		kind, _ := f.Properties["kind"].(string)
		switch kind {
		case "finger":
			position := 0
			if v, ok := f.Properties["position"].(float64); ok {
				position = int(v)
			}
			if f.ID == "" || isClientGeneratedID(f.ID) {
				if _, err := tx.Exec(ctx,
					`INSERT INTO dock_fingers (club_id, geometry, position)
					 VALUES ($1, $2, $3)`,
					claims.ClubID, []byte(f.Geometry), position,
				); err != nil {
					h.log.Error().Err(err).Msg("failed to insert dock finger")
					Error(w, http.StatusInternalServerError, "internal error")
					return
				}
			} else {
				if _, err := tx.Exec(ctx,
					`UPDATE dock_fingers
					    SET geometry = $1, position = $2, updated_at = $3
					  WHERE id = $4 AND club_id = $5`,
					[]byte(f.Geometry), position, time.Now(), f.ID, claims.ClubID,
				); err != nil {
					h.log.Error().Err(err).Msg("failed to update dock finger")
					Error(w, http.StatusInternalServerError, "internal error")
					return
				}
			}
		case "slip":
			if f.ID == "" {
				continue
			}
			// Geometry of "null" or missing → unplace the slip.
			var geom interface{}
			if len(f.Geometry) == 0 || string(f.Geometry) == "null" {
				geom = nil
			} else {
				geom = []byte(f.Geometry)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE slips SET location = $1, updated_at = $2
				  WHERE id = $3 AND club_id = $4`,
				geom, time.Now(), f.ID, claims.ClubID,
			); err != nil {
				h.log.Error().Err(err).Msg("failed to update slip location")
				Error(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit layout tx")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// isClientGeneratedID treats non-UUID feature IDs as new (the maplibre-
// gl-draw library auto-generates string IDs that are not valid UUIDs).
func isClientGeneratedID(id string) bool {
	// UUIDs are 36 chars with dashes at fixed positions; anything else is new.
	if len(id) != 36 {
		return true
	}
	if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return true
	}
	return false
}
