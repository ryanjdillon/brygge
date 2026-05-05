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

type dock struct {
	ID          string   `json:"id,omitempty"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	DefaultLng  *float64 `json:"default_lng"`
	DefaultLat  *float64 `json:"default_lat"`
	DefaultZoom *float64 `json:"default_zoom"`
	Position    int      `json:"position"`
}

type featureCollection struct {
	Type     string    `json:"type"`
	Mode     string    `json:"mode"`
	Features []feature `json:"features"`
	Docks    []dock    `json:"docks"`
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
		`SELECT id, geometry, position, notes
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
		var notes string
		if err := fingerRows.Scan(&id, &geom, &position, &notes); err != nil {
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
				"notes":    notes,
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
		        sa.user_id, u.first_name, u.last_name, u.email, u.phone,
		        u.hide_in_directory,
		        b.id, b.name, b.length_m, b.beam_m, b.manufacturer, b.model
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
		var assignmentType, userID, firstName, lastName, email, phone *string
		var hideInDirectory *bool
		var boatID, boatName, boatMfg, boatModel *string
		var boatLength, boatBeam *float64

		if err := slipRows.Scan(
			&id, &number, &section, &status,
			&lengthM, &widthM, &location,
			&assignmentType,
			&userID, &firstName, &lastName, &email, &phone,
			&hideInDirectory,
			&boatID, &boatName, &boatLength, &boatBeam, &boatMfg, &boatModel,
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

		// Member mode honors hide_in_directory; admin mode always sees the
		// occupant since admins need it for harbour operations.
		hidden := hideInDirectory != nil && *hideInDirectory
		if mode == "member" && !hidden && lastName != nil && *lastName != "" {
			props["occupant_last_name"] = *lastName
		} else if canSeeDetail && lastName != nil && *lastName != "" {
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
			if phone != nil && *phone != "" {
				props["occupant_phone"] = *phone
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
			if boatMfg != nil && *boatMfg != "" {
				props["boat_manufacturer"] = *boatMfg
			}
			if boatModel != nil && *boatModel != "" {
				props["boat_model"] = *boatModel
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

	// Auto-seed docks from distinct slip sections if none exist yet.
	var dockCount int
	if err := h.db.QueryRow(ctx,
		`SELECT count(*) FROM docks WHERE club_id = $1`, clubID,
	).Scan(&dockCount); err != nil {
		h.log.Error().Err(err).Msg("failed to count docks")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if dockCount == 0 {
		if _, err := h.db.Exec(ctx,
			`INSERT INTO docks (club_id, slug, name, position)
			 SELECT $1, section, section, ROW_NUMBER() OVER (ORDER BY section) - 1
			   FROM (SELECT DISTINCT section FROM slips
			          WHERE club_id = $1 AND section IS NOT NULL AND section <> '') s
			 ON CONFLICT (club_id, slug) DO NOTHING`,
			clubID,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to seed docks")
		}
	}

	docks := []dock{}
	dockRows, err := h.db.Query(ctx,
		`SELECT id, slug, name, default_lng, default_lat, default_zoom, position
		   FROM docks
		  WHERE club_id = $1
		  ORDER BY position, slug`, clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query docks")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	for dockRows.Next() {
		var d dock
		var zoom *float64
		if err := dockRows.Scan(&d.ID, &d.Slug, &d.Name, &d.DefaultLng, &d.DefaultLat, &zoom, &d.Position); err != nil {
			dockRows.Close()
			h.log.Error().Err(err).Msg("failed to scan dock")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		d.DefaultZoom = zoom
		docks = append(docks, d)
	}
	dockRows.Close()

	resp := featureCollection{
		Type:     "FeatureCollection",
		Mode:     mode,
		Features: features,
		Docks:    docks,
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
		Docks             []dock    `json:"docks"`
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
			notes, _ := f.Properties["notes"].(string)
			if f.ID == "" || isClientGeneratedID(f.ID) {
				if _, err := tx.Exec(ctx,
					`INSERT INTO dock_fingers (club_id, geometry, position, notes)
					 VALUES ($1, $2, $3, $4)`,
					claims.ClubID, []byte(f.Geometry), position, notes,
				); err != nil {
					h.log.Error().Err(err).Msg("failed to insert dock finger")
					Error(w, http.StatusInternalServerError, "internal error")
					return
				}
			} else {
				if _, err := tx.Exec(ctx,
					`UPDATE dock_fingers
					    SET geometry = $1, position = $2, notes = $3, updated_at = $4
					  WHERE id = $5 AND club_id = $6`,
					[]byte(f.Geometry), position, notes, time.Now(), f.ID, claims.ClubID,
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

	for _, d := range req.Docks {
		if d.Slug == "" {
			continue
		}
		name := d.Name
		if name == "" {
			name = d.Slug
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO docks (club_id, slug, name, default_lng, default_lat, default_zoom, position, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, now())
			 ON CONFLICT (club_id, slug)
			   DO UPDATE SET name = EXCLUDED.name,
			                 default_lng = EXCLUDED.default_lng,
			                 default_lat = EXCLUDED.default_lat,
			                 default_zoom = EXCLUDED.default_zoom,
			                 position = EXCLUDED.position,
			                 updated_at = now()`,
			claims.ClubID, d.Slug, name, d.DefaultLng, d.DefaultLat, d.DefaultZoom, d.Position,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to upsert dock")
			Error(w, http.StatusInternalServerError, "internal error")
			return
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
