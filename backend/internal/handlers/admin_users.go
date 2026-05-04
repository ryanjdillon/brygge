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
		sortCol = "primary_sa.section NULLS LAST, NULLIF(regexp_replace(primary_sa.number, '\\D', '', 'g'), '')::int NULLS LAST, primary_sa.number"
	case "-slip":
		sortCol = "primary_sa.section DESC NULLS LAST, NULLIF(regexp_replace(primary_sa.number, '\\D', '', 'g'), '')::int DESC NULLS LAST, primary_sa.number DESC"
	}

	// Optional ?spot=permanent|seasonal|none filter. Anything else is
	// silently ignored (returns the unfiltered set). Uses EXISTS so users
	// with multiple active assignments don't duplicate rows.
	spotFilter := ""
	switch r.URL.Query().Get("spot") {
	case "permanent":
		spotFilter = `AND EXISTS (
			SELECT 1 FROM slip_assignments sa2
			 WHERE sa2.user_id = u.id AND sa2.club_id = u.club_id
			   AND sa2.released_at IS NULL AND sa2.assignment_type = 'permanent'
		)`
	case "seasonal":
		spotFilter = `AND EXISTS (
			SELECT 1 FROM slip_assignments sa2
			 WHERE sa2.user_id = u.id AND sa2.club_id = u.club_id
			   AND sa2.released_at IS NULL AND sa2.assignment_type = 'seasonal'
		)`
	case "none":
		spotFilter = `AND NOT EXISTS (
			SELECT 1 FROM slip_assignments sa2
			 WHERE sa2.user_id = u.id AND sa2.club_id = u.club_id
			   AND sa2.released_at IS NULL
		)`
	}

	// Optional ?dock= filter restricts to users with at least one active
	// slip in that section.
	dockClause := ""
	args := []any{claims.ClubID}
	nextArg := 2
	if d := r.URL.Query().Get("dock"); d != "" {
		dockClause = fmt.Sprintf(` AND EXISTS (
			SELECT 1 FROM slip_assignments sa3
			  JOIN slips s3 ON s3.id = sa3.slip_id
			 WHERE sa3.user_id = u.id AND sa3.club_id = u.club_id
			   AND sa3.released_at IS NULL AND s3.section = $%d
		)`, nextArg)
		args = append(args, d)
		nextArg++
	}

	// Optional ?notes_only=true filter restricts to users with non-empty
	// admin_notes (an admin-only convenience for review backlogs).
	notesClause := ""
	if v := r.URL.Query().Get("notes_only"); v != "" &&
		(strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes")) {
		notesClause = " AND COALESCE(u.admin_notes, '') <> ''"
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
		`SELECT COUNT(*) FROM users u
		 WHERE u.club_id = $1 `+spotFilter+dockClause+notesClause+searchClause,
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
		        u.is_local, u.admin_notes, u.created_at, u.updated_at,
		        COALESCE(array_agg(DISTINCT ur.role) FILTER (WHERE ur.role IS NOT NULL), '{}'),
		        primary_sa.slip_id,
		        COALESCE(primary_sa.number, ''),
		        COALESCE(primary_sa.section, ''),
		        COALESCE(primary_sa.assignment_type, ''),
		        COALESCE(all_slips.slips, '[]'::jsonb)
		 FROM users u
		 LEFT JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		 LEFT JOIN LATERAL (
		     SELECT sa.slip_id, s.number, s.section, sa.assignment_type::text AS assignment_type
		       FROM slip_assignments sa
		       JOIN slips s ON s.id = sa.slip_id
		      WHERE sa.user_id = u.id AND sa.club_id = u.club_id AND sa.released_at IS NULL
		      ORDER BY s.section NULLS LAST,
		               NULLIF(regexp_replace(s.number, '\D', '', 'g'), '')::int NULLS LAST,
		               s.number
		      LIMIT 1
		 ) primary_sa ON true
		 LEFT JOIN LATERAL (
		     SELECT jsonb_agg(jsonb_build_object(
		                'slip_id', sa.slip_id,
		                'slip_number', s.number,
		                'slip_section', s.section,
		                'assignment_type', sa.assignment_type::text
		            ) ORDER BY s.section, s.number) AS slips
		       FROM slip_assignments sa
		       JOIN slips s ON s.id = sa.slip_id
		      WHERE sa.user_id = u.id AND sa.club_id = u.club_id AND sa.released_at IS NULL
		 ) all_slips ON true
		 WHERE u.club_id = $1 `+spotFilter+dockClause+notesClause+searchClause+`
		 GROUP BY u.id, primary_sa.slip_id, primary_sa.assignment_type, primary_sa.number, primary_sa.section, all_slips.slips
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
		AdminNotes         string    `json:"admin_notes"`
		CreatedAt          time.Time `json:"created_at"`
		UpdatedAt          time.Time `json:"updated_at"`
		Roles              []string  `json:"roles"`
		SlipID             *string         `json:"slip_id,omitempty"`
		SlipNumber         string          `json:"slip_number"`
		SlipSection        string          `json:"slip_section"`
		SlipAssignmentType string          `json:"slip_assignment_type"`
		Slips              json.RawMessage `json:"slips"`
	}

	var users []userRow
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.FullName, &u.Phone, &u.AddressLine, &u.PostalCode, &u.City, &u.IsLocal, &u.AdminNotes, &u.CreatedAt, &u.UpdatedAt, &u.Roles, &u.SlipID, &u.SlipNumber, &u.SlipSection, &u.SlipAssignmentType, &u.Slips); err != nil {
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

	type slipAssignmentDetail struct {
		SlipID         string  `json:"slip_id"`
		SlipNumber     string  `json:"slip_number"`
		SlipSection    string  `json:"slip_section"`
		AssignmentType string  `json:"assignment_type"`
		BoatID         *string `json:"boat_id"`
	}

	type userDetail struct {
		ID         string                 `json:"id"`
		Email      string                 `json:"email"`
		FirstName  string                 `json:"first_name"`
		LastName   string                 `json:"last_name"`
		FullName   string                 `json:"full_name"`
		Phone      string                 `json:"phone"`
		Address    string                 `json:"address_line"`
		PostalCd   string                 `json:"postal_code"`
		City       string                 `json:"city"`
		IsLocal    bool                   `json:"is_local"`
		AdminNotes string                 `json:"admin_notes"`
		CreatedAt  time.Time              `json:"created_at"`
		UpdatedAt  time.Time              `json:"updated_at"`
		Roles      []string               `json:"roles"`
		Slips      []slipAssignmentDetail `json:"slips"`
	}

	var u userDetail
	var slipsJSON []byte
	err := h.db.QueryRow(ctx,
		`SELECT u.id, u.email, u.first_name, u.last_name, COALESCE(u.full_name, ''),
		        u.phone, u.address_line, u.postal_code, u.city,
		        u.is_local, u.admin_notes, u.created_at, u.updated_at,
		        COALESCE(array_agg(DISTINCT ur.role) FILTER (WHERE ur.role IS NOT NULL), '{}'),
		        COALESCE((
		            SELECT jsonb_agg(jsonb_build_object(
		                'slip_id', sa.slip_id,
		                'slip_number', s.number,
		                'slip_section', s.section,
		                'assignment_type', sa.assignment_type::text,
		                'boat_id', sa.boat_id
		            ) ORDER BY s.section, s.number)
		              FROM slip_assignments sa
		              JOIN slips s ON s.id = sa.slip_id
		             WHERE sa.user_id = u.id AND sa.club_id = u.club_id AND sa.released_at IS NULL
		        ), '[]'::jsonb)
		 FROM users u
		 LEFT JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		 WHERE u.id = $1 AND u.club_id = $2
		 GROUP BY u.id`,
		userID, claims.ClubID,
	).Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.FullName,
		&u.Phone, &u.Address, &u.PostalCd, &u.City,
		&u.IsLocal, &u.AdminNotes, &u.CreatedAt, &u.UpdatedAt, &u.Roles, &slipsJSON)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("user_id", userID).Msg("failed to query user")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if len(slipsJSON) > 0 {
		_ = json.Unmarshal(slipsJSON, &u.Slips)
	}

	type boatRow struct {
		ID                 string   `json:"id"`
		Name               string   `json:"name"`
		Type               string   `json:"type"`
		Manufacturer       string   `json:"manufacturer"`
		Model              string   `json:"model"`
		LengthM            *float64 `json:"length_m"`
		BeamM              *float64 `json:"beam_m"`
		DraftM             *float64 `json:"draft_m"`
		WeightKg           *float64 `json:"weight_kg"`
		RegistrationNumber    string   `json:"registration_number"`
		MeasurementsConfirmed bool     `json:"measurements_confirmed"`
	}

	boatRows, err := h.db.Query(ctx,
		`SELECT b.id, b.name, b.type, b.manufacturer, b.model,
		        b.length_m, b.beam_m, b.draft_m, b.weight_kg, b.registration_number,
		        b.measurements_confirmed
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
		if err := boatRows.Scan(&b.ID, &b.Name, &b.Type, &b.Manufacturer, &b.Model,
			&b.LengthM, &b.BeamM, &b.DraftM, &b.WeightKg, &b.RegistrationNumber,
			&b.MeasurementsConfirmed); err != nil {
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
		PaidAt    *time.Time `json:"paid_at"`
		CreatedAt time.Time  `json:"created_at"`
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
	AdminNotes  string   `json:"admin_notes"`
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
		`INSERT INTO users (club_id, email, first_name, last_name, phone, address_line, postal_code, city, is_local, admin_notes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id`,
		clubID, email, firstName, lastName, req.Phone, req.AddressLine, req.PostalCode, req.City, req.IsLocal, req.AdminNotes,
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
			AdminNotes:  get(record, "admin_notes"),
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

// HandleExportUsersCSV streams the club's user list as a CSV file in the
// same column shape that HandleImportUsersCSV accepts, so an admin can
// round-trip (export → edit in spreadsheet → import) without column
// renames. Roles are joined with ';' to match the import parser.
func (h *AdminUsersHandler) HandleExportUsersCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx, `
		SELECT u.email,
		       u.first_name, u.last_name,
		       u.phone, u.address_line, u.postal_code, u.city, u.is_local,
		       COALESCE(u.admin_notes, ''),
		       COALESCE(
		           (SELECT string_agg(ur.role::text, ';' ORDER BY ur.role::text)
		              FROM user_roles ur
		             WHERE ur.user_id = u.id),
		           ''
		       ) AS roles
		  FROM users u
		 WHERE u.club_id = $1
		 ORDER BY u.last_name, u.first_name, u.email`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query users for CSV export")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="users_%s.csv"`, time.Now().Format("2006-01-02")))

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"email", "first_name", "last_name",
		"phone", "address_line", "postal_code", "city", "is_local",
		"admin_notes", "roles",
	})

	count := 0
	for rows.Next() {
		var email, firstName, lastName, phone, addr, postal, city, notes, roles string
		var isLocal bool
		if err := rows.Scan(&email, &firstName, &lastName, &phone, &addr, &postal, &city, &isLocal, &notes, &roles); err != nil {
			h.log.Error().Err(err).Msg("failed to scan user CSV row")
			continue
		}
		isLocalStr := "false"
		if isLocal {
			isLocalStr = "true"
		}
		_ = cw.Write([]string{email, firstName, lastName, phone, addr, postal, city, isLocalStr, notes, roles})
		count++
	}
	cw.Flush()

	h.log.Info().
		Str("actor", claims.UserID).
		Int("rows", count).
		Msg("user CSV export")
}

type adminUserUpdateRequest struct {
	Email       *string `json:"email,omitempty"`
	FirstName   *string `json:"first_name,omitempty"`
	LastName    *string `json:"last_name,omitempty"`
	Phone       *string `json:"phone,omitempty"`
	AddressLine *string `json:"address_line,omitempty"`
	PostalCode  *string `json:"postal_code,omitempty"`
	City        *string `json:"city,omitempty"`
	IsLocal     *bool   `json:"is_local,omitempty"`
	AdminNotes  *string `json:"admin_notes,omitempty"`
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
	if req.AdminNotes != nil {
		add("admin_notes", *req.AdminNotes)
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

// adminBoatSlipUpdateRequest is the body for PUT
// /admin/users/{userID}/boats/{boatID}/slip — assigns a single slip to a
// specific boat (or releases it when slip_id is null/omitted).
type adminBoatSlipUpdateRequest struct {
	SlipID         *string `json:"slip_id"`
	AssignmentType string  `json:"assignment_type,omitempty"`
}

// HandleSetUserBoatSlip atomically sets or releases the active slip
// assignment for a specific boat. Pass slip_id=null to release. Honors
// the per-boat unique active assignment constraint and the per-slip
// vacancy check. Conflicts come back as 409.
func (h *AdminUsersHandler) HandleSetUserBoatSlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	userID := chi.URLParam(r, "userID")
	boatID := chi.URLParam(r, "boatID")
	if userID == "" || boatID == "" {
		Error(w, http.StatusBadRequest, "user ID and boat ID are required")
		return
	}

	var req adminBoatSlipUpdateRequest
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

	var ownerCheck bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM boats
		    WHERE id = $1 AND user_id = $2 AND club_id = $3)`,
		boatID, userID, claims.ClubID,
	).Scan(&ownerCheck); err != nil || !ownerCheck {
		Error(w, http.StatusNotFound, "boat not found for user")
		return
	}

	// Release any existing active assignment for this boat.
	rows, err := tx.Query(ctx,
		`UPDATE slip_assignments SET released_at = now()
		 WHERE boat_id = $1 AND club_id = $2 AND released_at IS NULL
		 RETURNING slip_id`,
		boatID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("release prior boat assignments")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	var releasedSlipIDs []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
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
			`INSERT INTO slip_assignments (slip_id, user_id, club_id, assignment_type, boat_id)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING id`,
			*req.SlipID, userID, claims.ClubID, assignmentType, boatID,
		).Scan(&newAssignmentID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				Error(w, http.StatusConflict, "slip or boat already assigned")
				return
			}
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
		"boat_id":         boatID,
		"slip_id":         req.SlipID,
		"assignment_type": assignmentType,
		"released_slips":  releasedSlipIDs,
	})
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'set_user_boat_slip', 'boat', $3, $4)`,
		claims.ClubID, claims.UserID, boatID, auditData,
	); err != nil {
		h.log.Warn().Err(err).Msg("audit log set_user_boat_slip")
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("commit")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"user_id":         userID,
		"boat_id":         boatID,
		"slip_id":         req.SlipID,
		"assignment_type": assignmentType,
		"assignment_id":   newAssignmentID,
	})
}


type slipAssignmentRow struct {
	SlipID         string  `json:"slip_id"`
	BoatID         *string `json:"boat_id,omitempty"`
	AssignmentType string  `json:"assignment_type,omitempty"`
}

type adminUserSlipsUpdateRequest struct {
	Slips []slipAssignmentRow `json:"slips"`
}

// HandleSetUserSlips replaces the user's full set of active slip
// assignments in a single transaction. The schema already permits
// multiple active assignments per user (the only uniqueness constraint
// is one active assignment per slip). The handler diffs the requested
// set against the current active set:
//
//   - Slips removed from the set: released_at = now() (slip → vacant if
//     no other active assignment remains).
//   - Slips added to the set: INSERT (slip must currently be vacant).
//   - Slips present in both with a different assignment_type: UPDATE.
//
// On any conflict (slip taken by someone else) the whole transaction
// rolls back.
func (h *AdminUsersHandler) HandleSetUserSlips(w http.ResponseWriter, r *http.Request) {
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

	var req adminUserSlipsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	type wantedRow struct {
		assignmentType string
		boatID         string
	}
	wanted := map[string]wantedRow{}
	usedBoats := map[string]string{}
	for _, s := range req.Slips {
		if s.SlipID == "" {
			continue
		}
		t := s.AssignmentType
		if t == "" {
			t = "permanent"
		}
		if t != "permanent" && t != "seasonal" {
			Error(w, http.StatusBadRequest, "assignment_type must be 'permanent' or 'seasonal'")
			return
		}
		if _, dup := wanted[s.SlipID]; dup {
			Error(w, http.StatusBadRequest, "duplicate slip_id in request")
			return
		}
		bid := ""
		if s.BoatID != nil {
			bid = *s.BoatID
		}
		wanted[s.SlipID] = wantedRow{assignmentType: t, boatID: bid}
		if bid != "" {
			if other, dup := usedBoats[bid]; dup {
				Error(w, http.StatusBadRequest, fmt.Sprintf("boat %s assigned to multiple slips (%s and %s)", bid, other, s.SlipID))
				return
			}
			usedBoats[bid] = s.SlipID
		}
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

	// Load this user's boats so we can default the boat_id when omitted
	// and validate any explicitly-supplied ids.
	ownerBoats := []string{}
	boatRows, err := tx.Query(ctx,
		`SELECT id FROM boats WHERE user_id = $1 AND club_id = $2 ORDER BY created_at`,
		userID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("query owner boats")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	for boatRows.Next() {
		var id string
		if err := boatRows.Scan(&id); err != nil {
			boatRows.Close()
			h.log.Error().Err(err).Msg("scan owner boat")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		ownerBoats = append(ownerBoats, id)
	}
	boatRows.Close()
	ownedBoatSet := map[string]bool{}
	for _, id := range ownerBoats {
		ownedBoatSet[id] = true
	}

	// Resolve boat_id for every wanted row. Validate ownership.
	for sid, wr := range wanted {
		if wr.boatID == "" {
			if len(ownerBoats) == 0 {
				Error(w, http.StatusBadRequest, fmt.Sprintf("user has no boats; cannot assign slip %s", sid))
				return
			}
			if len(ownerBoats) > 1 {
				Error(w, http.StatusBadRequest, fmt.Sprintf("user has multiple boats; boat_id required for slip %s", sid))
				return
			}
			wr.boatID = ownerBoats[0]
			if other, dup := usedBoats[wr.boatID]; dup && other != sid {
				Error(w, http.StatusBadRequest, fmt.Sprintf("boat %s already assigned to slip %s", wr.boatID, other))
				return
			}
			usedBoats[wr.boatID] = sid
			wanted[sid] = wr
			continue
		}
		if !ownedBoatSet[wr.boatID] {
			Error(w, http.StatusBadRequest, fmt.Sprintf("boat %s not owned by user", wr.boatID))
			return
		}
	}

	// Snapshot current active assignments for this user.
	type currentRow struct {
		assignmentID   string
		assignmentType string
		boatID         *string
	}
	current := map[string]currentRow{}
	rows, err := tx.Query(ctx,
		`SELECT id, slip_id, assignment_type::text, boat_id
		   FROM slip_assignments
		  WHERE user_id = $1 AND club_id = $2 AND released_at IS NULL`,
		userID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("query current assignments")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	for rows.Next() {
		var aid, sid, at string
		var bid *string
		if err := rows.Scan(&aid, &sid, &at, &bid); err != nil {
			rows.Close()
			h.log.Error().Err(err).Msg("scan current assignment")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		current[sid] = currentRow{assignmentID: aid, assignmentType: at, boatID: bid}
	}
	rows.Close()

	released := []string{}
	for sid, cur := range current {
		if _, keep := wanted[sid]; keep {
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE slip_assignments SET released_at = now() WHERE id = $1`,
			cur.assignmentID,
		); err != nil {
			h.log.Error().Err(err).Msg("release assignment")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE slips SET status = 'vacant', updated_at = now() WHERE id = $1 AND club_id = $2`,
			sid, claims.ClubID,
		); err != nil {
			h.log.Error().Err(err).Msg("set slip vacant")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		released = append(released, sid)
	}

	added := []string{}
	for sid, wr := range wanted {
		if cur, exists := current[sid]; exists {
			needType := cur.assignmentType != wr.assignmentType
			needBoat := cur.boatID == nil || *cur.boatID != wr.boatID
			if needType || needBoat {
				if _, err := tx.Exec(ctx,
					`UPDATE slip_assignments SET assignment_type = $1, boat_id = $2 WHERE id = $3`,
					wr.assignmentType, wr.boatID, cur.assignmentID,
				); err != nil {
					var pgErr *pgconn.PgError
					if errors.As(err, &pgErr) && pgErr.Code == "23505" {
						Error(w, http.StatusConflict, "boat already assigned to another active slip")
						return
					}
					h.log.Error().Err(err).Msg("update assignment")
					Error(w, http.StatusInternalServerError, "internal error")
					return
				}
			}
			continue
		}
		var status string
		err := tx.QueryRow(ctx,
			`SELECT status FROM slips WHERE id = $1 AND club_id = $2 FOR UPDATE`,
			sid, claims.ClubID,
		).Scan(&status)
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, fmt.Sprintf("slip not found: %s", sid))
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
		if _, err := tx.Exec(ctx,
			`INSERT INTO slip_assignments (slip_id, user_id, club_id, assignment_type, boat_id)
			 VALUES ($1, $2, $3, $4, $5)`,
			sid, userID, claims.ClubID, wr.assignmentType, wr.boatID,
		); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				Error(w, http.StatusConflict, "slip or boat already assigned")
				return
			}
			h.log.Error().Err(err).Msg("insert assignment")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE slips SET status = 'occupied', updated_at = now() WHERE id = $1`,
			sid,
		); err != nil {
			h.log.Error().Err(err).Msg("set slip occupied")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		added = append(added, sid)
	}

	auditData, _ := json.Marshal(map[string]any{
		"wanted":   wanted,
		"released": released,
		"added":    added,
	})
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'set_user_slips', 'user', $3, $4)`,
		claims.ClubID, claims.UserID, userID, auditData,
	); err != nil {
		h.log.Warn().Err(err).Msg("audit log set_user_slips")
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("commit")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"user_id":  userID,
		"slips":    req.Slips,
		"released": released,
		"added":    added,
	})
}

// adminBoatCreateRequest mirrors createBoatRequest plus an admin-only
// `approve` flag. When approve=true, the new boat is marked
// measurements_confirmed=true with confirmed_by set to the acting admin
// — bypassing the model-match heuristic the member path relies on.
type adminBoatCreateRequest struct {
	createBoatRequest
	Approve bool `json:"approve"`
}

type adminBoatUpdateRequest struct {
	updateBoatRequest
	Approve bool `json:"approve"`
}

// HandleCreateUserBoat creates a boat for a target user via the shared
// createBoatForUser helper. Admin-only — regular members use
// /members/me/boats which dispatches through the same helper.
func (h *AdminUsersHandler) HandleCreateUserBoat(w http.ResponseWriter, r *http.Request) {
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
	var req adminBoatCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var exists bool
	if err := h.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND club_id = $2)`,
		userID, claims.ClubID,
	).Scan(&exists); err != nil || !exists {
		Error(w, http.StatusNotFound, "user not found")
		return
	}

	b, err := createBoatForUser(ctx, h.db, h.log,
		userID, claims.ClubID, claims.UserID, req.createBoatRequest, req.Approve)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	auditData, _ := json.Marshal(req)
	if _, err := h.db.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'admin_create_boat', 'boat', $3, $4)`,
		claims.ClubID, claims.UserID, b.ID, auditData,
	); err != nil {
		h.log.Warn().Err(err).Msg("audit admin_create_boat")
	}

	JSON(w, http.StatusCreated, b)
}

// HandleUpdateUserBoat updates a target user's boat via the shared helper.
// (boatID,userID) is verified so a stale URL can't reach into another
// user's row.
func (h *AdminUsersHandler) HandleUpdateUserBoat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	userID := chi.URLParam(r, "userID")
	boatID := chi.URLParam(r, "boatID")
	if userID == "" || boatID == "" {
		Error(w, http.StatusBadRequest, "user and boat IDs are required")
		return
	}
	var req adminBoatUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	b, err := updateBoatForUser(ctx, h.db, h.log,
		boatID, userID, claims.ClubID, claims.UserID, req.updateBoatRequest, req.Approve)
	if errors.Is(err, boatErrNotFound) {
		Error(w, http.StatusNotFound, "boat not found")
		return
	}
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	auditData, _ := json.Marshal(req)
	if _, err := h.db.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'admin_update_boat', 'boat', $3, $4)`,
		claims.ClubID, claims.UserID, boatID, auditData,
	); err != nil {
		h.log.Warn().Err(err).Msg("audit admin_update_boat")
	}

	JSON(w, http.StatusOK, b)
}

// HandleDeleteUserBoat removes a boat. ON DELETE SET NULL on
// slip_assignments.boat_id unlinks the boat from any active assignment.
func (h *AdminUsersHandler) HandleDeleteUserBoat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	userID := chi.URLParam(r, "userID")
	boatID := chi.URLParam(r, "boatID")
	if userID == "" || boatID == "" {
		Error(w, http.StatusBadRequest, "user and boat IDs are required")
		return
	}

	if err := deleteBoatForUser(ctx, h.db, h.log, boatID, userID, claims.ClubID); err != nil {
		if errors.Is(err, boatErrNotFound) {
			Error(w, http.StatusNotFound, "boat not found")
			return
		}
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if _, err := h.db.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'admin_delete_boat', 'boat', $3, $4)`,
		claims.ClubID, claims.UserID, boatID, json.RawMessage(`{}`),
	); err != nil {
		h.log.Warn().Err(err).Msg("audit admin_delete_boat")
	}

	w.WriteHeader(http.StatusNoContent)
}
