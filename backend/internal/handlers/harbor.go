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

type harborFinger struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	X1       float64  `json:"x1"`
	Y1       float64  `json:"y1"`
	X2       float64  `json:"x2"`
	Y2       float64  `json:"y2"`
	WidthM   *float64 `json:"width_m"`
	Position int      `json:"position"`
}

type harborSlip struct {
	ID             string   `json:"id"`
	Number         string   `json:"number"`
	Section        string   `json:"section"`
	Status         string   `json:"status"`
	LengthM        *float64 `json:"length_m"`
	WidthM         *float64 `json:"width_m"`
	MapX           *float64 `json:"map_x"`
	MapY           *float64 `json:"map_y"`
	MapRotation    float64  `json:"map_rotation"`
	MapFingerID    *string  `json:"map_finger_id"`
	MapSide        *string  `json:"map_side"`
	AssignmentType *string  `json:"assignment_type,omitempty"`

	OccupantLastName *string `json:"occupant_last_name,omitempty"`
	OccupantID       *string `json:"occupant_id,omitempty"`
	OccupantName     *string `json:"occupant_name,omitempty"`
	OccupantEmail    *string `json:"occupant_email,omitempty"`
	BoatID           *string `json:"boat_id,omitempty"`
	BoatName         *string `json:"boat_name,omitempty"`
	BoatLengthM      *float64 `json:"boat_length_m,omitempty"`
	BoatBeamM        *float64 `json:"boat_beam_m,omitempty"`
}

type harborLayoutResponse struct {
	ViewBox [4]float64     `json:"view_box"`
	Mode    string         `json:"mode"`
	Fingers []harborFinger `json:"fingers"`
	Slips   []harborSlip   `json:"slips"`
}

// HandleGetLayout returns the harbor layout. Detail level scales with
// the requesting principal: anonymous → counts/positions only;
// authenticated member → owner last name + boat summary; admin/board/
// harbor_master → full owner contact + boat detail.
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

	fingerRows, err := h.db.Query(ctx,
		`SELECT id, label, x1, y1, x2, y2, width_m, position
		   FROM dock_fingers
		  WHERE club_id = $1
		  ORDER BY position, label`, clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query dock fingers")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer fingerRows.Close()

	fingers := []harborFinger{}
	for fingerRows.Next() {
		var f harborFinger
		if err := fingerRows.Scan(&f.ID, &f.Label, &f.X1, &f.Y1, &f.X2, &f.Y2, &f.WidthM, &f.Position); err != nil {
			h.log.Error().Err(err).Msg("failed to scan dock finger")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		fingers = append(fingers, f)
	}

	slipRows, err := h.db.Query(ctx,
		`SELECT s.id, s.number, s.section, s.status,
		        s.length_m, s.width_m, s.map_x, s.map_y, s.map_rotation, s.map_finger_id, s.map_side,
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
	defer slipRows.Close()

	slips := []harborSlip{}
	for slipRows.Next() {
		var s harborSlip
		var assignmentType, userID, email *string
		var firstName, lastName *string
		var boatID, boatName *string
		var boatLength, boatBeam *float64
		if err := slipRows.Scan(
			&s.ID, &s.Number, &s.Section, &s.Status,
			&s.LengthM, &s.WidthM, &s.MapX, &s.MapY, &s.MapRotation, &s.MapFingerID, &s.MapSide,
			&assignmentType,
			&userID, &firstName, &lastName, &email,
			&boatID, &boatName, &boatLength, &boatBeam,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan slip")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}

		if assignmentType != nil {
			s.AssignmentType = assignmentType
		}

		if mode != "public" && lastName != nil {
			s.OccupantLastName = lastName
		}
		if canSeeDetail {
			s.OccupantID = userID
			if firstName != nil || lastName != nil {
				full := strings.TrimSpace(deref(firstName) + " " + deref(lastName))
				if full != "" {
					s.OccupantName = &full
				}
			}
			s.OccupantEmail = email
			s.BoatID = boatID
			s.BoatName = boatName
			s.BoatLengthM = boatLength
			s.BoatBeamM = boatBeam
		}

		slips = append(slips, s)
	}

	resp := harborLayoutResponse{
		ViewBox: [4]float64{0, 0, 757, 463},
		Mode:    mode,
		Fingers: fingers,
		Slips:   slips,
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

type updateLayoutFinger struct {
	ID       *string  `json:"id"`
	Label    string   `json:"label"`
	X1       float64  `json:"x1"`
	Y1       float64  `json:"y1"`
	X2       float64  `json:"x2"`
	Y2       float64  `json:"y2"`
	WidthM   *float64 `json:"width_m"`
	Position int      `json:"position"`
	Delete   bool     `json:"delete,omitempty"`
}

type updateLayoutSlip struct {
	ID          string   `json:"id"`
	MapX        *float64 `json:"map_x"`
	MapY        *float64 `json:"map_y"`
	MapRotation *float64 `json:"map_rotation"`
	MapFingerID *string  `json:"map_finger_id"`
	MapSide     *string  `json:"map_side"`
}

type updateLayoutRequest struct {
	Fingers []updateLayoutFinger `json:"fingers"`
	Slips   []updateLayoutSlip   `json:"slips"`
}

// HandlePutLayout upserts dock fingers and updates slip map positions.
// Admin-only; route group must enforce RequireRole.
func (h *HarborHandler) HandlePutLayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req updateLayoutRequest
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

	for _, f := range req.Fingers {
		if f.Delete && f.ID != nil {
			if _, err := tx.Exec(ctx,
				`DELETE FROM dock_fingers WHERE id = $1 AND club_id = $2`,
				*f.ID, claims.ClubID,
			); err != nil {
				h.log.Error().Err(err).Msg("failed to delete dock finger")
				Error(w, http.StatusInternalServerError, "internal error")
				return
			}
			continue
		}
		if f.ID == nil {
			if _, err := tx.Exec(ctx,
				`INSERT INTO dock_fingers (club_id, label, x1, y1, x2, y2, width_m, position)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				claims.ClubID, f.Label, f.X1, f.Y1, f.X2, f.Y2, f.WidthM, f.Position,
			); err != nil {
				h.log.Error().Err(err).Msg("failed to insert dock finger")
				Error(w, http.StatusInternalServerError, "internal error")
				return
			}
		} else {
			if _, err := tx.Exec(ctx,
				`UPDATE dock_fingers
				    SET label = $1, x1 = $2, y1 = $3, x2 = $4, y2 = $5,
				        width_m = $6, position = $7, updated_at = $8
				  WHERE id = $9 AND club_id = $10`,
				f.Label, f.X1, f.Y1, f.X2, f.Y2, f.WidthM, f.Position, time.Now(), *f.ID, claims.ClubID,
			); err != nil {
				h.log.Error().Err(err).Msg("failed to update dock finger")
				Error(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
	}

	for _, s := range req.Slips {
		if s.MapSide != nil {
			if *s.MapSide != "port" && *s.MapSide != "starboard" {
				Error(w, http.StatusBadRequest, "map_side must be 'port' or 'starboard'")
				return
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE slips
			    SET map_x = $1, map_y = $2,
			        map_rotation = COALESCE($3, map_rotation),
			        map_finger_id = $4, map_side = $5, updated_at = $6
			  WHERE id = $7 AND club_id = $8`,
			s.MapX, s.MapY, s.MapRotation, s.MapFingerID, s.MapSide, time.Now(), s.ID, claims.ClubID,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to update slip layout")
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
