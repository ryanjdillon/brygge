package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/roles"
)

type VolunteerHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewVolunteerHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	log zerolog.Logger,
) *VolunteerHandler {
	return &VolunteerHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "volunteer").Logger(),
	}
}

type taskParticipant struct {
	TaskID   string    `json:"task_id"`
	UserID   string    `json:"user_id"`
	Role     string    `json:"role"`
	Hours    *float64  `json:"hours"`
	JoinedAt time.Time `json:"joined_at"`
	Name     string    `json:"name"`
}

type volunteerHoursSummary struct {
	UserID         string  `json:"user_id"`
	Name           string  `json:"name"`
	SignedUpHours  float64 `json:"signed_up_hours"`
	CompletedHours float64 `json:"completed_hours"`
	RequiredHours  float64 `json:"required_hours"`
	Remaining      float64 `json:"remaining"`
}

type adjustHoursRequest struct {
	Participants []struct {
		UserID string  `json:"user_id"`
		Hours  float64 `json:"hours"`
	} `json:"participants"`
	ActualHours float64 `json:"actual_hours"`
}

type linkProjectRequest struct {
	ProjectID string `json:"project_id"`
}

type setRequiredHoursRequest struct {
	Hours float64 `json:"hours"`
}

// HandleJoinTask lets a member join a task as a participant.
func (h *VolunteerHandler) HandleJoinTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	taskID := chi.URLParam(r, "taskID")

	var maxCollab int
	var currentCount int
	err := h.db.QueryRow(ctx,
		`SELECT t.max_collaborators,
		        (SELECT COUNT(*) FROM task_participants WHERE task_id = t.id)
		 FROM tasks t WHERE t.id = $1 AND t.club_id = $2`,
		taskID, claims.ClubID,
	).Scan(&maxCollab, &currentCount)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check task capacity")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if currentCount >= maxCollab {
		Error(w, http.StatusConflict, "task is full")
		return
	}

	role := "collaborator"
	if currentCount == 0 {
		role = "responsible"
	}

	_, err = h.db.Exec(ctx,
		`INSERT INTO task_participants (task_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (task_id, user_id) DO NOTHING`,
		taskID, claims.UserID, role,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to join task")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if role == "responsible" {
		_, _ = h.db.Exec(ctx,
			`UPDATE tasks SET responsible_id = $1 WHERE id = $2`,
			claims.UserID, taskID,
		)
	}

	JSON(w, http.StatusOK, map[string]string{"status": "joined", "role": role})
}

// HandleLeaveTask removes a member from a task.
func (h *VolunteerHandler) HandleLeaveTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	taskID := chi.URLParam(r, "taskID")

	var role string
	err := h.db.QueryRow(ctx,
		`DELETE FROM task_participants
		 WHERE task_id = $1 AND user_id = $2
		 RETURNING role`,
		taskID, claims.UserID,
	).Scan(&role)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "not a participant")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to leave task")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if role == "responsible" {
		_, _ = h.db.Exec(ctx,
			`UPDATE tasks SET responsible_id = (
				SELECT user_id FROM task_participants
				WHERE task_id = $1 ORDER BY joined_at LIMIT 1
			) WHERE id = $1`,
			taskID,
		)
	}

	JSON(w, http.StatusOK, map[string]string{"status": "left"})
}

// taskParticipantsQuery lists a task's participants. $1 is the task id, $2 the
// caller's club, $3 whether the caller may see every name (board or admin), $4
// the caller's user id and $5 the board role set.
//
// Other members see a participant's name unless the participant set
// hide_in_directory; then the name is empty. The caller's own row and a
// participant holding a board role keep the name (BRD-12: the board's names are
// in the directory whatever the setting, so naming them here leaks nothing
// new). Only a privileged caller gets the email fallback for a nameless row.
// The response never carries the hide flag.
const taskParticipantsQuery = `SELECT tp.task_id, tp.user_id, tp.role, tp.hours, tp.joined_at,
	        CASE
	          WHEN $3::bool THEN COALESCE(NULLIF(btrim(u.first_name || ' ' || u.last_name), ''), u.email)
	          WHEN NOT u.hide_in_directory
	            OR u.id::text = $4
	            OR EXISTS (SELECT 1 FROM user_roles ur
	                        WHERE ur.user_id = u.id AND ur.club_id = t.club_id
	                          AND ur.role::text = ANY($5::text[]))
	            THEN COALESCE(btrim(u.first_name || ' ' || u.last_name), '')
	          ELSE ''
	        END AS name
	 FROM task_participants tp
	 JOIN tasks t ON t.id = tp.task_id
	 JOIN users u ON u.id = tp.user_id
	 WHERE t.id::text = $1 AND t.club_id = $2
	 ORDER BY tp.joined_at`

// HandleListTaskParticipants returns participants for one of the caller's
// club's tasks; another club's task is a 404, as if it did not exist.
func (h *VolunteerHandler) HandleListTaskParticipants(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	taskID := chi.URLParam(r, "taskID")

	var taskInClub bool
	if err := h.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE id::text = $1 AND club_id = $2)`,
		taskID, claims.ClubID,
	).Scan(&taskInClub); err != nil {
		h.log.Error().Err(err).Msg("failed to look up task for participants")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !taskInClub {
		Error(w, http.StatusNotFound, "task not found")
		return
	}

	rows, err := h.db.Query(ctx, taskParticipantsQuery,
		taskID, claims.ClubID, roles.CanViewAdmin(claims.Roles), claims.UserID, roles.Board,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list participants")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	participants := make([]taskParticipant, 0)
	for rows.Next() {
		var p taskParticipant
		if err := rows.Scan(&p.TaskID, &p.UserID, &p.Role, &p.Hours, &p.JoinedAt, &p.Name); err != nil {
			h.log.Error().Err(err).Msg("failed to scan participant")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		participants = append(participants, p)
	}

	JSON(w, http.StatusOK, participants)
}

// HandleAssignTask assigns a member to a task (admin).
func (h *VolunteerHandler) HandleAssignTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	taskID := chi.URLParam(r, "taskID")

	var req struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID == "" {
		Error(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if req.Role == "" {
		req.Role = "collaborator"
	}

	if err := loadPersonUser(ctx, h.db, claims.ClubID, req.UserID); err != nil {
		writePersonUserError(w, h.log, err)
		return
	}
	// The task must be the club's own: the participant insert below names it
	// by id alone.
	var taskInClub bool
	if err := h.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE id::text = $1 AND club_id = $2)`,
		taskID, claims.ClubID,
	).Scan(&taskInClub); err != nil {
		h.log.Error().Err(err).Msg("failed to look up task for assign")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !taskInClub {
		Error(w, http.StatusNotFound, "task not found")
		return
	}

	_, err := h.db.Exec(ctx,
		`INSERT INTO task_participants (task_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (task_id, user_id) DO UPDATE SET role = $3`,
		taskID, req.UserID, req.Role,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to assign task")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if req.Role == "responsible" {
		_, _ = h.db.Exec(ctx,
			`UPDATE tasks SET responsible_id = $1 WHERE id = $2 AND club_id = $3`,
			req.UserID, taskID, claims.ClubID,
		)
	}

	JSON(w, http.StatusOK, map[string]string{"status": "assigned"})
}

// HandleAdjustHours sets actual hours for a completed task and per-participant hours.
func (h *VolunteerHandler) HandleAdjustHours(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	taskID := chi.URLParam(r, "taskID")

	var req adjustHoursRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin tx")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`UPDATE tasks SET actual_hours = $1, updated_at = now()
		 WHERE id = $2 AND club_id = $3`,
		req.ActualHours, taskID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to set actual hours")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	for _, p := range req.Participants {
		_, err = tx.Exec(ctx,
			`UPDATE task_participants SET hours = $1
			 WHERE task_id = $2 AND user_id = $3`,
			p.Hours, taskID, p.UserID,
		)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to set participant hours")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit hours")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// HandleGetMyVolunteerHours returns the current user's volunteer hour summary.
func (h *VolunteerHandler) HandleGetMyVolunteerHours(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var summary volunteerHoursSummary
	err := h.db.QueryRow(ctx,
		`SELECT
			$1::uuid AS user_id,
			COALESCE(u.first_name || ' ' || u.last_name, u.email) AS name,
			COALESCE((
				SELECT SUM(t.estimated_hours)
				FROM task_participants tp
				JOIN tasks t ON t.id = tp.task_id
				WHERE tp.user_id = $1 AND t.status != 'done' AND t.estimated_hours IS NOT NULL
			), 0) AS signed_up_hours,
			COALESCE((
				SELECT SUM(tp.hours)
				FROM task_participants tp
				JOIN tasks t ON t.id = tp.task_id
				WHERE tp.user_id = $1 AND t.status = 'done' AND tp.hours IS NOT NULL
			), 0) AS completed_hours,
			COALESCE(c.required_volunteer_hours, 0) AS required_hours
		 FROM users u
		 JOIN clubs c ON c.id = u.club_id
		 WHERE u.id = $1`,
		claims.UserID,
	).Scan(&summary.UserID, &summary.Name, &summary.SignedUpHours, &summary.CompletedHours, &summary.RequiredHours)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get volunteer hours")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	summary.Remaining = summary.RequiredHours - summary.CompletedHours

	JSON(w, http.StatusOK, summary)
}

// HandleListAllVolunteerHours returns volunteer hours for all members (admin).
func (h *VolunteerHandler) HandleListAllVolunteerHours(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT
			u.id,
			COALESCE(u.first_name || ' ' || u.last_name, u.email) AS name,
			COALESCE((
				SELECT SUM(t.estimated_hours)
				FROM task_participants tp
				JOIN tasks t ON t.id = tp.task_id
				WHERE tp.user_id = u.id AND t.status != 'done' AND t.estimated_hours IS NOT NULL
			), 0) AS signed_up_hours,
			COALESCE((
				SELECT SUM(tp2.hours)
				FROM task_participants tp2
				JOIN tasks t2 ON t2.id = tp2.task_id
				WHERE tp2.user_id = u.id AND t2.status = 'done' AND tp2.hours IS NOT NULL
			), 0) AS completed_hours,
			COALESCE(c.required_volunteer_hours, 0) AS required_hours
		 FROM users u
		 JOIN clubs c ON c.id = u.club_id
		 WHERE u.club_id = $1 AND u.kind = 'person'
		 ORDER BY name`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list volunteer hours")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	summaries := make([]volunteerHoursSummary, 0)
	for rows.Next() {
		var s volunteerHoursSummary
		if err := rows.Scan(&s.UserID, &s.Name, &s.SignedUpHours, &s.CompletedHours, &s.RequiredHours); err != nil {
			h.log.Error().Err(err).Msg("failed to scan volunteer hours")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		s.Remaining = s.RequiredHours - s.CompletedHours
		summaries = append(summaries, s)
	}

	JSON(w, http.StatusOK, summaries)
}

// HandleSetRequiredHours sets the club's required volunteer hours.
func (h *VolunteerHandler) HandleSetRequiredHours(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req setRequiredHoursRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	_, err := h.db.Exec(ctx,
		`UPDATE clubs SET required_volunteer_hours = $1 WHERE id = $2`,
		req.Hours, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to set required hours")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// HandleLinkProjectEvent links a project to a volunteer event.
func (h *VolunteerHandler) HandleLinkProjectEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	eventID := chi.URLParam(r, "eventID")

	var req linkProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ProjectID == "" {
		Error(w, http.StatusBadRequest, "project_id is required")
		return
	}

	var exists bool
	err := h.db.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM events WHERE id = $1 AND club_id = $2
		)`, eventID, claims.ClubID,
	).Scan(&exists)
	if err != nil || !exists {
		Error(w, http.StatusNotFound, "event not found")
		return
	}

	_, err = h.db.Exec(ctx,
		`INSERT INTO project_events (project_id, event_id)
		 VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`,
		req.ProjectID, eventID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to link project")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, map[string]string{"status": "linked"})
}

// HandleUnlinkProjectEvent removes a project-event link.
func (h *VolunteerHandler) HandleUnlinkProjectEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	eventID := chi.URLParam(r, "eventID")
	projectID := chi.URLParam(r, "projectID")

	var exists bool
	_ = h.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM events WHERE id = $1 AND club_id = $2)`,
		eventID, claims.ClubID,
	).Scan(&exists)
	if !exists {
		Error(w, http.StatusNotFound, "event not found")
		return
	}

	_, err := h.db.Exec(ctx,
		`DELETE FROM project_events WHERE project_id = $1 AND event_id = $2`,
		projectID, eventID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to unlink project")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}

// HandleGetEventProjects returns projects linked to an event.
func (h *VolunteerHandler) HandleGetEventProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	eventID := chi.URLParam(r, "eventID")

	rows, err := h.db.Query(ctx,
		`SELECT p.id, p.club_id, p.name, p.description, p.created_by, p.created_at, p.updated_at,
		        COALESCE(SUM(CASE WHEN t.status = 'todo' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN t.status = 'in_progress' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN t.status = 'done' THEN 1 ELSE 0 END), 0)
		 FROM projects p
		 JOIN project_events pe ON pe.project_id = p.id
		 LEFT JOIN tasks t ON t.project_id = p.id
		 WHERE pe.event_id = $1 AND p.club_id = $2
		 GROUP BY p.id`,
		eventID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list event projects")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	projects := make([]projectWithCounts, 0)
	for rows.Next() {
		var p projectWithCounts
		if err := rows.Scan(
			&p.ID, &p.ClubID, &p.Name, &p.Description,
			&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt,
			&p.TodoCount, &p.InProgressCount, &p.DoneCount,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan event project")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		projects = append(projects, p)
	}

	JSON(w, http.StatusOK, projects)
}
