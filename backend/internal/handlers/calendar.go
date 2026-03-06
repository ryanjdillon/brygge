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

type CalendarHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewCalendarHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	log zerolog.Logger,
) *CalendarHandler {
	return &CalendarHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "calendar").Logger(),
	}
}

type event struct {
	ID          string    `json:"id"`
	ClubID      string    `json:"club_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Location    string    `json:"location"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Tag         string    `json:"tag"`
	IsPublic    bool      `json:"is_public"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type createEventRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Tag         string `json:"tag"`
	IsPublic    *bool  `json:"is_public"`
}

type updateEventRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Location    *string `json:"location,omitempty"`
	StartTime   *string `json:"start_time,omitempty"`
	EndTime     *string `json:"end_time,omitempty"`
	Tag         *string `json:"tag,omitempty"`
	IsPublic    *bool   `json:"is_public,omitempty"`
}

func (h *CalendarHandler) HandleListPublicEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	tagFilter := r.URL.Query().Get("tag")

	query := `SELECT id, club_id, title, description, location, start_time, end_time, tag, is_public, created_by, created_at, updated_at
	          FROM events
	          WHERE club_id = (SELECT id FROM clubs WHERE slug = $1) AND is_public = true`
	args := []any{h.config.ClubSlug}
	argIdx := 2

	if startStr != "" {
		startDate, err := time.Parse("2006-01-02", startStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid start date format, use YYYY-MM-DD")
			return
		}
		query += fmt.Sprintf(` AND end_time > $%d`, argIdx)
		args = append(args, startDate)
		argIdx++
	}
	if endStr != "" {
		endDate, err := time.Parse("2006-01-02", endStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid end date format, use YYYY-MM-DD")
			return
		}
		query += fmt.Sprintf(` AND start_time < $%d`, argIdx)
		args = append(args, endDate.AddDate(0, 0, 1))
		argIdx++
	}
	if tagFilter != "" {
		query += fmt.Sprintf(` AND tag = $%d`, argIdx)
		args = append(args, tagFilter)
		argIdx++
	}
	query += ` ORDER BY start_time`

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list public events")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	events := make([]event, 0)
	for rows.Next() {
		var e event
		if err := rows.Scan(
			&e.ID, &e.ClubID, &e.Title, &e.Description, &e.Location,
			&e.StartTime, &e.EndTime, &e.Tag, &e.IsPublic,
			&e.CreatedBy, &e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan event")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating event rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, events)
}

func (h *CalendarHandler) HandleGetEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	eventID := chi.URLParam(r, "eventID")
	if eventID == "" {
		Error(w, http.StatusBadRequest, "missing event ID")
		return
	}

	var e event
	err := h.db.QueryRow(ctx,
		`SELECT id, club_id, title, description, location, start_time, end_time, tag, is_public, created_by, created_at, updated_at
		 FROM events
		 WHERE id = $1 AND club_id = (SELECT id FROM clubs WHERE slug = $2)`,
		eventID, h.config.ClubSlug,
	).Scan(
		&e.ID, &e.ClubID, &e.Title, &e.Description, &e.Location,
		&e.StartTime, &e.EndTime, &e.Tag, &e.IsPublic,
		&e.CreatedBy, &e.CreatedAt, &e.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch event")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, e)
}

func (h *CalendarHandler) HandleExportICS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Query(ctx,
		`SELECT id, title, description, location, start_time, end_time
		 FROM events
		 WHERE club_id = (SELECT id FROM clubs WHERE slug = $1) AND is_public = true
		 ORDER BY start_time`,
		h.config.ClubSlug,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query events for ICS export")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString(fmt.Sprintf("PRODID:-//Brygge//%s//EN\r\n", h.config.ClubSlug))
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString("METHOD:PUBLISH\r\n")

	for rows.Next() {
		var id, title, description, location string
		var startTime, endTime time.Time
		if err := rows.Scan(&id, &title, &description, &location, &startTime, &endTime); err != nil {
			h.log.Error().Err(err).Msg("failed to scan event for ICS")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}

		b.WriteString("BEGIN:VEVENT\r\n")
		b.WriteString(fmt.Sprintf("UID:%s@brygge\r\n", id))
		b.WriteString(fmt.Sprintf("DTSTART:%s\r\n", startTime.UTC().Format("20060102T150405Z")))
		b.WriteString(fmt.Sprintf("DTEND:%s\r\n", endTime.UTC().Format("20060102T150405Z")))
		b.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", icsEscape(title)))
		if description != "" {
			b.WriteString(fmt.Sprintf("DESCRIPTION:%s\r\n", icsEscape(description)))
		}
		if location != "" {
			b.WriteString(fmt.Sprintf("LOCATION:%s\r\n", icsEscape(location)))
		}
		b.WriteString("END:VEVENT\r\n")
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating events for ICS")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	b.WriteString("END:VCALENDAR\r\n")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=public.ics")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(b.String()))
}

func (h *CalendarHandler) HandleCreateEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title == "" || req.StartTime == "" || req.EndTime == "" {
		Error(w, http.StatusBadRequest, "title, start_time, and end_time are required")
		return
	}

	startTime, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid start_time format, use RFC3339")
		return
	}

	endTime, err := time.Parse(time.RFC3339, req.EndTime)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid end_time format, use RFC3339")
		return
	}

	if endTime.Before(startTime) {
		Error(w, http.StatusBadRequest, "end_time must be after start_time")
		return
	}

	tag := "other"
	if req.Tag != "" {
		tag = req.Tag
	}

	isPublic := true
	if req.IsPublic != nil {
		isPublic = *req.IsPublic
	}

	var e event
	err = h.db.QueryRow(ctx,
		`INSERT INTO events (club_id, title, description, location, start_time, end_time, tag, is_public, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, club_id, title, description, location, start_time, end_time, tag, is_public, created_by, created_at, updated_at`,
		claims.ClubID, req.Title, req.Description, req.Location,
		startTime, endTime, tag, isPublic, claims.UserID,
	).Scan(
		&e.ID, &e.ClubID, &e.Title, &e.Description, &e.Location,
		&e.StartTime, &e.EndTime, &e.Tag, &e.IsPublic,
		&e.CreatedBy, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create event")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, e)
}

func (h *CalendarHandler) HandleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	eventID := chi.URLParam(r, "eventID")
	if eventID == "" {
		Error(w, http.StatusBadRequest, "missing event ID")
		return
	}

	var existing event
	err := h.db.QueryRow(ctx,
		`SELECT id, club_id, title, description, location, start_time, end_time, tag, is_public, created_by, created_at, updated_at
		 FROM events WHERE id = $1 AND club_id = $2`,
		eventID, claims.ClubID,
	).Scan(
		&existing.ID, &existing.ClubID, &existing.Title, &existing.Description, &existing.Location,
		&existing.StartTime, &existing.EndTime, &existing.Tag, &existing.IsPublic,
		&existing.CreatedBy, &existing.CreatedAt, &existing.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch event for update")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var req updateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Location != nil {
		existing.Location = *req.Location
	}
	if req.StartTime != nil {
		t, err := time.Parse(time.RFC3339, *req.StartTime)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid start_time format, use RFC3339")
			return
		}
		existing.StartTime = t
	}
	if req.EndTime != nil {
		t, err := time.Parse(time.RFC3339, *req.EndTime)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid end_time format, use RFC3339")
			return
		}
		existing.EndTime = t
	}
	if req.Tag != nil {
		existing.Tag = *req.Tag
	}
	if req.IsPublic != nil {
		existing.IsPublic = *req.IsPublic
	}

	var e event
	err = h.db.QueryRow(ctx,
		`UPDATE events
		 SET title = $3, description = $4, location = $5, start_time = $6, end_time = $7, tag = $8, is_public = $9, updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, club_id, title, description, location, start_time, end_time, tag, is_public, created_by, created_at, updated_at`,
		eventID, claims.ClubID,
		existing.Title, existing.Description, existing.Location,
		existing.StartTime, existing.EndTime, existing.Tag, existing.IsPublic,
	).Scan(
		&e.ID, &e.ClubID, &e.Title, &e.Description, &e.Location,
		&e.StartTime, &e.EndTime, &e.Tag, &e.IsPublic,
		&e.CreatedBy, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update event")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, e)
}

func (h *CalendarHandler) HandleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	eventID := chi.URLParam(r, "eventID")
	if eventID == "" {
		Error(w, http.StatusBadRequest, "missing event ID")
		return
	}

	tag, err := h.db.Exec(ctx,
		`DELETE FROM events WHERE id = $1 AND club_id = $2`,
		eventID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete event")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "event not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func icsEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
