package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type FeatureRequestsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewFeatureRequestsHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	log zerolog.Logger,
) *FeatureRequestsHandler {
	return &FeatureRequestsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "feature_requests").Logger(),
	}
}

type featureRequest struct {
	ID          string    `json:"id"`
	ClubID      string    `json:"club_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	SubmittedBy string    `json:"submitted_by"`
	VoteCount   int       `json:"vote_count"`
	UserVote    *int      `json:"user_vote"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type createFeatureRequestRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type voteRequest struct {
	Value int `json:"value"`
}

type updateStatusRequest struct {
	Status string `json:"status"`
}

type promoteRequest struct {
	ProjectID string `json:"project_id"`
}

func (h *FeatureRequestsHandler) HandleListFeatureRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	statusFilter := r.URL.Query().Get("status")

	query := `SELECT fr.id, fr.club_id, fr.title, fr.description, fr.status, fr.submitted_by,
	                 COALESCE(SUM(v.value), 0) AS vote_count,
	                 (SELECT value FROM votes WHERE feature_request_id = fr.id AND user_id = $2) AS user_vote,
	                 fr.created_at, fr.updated_at
	          FROM feature_requests fr
	          LEFT JOIN votes v ON v.feature_request_id = fr.id
	          WHERE fr.club_id = $1`
	args := []any{claims.ClubID, claims.UserID}
	argIdx := 3

	if statusFilter != "" {
		query += fmt.Sprintf(` AND fr.status = $%d`, argIdx)
		args = append(args, statusFilter)
		argIdx++
	}

	query += ` GROUP BY fr.id ORDER BY vote_count DESC, fr.created_at DESC`
	_ = argIdx

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list feature requests")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	requests := make([]featureRequest, 0)
	for rows.Next() {
		var fr featureRequest
		if err := rows.Scan(
			&fr.ID, &fr.ClubID, &fr.Title, &fr.Description, &fr.Status,
			&fr.SubmittedBy, &fr.VoteCount, &fr.UserVote,
			&fr.CreatedAt, &fr.UpdatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan feature request")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		requests = append(requests, fr)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating feature request rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, requests)
}

func (h *FeatureRequestsHandler) HandleCreateFeatureRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createFeatureRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title == "" {
		Error(w, http.StatusBadRequest, "title is required")
		return
	}

	var fr featureRequest
	err := h.db.QueryRow(ctx,
		`INSERT INTO feature_requests (club_id, title, description, submitted_by)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, club_id, title, description, status, submitted_by, created_at, updated_at`,
		claims.ClubID, req.Title, req.Description, claims.UserID,
	).Scan(
		&fr.ID, &fr.ClubID, &fr.Title, &fr.Description, &fr.Status,
		&fr.SubmittedBy, &fr.CreatedAt, &fr.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create feature request")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	fr.VoteCount = 0

	JSON(w, http.StatusCreated, fr)
}

func (h *FeatureRequestsHandler) HandleGetFeatureRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	requestID := chi.URLParam(r, "requestID")
	if requestID == "" {
		Error(w, http.StatusBadRequest, "missing request ID")
		return
	}

	var fr featureRequest
	err := h.db.QueryRow(ctx,
		`SELECT fr.id, fr.club_id, fr.title, fr.description, fr.status, fr.submitted_by,
		        COALESCE((SELECT SUM(value) FROM votes WHERE feature_request_id = fr.id), 0) AS vote_count,
		        (SELECT value FROM votes WHERE feature_request_id = fr.id AND user_id = $3) AS user_vote,
		        fr.created_at, fr.updated_at
		 FROM feature_requests fr
		 WHERE fr.id = $1 AND fr.club_id = $2`,
		requestID, claims.ClubID, claims.UserID,
	).Scan(
		&fr.ID, &fr.ClubID, &fr.Title, &fr.Description, &fr.Status,
		&fr.SubmittedBy, &fr.VoteCount, &fr.UserVote,
		&fr.CreatedAt, &fr.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "feature request not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch feature request")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, fr)
}

func (h *FeatureRequestsHandler) HandleVote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	requestID := chi.URLParam(r, "requestID")
	if requestID == "" {
		Error(w, http.StatusBadRequest, "missing request ID")
		return
	}

	var req voteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Value != 1 && req.Value != -1 {
		Error(w, http.StatusBadRequest, "value must be 1 or -1")
		return
	}

	var exists bool
	err := h.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM feature_requests WHERE id = $1 AND club_id = $2)`,
		requestID, claims.ClubID,
	).Scan(&exists)
	if err != nil || !exists {
		Error(w, http.StatusNotFound, "feature request not found")
		return
	}

	_, err = h.db.Exec(ctx,
		`INSERT INTO votes (feature_request_id, user_id, value)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (feature_request_id, user_id)
		 DO UPDATE SET value = EXCLUDED.value`,
		requestID, claims.UserID, req.Value,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to upsert vote")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var voteCount int
	err = h.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(value), 0) FROM votes WHERE feature_request_id = $1`,
		requestID,
	).Scan(&voteCount)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get vote count")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"vote_count": voteCount,
		"user_vote":  req.Value,
	})
}

func (h *FeatureRequestsHandler) HandleUpdateFeatureRequestStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	requestID := chi.URLParam(r, "requestID")
	if requestID == "" {
		Error(w, http.StatusBadRequest, "missing request ID")
		return
	}

	var req updateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	validStatuses := map[string]bool{
		"reviewing": true,
		"accepted":  true,
		"rejected":  true,
		"done":      true,
	}
	if !validStatuses[req.Status] {
		Error(w, http.StatusBadRequest, "status must be one of: reviewing, accepted, rejected, done")
		return
	}

	tag, err := h.db.Exec(ctx,
		`UPDATE feature_requests SET status = $3, updated_at = now()
		 WHERE id = $1 AND club_id = $2`,
		requestID, claims.ClubID, req.Status,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update feature request status")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "feature request not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

func (h *FeatureRequestsHandler) HandlePromoteToTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	requestID := chi.URLParam(r, "requestID")
	if requestID == "" {
		Error(w, http.StatusBadRequest, "missing request ID")
		return
	}

	var req promoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ProjectID == "" {
		Error(w, http.StatusBadRequest, "project_id is required")
		return
	}

	var fr struct {
		title       string
		description string
	}
	err := h.db.QueryRow(ctx,
		`SELECT title, description FROM feature_requests WHERE id = $1 AND club_id = $2`,
		requestID, claims.ClubID,
	).Scan(&fr.title, &fr.description)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "feature request not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch feature request for promotion")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var projectExists bool
	err = h.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1 AND club_id = $2)`,
		req.ProjectID, claims.ClubID,
	).Scan(&projectExists)
	if err != nil || !projectExists {
		Error(w, http.StatusNotFound, "project not found")
		return
	}

	var t task
	var dueDateOut *time.Time
	err = h.db.QueryRow(ctx,
		`INSERT INTO tasks (project_id, club_id, title, description, created_by)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, project_id, club_id, title, description, assignee_id, status, priority, due_date, created_by, created_at, updated_at`,
		req.ProjectID, claims.ClubID, fr.title, fr.description, claims.UserID,
	).Scan(
		&t.ID, &t.ProjectID, &t.ClubID, &t.Title, &t.Description,
		&t.AssigneeID, &t.Status, &t.Priority, &dueDateOut,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create task from feature request")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if dueDateOut != nil {
		formatted := dueDateOut.Format("2006-01-02")
		t.DueDate = &formatted
	}

	_, err = h.db.Exec(ctx,
		`UPDATE feature_requests SET status = 'accepted', updated_at = now()
		 WHERE id = $1 AND club_id = $2`,
		requestID, claims.ClubID,
	)
	if err != nil {
		h.log.Warn().Err(err).Msg("failed to update feature request status after promotion")
	}

	JSON(w, http.StatusCreated, t)
}

