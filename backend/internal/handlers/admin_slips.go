package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type AdminSlipsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewAdminSlipsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *AdminSlipsHandler {
	return &AdminSlipsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "admin_slips").Logger(),
	}
}

func (h *AdminSlipsHandler) clubID(r *http.Request) (string, string, error) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		return "", "", pgx.ErrNoRows
	}
	var clubID string
	err := h.db.QueryRow(r.Context(), `SELECT id FROM clubs WHERE slug = $1`, claims.ClubID).Scan(&clubID)
	return clubID, claims.UserID, err
}

func (h *AdminSlipsHandler) HandleListSlips(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	status := r.URL.Query().Get("status")
	section := r.URL.Query().Get("section")

	query := `
		SELECT s.id, s.number, s.section, s.length_m, s.width_m, s.depth_m,
		       s.status, s.map_x, s.map_y, s.created_at, s.updated_at,
		       sa.user_id, u.full_name, u.email
		FROM slips s
		JOIN clubs c ON c.id = s.club_id
		LEFT JOIN slip_assignments sa ON sa.slip_id = s.id AND sa.released_at IS NULL
		LEFT JOIN users u ON u.id = sa.user_id
		WHERE c.slug = $1`

	args := []any{claims.ClubID}
	argIdx := 2

	if status != "" {
		query += ` AND s.status = $` + itoa(argIdx)
		args = append(args, status)
		argIdx++
	}
	if section != "" {
		query += ` AND s.section = $` + itoa(argIdx)
		args = append(args, section)
		argIdx++
	}
	_ = argIdx

	query += ` ORDER BY s.section, s.number`

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query slips")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type slipRow struct {
		ID            string   `json:"id"`
		Number        string   `json:"number"`
		Section       string   `json:"section"`
		LengthM       *float64 `json:"length_m"`
		WidthM        *float64 `json:"width_m"`
		DepthM        *float64 `json:"depth_m"`
		Status        string   `json:"status"`
		MapX          *float64 `json:"map_x"`
		MapY          *float64 `json:"map_y"`
		CreatedAt     string   `json:"created_at"`
		UpdatedAt     string   `json:"updated_at"`
		OccupantID    *string  `json:"occupant_id"`
		OccupantName  *string  `json:"occupant_name"`
		OccupantEmail *string  `json:"occupant_email"`
	}

	var slips []slipRow
	for rows.Next() {
		var s slipRow
		if err := rows.Scan(
			&s.ID, &s.Number, &s.Section, &s.LengthM, &s.WidthM, &s.DepthM,
			&s.Status, &s.MapX, &s.MapY, &s.CreatedAt, &s.UpdatedAt,
			&s.OccupantID, &s.OccupantName, &s.OccupantEmail,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan slip row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		slips = append(slips, s)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("slip rows iteration error")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if slips == nil {
		slips = []slipRow{}
	}

	JSON(w, http.StatusOK, map[string]any{"slips": slips})
}

func (h *AdminSlipsHandler) HandleGetSlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	slipID := chi.URLParam(r, "slipID")
	if slipID == "" {
		Error(w, http.StatusBadRequest, "slip ID is required")
		return
	}

	type slipDetail struct {
		ID        string   `json:"id"`
		Number    string   `json:"number"`
		Section   string   `json:"section"`
		LengthM   *float64 `json:"length_m"`
		WidthM    *float64 `json:"width_m"`
		DepthM    *float64 `json:"depth_m"`
		Status    string   `json:"status"`
		MapX      *float64 `json:"map_x"`
		MapY      *float64 `json:"map_y"`
		CreatedAt string   `json:"created_at"`
		UpdatedAt string   `json:"updated_at"`
	}

	var s slipDetail
	err := h.db.QueryRow(ctx,
		`SELECT s.id, s.number, s.section, s.length_m, s.width_m, s.depth_m,
		        s.status, s.map_x, s.map_y, s.created_at, s.updated_at
		 FROM slips s
		 JOIN clubs c ON c.id = s.club_id
		 WHERE s.id = $1 AND c.slug = $2`,
		slipID, claims.ClubID,
	).Scan(&s.ID, &s.Number, &s.Section, &s.LengthM, &s.WidthM, &s.DepthM,
		&s.Status, &s.MapX, &s.MapY, &s.CreatedAt, &s.UpdatedAt)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "slip not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("slip_id", slipID).Msg("failed to query slip")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	type assignmentRow struct {
		ID          string   `json:"id"`
		UserID      string   `json:"user_id"`
		UserName    string   `json:"user_name"`
		AndelAmount *float64 `json:"andel_amount"`
		AssignedAt  string   `json:"assigned_at"`
		ReleasedAt  *string  `json:"released_at"`
	}

	aRows, err := h.db.Query(ctx,
		`SELECT sa.id, sa.user_id, u.full_name, sa.andel_amount, sa.assigned_at, sa.released_at
		 FROM slip_assignments sa
		 JOIN users u ON u.id = sa.user_id
		 WHERE sa.slip_id = $1
		 ORDER BY sa.assigned_at DESC`,
		slipID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query assignment history")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer aRows.Close()

	var assignments []assignmentRow
	for aRows.Next() {
		var a assignmentRow
		if err := aRows.Scan(&a.ID, &a.UserID, &a.UserName, &a.AndelAmount, &a.AssignedAt, &a.ReleasedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan assignment row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		assignments = append(assignments, a)
	}
	if assignments == nil {
		assignments = []assignmentRow{}
	}

	JSON(w, http.StatusOK, map[string]any{
		"slip":        s,
		"assignments": assignments,
	})
}

func (h *AdminSlipsHandler) HandleCreateSlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, _, err := h.clubID(r)
	if err != nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		Number  string   `json:"number"`
		Section string   `json:"section"`
		LengthM *float64 `json:"length_m"`
		WidthM  *float64 `json:"width_m"`
		DepthM  *float64 `json:"depth_m"`
		MapX    *float64 `json:"map_x"`
		MapY    *float64 `json:"map_y"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Number == "" {
		Error(w, http.StatusBadRequest, "number is required")
		return
	}

	var slipID string
	err = h.db.QueryRow(ctx,
		`INSERT INTO slips (club_id, number, section, length_m, width_m, depth_m, map_x, map_y)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		clubID, req.Number, req.Section, req.LengthM, req.WidthM, req.DepthM, req.MapX, req.MapY,
	).Scan(&slipID)
	if err != nil {
		h.log.Error().Err(err).Str("number", req.Number).Msg("failed to create slip")
		Error(w, http.StatusConflict, "slip number already exists or creation failed")
		return
	}

	h.log.Info().Str("slip_id", slipID).Str("number", req.Number).Msg("slip created")

	JSON(w, http.StatusCreated, map[string]string{"id": slipID})
}

func (h *AdminSlipsHandler) HandleUpdateSlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, _, err := h.clubID(r)
	if err != nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	slipID := chi.URLParam(r, "slipID")
	if slipID == "" {
		Error(w, http.StatusBadRequest, "slip ID is required")
		return
	}

	var req struct {
		Number  *string  `json:"number"`
		Section *string  `json:"section"`
		LengthM *float64 `json:"length_m"`
		WidthM  *float64 `json:"width_m"`
		DepthM  *float64 `json:"depth_m"`
		Status  *string  `json:"status"`
		MapX    *float64 `json:"map_x"`
		MapY    *float64 `json:"map_y"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Status != nil {
		validStatuses := map[string]bool{
			"vacant": true, "occupied": true, "reserved": true, "maintenance": true,
		}
		if !validStatuses[*req.Status] {
			Error(w, http.StatusBadRequest, "invalid status")
			return
		}
	}

	tag, err := h.db.Exec(ctx,
		`UPDATE slips SET
		    number   = COALESCE($3, number),
		    section  = COALESCE($4, section),
		    length_m = COALESCE($5, length_m),
		    width_m  = COALESCE($6, width_m),
		    depth_m  = COALESCE($7, depth_m),
		    status   = COALESCE($8, status),
		    map_x    = COALESCE($9, map_x),
		    map_y    = COALESCE($10, map_y),
		    updated_at = now()
		 WHERE id = $1 AND club_id = $2`,
		slipID, clubID,
		req.Number, req.Section, req.LengthM, req.WidthM, req.DepthM,
		req.Status, req.MapX, req.MapY,
	)
	if err != nil {
		h.log.Error().Err(err).Str("slip_id", slipID).Msg("failed to update slip")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "slip not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *AdminSlipsHandler) HandleAssignSlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, actorID, err := h.clubID(r)
	if err != nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	slipID := chi.URLParam(r, "slipID")
	if slipID == "" {
		Error(w, http.StatusBadRequest, "slip ID is required")
		return
	}

	var req struct {
		UserID      string   `json:"user_id"`
		AndelAmount *float64 `json:"andel_amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID == "" {
		Error(w, http.StatusBadRequest, "user_id is required")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM slips WHERE id = $1 AND club_id = $2 FOR UPDATE`,
		slipID, clubID,
	).Scan(&currentStatus)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "slip not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to lock slip")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if currentStatus == "occupied" {
		Error(w, http.StatusConflict, "slip is already occupied")
		return
	}

	var assignmentID string
	err = tx.QueryRow(ctx,
		`INSERT INTO slip_assignments (slip_id, user_id, club_id, andel_amount)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		slipID, req.UserID, clubID, req.AndelAmount,
	).Scan(&assignmentID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create slip assignment")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, err = tx.Exec(ctx,
		`UPDATE slips SET status = 'occupied', updated_at = now() WHERE id = $1`,
		slipID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update slip status")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	newData, _ := json.Marshal(map[string]any{
		"user_id":       req.UserID,
		"andel_amount":  req.AndelAmount,
		"assignment_id": assignmentID,
	})
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, user_id, action, entity_type, entity_id, new_data)
		 VALUES ($1, $2, 'assign_slip', 'slip', $3, $4)`,
		clubID, actorID, slipID, newData,
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
		Str("slip_id", slipID).
		Str("user_id", req.UserID).
		Str("assignment_id", assignmentID).
		Msg("slip assigned")

	JSON(w, http.StatusOK, map[string]string{
		"assignment_id": assignmentID,
		"status":        "assigned",
	})
}

func (h *AdminSlipsHandler) HandleReleaseSlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, actorID, err := h.clubID(r)
	if err != nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	slipID := chi.URLParam(r, "slipID")
	if slipID == "" {
		Error(w, http.StatusBadRequest, "slip ID is required")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var assignmentID, assignedUserID string
	err = tx.QueryRow(ctx,
		`UPDATE slip_assignments SET released_at = now()
		 WHERE slip_id = $1 AND club_id = $2 AND released_at IS NULL
		 RETURNING id, user_id`,
		slipID, clubID,
	).Scan(&assignmentID, &assignedUserID)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "no active assignment for this slip")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to release assignment")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, err = tx.Exec(ctx,
		`UPDATE slips SET status = 'vacant', updated_at = now()
		 WHERE id = $1 AND club_id = $2`,
		slipID, clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update slip status")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	oldData, _ := json.Marshal(map[string]any{
		"user_id":       assignedUserID,
		"assignment_id": assignmentID,
	})
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, user_id, action, entity_type, entity_id, old_data)
		 VALUES ($1, $2, 'release_slip', 'slip', $3, $4)`,
		clubID, actorID, slipID, oldData,
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
		Str("slip_id", slipID).
		Str("released_user", assignedUserID).
		Msg("slip released")

	JSON(w, http.StatusOK, map[string]string{"status": "released"})
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
