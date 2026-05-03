package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

var validUserRoles = map[string]bool{
	"applicant": true, "member": true, "slip_holder": true,
	"board": true, "harbor_master": true, "treasurer": true, "admin": true,
	// Board-officer roles. Identifier strings stay English snake_case
	// to match the existing convention; display labels are localized
	// in the SPA.
	"chair": true, "vice_chair": true, "deputy": true, "secretary": true,
}

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

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	// Accept either ?offset (matches the OpenAPI PaginationParams shape)
	// or legacy ?page=N as a 1-based index. Offset wins if both are set.
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, _ = strconv.Atoi(v)
	} else if v := r.URL.Query().Get("page"); v != "" {
		if page, _ := strconv.Atoi(v); page > 1 {
			offset = (page - 1) * limit
		}
	}
	if offset < 0 {
		offset = 0
	}

	// Whitelist sort columns + direction so callers can't smuggle SQL.
	// Default surface (last_name, first_name) keeps the natural roster
	// ordering for admin scanning.
	sortCol := "u.last_name NULLS LAST, u.first_name"
	switch r.URL.Query().Get("sort") {
	case "first_name":
		sortCol = "u.first_name, u.last_name"
	case "-first_name":
		sortCol = "u.first_name DESC, u.last_name DESC"
	case "last_name":
		sortCol = "u.last_name NULLS LAST, u.first_name"
	case "-last_name":
		sortCol = "u.last_name DESC NULLS LAST, u.first_name DESC"
	case "email":
		sortCol = "u.email"
	case "-email":
		sortCol = "u.email DESC"
	case "created_at":
		sortCol = "u.created_at"
	case "-created_at":
		sortCol = "u.created_at DESC"
	case "slip":
		// Section then number, naturally; users with no slip last so a
		// scan over assigned slips reads as a continuous block.
		sortCol = "s.section NULLS LAST, NULLIF(regexp_replace(s.number, '\\D', '', 'g'), '')::int NULLS LAST, s.number"
	case "-slip":
		sortCol = "s.section DESC NULLS LAST, NULLIF(regexp_replace(s.number, '\\D', '', 'g'), '')::int DESC NULLS LAST, s.number DESC"
	}

	// Optional ?spot=permanent|seasonal|none filter. Anything else is
	// silently ignored (returns the unfiltered set).
	spotFilter := ""
	switch r.URL.Query().Get("spot") {
	case "permanent":
		spotFilter = "AND sa.id IS NOT NULL AND sa.assignment_type = 'permanent'"
	case "seasonal":
		spotFilter = "AND sa.id IS NOT NULL AND sa.assignment_type = 'seasonal'"
	case "none":
		spotFilter = "AND sa.id IS NULL"
	}

	// Optional ?dock= filter restricts to users whose active slip is in
	// that section. Implies "has any slip" since the LEFT JOIN row would
	// otherwise be NULL.
	dockClause := ""
	args := []any{claims.ClubID}
	nextArg := 2
	if d := r.URL.Query().Get("dock"); d != "" {
		dockClause = fmt.Sprintf(" AND s.section = $%d", nextArg)
		args = append(args, d)
		nextArg++
	}

	// Optional ?q= fuzzy search across first_name, last_name, email.
	// Token-AND so multi-word queries (e.g. "ola nor") narrow the result;
	// each token is independently anchored with ILIKE %tok% across the
	// three name/email fields.
	searchClause := ""
	if q := r.URL.Query().Get("q"); q != "" {
		var conds []string
		for _, raw := range strings.Fields(q) {
			tok := "%" + strings.ReplaceAll(strings.ReplaceAll(raw, "%", `\%`), "_", `\_`) + "%"
			conds = append(conds, fmt.Sprintf("(u.first_name ILIKE $%d OR u.last_name ILIKE $%d OR u.email ILIKE $%d)", nextArg, nextArg, nextArg))
			args = append(args, tok)
			nextArg++
		}
		if len(conds) > 0 {
			searchClause = " AND " + strings.Join(conds, " AND ")
		}
	}

	var totalCount int
	err := h.db.QueryRow(ctx,
		`SELECT COUNT(DISTINCT u.id) FROM users u
		 LEFT JOIN slip_assignments sa
		        ON sa.user_id = u.id AND sa.club_id = u.club_id AND sa.released_at IS NULL
		 LEFT JOIN slips s ON s.id = sa.slip_id
		 WHERE u.club_id = $1 `+spotFilter+dockClause+searchClause,
		args...,
	).Scan(&totalCount)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to count users")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	limitArg := len(args) + 1
	offsetArg := len(args) + 2
	rows, err := h.db.Query(ctx,
		`SELECT u.id, u.email, u.first_name, u.last_name, COALESCE(u.full_name, ''),
		        u.phone, u.address_line, u.postal_code, u.city,
		        u.is_local, u.created_at, u.updated_at,
		        COALESCE(array_agg(DISTINCT ur.role) FILTER (WHERE ur.role IS NOT NULL), '{}'),
		        sa.slip_id, COALESCE(s.number, ''), COALESCE(s.section, ''),
		        COALESCE(sa.assignment_type::text, '')
		 FROM users u
		 LEFT JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		 LEFT JOIN slip_assignments sa
		        ON sa.user_id = u.id AND sa.club_id = u.club_id AND sa.released_at IS NULL
		 LEFT JOIN slips s ON s.id = sa.slip_id
		 WHERE u.club_id = $1 `+spotFilter+dockClause+searchClause+`
		 GROUP BY u.id, sa.slip_id, sa.assignment_type, s.number, s.section
		 ORDER BY `+sortCol+`
		 LIMIT $`+strconv.Itoa(limitArg)+` OFFSET $`+strconv.Itoa(offsetArg),
		append(args, limit, offset)...,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query users")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type userRow struct {
		ID                 string    `json:"id"`
		Email              string    `json:"email"`
		FirstName          string    `json:"first_name"`
		LastName           string    `json:"last_name"`
		FullName           string    `json:"full_name"`
		Phone              string    `json:"phone"`
		AddressLine        string    `json:"address_line"`
		PostalCode         string    `json:"postal_code"`
		City               string    `json:"city"`
		IsLocal            bool      `json:"is_local"`
		CreatedAt          time.Time `json:"created_at"`
		UpdatedAt          time.Time `json:"updated_at"`
		Roles              []string  `json:"roles"`
		SlipID             *string   `json:"slip_id,omitempty"`
		SlipNumber         string    `json:"slip_number"`
		SlipSection        string    `json:"slip_section"`
		SlipAssignmentType string    `json:"slip_assignment_type"`
	}

	var users []userRow
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.FullName, &u.Phone, &u.AddressLine, &u.PostalCode, &u.City, &u.IsLocal, &u.CreatedAt, &u.UpdatedAt, &u.Roles, &u.SlipID, &u.SlipNumber, &u.SlipSection, &u.SlipAssignmentType); err != nil {
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
		"limit":       limit,
		"offset":      offset,
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
		FirstName string   `json:"first_name"`
		LastName  string   `json:"last_name"`
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
		`SELECT u.id, u.email, u.first_name, u.last_name, COALESCE(u.full_name, ''),
		        u.phone, u.address_line, u.postal_code, u.city,
		        u.is_local, u.created_at, u.updated_at,
		        COALESCE(array_agg(ur.role) FILTER (WHERE ur.role IS NOT NULL), '{}')
		 FROM users u
		 LEFT JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		 WHERE u.id = $1 AND u.club_id = $2
		 GROUP BY u.id`,
		userID, claims.ClubID,
	).Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.FullName,
		&u.Phone, &u.Address, &u.PostalCd, &u.City,
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

	for _, role := range req.Roles {
		if !validUserRoles[role] {
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

	auditDetails, _ := json.Marshal(map[string]any{"old_roles": oldRoles, "new_roles": req.Roles})
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'update_roles', 'user', $3, $4)`,
		clubID, claims.UserID, userID, auditDetails,
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

	auditDetails, _ := json.Marshal(map[string]any{
		"old_email":     userEmail,
		"old_full_name": userName,
		"erasure":       erasure,
	})
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'delete_user', 'user', $3, $4)`,
		clubID, claims.UserID, userID, auditDetails,
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
			`UPDATE users SET email = 'deleted@deleted', first_name = 'Deleted', last_name = 'User',
			 phone = '', address_line = '', postal_code = '', city = '',
			 updated_at = now()
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

type adminUserCreateRequest struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	// FullName is accepted as a deprecated fallback while clients
	// migrate (DIL-228 → DIL-229). When supplied without first/last,
	// createUser splits it on the last space.
	FullName    string   `json:"full_name"`
	Phone       string   `json:"phone"`
	AddressLine string   `json:"address_line"`
	PostalCode  string   `json:"postal_code"`
	City        string   `json:"city"`
	IsLocal     bool     `json:"is_local"`
	Roles       []string `json:"roles"`
}

// splitFullName implements the same last-space heuristic the SQL backfill
// uses (DIL-227). Used to accept legacy full_name input from older clients
// and the CSV importer until callers migrate to first/last directly.
func splitFullName(s string) (first, last string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if i := strings.LastIndex(s, " "); i > 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func (h *AdminUsersHandler) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req adminUserCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	id, err := h.createUser(ctx, claims.ClubID, claims.UserID, req)
	if err != nil {
		var ve *validationError
		if errors.As(err, &ve) {
			Error(w, http.StatusBadRequest, ve.Error())
			return
		}
		var de *duplicateError
		if errors.As(err, &de) {
			Error(w, http.StatusConflict, de.Error())
			return
		}
		h.log.Error().Err(err).Msg("failed to create user")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	first, last := req.FirstName, req.LastName
	if first == "" && last == "" {
		first, last = splitFullName(req.FullName)
	}
	JSON(w, http.StatusCreated, map[string]any{
		"id":         id,
		"email":      req.Email,
		"first_name": first,
		"last_name":  last,
		"full_name":  strings.TrimSpace(first + " " + last),
		"roles":      req.Roles,
	})
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

type duplicateError struct{ email string }

func (e *duplicateError) Error() string { return fmt.Sprintf("user with email %q already exists", e.email) }

// createUser inserts a single user + roles inside a transaction. Returns
// the new user's ID. Errors are typed (validationError / duplicateError)
// so callers can map them to HTTP status codes.
func (h *AdminUsersHandler) createUser(ctx context.Context, clubID, actorID string, req adminUserCreateRequest) (string, error) {
	email := strings.TrimSpace(strings.ToLower(req.Email))
	firstName := strings.TrimSpace(req.FirstName)
	lastName := strings.TrimSpace(req.LastName)
	if firstName == "" && lastName == "" {
		// Legacy clients (and the CSV importer until DIL-229) may send
		// full_name only — split on the last space.
		firstName, lastName = splitFullName(req.FullName)
	}

	if email == "" {
		return "", &validationError{"email is required"}
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", &validationError{fmt.Sprintf("invalid email %q", email)}
	}
	if firstName == "" && lastName == "" {
		return "", &validationError{"first_name (or full_name) is required"}
	}
	for _, role := range req.Roles {
		if !validUserRoles[role] {
			return "", &validationError{fmt.Sprintf("invalid role: %s", role)}
		}
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name, phone, address_line, postal_code, city, is_local)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id`,
		clubID, email, firstName, lastName, req.Phone, req.AddressLine, req.PostalCode, req.City, req.IsLocal,
	).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", &duplicateError{email: email}
		}
		return "", fmt.Errorf("insert user: %w", err)
	}

	for _, role := range req.Roles {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_roles (user_id, club_id, role, granted_by) VALUES ($1, $2, $3, $4)`,
			id, clubID, role, actorID,
		); err != nil {
			return "", fmt.Errorf("insert role %s: %w", role, err)
		}
	}

	auditData, _ := json.Marshal(map[string]any{
		"email":      email,
		"first_name": firstName,
		"last_name":  lastName,
		"roles":      req.Roles,
	})
	// audit_log canonical schema (000001_init): actor_id, resource,
	// resource_id, details. Sibling admin handlers in this file use a
	// (user_id, entity_type, entity_id, new_data) shape that doesn't
	// match the table — tracked separately as a latent-bug sweep.
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'create_user', 'user', $3, $4)`,
		clubID, actorID, id, auditData,
	); err != nil {
		return "", fmt.Errorf("audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit tx: %w", err)
	}
	return id, nil
}

type csvImportRow struct {
	Row    int    `json:"row"`
	Email  string `json:"email"`
	Status string `json:"status"` // created | skipped | error
	Error  string `json:"error,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

// HandleImportUsersCSV ingests a CSV. Required columns: email AND
// (first_name OR full_name — full_name is split on the last space for
// backwards compat). Optional: last_name, phone, address_line,
// postal_code, city, is_local, roles (semicolon-separated, e.g.
// "member;board"). Each row commits in its own transaction so a single
// bad row doesn't poison the batch.
func (h *AdminUsersHandler) HandleImportUsersCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		Error(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		Error(w, http.StatusBadRequest, `missing "file" field`)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		Error(w, http.StatusBadRequest, "empty CSV or unreadable header")
		return
	}
	idx := map[string]int{}
	for i, name := range header {
		idx[strings.ToLower(strings.TrimSpace(name))] = i
	}
	if _, ok := idx["email"]; !ok {
		Error(w, http.StatusBadRequest, `missing required column "email"`)
		return
	}
	_, hasFirst := idx["first_name"]
	_, hasFull := idx["full_name"]
	if !hasFirst && !hasFull {
		Error(w, http.StatusBadRequest, `missing required column "first_name" (or legacy "full_name")`)
		return
	}

	get := func(record []string, col string) string {
		i, ok := idx[col]
		if !ok || i >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[i])
	}

	results := []csvImportRow{}
	created := 0
	rowNum := 1 // header is row 1; data rows start at 2
	for {
		rowNum++
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			results = append(results, csvImportRow{
				Row: rowNum, Status: "error", Error: fmt.Sprintf("parse: %v", err),
			})
			continue
		}

		req := adminUserCreateRequest{
			Email:       get(record, "email"),
			FirstName:   get(record, "first_name"),
			LastName:    get(record, "last_name"),
			FullName:    get(record, "full_name"),
			Phone:       get(record, "phone"),
			AddressLine: get(record, "address_line"),
			PostalCode:  get(record, "postal_code"),
			City:        get(record, "city"),
		}
		if v := get(record, "is_local"); v != "" {
			req.IsLocal = strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes")
		}
		if v := get(record, "roles"); v != "" {
			for _, role := range strings.Split(v, ";") {
				role = strings.TrimSpace(role)
				if role != "" {
					req.Roles = append(req.Roles, role)
				}
			}
		}

		id, err := h.createUser(ctx, claims.ClubID, claims.UserID, req)
		row := csvImportRow{Row: rowNum, Email: req.Email}
		switch {
		case err == nil:
			row.Status = "created"
			row.UserID = id
			created++
		case errors.As(err, new(*duplicateError)):
			row.Status = "skipped"
			row.Error = err.Error()
		case errors.As(err, new(*validationError)):
			row.Status = "error"
			row.Error = err.Error()
		default:
			h.log.Error().Err(err).Int("row", rowNum).Msg("csv import row failed")
			row.Status = "error"
			row.Error = "internal error"
		}
		results = append(results, row)
	}

	h.log.Info().
		Str("actor", claims.UserID).
		Int("processed", len(results)).
		Int("created", created).
		Msg("user CSV import complete")

	JSON(w, http.StatusOK, map[string]any{
		"created": created,
		"total":   len(results),
		"rows":    results,
	})
}

type adminUserUpdateRequest struct {
	FirstName   *string `json:"first_name,omitempty"`
	LastName    *string `json:"last_name,omitempty"`
	Phone       *string `json:"phone,omitempty"`
	AddressLine *string `json:"address_line,omitempty"`
	PostalCode  *string `json:"postal_code,omitempty"`
	City        *string `json:"city,omitempty"`
	IsLocal     *bool   `json:"is_local,omitempty"`
}

// HandleUpdateUser applies a partial update to a user's profile fields.
// Only supplied keys are written; nil pointers are left untouched. Email
// + roles are intentionally out of scope (separate endpoints).
func (h *AdminUsersHandler) HandleUpdateUser(w http.ResponseWriter, r *http.Request) {
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

	var req adminUserUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sets := []string{}
	args := []any{}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if req.Email != nil {
		v := strings.ToLower(strings.TrimSpace(*req.Email))
		if _, err := mail.ParseAddress(v); err != nil {
			Error(w, http.StatusBadRequest, "invalid email")
			return
		}
		add("email", v)
	}
	if req.FirstName != nil {
		v := strings.TrimSpace(*req.FirstName)
		if v == "" && (req.LastName == nil || strings.TrimSpace(*req.LastName) == "") {
			Error(w, http.StatusBadRequest, "first_name and last_name cannot both be empty")
			return
		}
		add("first_name", v)
	}
	if req.LastName != nil {
		add("last_name", strings.TrimSpace(*req.LastName))
	}
	if req.Phone != nil {
		add("phone", strings.TrimSpace(*req.Phone))
	}
	if req.AddressLine != nil {
		add("address_line", strings.TrimSpace(*req.AddressLine))
	}
	if req.PostalCode != nil {
		add("postal_code", strings.TrimSpace(*req.PostalCode))
	}
	if req.City != nil {
		add("city", strings.TrimSpace(*req.City))
	}
	if req.IsLocal != nil {
		add("is_local", *req.IsLocal)
	}
	if len(sets) == 0 {
		Error(w, http.StatusBadRequest, "no fields to update")
		return
	}
	sets = append(sets, "updated_at = now()")
	args = append(args, userID, claims.ClubID)

	q := fmt.Sprintf(
		`UPDATE users SET %s WHERE id = $%d AND club_id = $%d`,
		strings.Join(sets, ", "), len(args)-1, len(args),
	)

	tag, err := h.db.Exec(ctx, q, args...)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			Error(w, http.StatusConflict, "email already in use")
			return
		}
		h.log.Error().Err(err).Str("user_id", userID).Msg("failed to update user")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "user not found")
		return
	}

	auditData, _ := json.Marshal(req)
	if _, err := h.db.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'update_user', 'user', $3, $4)`,
		claims.ClubID, claims.UserID, userID, auditData,
	); err != nil {
		h.log.Warn().Err(err).Msg("failed to write audit log for update_user")
	}

	h.log.Info().
		Str("target_user", userID).
		Str("actor", claims.UserID).
		Msg("user updated")

	JSON(w, http.StatusOK, map[string]any{"id": userID, "updated": true})
}

type adminUserSlipUpdateRequest struct {
	SlipID         *string `json:"slip_id"`
	AssignmentType string  `json:"assignment_type,omitempty"`
}

// HandleSetUserSlip atomically sets or releases a user's active slip
// assignment. Pass slip_id=null to release. The new assignment_type
// defaults to 'permanent'. Conflicts (slip already taken, etc.) come
// back as 409.
func (h *AdminUsersHandler) HandleSetUserSlip(w http.ResponseWriter, r *http.Request) {
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

	var req adminUserSlipUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	assignmentType := req.AssignmentType
	if assignmentType == "" {
		assignmentType = "permanent"
	}
	if assignmentType != "permanent" && assignmentType != "seasonal" {
		Error(w, http.StatusBadRequest, "assignment_type must be 'permanent' or 'seasonal'")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("begin tx")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND club_id = $2)`,
		userID, claims.ClubID,
	).Scan(&exists); err != nil || !exists {
		Error(w, http.StatusNotFound, "user not found")
		return
	}

	// Release any existing active assignment for this user. Also flip
	// the slip back to vacant so /admin/slips listings stay consistent.
	rows, err := tx.Query(ctx,
		`UPDATE slip_assignments SET released_at = now()
		 WHERE user_id = $1 AND club_id = $2 AND released_at IS NULL
		 RETURNING slip_id`,
		userID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("release prior assignments")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	var releasedSlipIDs []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			h.log.Error().Err(err).Msg("scan released slip id")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		releasedSlipIDs = append(releasedSlipIDs, sid)
	}
	rows.Close()
	for _, sid := range releasedSlipIDs {
		if _, err := tx.Exec(ctx,
			`UPDATE slips SET status = 'vacant', updated_at = now()
			 WHERE id = $1 AND club_id = $2`,
			sid, claims.ClubID,
		); err != nil {
			h.log.Error().Err(err).Msg("update slip vacant")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	var newAssignmentID string
	if req.SlipID != nil && *req.SlipID != "" {
		var status string
		err = tx.QueryRow(ctx,
			`SELECT status FROM slips WHERE id = $1 AND club_id = $2 FOR UPDATE`,
			*req.SlipID, claims.ClubID,
		).Scan(&status)
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "slip not found")
			return
		}
		if err != nil {
			h.log.Error().Err(err).Msg("lock slip")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if status == "occupied" {
			Error(w, http.StatusConflict, "slip is already occupied")
			return
		}

		if err := tx.QueryRow(ctx,
			`INSERT INTO slip_assignments (slip_id, user_id, club_id, assignment_type)
			 VALUES ($1, $2, $3, $4)
			 RETURNING id`,
			*req.SlipID, userID, claims.ClubID, assignmentType,
		).Scan(&newAssignmentID); err != nil {
			h.log.Error().Err(err).Msg("insert assignment")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE slips SET status = 'occupied', updated_at = now() WHERE id = $1`,
			*req.SlipID,
		); err != nil {
			h.log.Error().Err(err).Msg("set slip occupied")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	auditData, _ := json.Marshal(map[string]any{
		"slip_id":         req.SlipID,
		"assignment_type": assignmentType,
		"released_slips":  releasedSlipIDs,
	})
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'set_user_slip', 'user', $3, $4)`,
		claims.ClubID, claims.UserID, userID, auditData,
	); err != nil {
		h.log.Warn().Err(err).Msg("audit log set_user_slip")
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("commit")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"user_id":         userID,
		"slip_id":         req.SlipID,
		"assignment_type": assignmentType,
		"assignment_id":   newAssignmentID,
	})
}
