package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type MapHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewMapHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	log zerolog.Logger,
) *MapHandler {
	return &MapHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "map").Logger(),
	}
}

type mapMarker struct {
	ID         string    `json:"id"`
	ClubID     string    `json:"club_id"`
	MarkerType string    `json:"marker_type"`
	Label      string    `json:"label"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	SortOrder  int       `json:"sort_order"`
	CreatedAt  time.Time `json:"created_at"`
}

type clubCoordinates struct {
	Name      string   `json:"name"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

type createMarkerRequest struct {
	MarkerType string  `json:"marker_type"`
	Label      string  `json:"label"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	SortOrder  int     `json:"sort_order"`
}

type updateMarkerRequest struct {
	MarkerType *string  `json:"marker_type,omitempty"`
	Label      *string  `json:"label,omitempty"`
	Lat        *float64 `json:"lat,omitempty"`
	Lng        *float64 `json:"lng,omitempty"`
	SortOrder  *int     `json:"sort_order,omitempty"`
}

// HandleGetClubCoordinates returns public club lat/lng for maps.
func (h *MapHandler) HandleGetClubCoordinates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var c clubCoordinates
	err := h.db.QueryRow(ctx,
		`SELECT name, latitude, longitude FROM clubs WHERE slug = $1`,
		h.config.ClubSlug,
	).Scan(&c.Name, &c.Latitude, &c.Longitude)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "club not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get club coordinates")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, c)
}

// HandleListMarkers returns all map markers for the club (public).
func (h *MapHandler) HandleListMarkers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var clubID string
	err := h.db.QueryRow(ctx,
		`SELECT id FROM clubs WHERE slug = $1`, h.config.ClubSlug,
	).Scan(&clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get club")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT id, club_id, marker_type, label, lat, lng, sort_order, created_at
		 FROM map_markers
		 WHERE club_id = $1
		 ORDER BY sort_order, created_at`,
		clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list markers")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	markers := make([]mapMarker, 0)
	for rows.Next() {
		var m mapMarker
		if err := rows.Scan(&m.ID, &m.ClubID, &m.MarkerType, &m.Label, &m.Lat, &m.Lng, &m.SortOrder, &m.CreatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan marker")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		markers = append(markers, m)
	}

	JSON(w, http.StatusOK, markers)
}

// HandleExportGPX exports markers as a GPX file.
func (h *MapHandler) HandleExportGPX(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var clubName string
	var clubID string
	var lat, lng *float64
	err := h.db.QueryRow(ctx,
		`SELECT id, name, latitude, longitude FROM clubs WHERE slug = $1`,
		h.config.ClubSlug,
	).Scan(&clubID, &clubName, &lat, &lng)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get club for export")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT marker_type, label, lat, lng, sort_order
		 FROM map_markers WHERE club_id = $1
		 ORDER BY sort_order, created_at`,
		clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list markers for export")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var wpts []string
	var rtePts []string

	if lat != nil && lng != nil {
		wpts = append(wpts, fmt.Sprintf(
			`  <wpt lat="%f" lon="%f"><name>%s</name><type>harbor</type></wpt>`,
			*lat, *lng, xmlEscape(clubName),
		))
	}

	for rows.Next() {
		var mType, label string
		var mLat, mLng float64
		var sortOrder int
		if err := rows.Scan(&mType, &label, &mLat, &mLng, &sortOrder); err != nil {
			continue
		}
		if label == "" {
			label = mType
		}
		wpts = append(wpts, fmt.Sprintf(
			`  <wpt lat="%f" lon="%f"><name>%s</name><type>%s</type></wpt>`,
			mLat, mLng, xmlEscape(label), xmlEscape(mType),
		))
		if mType == "waypoint" {
			rtePts = append(rtePts, fmt.Sprintf(
				`    <rtept lat="%f" lon="%f"><name>%s</name></rtept>`,
				mLat, mLng, xmlEscape(label),
			))
		}
	}

	var rte string
	if len(rtePts) > 0 {
		rte = fmt.Sprintf("  <rte>\n    <name>Innseiling %s</name>\n%s\n  </rte>",
			xmlEscape(clubName), strings.Join(rtePts, "\n"))
	}

	gpx := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="Brygge"
     xmlns="http://www.topografix.com/GPX/1/1">
  <metadata><name>%s</name></metadata>
%s
%s
</gpx>`, xmlEscape(clubName), strings.Join(wpts, "\n"), rte)

	slug := strings.ReplaceAll(strings.ToLower(clubName), " ", "-")
	w.Header().Set("Content-Type", "application/gpx+xml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="brygge-%s-waypoints.gpx"`, slug))
	w.Write([]byte(gpx))
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// HandleCreateMarker creates a new map marker (admin).
func (h *MapHandler) HandleCreateMarker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createMarkerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MarkerType == "" {
		Error(w, http.StatusBadRequest, "marker_type is required")
		return
	}

	var m mapMarker
	err := h.db.QueryRow(ctx,
		`INSERT INTO map_markers (club_id, marker_type, label, lat, lng, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, club_id, marker_type, label, lat, lng, sort_order, created_at`,
		claims.ClubID, req.MarkerType, req.Label, req.Lat, req.Lng, req.SortOrder,
	).Scan(&m.ID, &m.ClubID, &m.MarkerType, &m.Label, &m.Lat, &m.Lng, &m.SortOrder, &m.CreatedAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create marker")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, m)
}

// HandleUpdateMarker updates a map marker (admin).
func (h *MapHandler) HandleUpdateMarker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	markerID := chi.URLParam(r, "markerID")

	var req updateMarkerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var m mapMarker
	err := h.db.QueryRow(ctx,
		`UPDATE map_markers SET
			marker_type = COALESCE($3, marker_type),
			label = COALESCE($4, label),
			lat = COALESCE($5, lat),
			lng = COALESCE($6, lng),
			sort_order = COALESCE($7, sort_order)
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, club_id, marker_type, label, lat, lng, sort_order, created_at`,
		markerID, claims.ClubID, req.MarkerType, req.Label, req.Lat, req.Lng, req.SortOrder,
	).Scan(&m.ID, &m.ClubID, &m.MarkerType, &m.Label, &m.Lat, &m.Lng, &m.SortOrder, &m.CreatedAt)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "marker not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update marker")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, m)
}

// HandleDeleteMarker deletes a map marker (admin).
func (h *MapHandler) HandleDeleteMarker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	markerID := chi.URLParam(r, "markerID")

	tag, err := h.db.Exec(ctx,
		`DELETE FROM map_markers WHERE id = $1 AND club_id = $2`,
		markerID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete marker")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "marker not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
