package handlers

import (
	"context"
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

type SlipSharesHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewSlipSharesHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *SlipSharesHandler {
	return &SlipSharesHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "slip_shares").Logger(),
	}
}

type slipShare struct {
	ID               string    `json:"id"`
	SlipAssignmentID string    `json:"slip_assignment_id"`
	ClubID           string    `json:"club_id"`
	AvailableFrom    string    `json:"available_from"`
	AvailableTo      string    `json:"available_to"`
	Notes            string    `json:"notes"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type slipShareAdmin struct {
	slipShare
	SlipNumber string `json:"slip_number"`
	Section    string `json:"section"`
	MemberName string `json:"member_name"`
}

type slipShareRebate struct {
	ID           string    `json:"id"`
	SlipShareID  string    `json:"slip_share_id"`
	BookingID    string    `json:"booking_id"`
	NightsRented int       `json:"nights_rented"`
	RebatePct    float64   `json:"rebate_pct"`
	RentalIncome float64   `json:"rental_income"`
	RebateAmount float64   `json:"rebate_amount"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

type createSlipShareRequest struct {
	AvailableFrom string `json:"available_from"`
	AvailableTo   string `json:"available_to"`
	Notes         string `json:"notes"`
}

type updateSlipShareRequest struct {
	AvailableTo string `json:"available_to"`
	Notes       string `json:"notes"`
}

// HandleCreateSlipShare registers a member's unavailability window for slip sharing.
func (h *SlipSharesHandler) HandleCreateSlipShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createSlipShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.AvailableFrom == "" || req.AvailableTo == "" {
		Error(w, http.StatusBadRequest, "available_from and available_to are required")
		return
	}

	fromDate, err := time.Parse("2006-01-02", req.AvailableFrom)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid available_from format, use YYYY-MM-DD")
		return
	}
	toDate, err := time.Parse("2006-01-02", req.AvailableTo)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid available_to format, use YYYY-MM-DD")
		return
	}
	if !toDate.After(fromDate) {
		Error(w, http.StatusBadRequest, "available_to must be after available_from")
		return
	}

	var assignmentID string
	err = h.db.QueryRow(ctx,
		`SELECT sa.id FROM slip_assignments sa
		 WHERE sa.user_id = $1 AND sa.club_id = $2 AND sa.released_at IS NULL`,
		claims.UserID, claims.ClubID,
	).Scan(&assignmentID)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusForbidden, "you must have an active slip assignment to share")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to find slip assignment")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var overlapCount int
	err = h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM slip_shares
		 WHERE slip_assignment_id = $1 AND status = 'active'
		 AND available_from < $3 AND available_to > $2`,
		assignmentID, fromDate, toDate,
	).Scan(&overlapCount)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check overlap")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if overlapCount > 0 {
		Error(w, http.StatusConflict, "this date range overlaps with an existing availability window")
		return
	}

	var ss slipShare
	err = h.db.QueryRow(ctx,
		`INSERT INTO slip_shares (slip_assignment_id, club_id, available_from, available_to, notes)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, slip_assignment_id, club_id, available_from, available_to, notes, status, created_at, updated_at`,
		assignmentID, claims.ClubID, fromDate, toDate, req.Notes,
	).Scan(
		&ss.ID, &ss.SlipAssignmentID, &ss.ClubID,
		&ss.AvailableFrom, &ss.AvailableTo,
		&ss.Notes, &ss.Status, &ss.CreatedAt, &ss.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create slip share")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.ensureSharedSlipUnit(ctx, assignmentID, claims.ClubID)

	JSON(w, http.StatusCreated, ss)
}

// ensureSharedSlipUnit creates a resource_unit for this slip if one doesn't exist yet.
func (h *SlipSharesHandler) ensureSharedSlipUnit(ctx context.Context, assignmentID, clubID string) {
	_, err := h.db.Exec(ctx,
		`INSERT INTO resource_units (resource_id, slip_id, label, metadata)
		 SELECT r.id, sa.slip_id, 'Shared ' || s.number, jsonb_build_object(
		     'length_m', s.length_m, 'width_m', s.width_m, 'depth_m', s.depth_m, 'shared', true
		 )
		 FROM slip_assignments sa
		 JOIN slips s ON s.id = sa.slip_id
		 CROSS JOIN (SELECT id FROM resources WHERE club_id = $2 AND type = 'shared_slip' LIMIT 1) r
		 WHERE sa.id = $1
		 AND NOT EXISTS (
		     SELECT 1 FROM resource_units ru WHERE ru.slip_id = sa.slip_id
		     AND ru.resource_id = r.id
		 )`,
		assignmentID, clubID,
	)
	if err != nil {
		h.log.Warn().Err(err).Str("assignment_id", assignmentID).Msg("failed to ensure shared slip unit")
	}
}

func (h *SlipSharesHandler) HandleListMySlipShares(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT ss.id, ss.slip_assignment_id, ss.club_id, ss.available_from, ss.available_to,
		        ss.notes, ss.status, ss.created_at, ss.updated_at
		 FROM slip_shares ss
		 JOIN slip_assignments sa ON sa.id = ss.slip_assignment_id
		 WHERE sa.user_id = $1 AND ss.club_id = $2
		 ORDER BY ss.available_from DESC`,
		claims.UserID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list slip shares")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	shares := make([]slipShare, 0)
	for rows.Next() {
		var ss slipShare
		if err := rows.Scan(
			&ss.ID, &ss.SlipAssignmentID, &ss.ClubID,
			&ss.AvailableFrom, &ss.AvailableTo,
			&ss.Notes, &ss.Status, &ss.CreatedAt, &ss.UpdatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan slip share")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		shares = append(shares, ss)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating slip shares")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, shares)
}

func (h *SlipSharesHandler) HandleUpdateSlipShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	shareID := chi.URLParam(r, "shareID")
	if shareID == "" {
		Error(w, http.StatusBadRequest, "missing share ID")
		return
	}

	var req updateSlipShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var ownerUserID, currentStatus string
	var currentFrom time.Time
	err := h.db.QueryRow(ctx,
		`SELECT sa.user_id, ss.status, ss.available_from
		 FROM slip_shares ss
		 JOIN slip_assignments sa ON sa.id = ss.slip_assignment_id
		 WHERE ss.id = $1 AND ss.club_id = $2`,
		shareID, claims.ClubID,
	).Scan(&ownerUserID, &currentStatus, &currentFrom)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "slip share not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch slip share")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if ownerUserID != claims.UserID {
		Error(w, http.StatusForbidden, "you can only edit your own slip shares")
		return
	}
	if currentStatus != "active" {
		Error(w, http.StatusConflict, "only active slip shares can be edited")
		return
	}

	newTo, err := time.Parse("2006-01-02", req.AvailableTo)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid available_to format")
		return
	}
	if !newTo.After(currentFrom) {
		Error(w, http.StatusBadRequest, "available_to must be after the start date")
		return
	}

	var bookingConflict int
	err = h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM bookings b
		 JOIN resource_units ru ON ru.id = b.resource_unit_id
		 JOIN slip_assignments sa ON sa.slip_id = ru.slip_id
		 JOIN slip_shares ss ON ss.slip_assignment_id = sa.id
		 WHERE ss.id = $1 AND b.status IN ('pending', 'confirmed')
		 AND b.start_date >= $2`,
		shareID, newTo,
	).Scan(&bookingConflict)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check booking conflicts")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if bookingConflict > 0 {
		Error(w, http.StatusConflict, "cannot shorten window — confirmed bookings exist in the removed range")
		return
	}

	var ss slipShare
	err = h.db.QueryRow(ctx,
		`UPDATE slip_shares SET available_to = $2, notes = $3, updated_at = now()
		 WHERE id = $1
		 RETURNING id, slip_assignment_id, club_id, available_from, available_to, notes, status, created_at, updated_at`,
		shareID, newTo, req.Notes,
	).Scan(
		&ss.ID, &ss.SlipAssignmentID, &ss.ClubID,
		&ss.AvailableFrom, &ss.AvailableTo,
		&ss.Notes, &ss.Status, &ss.CreatedAt, &ss.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update slip share")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, ss)
}

func (h *SlipSharesHandler) HandleDeleteSlipShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	shareID := chi.URLParam(r, "shareID")
	if shareID == "" {
		Error(w, http.StatusBadRequest, "missing share ID")
		return
	}

	var ownerUserID, currentStatus string
	err := h.db.QueryRow(ctx,
		`SELECT sa.user_id, ss.status
		 FROM slip_shares ss
		 JOIN slip_assignments sa ON sa.id = ss.slip_assignment_id
		 WHERE ss.id = $1 AND ss.club_id = $2`,
		shareID, claims.ClubID,
	).Scan(&ownerUserID, &currentStatus)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "slip share not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch slip share for delete")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if ownerUserID != claims.UserID {
		Error(w, http.StatusForbidden, "you can only cancel your own slip shares")
		return
	}
	if currentStatus != "active" {
		Error(w, http.StatusConflict, "only active slip shares can be cancelled")
		return
	}

	var confirmedBookings int
	err = h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM bookings b
		 JOIN resource_units ru ON ru.id = b.resource_unit_id
		 JOIN slip_assignments sa ON sa.slip_id = ru.slip_id
		 JOIN slip_shares ss ON ss.slip_assignment_id = sa.id
		 WHERE ss.id = $1 AND b.status IN ('pending', 'confirmed')
		 AND b.start_date < ss.available_to AND b.end_date > ss.available_from`,
		shareID,
	).Scan(&confirmedBookings)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check confirmed bookings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if confirmedBookings > 0 {
		Error(w, http.StatusConflict, "cannot cancel — confirmed bookings exist in this window; contact styre")
		return
	}

	_, err = h.db.Exec(ctx,
		`UPDATE slip_shares SET status = 'cancelled', updated_at = now() WHERE id = $1`,
		shareID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to cancel slip share")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (h *SlipSharesHandler) HandleListMyRebates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT ssr.id, ssr.slip_share_id, ssr.booking_id, ssr.nights_rented,
		        ssr.rebate_pct, ssr.rental_income, ssr.rebate_amount, ssr.status, ssr.created_at
		 FROM slip_share_rebates ssr
		 JOIN slip_shares ss ON ss.id = ssr.slip_share_id
		 JOIN slip_assignments sa ON sa.id = ss.slip_assignment_id
		 WHERE sa.user_id = $1 AND ss.club_id = $2
		 ORDER BY ssr.created_at DESC`,
		claims.UserID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list rebates")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	rebates := make([]slipShareRebate, 0)
	for rows.Next() {
		var rb slipShareRebate
		if err := rows.Scan(
			&rb.ID, &rb.SlipShareID, &rb.BookingID, &rb.NightsRented,
			&rb.RebatePct, &rb.RentalIncome, &rb.RebateAmount, &rb.Status, &rb.CreatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan rebate")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		rebates = append(rebates, rb)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating rebates")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, rebates)
}

// Admin endpoints

func (h *SlipSharesHandler) HandleListAllSlipShares(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	statusFilter := r.URL.Query().Get("status")

	query := `SELECT ss.id, ss.slip_assignment_id, ss.club_id, ss.available_from, ss.available_to,
	                 ss.notes, ss.status, ss.created_at, ss.updated_at,
	                 s.number, s.section, u.full_name
	          FROM slip_shares ss
	          JOIN slip_assignments sa ON sa.id = ss.slip_assignment_id
	          JOIN slips s ON s.id = sa.slip_id
	          JOIN users u ON u.id = sa.user_id
	          WHERE ss.club_id = $1`
	args := []any{claims.ClubID}
	argIdx := 2

	if statusFilter != "" {
		query += fmt.Sprintf(` AND ss.status = $%d`, argIdx)
		args = append(args, statusFilter)
	}
	query += ` ORDER BY ss.available_from DESC`

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list all slip shares")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	shares := make([]slipShareAdmin, 0)
	for rows.Next() {
		var sa slipShareAdmin
		if err := rows.Scan(
			&sa.ID, &sa.SlipAssignmentID, &sa.ClubID,
			&sa.AvailableFrom, &sa.AvailableTo,
			&sa.Notes, &sa.Status, &sa.CreatedAt, &sa.UpdatedAt,
			&sa.SlipNumber, &sa.Section, &sa.MemberName,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan slip share admin")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		shares = append(shares, sa)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating slip shares admin")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, shares)
}

func (h *SlipSharesHandler) HandleListAllRebates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT ssr.id, ssr.slip_share_id, ssr.booking_id, ssr.nights_rented,
		        ssr.rebate_pct, ssr.rental_income, ssr.rebate_amount, ssr.status, ssr.created_at
		 FROM slip_share_rebates ssr
		 JOIN slip_shares ss ON ss.id = ssr.slip_share_id
		 WHERE ss.club_id = $1
		 ORDER BY ssr.created_at DESC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list all rebates")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	rebates := make([]slipShareRebate, 0)
	for rows.Next() {
		var rb slipShareRebate
		if err := rows.Scan(
			&rb.ID, &rb.SlipShareID, &rb.BookingID, &rb.NightsRented,
			&rb.RebatePct, &rb.RentalIncome, &rb.RebateAmount, &rb.Status, &rb.CreatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan rebate admin")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		rebates = append(rebates, rb)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating rebates admin")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, rebates)
}

func (h *SlipSharesHandler) HandleUpdateRebateStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rebateID := chi.URLParam(r, "rebateID")
	if rebateID == "" {
		Error(w, http.StatusBadRequest, "missing rebate ID")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	validStatuses := map[string]bool{"pending": true, "credited": true, "paid_out": true}
	if !validStatuses[req.Status] {
		Error(w, http.StatusBadRequest, "status must be one of: pending, credited, paid_out")
		return
	}

	tag, err := h.db.Exec(ctx,
		`UPDATE slip_share_rebates SET status = $2
		 WHERE id = $1
		 AND slip_share_id IN (SELECT id FROM slip_shares WHERE club_id = $3)`,
		rebateID, req.Status, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update rebate status")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "rebate not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": req.Status})
}
