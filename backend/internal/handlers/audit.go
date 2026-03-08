package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// LogAudit bridges existing handler calls to the new audit_log schema.
// Kept for backward compatibility with handlers that haven't been refactored yet.
func LogAudit(ctx context.Context, db *pgxpool.Pool, clubID, userID, action, resource, resourceID string, oldData, newData any) error {
	details := map[string]any{}
	if oldData != nil {
		details["old"] = oldData
	}
	if newData != nil {
		details["new"] = newData
	}

	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return err
	}

	_, err = db.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, actor_ip, action, resource, resource_id, details)
		 VALUES ($1, $2, '', $3, $4, $5, $6)`,
		clubID, userID, action, resource, resourceID, detailsJSON,
	)
	return err
}

type AuditHandler struct {
	db     *pgxpool.Pool
	audit  *audit.Service
	config *config.Config
	log    zerolog.Logger
}

func NewAuditHandler(db *pgxpool.Pool, auditSvc *audit.Service, cfg *config.Config, log zerolog.Logger) *AuditHandler {
	return &AuditHandler{
		db:     db,
		audit:  auditSvc,
		config: cfg,
		log:    log.With().Str("handler", "audit").Logger(),
	}
}

type auditLogEntry struct {
	ID         string  `json:"id"`
	ClubID     *string `json:"club_id"`
	ActorID    *string `json:"actor_id"`
	ActorIP    string  `json:"actor_ip"`
	Action     string  `json:"action"`
	Resource   string  `json:"resource"`
	ResourceID *string `json:"resource_id"`
	Details    any     `json:"details"`
	CreatedAt  string  `json:"created_at"`
}

func (h *AuditHandler) HandleListAuditLog(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, club_id, actor_id, actor_ip, action, resource, resource_id, details, created_at
		FROM audit_log WHERE 1=1`
	args := []any{}
	argN := 1

	if action := q.Get("action"); action != "" {
		query += ` AND action = $` + strconv.Itoa(argN)
		args = append(args, action)
		argN++
	}
	if resource := q.Get("resource"); resource != "" {
		query += ` AND resource = $` + strconv.Itoa(argN)
		args = append(args, resource)
		argN++
	}
	if actorID := q.Get("actor_id"); actorID != "" {
		query += ` AND actor_id = $` + strconv.Itoa(argN)
		args = append(args, actorID)
		argN++
	}
	if from := q.Get("from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			query += ` AND created_at >= $` + strconv.Itoa(argN)
			args = append(args, t)
			argN++
		}
	}
	if to := q.Get("to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			query += ` AND created_at <= $` + strconv.Itoa(argN)
			args = append(args, t)
			argN++
		}
	}

	query += ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(argN) + ` OFFSET $` + strconv.Itoa(argN+1)
	args = append(args, limit, offset)

	rows, err := h.db.Query(r.Context(), query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query audit log")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	entries := []auditLogEntry{}
	for rows.Next() {
		var e auditLogEntry
		var details []byte
		var createdAt time.Time
		if err := rows.Scan(&e.ID, &e.ClubID, &e.ActorID, &e.ActorIP, &e.Action, &e.Resource, &e.ResourceID, &details, &createdAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan audit log entry")
			continue
		}
		e.CreatedAt = createdAt.Format(time.RFC3339)
		if details != nil {
			var d any
			if err := json.Unmarshal(details, &d); err == nil {
				e.Details = d
			}
		}
		entries = append(entries, e)
	}

	JSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"limit":   limit,
		"offset":  offset,
	})
}
