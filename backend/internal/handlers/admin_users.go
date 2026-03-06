package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type AdminUsersHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewAdminUsersHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *AdminUsersHandler {
	return &AdminUsersHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "admin_users").Logger(),
	}
}

func (h *AdminUsersHandler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var totalCount int
	err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE club_id = $1`,
		claims.ClubID,
	).Scan(&totalCount)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to count users")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT u.id, u.email, u.full_name, u.phone, u.is_local, u.created_at, u.updated_at,
		        COALESCE(array_agg(ur.role) FILTER (WHERE ur.role IS NOT NULL), '{}')
		 FROM users u
		 LEFT JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		 WHERE u.club_id = $1
		 GROUP BY u.id
		 ORDER BY u.created_at DESC
		 LIMIT $2 OFFSET $3`,
		claims.ClubID, limit, offset,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query users")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type userRow struct {
		ID        string    `json:"id"`
		Email     string    `json:"email"`
		FullName  string    `json:"full_name"`
		Phone     string    `json:"phone"`
		IsLocal   bool      `json:"is_local"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Roles     []string  `json:"roles"`
	}

	var users []userRow
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Email, &u.FullName, &u.Phone, &u.IsLocal, &u.CreatedAt, &u.UpdatedAt, &u.Roles); err != nil {
			h.log.Error().Err(err).Msg("failed to scan user row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("user rows iteration error")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if users == nil {
		users = []userRow{}
	}

	JSON(w, http.StatusOK, map[string]any{
		"users":       users,
		"total_count": totalCount,
		"page":        page,
		"limit":       limit,
	})
}

func (h *AdminUsersHandler) HandleGetUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	userID := chi.URLParam(r, "userID")
	if userID == "" {
		Error(w, http.StatusBadRequest, "user ID is required")
		return
	}

	type userDetail struct {
		ID        string   `json:"id"`
		Email     string   `json:"email"`
		FullName  string   `json:"full_name"`
		Phone     string   `json:"phone"`
		Address   string   `json:"address_line"`
		PostalCd  string   `json:"postal_code"`
		City      string   `json:"city"`
		IsLocal   bool     `json:"is_local"`
		CreatedAt string   `json:"created_at"`
		UpdatedAt string   `json:"updated_at"`
		Roles     []string `json:"roles"`
	}

	var u userDetail
	err := h.db.QueryRow(ctx,
		`SELECT u.id, u.email, u.full_name, u.phone, u.address_line, u.postal_code, u.city,
		        u.is_local, u.created_at, u.updated_at,
		        COALESCE(array_agg(ur.role) FILTER (WHERE ur.role IS NOT NULL), '{}')
		 FROM users u
		 LEFT JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		 WHERE u.id = $1 AND u.club_id = $2
		 GROUP BY u.id`,
		userID, claims.ClubID,
	).Scan(&u.ID, &u.Email, &u.FullName, &u.Phone, &u.Address, &u.PostalCd, &u.City,
		&u.IsLocal, &u.CreatedAt, &u.UpdatedAt, &u.Roles)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("user_id", userID).Msg("failed to query user")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	type boatRow struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Type    string   `json:"type"`
		LengthM *float64 `json:"length_m"`
		BeamM   *float64 `json:"beam_m"`
		RegNum  string   `json:"registration_number"`
	}

	boatRows, err := h.db.Query(ctx,
		`SELECT b.id, b.name, b.type, b.length_m, b.beam_m, b.registration_number
		 FROM boats b
		 WHERE b.user_id = $1 AND b.club_id = $2
		 ORDER BY b.created_at DESC`,
		userID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query boats")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer boatRows.Close()

	var boats []boatRow
	for boatRows.Next() {
		var b boatRow
		if err := boatRows.Scan(&b.ID, &b.Name, &b.Type, &b.LengthM, &b.BeamM, &b.RegNum); err != nil {
			h.log.Error().Err(err).Msg("failed to scan boat row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		boats = append(boats, b)
	}
	if boats == nil {
		boats = []boatRow{}
	}

	type paymentRow struct {
		ID        string  `json:"id"`
		Type      string  `json:"type"`
		Amount    float64 `json:"amount"`
		Currency  string  `json:"currency"`
		Status    string  `json:"status"`
		PaidAt    *string `json:"paid_at"`
		CreatedAt string  `json:"created_at"`
	}

	payRows, err := h.db.Query(ctx,
		`SELECT p.id, p.type, p.amount, p.currency, p.status, p.paid_at, p.created_at
		 FROM payments p
		 WHERE p.user_id = $1 AND p.club_id = $2
		 ORDER BY p.created_at DESC
		 LIMIT 50`,
		userID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query payments")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer payRows.Close()

	var payments []paymentRow
	for payRows.Next() {
		var p paymentRow
		if err := payRows.Scan(&p.ID, &p.Type, &p.Amount, &p.Currency, &p.Status, &p.PaidAt, &p.CreatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan payment row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		payments = append(payments, p)
	}
	if payments == nil {
		payments = []paymentRow{}
	}

	JSON(w, http.StatusOK, map[string]any{
		"user":     u,
		"boats":    boats,
		"payments": payments,
	})
}

func (h *AdminUsersHandler) HandleUpdateUserRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	userID := chi.URLParam(r, "userID")
	if userID == "" {
		Error(w, http.StatusBadRequest, "user ID is required")
		return
	}

	var req struct {
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	validRoles := map[string]bool{
		"applicant": true, "member": true, "slip_owner": true,
		"styre": true, "harbour_master": true, "treasurer": true, "admin": true,
	}
	for _, role := range req.Roles {
		if !validRoles[role] {
			Error(w, http.StatusBadRequest, fmt.Sprintf("invalid role: %s", role))
			return
		}
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	clubID := claims.ClubID

	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND club_id = $2)`,
		userID, clubID,
	).Scan(&exists)
	if err != nil || !exists {
		Error(w, http.StatusNotFound, "user not found")
		return
	}

	var oldRoles []string
	rows, err := tx.Query(ctx,
		`SELECT role FROM user_roles WHERE user_id = $1 AND club_id = $2`,
		userID, clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query existing roles")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			rows.Close()
			h.log.Error().Err(err).Msg("failed to scan role")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		oldRoles = append(oldRoles, role)
	}
	rows.Close()

	_, err = tx.Exec(ctx,
		`DELETE FROM user_roles WHERE user_id = $1 AND club_id = $2`,
		userID, clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete existing roles")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	for _, role := range req.Roles {
		_, err = tx.Exec(ctx,
			`INSERT INTO user_roles (user_id, club_id, role, granted_by) VALUES ($1, $2, $3, $4)`,
			userID, clubID, role, claims.UserID,
		)
		if err != nil {
			h.log.Error().Err(err).Str("role", role).Msg("failed to insert role")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	oldData, _ := json.Marshal(map[string]any{"roles": oldRoles})
	newData, _ := json.Marshal(map[string]any{"roles": req.Roles})
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, user_id, action, entity_type, entity_id, old_data, new_data)
		 VALUES ($1, $2, 'update_roles', 'user', $3, $4, $5)`,
		clubID, claims.UserID, userID, oldData, newData,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to write audit log")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info().
		Str("target_user", userID).
		Str("actor", claims.UserID).
		Strs("new_roles", req.Roles).
		Msg("user roles updated")

	JSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"roles":   req.Roles,
	})
}

func (h *AdminUsersHandler) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	userID := chi.URLParam(r, "userID")
	if userID == "" {
		Error(w, http.StatusBadRequest, "user ID is required")
		return
	}

	if userID == claims.UserID {
		Error(w, http.StatusBadRequest, "cannot delete your own account via admin")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	clubID := claims.ClubID

	var userEmail, userName string
	err = tx.QueryRow(ctx,
		`SELECT email, full_name FROM users WHERE id = $1 AND club_id = $2`,
		userID, clubID,
	).Scan(&userEmail, &userName)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query user for deletion")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	erasure := r.URL.Query().Get("erasure") == "true"

	oldData, _ := json.Marshal(map[string]any{
		"email":     userEmail,
		"full_name": userName,
		"erasure":   erasure,
	})
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, user_id, action, entity_type, entity_id, old_data)
		 VALUES ($1, $2, 'delete_user', 'user', $3, $4)`,
		clubID, claims.UserID, userID, oldData,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to write audit log")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if erasure {
		_, err = tx.Exec(ctx, `DELETE FROM users WHERE id = $1 AND club_id = $2`, userID, clubID)
	} else {
		_, err = tx.Exec(ctx,
			`UPDATE users SET email = 'deleted@deleted', full_name = 'Deleted User',
			 phone = '', address_line = '', postal_code = '', city = '',
			 password_hash = NULL, vipps_sub = NULL, updated_at = now()
			 WHERE id = $1 AND club_id = $2`,
			userID, clubID,
		)
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete user")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	action := "soft_deleted"
	if erasure {
		action = "erased"
	}

	h.log.Info().
		Str("target_user", userID).
		Str("actor", claims.UserID).
		Str("action", action).
		Msg("user deleted")

	JSON(w, http.StatusOK, map[string]string{"status": action})
}
