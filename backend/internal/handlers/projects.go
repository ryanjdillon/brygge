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
)

type ProjectsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewProjectsHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	log zerolog.Logger,
) *ProjectsHandler {
	return &ProjectsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "projects").Logger(),
	}
}

type project struct {
	ID          string    `json:"id"`
	ClubID      string    `json:"club_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type projectWithCounts struct {
	project
	TodoCount       int `json:"todo_count"`
	InProgressCount int `json:"in_progress_count"`
	DoneCount       int `json:"done_count"`
}

type task struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	ClubID      string    `json:"club_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	AssigneeID  *string   `json:"assignee_id"`
	Status      string    `json:"status"`
	Priority    string    `json:"priority"`
	DueDate     *string   `json:"due_date"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type groupedTasks struct {
	Todo       []task `json:"todo"`
	InProgress []task `json:"in_progress"`
	Done       []task `json:"done"`
}

type createProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type createTaskRequest struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	AssigneeID  *string `json:"assignee_id"`
	DueDate     *string `json:"due_date"`
	Priority    string  `json:"priority"`
}

type updateTaskRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	AssigneeID  *string `json:"assignee_id,omitempty"`
	Status      *string `json:"status,omitempty"`
	Priority    *string `json:"priority,omitempty"`
	DueDate     *string `json:"due_date,omitempty"`
}

func (h *ProjectsHandler) HandleListProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT p.id, p.club_id, p.name, p.description, p.created_by, p.created_at, p.updated_at,
		        COALESCE(SUM(CASE WHEN t.status = 'todo' THEN 1 ELSE 0 END), 0) AS todo_count,
		        COALESCE(SUM(CASE WHEN t.status = 'in_progress' THEN 1 ELSE 0 END), 0) AS in_progress_count,
		        COALESCE(SUM(CASE WHEN t.status = 'done' THEN 1 ELSE 0 END), 0) AS done_count
		 FROM projects p
		 LEFT JOIN tasks t ON t.project_id = p.id
		 WHERE p.club_id = $1
		 GROUP BY p.id
		 ORDER BY p.created_at DESC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list projects")
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
			h.log.Error().Err(err).Msg("failed to scan project")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating project rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, projects)
}

func (h *ProjectsHandler) HandleCreateProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}

	var p project
	err := h.db.QueryRow(ctx,
		`INSERT INTO projects (club_id, name, description, created_by)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, club_id, name, description, created_by, created_at, updated_at`,
		claims.ClubID, req.Name, req.Description, claims.UserID,
	).Scan(
		&p.ID, &p.ClubID, &p.Name, &p.Description,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create project")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, p)
}

func (h *ProjectsHandler) HandleGetProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		Error(w, http.StatusBadRequest, "missing project ID")
		return
	}

	var p project
	err := h.db.QueryRow(ctx,
		`SELECT id, club_id, name, description, created_by, created_at, updated_at
		 FROM projects
		 WHERE id = $1 AND club_id = $2`,
		projectID, claims.ClubID,
	).Scan(
		&p.ID, &p.ClubID, &p.Name, &p.Description,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch project")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, p)
}

func (h *ProjectsHandler) HandleListTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		Error(w, http.StatusBadRequest, "missing project ID")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT id, project_id, club_id, title, description, assignee_id, status, priority, due_date, created_by, created_at, updated_at
		 FROM tasks
		 WHERE project_id = $1 AND club_id = $2
		 ORDER BY priority DESC, created_at`,
		projectID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list tasks")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	result := groupedTasks{
		Todo:       make([]task, 0),
		InProgress: make([]task, 0),
		Done:       make([]task, 0),
	}

	for rows.Next() {
		var t task
		var dueDate *time.Time
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.ClubID, &t.Title, &t.Description,
			&t.AssigneeID, &t.Status, &t.Priority, &dueDate,
			&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan task")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if dueDate != nil {
			formatted := dueDate.Format("2006-01-02")
			t.DueDate = &formatted
		}
		switch t.Status {
		case "in_progress":
			result.InProgress = append(result.InProgress, t)
		case "done":
			result.Done = append(result.Done, t)
		default:
			result.Todo = append(result.Todo, t)
		}
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating task rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, result)
}

func (h *ProjectsHandler) HandleCreateTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		Error(w, http.StatusBadRequest, "missing project ID")
		return
	}

	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title == "" {
		Error(w, http.StatusBadRequest, "title is required")
		return
	}

	priority := "medium"
	if req.Priority != "" {
		priority = req.Priority
	}

	var dueDate *time.Time
	if req.DueDate != nil && *req.DueDate != "" {
		d, err := time.Parse("2006-01-02", *req.DueDate)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid due_date format, use YYYY-MM-DD")
			return
		}
		dueDate = &d
	}

	var exists bool
	err := h.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1 AND club_id = $2)`,
		projectID, claims.ClubID,
	).Scan(&exists)
	if err != nil || !exists {
		Error(w, http.StatusNotFound, "project not found")
		return
	}

	var t task
	var dueDateOut *time.Time
	err = h.db.QueryRow(ctx,
		`INSERT INTO tasks (project_id, club_id, title, description, assignee_id, priority, due_date, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, project_id, club_id, title, description, assignee_id, status, priority, due_date, created_by, created_at, updated_at`,
		projectID, claims.ClubID, req.Title, req.Description,
		req.AssigneeID, priority, dueDate, claims.UserID,
	).Scan(
		&t.ID, &t.ProjectID, &t.ClubID, &t.Title, &t.Description,
		&t.AssigneeID, &t.Status, &t.Priority, &dueDateOut,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create task")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if dueDateOut != nil {
		formatted := dueDateOut.Format("2006-01-02")
		t.DueDate = &formatted
	}

	JSON(w, http.StatusCreated, t)
}

func (h *ProjectsHandler) HandleUpdateTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	taskID := chi.URLParam(r, "taskID")
	if taskID == "" {
		Error(w, http.StatusBadRequest, "missing task ID")
		return
	}

	var existing struct {
		title       string
		description string
		assigneeID  *string
		status      string
		priority    string
		dueDate     *time.Time
	}
	err := h.db.QueryRow(ctx,
		`SELECT title, description, assignee_id, status, priority, due_date
		 FROM tasks WHERE id = $1 AND club_id = $2`,
		taskID, claims.ClubID,
	).Scan(&existing.title, &existing.description, &existing.assigneeID,
		&existing.status, &existing.priority, &existing.dueDate)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch task for update")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var req updateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title != nil {
		existing.title = *req.Title
	}
	if req.Description != nil {
		existing.description = *req.Description
	}
	if req.AssigneeID != nil {
		if *req.AssigneeID == "" {
			existing.assigneeID = nil
		} else {
			existing.assigneeID = req.AssigneeID
		}
	}
	if req.Status != nil {
		existing.status = *req.Status
	}
	if req.Priority != nil {
		existing.priority = *req.Priority
	}
	if req.DueDate != nil {
		if *req.DueDate == "" {
			existing.dueDate = nil
		} else {
			d, err := time.Parse("2006-01-02", *req.DueDate)
			if err != nil {
				Error(w, http.StatusBadRequest, "invalid due_date format, use YYYY-MM-DD")
				return
			}
			existing.dueDate = &d
		}
	}

	var t task
	var dueDateOut *time.Time
	err = h.db.QueryRow(ctx,
		`UPDATE tasks
		 SET title = $3, description = $4, assignee_id = $5, status = $6, priority = $7, due_date = $8, updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, project_id, club_id, title, description, assignee_id, status, priority, due_date, created_by, created_at, updated_at`,
		taskID, claims.ClubID,
		existing.title, existing.description, existing.assigneeID,
		existing.status, existing.priority, existing.dueDate,
	).Scan(
		&t.ID, &t.ProjectID, &t.ClubID, &t.Title, &t.Description,
		&t.AssigneeID, &t.Status, &t.Priority, &dueDateOut,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update task")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if dueDateOut != nil {
		formatted := dueDateOut.Format("2006-01-02")
		t.DueDate = &formatted
	}

	JSON(w, http.StatusOK, t)
}

func (h *ProjectsHandler) HandleDeleteTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	taskID := chi.URLParam(r, "taskID")
	if taskID == "" {
		Error(w, http.StatusBadRequest, "missing task ID")
		return
	}

	tag, err := h.db.Exec(ctx,
		`DELETE FROM tasks WHERE id = $1 AND club_id = $2`,
		taskID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete task")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "task not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
