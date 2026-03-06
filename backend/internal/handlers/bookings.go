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

const (
	bookingLockPrefix = "booking_lock:"
	bookingLockTTL    = 30 * time.Second
)

type BookingsHandler struct {
	db     *pgxpool.Pool
	redis  *redis.Client
	config *config.Config
	log    zerolog.Logger
}

func NewBookingsHandler(
	db *pgxpool.Pool,
	rdb *redis.Client,
	cfg *config.Config,
	log zerolog.Logger,
) *BookingsHandler {
	return &BookingsHandler{
		db:     db,
		redis:  rdb,
		config: cfg,
		log:    log.With().Str("handler", "bookings").Logger(),
	}
}

type resource struct {
	ID           string   `json:"id"`
	ClubID       string   `json:"club_id"`
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Capacity     int      `json:"capacity"`
	PricePerUnit float64  `json:"price_per_unit"`
	Unit         string   `json:"unit"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type booking struct {
	ID         string     `json:"id"`
	ResourceID string     `json:"resource_id"`
	UserID     *string    `json:"user_id,omitempty"`
	ClubID     string     `json:"club_id"`
	StartDate  time.Time  `json:"start_date"`
	EndDate    time.Time  `json:"end_date"`
	Status     string     `json:"status"`
	GuestName  *string    `json:"guest_name,omitempty"`
	GuestEmail *string    `json:"guest_email,omitempty"`
	GuestPhone *string    `json:"guest_phone,omitempty"`
	PaymentID  *string    `json:"payment_id,omitempty"`
	Notes      string     `json:"notes"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type bookingAdmin struct {
	booking
	ResourceName string  `json:"resource_name"`
	ResourceType string  `json:"resource_type"`
	UserName     *string `json:"user_name,omitempty"`
	UserEmail    *string `json:"user_email,omitempty"`
}

type availabilitySlot struct {
	Date      string `json:"date"`
	Available bool   `json:"available"`
}

type createBookingRequest struct {
	ResourceID string  `json:"resource_id"`
	StartDate  string  `json:"start_date"`
	EndDate    string  `json:"end_date"`
	GuestName  *string `json:"guest_name,omitempty"`
	GuestEmail *string `json:"guest_email,omitempty"`
	GuestPhone *string `json:"guest_phone,omitempty"`
	Notes      string  `json:"notes"`
}

func (h *BookingsHandler) HandleListResources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	typeFilter := r.URL.Query().Get("type")

	query := `SELECT id, club_id, type, name, description, capacity, price_per_unit, unit, created_at, updated_at
	          FROM resources WHERE club_id = (SELECT id FROM clubs WHERE slug = $1)`
	args := []any{h.config.ClubSlug}

	if typeFilter != "" {
		query += ` AND type = $2`
		args = append(args, typeFilter)
	}
	query += ` ORDER BY name`

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list resources")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	resources := make([]resource, 0)
	for rows.Next() {
		var res resource
		if err := rows.Scan(
			&res.ID, &res.ClubID, &res.Type, &res.Name, &res.Description,
			&res.Capacity, &res.PricePerUnit, &res.Unit,
			&res.CreatedAt, &res.UpdatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan resource")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		resources = append(resources, res)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating resource rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, resources)
}

func (h *BookingsHandler) HandleGetResourceAvailability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resourceID := chi.URLParam(r, "resourceID")
	if resourceID == "" {
		Error(w, http.StatusBadRequest, "missing resource ID")
		return
	}

	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	if startStr == "" || endStr == "" {
		Error(w, http.StatusBadRequest, "start and end query parameters are required")
		return
	}

	startDate, err := time.Parse("2006-01-02", startStr)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid start date format, use YYYY-MM-DD")
		return
	}

	endDate, err := time.Parse("2006-01-02", endStr)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid end date format, use YYYY-MM-DD")
		return
	}

	if endDate.Before(startDate) {
		Error(w, http.StatusBadRequest, "end date must be after start date")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT start_date, end_date FROM bookings
		 WHERE resource_id = $1 AND status IN ('pending', 'confirmed')
		 AND start_date < $3 AND end_date > $2`,
		resourceID, startDate, endDate.AddDate(0, 0, 1),
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query bookings for availability")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	bookedDays := make(map[string]bool)
	for rows.Next() {
		var bStart, bEnd time.Time
		if err := rows.Scan(&bStart, &bEnd); err != nil {
			h.log.Error().Err(err).Msg("failed to scan booking dates")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		for d := bStart; d.Before(bEnd); d = d.AddDate(0, 0, 1) {
			bookedDays[d.Format("2006-01-02")] = true
		}
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating booking rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	slots := make([]availabilitySlot, 0)
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		slots = append(slots, availabilitySlot{
			Date:      dateStr,
			Available: !bookedDays[dateStr],
		})
	}

	JSON(w, http.StatusOK, slots)
}

func (h *BookingsHandler) HandleCreateBooking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ResourceID == "" || req.StartDate == "" || req.EndDate == "" {
		Error(w, http.StatusBadRequest, "resource_id, start_date, and end_date are required")
		return
	}

	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid start_date format, use YYYY-MM-DD")
		return
	}

	endDate, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid end_date format, use YYYY-MM-DD")
		return
	}

	if endDate.Before(startDate) {
		Error(w, http.StatusBadRequest, "end_date must be after start_date")
		return
	}

	var clubID string
	err = h.db.QueryRow(ctx,
		`SELECT r.club_id FROM resources r
		 JOIN clubs c ON c.id = r.club_id
		 WHERE r.id = $1 AND c.slug = $2`,
		req.ResourceID, h.config.ClubSlug,
	).Scan(&clubID)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "resource not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to verify resource")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	lockKey := fmt.Sprintf("%s%s:%s:%s", bookingLockPrefix, req.ResourceID, req.StartDate, req.EndDate)
	acquired, err := h.redis.SetNX(ctx, lockKey, "1", bookingLockTTL).Result()
	if err != nil {
		h.log.Error().Err(err).Msg("failed to acquire booking lock")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !acquired {
		Error(w, http.StatusConflict, "another booking is being processed for this resource and date range")
		return
	}
	defer h.redis.Del(ctx, lockKey)

	var conflictCount int
	err = h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM bookings
		 WHERE resource_id = $1 AND status IN ('pending', 'confirmed')
		 AND start_date < $3 AND end_date > $2`,
		req.ResourceID, startDate, endDate,
	).Scan(&conflictCount)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check booking conflicts")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if conflictCount > 0 {
		Error(w, http.StatusConflict, "resource is already booked for the requested dates")
		return
	}

	var userID *string
	claims := middleware.GetClaims(ctx)
	if claims != nil {
		userID = &claims.UserID
	}

	var b booking
	err = h.db.QueryRow(ctx,
		`INSERT INTO bookings (resource_id, user_id, club_id, start_date, end_date, status, guest_name, guest_email, guest_phone, notes)
		 VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $8, $9)
		 RETURNING id, resource_id, user_id, club_id, start_date, end_date, status, guest_name, guest_email, guest_phone, payment_id, notes, created_at, updated_at`,
		req.ResourceID, userID, clubID, startDate, endDate,
		req.GuestName, req.GuestEmail, req.GuestPhone, req.Notes,
	).Scan(
		&b.ID, &b.ResourceID, &b.UserID, &b.ClubID,
		&b.StartDate, &b.EndDate, &b.Status,
		&b.GuestName, &b.GuestEmail, &b.GuestPhone, &b.PaymentID,
		&b.Notes, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create booking")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, b)
}

func (h *BookingsHandler) HandleGetBooking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	bookingID := chi.URLParam(r, "bookingID")
	if bookingID == "" {
		Error(w, http.StatusBadRequest, "missing booking ID")
		return
	}

	var b booking
	err := h.db.QueryRow(ctx,
		`SELECT b.id, b.resource_id, b.user_id, b.club_id, b.start_date, b.end_date, b.status,
		        b.guest_name, b.guest_email, b.guest_phone, b.payment_id, b.notes, b.created_at, b.updated_at
		 FROM bookings b
		 JOIN clubs c ON c.id = b.club_id
		 WHERE b.id = $1 AND c.slug = $2`,
		bookingID, h.config.ClubSlug,
	).Scan(
		&b.ID, &b.ResourceID, &b.UserID, &b.ClubID,
		&b.StartDate, &b.EndDate, &b.Status,
		&b.GuestName, &b.GuestEmail, &b.GuestPhone, &b.PaymentID,
		&b.Notes, &b.CreatedAt, &b.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch booking")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, b)
}

func (h *BookingsHandler) HandleListMyBookings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	statusFilter := r.URL.Query().Get("status")

	query := `SELECT id, resource_id, user_id, club_id, start_date, end_date, status,
	                 guest_name, guest_email, guest_phone, payment_id, notes, created_at, updated_at
	          FROM bookings
	          WHERE user_id = $1 AND club_id = $2`
	args := []any{claims.UserID, claims.ClubID}

	if statusFilter != "" {
		query += ` AND status = $3`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY start_date DESC`

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list user bookings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	bookings := make([]booking, 0)
	for rows.Next() {
		var b booking
		if err := rows.Scan(
			&b.ID, &b.ResourceID, &b.UserID, &b.ClubID,
			&b.StartDate, &b.EndDate, &b.Status,
			&b.GuestName, &b.GuestEmail, &b.GuestPhone, &b.PaymentID,
			&b.Notes, &b.CreatedAt, &b.UpdatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan booking")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		bookings = append(bookings, b)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating booking rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, bookings)
}

func (h *BookingsHandler) HandleCancelBooking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	bookingID := chi.URLParam(r, "bookingID")
	if bookingID == "" {
		Error(w, http.StatusBadRequest, "missing booking ID")
		return
	}

	var b booking
	err := h.db.QueryRow(ctx,
		`SELECT id, resource_id, user_id, club_id, start_date, end_date, status,
		        guest_name, guest_email, guest_phone, payment_id, notes, created_at, updated_at
		 FROM bookings WHERE id = $1 AND club_id = $2`,
		bookingID, claims.ClubID,
	).Scan(
		&b.ID, &b.ResourceID, &b.UserID, &b.ClubID,
		&b.StartDate, &b.EndDate, &b.Status,
		&b.GuestName, &b.GuestEmail, &b.GuestPhone, &b.PaymentID,
		&b.Notes, &b.CreatedAt, &b.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch booking for cancel")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	isOwner := b.UserID != nil && *b.UserID == claims.UserID
	isStyre := hasRole(claims.Roles, "styre") || hasRole(claims.Roles, "harbour_master")
	if !isOwner && !isStyre {
		Error(w, http.StatusForbidden, "only the booking owner or styre can cancel")
		return
	}

	if b.Status == "cancelled" {
		Error(w, http.StatusConflict, "booking is already cancelled")
		return
	}

	oldStatus := b.Status
	err = h.db.QueryRow(ctx,
		`UPDATE bookings SET status = 'cancelled', updated_at = now()
		 WHERE id = $1
		 RETURNING id, resource_id, user_id, club_id, start_date, end_date, status,
		           guest_name, guest_email, guest_phone, payment_id, notes, created_at, updated_at`,
		bookingID,
	).Scan(
		&b.ID, &b.ResourceID, &b.UserID, &b.ClubID,
		&b.StartDate, &b.EndDate, &b.Status,
		&b.GuestName, &b.GuestEmail, &b.GuestPhone, &b.PaymentID,
		&b.Notes, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to cancel booking")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "cancel_booking", "booking", bookingID,
		map[string]string{"status": oldStatus},
		map[string]string{"status": "cancelled"},
	); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, b)
}

func (h *BookingsHandler) HandleConfirmBooking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	bookingID := chi.URLParam(r, "bookingID")
	if bookingID == "" {
		Error(w, http.StatusBadRequest, "missing booking ID")
		return
	}

	var currentStatus string
	err := h.db.QueryRow(ctx,
		`SELECT status FROM bookings WHERE id = $1 AND club_id = $2`,
		bookingID, claims.ClubID,
	).Scan(&currentStatus)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch booking for confirm")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if currentStatus != "pending" {
		Error(w, http.StatusConflict, fmt.Sprintf("booking status is '%s', expected 'pending'", currentStatus))
		return
	}

	var b booking
	err = h.db.QueryRow(ctx,
		`UPDATE bookings SET status = 'confirmed', updated_at = now()
		 WHERE id = $1
		 RETURNING id, resource_id, user_id, club_id, start_date, end_date, status,
		           guest_name, guest_email, guest_phone, payment_id, notes, created_at, updated_at`,
		bookingID,
	).Scan(
		&b.ID, &b.ResourceID, &b.UserID, &b.ClubID,
		&b.StartDate, &b.EndDate, &b.Status,
		&b.GuestName, &b.GuestEmail, &b.GuestPhone, &b.PaymentID,
		&b.Notes, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to confirm booking")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "confirm_booking", "booking", bookingID,
		map[string]string{"status": "pending"},
		map[string]string{"status": "confirmed"},
	); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, b)
}

func (h *BookingsHandler) HandleListBookingsAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	statusFilter := r.URL.Query().Get("status")
	resourceTypeFilter := r.URL.Query().Get("resource_type")
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	query := `SELECT b.id, b.resource_id, b.user_id, b.club_id, b.start_date, b.end_date, b.status,
	                 b.guest_name, b.guest_email, b.guest_phone, b.payment_id, b.notes, b.created_at, b.updated_at,
	                 r.name, r.type, u.full_name, u.email
	          FROM bookings b
	          JOIN resources r ON r.id = b.resource_id
	          LEFT JOIN users u ON u.id = b.user_id
	          WHERE b.club_id = $1`
	args := []any{claims.ClubID}
	argIdx := 2

	if statusFilter != "" {
		query += fmt.Sprintf(` AND b.status = $%d`, argIdx)
		args = append(args, statusFilter)
		argIdx++
	}
	if resourceTypeFilter != "" {
		query += fmt.Sprintf(` AND r.type = $%d`, argIdx)
		args = append(args, resourceTypeFilter)
		argIdx++
	}
	if startStr != "" {
		startDate, err := time.Parse("2006-01-02", startStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid start date format, use YYYY-MM-DD")
			return
		}
		query += fmt.Sprintf(` AND b.end_date > $%d`, argIdx)
		args = append(args, startDate)
		argIdx++
	}
	if endStr != "" {
		endDate, err := time.Parse("2006-01-02", endStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid end date format, use YYYY-MM-DD")
			return
		}
		query += fmt.Sprintf(` AND b.start_date < $%d`, argIdx)
		args = append(args, endDate.AddDate(0, 0, 1))
		argIdx++
	}
	query += ` ORDER BY b.start_date DESC`

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list admin bookings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	bookings := make([]bookingAdmin, 0)
	for rows.Next() {
		var ba bookingAdmin
		if err := rows.Scan(
			&ba.ID, &ba.ResourceID, &ba.UserID, &ba.ClubID,
			&ba.StartDate, &ba.EndDate, &ba.Status,
			&ba.GuestName, &ba.GuestEmail, &ba.GuestPhone, &ba.PaymentID,
			&ba.Notes, &ba.CreatedAt, &ba.UpdatedAt,
			&ba.ResourceName, &ba.ResourceType, &ba.UserName, &ba.UserEmail,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan admin booking")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		bookings = append(bookings, ba)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating admin booking rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, bookings)
}

func hasRole(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}
