package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

const defaultOfferDeadlineDays = 14

type WaitingListHandler struct {
	db     *pgxpool.Pool
	redis  *redis.Client
	config *config.Config
	log    zerolog.Logger
}

func NewWaitingListHandler(
	db *pgxpool.Pool,
	rdb *redis.Client,
	cfg *config.Config,
	log zerolog.Logger,
) *WaitingListHandler {
	return &WaitingListHandler{
		db:     db,
		redis:  rdb,
		config: cfg,
		log:    log.With().Str("handler", "waiting_list").Logger(),
	}
}

type waitingListEntry struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	ClubID        string     `json:"club_id"`
	Position      int        `json:"position"`
	IsLocal       bool       `json:"is_local"`
	Status        string     `json:"status"`
	OfferDeadline *time.Time `json:"offer_deadline,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type waitingListEntryWithUser struct {
	waitingListEntry
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
}

type joinResponse struct {
	Position int              `json:"position"`
	Entry    waitingListEntry `json:"entry"`
}

type offerRequest struct {
	DeadlineDays *int `json:"deadline_days,omitempty"`
}

type reorderRequest struct {
	NewPosition int `json:"new_position"`
}

func (h *WaitingListHandler) HandleJoinWaitingList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var existing string
	err = tx.QueryRow(ctx,
		`SELECT id FROM waiting_list_entries
		 WHERE user_id = $1 AND club_id = $2 AND status IN ('active', 'offered')`,
		claims.UserID, claims.ClubID,
	).Scan(&existing)
	if err == nil {
		Error(w, http.StatusConflict, "already on waiting list")
		return
	}
	if err != pgx.ErrNoRows {
		h.log.Error().Err(err).Msg("failed to check existing entry")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var nextPos int
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(position), 0) + 1 FROM waiting_list_entries WHERE club_id = $1`,
		claims.ClubID,
	).Scan(&nextPos)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to determine next position")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var isLocal bool
	err = tx.QueryRow(ctx,
		`SELECT is_local FROM users WHERE id = $1`,
		claims.UserID,
	).Scan(&isLocal)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch user locality")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var entry waitingListEntry
	err = tx.QueryRow(ctx,
		`INSERT INTO waiting_list_entries (user_id, club_id, position, is_local, status)
		 VALUES ($1, $2, $3, $4, 'active')
		 RETURNING id, user_id, club_id, position, is_local, status, offer_deadline, created_at, updated_at`,
		claims.UserID, claims.ClubID, nextPos, isLocal,
	).Scan(
		&entry.ID, &entry.UserID, &entry.ClubID, &entry.Position,
		&entry.IsLocal, &entry.Status, &entry.OfferDeadline,
		&entry.CreatedAt, &entry.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to insert waiting list entry")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, joinResponse{
		Position: entry.Position,
		Entry:    entry,
	})
}

func (h *WaitingListHandler) HandleGetMyPosition(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var entry waitingListEntry
	err := h.db.QueryRow(ctx,
		`SELECT id, user_id, club_id, position, is_local, status, offer_deadline, created_at, updated_at
		 FROM waiting_list_entries
		 WHERE user_id = $1 AND club_id = $2 AND status IN ('active', 'offered')`,
		claims.UserID, claims.ClubID,
	).Scan(
		&entry.ID, &entry.UserID, &entry.ClubID, &entry.Position,
		&entry.IsLocal, &entry.Status, &entry.OfferDeadline,
		&entry.CreatedAt, &entry.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "not on waiting list")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch waiting list position")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, entry)
}

func (h *WaitingListHandler) HandleListWaitingList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	statusFilter := r.URL.Query().Get("status")

	query := `SELECT wle.id, wle.user_id, wle.club_id, wle.position, wle.is_local,
	                 wle.status, wle.offer_deadline, wle.created_at, wle.updated_at,
	                 u.full_name, u.email, u.phone
	          FROM waiting_list_entries wle
	          JOIN users u ON u.id = wle.user_id
	          WHERE wle.club_id = $1`
	args := []any{claims.ClubID}

	if statusFilter != "" {
		query += ` AND wle.status = $2`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY wle.position`

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list waiting list")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	entries := make([]waitingListEntryWithUser, 0)
	for rows.Next() {
		var e waitingListEntryWithUser
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.ClubID, &e.Position, &e.IsLocal,
			&e.Status, &e.OfferDeadline, &e.CreatedAt, &e.UpdatedAt,
			&e.FullName, &e.Email, &e.Phone,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan waiting list entry")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating waiting list rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, entries)
}

func (h *WaitingListHandler) HandleOfferSlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	entryID := chi.URLParam(r, "entryID")
	if entryID == "" {
		Error(w, http.StatusBadRequest, "missing entry ID")
		return
	}

	var req offerRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			Error(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	days := defaultOfferDeadlineDays
	if req.DeadlineDays != nil && *req.DeadlineDays > 0 {
		days = *req.DeadlineDays
	}
	deadline := time.Now().AddDate(0, 0, days)

	var oldStatus string
	err := h.db.QueryRow(ctx,
		`SELECT status FROM waiting_list_entries WHERE id = $1 AND club_id = $2`,
		entryID, claims.ClubID,
	).Scan(&oldStatus)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "entry not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch entry for offer")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if oldStatus != "active" {
		Error(w, http.StatusConflict, fmt.Sprintf("entry status is '%s', expected 'active'", oldStatus))
		return
	}

	var entry waitingListEntry
	err = h.db.QueryRow(ctx,
		`UPDATE waiting_list_entries
		 SET status = 'offered', offer_deadline = $3, updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, user_id, club_id, position, is_local, status, offer_deadline, created_at, updated_at`,
		entryID, claims.ClubID, deadline,
	).Scan(
		&entry.ID, &entry.UserID, &entry.ClubID, &entry.Position,
		&entry.IsLocal, &entry.Status, &entry.OfferDeadline,
		&entry.CreatedAt, &entry.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update entry to offered")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "offer_slip", "waiting_list_entry", entryID,
		map[string]string{"status": oldStatus},
		map[string]any{"status": "offered", "offer_deadline": deadline},
	); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, entry)
}

func (h *WaitingListHandler) HandleReorderEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	entryID := chi.URLParam(r, "entryID")
	if entryID == "" {
		Error(w, http.StatusBadRequest, "missing entry ID")
		return
	}

	var req reorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.NewPosition < 1 {
		Error(w, http.StatusBadRequest, "new_position must be >= 1")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var oldPosition int
	err = tx.QueryRow(ctx,
		`SELECT position FROM waiting_list_entries WHERE id = $1 AND club_id = $2`,
		entryID, claims.ClubID,
	).Scan(&oldPosition)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "entry not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch entry position")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if oldPosition == req.NewPosition {
		JSON(w, http.StatusOK, map[string]string{"status": "no_change"})
		return
	}

	if req.NewPosition < oldPosition {
		_, err = tx.Exec(ctx,
			`UPDATE waiting_list_entries
			 SET position = position + 1, updated_at = now()
			 WHERE club_id = $1 AND position >= $2 AND position < $3 AND id != $4`,
			claims.ClubID, req.NewPosition, oldPosition, entryID,
		)
	} else {
		_, err = tx.Exec(ctx,
			`UPDATE waiting_list_entries
			 SET position = position - 1, updated_at = now()
			 WHERE club_id = $1 AND position > $2 AND position <= $3 AND id != $4`,
			claims.ClubID, oldPosition, req.NewPosition, entryID,
		)
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to shift positions")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var entry waitingListEntry
	err = tx.QueryRow(ctx,
		`UPDATE waiting_list_entries
		 SET position = $3, updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, user_id, club_id, position, is_local, status, offer_deadline, created_at, updated_at`,
		entryID, claims.ClubID, req.NewPosition,
	).Scan(
		&entry.ID, &entry.UserID, &entry.ClubID, &entry.Position,
		&entry.IsLocal, &entry.Status, &entry.OfferDeadline,
		&entry.CreatedAt, &entry.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update entry position")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit reorder transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "reorder_entry", "waiting_list_entry", entryID,
		map[string]int{"position": oldPosition},
		map[string]int{"position": req.NewPosition},
	); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, entry)
}

func (h *WaitingListHandler) HandleAcceptOffer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	entryID := chi.URLParam(r, "entryID")
	if entryID == "" {
		Error(w, http.StatusBadRequest, "missing entry ID")
		return
	}

	var currentStatus, ownerID string
	var offerDeadline *time.Time
	err := h.db.QueryRow(ctx,
		`SELECT status, user_id, offer_deadline FROM waiting_list_entries WHERE id = $1 AND club_id = $2`,
		entryID, claims.ClubID,
	).Scan(&currentStatus, &ownerID, &offerDeadline)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "entry not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch entry for accept")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if ownerID != claims.UserID {
		Error(w, http.StatusForbidden, "you can only accept your own offer")
		return
	}

	if currentStatus != "offered" {
		Error(w, http.StatusConflict, fmt.Sprintf("entry status is '%s', expected 'offered'", currentStatus))
		return
	}

	if offerDeadline != nil && time.Now().After(*offerDeadline) {
		Error(w, http.StatusGone, "offer has expired")
		return
	}

	var entry waitingListEntry
	err = h.db.QueryRow(ctx,
		`UPDATE waiting_list_entries
		 SET status = 'accepted', updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, user_id, club_id, position, is_local, status, offer_deadline, created_at, updated_at`,
		entryID, claims.ClubID,
	).Scan(
		&entry.ID, &entry.UserID, &entry.ClubID, &entry.Position,
		&entry.IsLocal, &entry.Status, &entry.OfferDeadline,
		&entry.CreatedAt, &entry.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to accept offer")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "accept_offer", "waiting_list_entry", entryID,
		map[string]string{"status": "offered"},
		map[string]string{"status": "accepted"},
	); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, entry)
}

func (h *WaitingListHandler) HandleWithdraw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var entryID, oldStatus string
	err := h.db.QueryRow(ctx,
		`SELECT id, status FROM waiting_list_entries
		 WHERE user_id = $1 AND club_id = $2 AND status IN ('active', 'offered')`,
		claims.UserID, claims.ClubID,
	).Scan(&entryID, &oldStatus)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "not on waiting list")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch entry for withdrawal")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var entry waitingListEntry
	err = h.db.QueryRow(ctx,
		`UPDATE waiting_list_entries
		 SET status = 'withdrawn', updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, user_id, club_id, position, is_local, status, offer_deadline, created_at, updated_at`,
		entryID, claims.ClubID,
	).Scan(
		&entry.ID, &entry.UserID, &entry.ClubID, &entry.Position,
		&entry.IsLocal, &entry.Status, &entry.OfferDeadline,
		&entry.CreatedAt, &entry.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to withdraw from waiting list")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "withdraw", "waiting_list_entry", entryID,
		map[string]string{"status": oldStatus},
		map[string]string{"status": "withdrawn"},
	); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, entry)
}
