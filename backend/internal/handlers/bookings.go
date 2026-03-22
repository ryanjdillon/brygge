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
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/shared"
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
	ID           string    `json:"id"`
	ClubID       string    `json:"club_id"`
	Type         string    `json:"type"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Capacity     int       `json:"capacity"`
	PricePerUnit float64   `json:"price_per_unit"`
	Unit         string    `json:"unit"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type booking struct {
	ID             string    `json:"id"`
	ResourceID     string    `json:"resource_id"`
	ResourceUnitID *string   `json:"resource_unit_id,omitempty"`
	UserID         *string   `json:"user_id,omitempty"`
	ClubID         string    `json:"club_id"`
	StartDate      time.Time `json:"start_date"`
	EndDate        time.Time `json:"end_date"`
	Status         string    `json:"status"`
	GuestName      *string   `json:"guest_name,omitempty"`
	GuestEmail     *string   `json:"guest_email,omitempty"`
	GuestPhone     *string   `json:"guest_phone,omitempty"`
	PaymentID      *string   `json:"payment_id,omitempty"`
	BoatLengthM    *float64  `json:"boat_length_m,omitempty"`
	BoatBeamM      *float64  `json:"boat_beam_m,omitempty"`
	BoatDraftM     *float64  `json:"boat_draft_m,omitempty"`
	Notes          string    `json:"notes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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

type aggregateAvailability struct {
	Date           string `json:"date"`
	TotalUnits     int    `json:"total_units"`
	AvailableUnits int    `json:"available_units"`
}

type todayAvailability struct {
	Available int `json:"available"`
	Total     int `json:"total"`
}

type hoistSlot struct {
	Start     string  `json:"start"`
	End       string  `json:"end"`
	Available bool    `json:"available"`
	BookedBy  *string `json:"booked_by,omitempty"`
}

type hoistSlotsResponse struct {
	Date                string      `json:"date"`
	SlotDurationMinutes int         `json:"slot_duration_minutes"`
	Slots               []hoistSlot `json:"slots"`
}

type createBookingRequest struct {
	ResourceID   string   `json:"resource_id"`
	ResourceType string   `json:"resource_type"`
	StartDate    string   `json:"start_date"`
	EndDate      string   `json:"end_date"`
	BoatLengthM  *float64 `json:"boat_length_m,omitempty"`
	BoatBeamM    *float64 `json:"boat_beam_m,omitempty"`
	BoatDraftM   *float64 `json:"boat_draft_m,omitempty"`
	Season       string   `json:"season,omitempty"`
	GuestName    *string  `json:"guest_name,omitempty"`
	GuestEmail   *string  `json:"guest_email,omitempty"`
	GuestPhone   *string  `json:"guest_phone,omitempty"`
	Notes        string   `json:"notes"`
}

// bookingColumns is the shared list of columns for scanning a booking row.
const bookingColumns = `id, resource_id, resource_unit_id, user_id, club_id, start_date, end_date, status,
	guest_name, guest_email, guest_phone, payment_id, boat_length_m, boat_beam_m, boat_draft_m, notes, created_at, updated_at`

func scanBooking(row interface{ Scan(dest ...any) error }, b *booking) error {
	return row.Scan(
		&b.ID, &b.ResourceID, &b.ResourceUnitID, &b.UserID, &b.ClubID,
		&b.StartDate, &b.EndDate, &b.Status,
		&b.GuestName, &b.GuestEmail, &b.GuestPhone, &b.PaymentID,
		&b.BoatLengthM, &b.BoatBeamM, &b.BoatDraftM,
		&b.Notes, &b.CreatedAt, &b.UpdatedAt,
	)
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

type boatDimensions struct {
	length, beam, draft float64
}

func (h *BookingsHandler) countTotalUnits(ctx context.Context, clubSlug, resourceType string, dims *boatDimensions) (int, error) {
	var totalUnits int
	var err error
	if dims != nil {
		err = h.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM resource_units ru
			 JOIN resources r ON r.id = ru.resource_id
			 LEFT JOIN slips s ON s.id = ru.slip_id
			 WHERE r.club_id = (SELECT id FROM clubs WHERE slug = $1)
			 AND r.type = $2 AND ru.is_active = true
			 AND (s.id IS NULL OR (s.length_m >= $3 AND s.width_m >= $4 AND s.depth_m >= $5))`,
			clubSlug, resourceType, dims.length, dims.beam, dims.draft,
		).Scan(&totalUnits)
	} else {
		err = h.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM resource_units ru
			 JOIN resources r ON r.id = ru.resource_id
			 WHERE r.club_id = (SELECT id FROM clubs WHERE slug = $1)
			 AND r.type = $2 AND ru.is_active = true`,
			clubSlug, resourceType,
		).Scan(&totalUnits)
	}
	return totalUnits, err
}

func (h *BookingsHandler) queryBookingsForRange(ctx context.Context, clubSlug, resourceType string, start, end time.Time, dims *boatDimensions) (pgx.Rows, error) {
	if dims != nil {
		return h.db.Query(ctx,
			`SELECT b.resource_unit_id, b.start_date, b.end_date
			 FROM bookings b
			 JOIN resources r ON r.id = b.resource_id
			 JOIN resource_units ru ON ru.id = b.resource_unit_id
			 LEFT JOIN slips s ON s.id = ru.slip_id
			 WHERE r.club_id = (SELECT id FROM clubs WHERE slug = $1)
			 AND r.type = $2
			 AND b.status IN ('pending', 'confirmed')
			 AND b.start_date < $4 AND b.end_date > $3
			 AND b.resource_unit_id IS NOT NULL
			 AND (s.id IS NULL OR (s.length_m >= $5 AND s.width_m >= $6 AND s.depth_m >= $7))`,
			clubSlug, resourceType, start, end.AddDate(0, 0, 1),
			dims.length, dims.beam, dims.draft,
		)
	}
	return h.db.Query(ctx,
		`SELECT b.resource_unit_id, b.start_date, b.end_date
		 FROM bookings b
		 JOIN resources r ON r.id = b.resource_id
		 WHERE r.club_id = (SELECT id FROM clubs WHERE slug = $1)
		 AND r.type = $2
		 AND b.status IN ('pending', 'confirmed')
		 AND b.start_date < $4 AND b.end_date > $3
		 AND b.resource_unit_id IS NOT NULL`,
		clubSlug, resourceType, start, end.AddDate(0, 0, 1),
	)
}

// HandleAggregateAvailability returns per-day unit counts for a resource type.
func (h *BookingsHandler) HandleAggregateAvailability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resourceType := r.URL.Query().Get("type")
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	if resourceType == "" || startStr == "" || endStr == "" {
		Error(w, http.StatusBadRequest, "type, start, and end query parameters are required")
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
		Error(w, http.StatusBadRequest, "end must be after start")
		return
	}

	lengthStr := r.URL.Query().Get("length")
	beamStr := r.URL.Query().Get("beam")
	draftStr := r.URL.Query().Get("draft")

	var dims *boatDimensions
	if lengthStr != "" && beamStr != "" && draftStr != "" {
		dims = &boatDimensions{}
		fmt.Sscanf(lengthStr, "%f", &dims.length)
		fmt.Sscanf(beamStr, "%f", &dims.beam)
		fmt.Sscanf(draftStr, "%f", &dims.draft)
	}

	totalUnits, err := h.countTotalUnits(ctx, h.config.ClubSlug, resourceType, dims)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to count total units")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.queryBookingsForRange(ctx, h.config.ClubSlug, resourceType, startDate, endDate, dims)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query bookings for aggregate availability")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	bookedPerDay := make(map[string]map[string]bool)
	for rows.Next() {
		var unitID *string
		var bStart, bEnd time.Time
		if err := rows.Scan(&unitID, &bStart, &bEnd); err != nil {
			h.log.Error().Err(err).Msg("failed to scan booking")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if unitID == nil {
			continue
		}
		for d := bStart; d.Before(bEnd); d = d.AddDate(0, 0, 1) {
			ds := d.Format("2006-01-02")
			if bookedPerDay[ds] == nil {
				bookedPerDay[ds] = make(map[string]bool)
			}
			bookedPerDay[ds][*unitID] = true
		}
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating aggregate bookings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	result := make([]aggregateAvailability, 0)
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		ds := d.Format("2006-01-02")
		booked := len(bookedPerDay[ds])
		avail := totalUnits - booked
		if avail < 0 {
			avail = 0
		}
		result = append(result, aggregateAvailability{
			Date:           ds,
			TotalUnits:     totalUnits,
			AvailableUnits: avail,
		})
	}

	JSON(w, http.StatusOK, map[string]any{"dates": result})
}

// HandleTodayAvailability returns today's available/total for a resource type.
func (h *BookingsHandler) HandleTodayAvailability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resourceType := r.URL.Query().Get("type")
	if resourceType == "" {
		Error(w, http.StatusBadRequest, "type query parameter is required")
		return
	}

	today := time.Now().Truncate(24 * time.Hour)
	tomorrow := today.AddDate(0, 0, 1)

	var totalUnits int
	err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM resource_units ru
		 JOIN resources r ON r.id = ru.resource_id
		 WHERE r.club_id = (SELECT id FROM clubs WHERE slug = $1)
		 AND r.type = $2 AND ru.is_active = true`,
		h.config.ClubSlug, resourceType,
	).Scan(&totalUnits)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to count units for today")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var bookedUnits int
	err = h.db.QueryRow(ctx,
		`SELECT COUNT(DISTINCT b.resource_unit_id) FROM bookings b
		 JOIN resources r ON r.id = b.resource_id
		 WHERE r.club_id = (SELECT id FROM clubs WHERE slug = $1)
		 AND r.type = $2
		 AND b.status IN ('pending', 'confirmed')
		 AND b.start_date < $4 AND b.end_date > $3
		 AND b.resource_unit_id IS NOT NULL`,
		h.config.ClubSlug, resourceType, today, tomorrow,
	).Scan(&bookedUnits)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to count booked units for today")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	avail := totalUnits - bookedUnits
	if avail < 0 {
		avail = 0
	}

	JSON(w, http.StatusOK, todayAvailability{Available: avail, Total: totalUnits})
}

func shortenName(fullName string) string {
	if len(fullName) == 0 {
		return fullName
	}
	parts := []rune(fullName)
	for i, c := range parts {
		if c == ' ' && i+1 < len(parts) {
			return string(parts[:i+2]) + "."
		}
	}
	return fullName
}

// HandleHoistSlots returns time-slot availability for the slip hoist on a given date.
func (h *BookingsHandler) HandleHoistSlots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		Error(w, http.StatusBadRequest, "date query parameter is required")
		return
	}

	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid date format, use YYYY-MM-DD")
		return
	}

	settings, err := h.getClubSettings(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to load club settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	slotDuration := getIntSetting(settings, "hoist_slot_duration_minutes", 120)
	openHour := getIntSetting(settings, "hoist_open_hour", 8)
	closeHour := getIntSetting(settings, "hoist_close_hour", 20)

	loc := time.FixedZone("CET", 3600)
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), openHour, 0, 0, 0, loc)
	dayEnd := time.Date(date.Year(), date.Month(), date.Day(), closeHour, 0, 0, 0, loc)

	rows, err := h.db.Query(ctx,
		`SELECT b.start_date, b.end_date, COALESCE(u.full_name, b.guest_name, '')
		 FROM bookings b
		 JOIN resources r ON r.id = b.resource_id
		 LEFT JOIN users u ON u.id = b.user_id
		 WHERE r.club_id = (SELECT id FROM clubs WHERE slug = $1)
		 AND r.type = 'slip_hoist'
		 AND b.status IN ('pending', 'confirmed')
		 AND b.start_date < $3 AND b.end_date > $2`,
		h.config.ClubSlug, dayStart, dayEnd,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query hoist bookings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type bookedSlot struct {
		start, end time.Time
		name       string
	}
	var booked []bookedSlot
	for rows.Next() {
		var bs bookedSlot
		if err := rows.Scan(&bs.start, &bs.end, &bs.name); err != nil {
			h.log.Error().Err(err).Msg("failed to scan hoist booking")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		booked = append(booked, bs)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating hoist bookings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	slots := make([]hoistSlot, 0)
	for t := dayStart; t.Before(dayEnd); t = t.Add(time.Duration(slotDuration) * time.Minute) {
		slotEnd := t.Add(time.Duration(slotDuration) * time.Minute)
		if slotEnd.After(dayEnd) {
			break
		}
		hs := hoistSlot{
			Start:     t.Format("15:04"),
			End:       slotEnd.Format("15:04"),
			Available: true,
		}
		for _, bs := range booked {
			if bs.start.Before(slotEnd) && bs.end.After(t) {
				hs.Available = false
				shortened := shortenName(bs.name)
				hs.BookedBy = &shortened
				break
			}
		}
		slots = append(slots, hs)
	}

	JSON(w, http.StatusOK, hoistSlotsResponse{
		Date:                dateStr,
		SlotDurationMinutes: slotDuration,
		Slots:               slots,
	})
}

func (h *BookingsHandler) getClubSettings(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := h.db.Query(ctx,
		`SELECT key, value FROM club_settings
		 WHERE club_id = (SELECT id FROM clubs WHERE slug = $1)`,
		h.config.ClubSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]json.RawMessage)
	for rows.Next() {
		var k string
		var v json.RawMessage
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		settings[k] = v
	}
	return settings, rows.Err()
}

func getIntSetting(settings map[string]json.RawMessage, key string, defaultVal int) int {
	v, ok := settings[key]
	if !ok {
		return defaultVal
	}
	var n int
	if err := json.Unmarshal(v, &n); err != nil {
		return defaultVal
	}
	return n
}

func (h *BookingsHandler) parseBookingDates(req createBookingRequest) (time.Time, time.Time, error) {
	isTimeBased := req.ResourceType == "slip_hoist"
	if isTimeBased {
		start, err := time.Parse(time.RFC3339, req.StartDate)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start_date format for hoist, use RFC3339")
		}
		end, err := time.Parse(time.RFC3339, req.EndDate)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end_date format for hoist, use RFC3339")
		}
		return start, end, nil
	}
	start, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid start_date format, use YYYY-MM-DD")
	}
	end, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid end_date format, use YYYY-MM-DD")
	}
	return start, end, nil
}

func (h *BookingsHandler) resolveResource(ctx context.Context, resourceID, resourceType, clubSlug string) (string, string, error) {
	var resID, clubID string
	var err error
	if resourceID != "" {
		err = h.db.QueryRow(ctx,
			`SELECT r.id, r.club_id FROM resources r
			 JOIN clubs c ON c.id = r.club_id
			 WHERE r.id = $1 AND c.slug = $2`,
			resourceID, clubSlug,
		).Scan(&resID, &clubID)
	} else {
		err = h.db.QueryRow(ctx,
			`SELECT r.id, r.club_id FROM resources r
			 JOIN clubs c ON c.id = r.club_id
			 WHERE r.type = $1 AND c.slug = $2
			 LIMIT 1`,
			resourceType, clubSlug,
		).Scan(&resID, &clubID)
	}
	return resID, clubID, err
}

func needsBoatDimensions(resourceType string) bool {
	return resourceType == "guest_slip" || resourceType == "shared_slip" || resourceType == "seasonal_rental"
}

func (h *BookingsHandler) findBookingUnit(ctx context.Context, req createBookingRequest, resourceID, clubID string, start, end time.Time) (*string, error) {
	if needsBoatDimensions(req.ResourceType) {
		unitID, err := h.findBestFitUnit(ctx, resourceID, clubID, start, end, *req.BoatLengthM, *req.BoatBeamM, *req.BoatDraftM)
		if err != nil {
			return nil, fmt.Errorf("find matching unit: %w", err)
		}
		if unitID == nil {
			return nil, &httpError{http.StatusConflict, "no suitable slip available for the given boat dimensions and date range"}
		}
		return unitID, nil
	}
	unitID, err := h.findAvailableUnit(ctx, resourceID, start, end)
	if err != nil {
		return nil, fmt.Errorf("find available unit: %w", err)
	}
	if unitID == nil {
		return nil, &httpError{http.StatusConflict, "no available units for the requested dates"}
	}
	return unitID, nil
}

func (h *BookingsHandler) HandleCreateBooking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.StartDate == "" || req.EndDate == "" {
		Error(w, http.StatusBadRequest, "start_date and end_date are required")
		return
	}
	if req.ResourceType == "" && req.ResourceID == "" {
		Error(w, http.StatusBadRequest, "resource_type or resource_id is required")
		return
	}

	if needsBoatDimensions(req.ResourceType) && (req.BoatLengthM == nil || req.BoatBeamM == nil || req.BoatDraftM == nil) {
		Error(w, http.StatusBadRequest, "boat_length_m, boat_beam_m, and boat_draft_m are required for slip bookings")
		return
	}

	startDate, endDate, err := h.parseBookingDates(req)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if !endDate.After(startDate) {
		Error(w, http.StatusBadRequest, "end_date must be after start_date")
		return
	}

	resourceID, clubID, err := h.resolveResource(ctx, req.ResourceID, req.ResourceType, h.config.ClubSlug)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "resource not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to resolve resource")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	lockKey := fmt.Sprintf("%s%s:%s:%s", bookingLockPrefix, resourceID, req.StartDate, req.EndDate)
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

	unitID, err := h.findBookingUnit(ctx, req, resourceID, clubID, startDate, endDate)
	if err != nil {
		if he, ok := err.(*httpError); ok {
			Error(w, he.status, he.message)
			return
		}
		h.log.Error().Err(err).Msg("failed to find unit")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var userID *string
	claims := middleware.GetClaims(ctx)
	if claims != nil {
		userID = &claims.UserID
	}

	var b booking
	row := h.db.QueryRow(ctx,
		`INSERT INTO bookings (resource_id, resource_unit_id, user_id, club_id, start_date, end_date, status,
		 guest_name, guest_email, guest_phone, boat_length_m, boat_beam_m, boat_draft_m, notes)
		 VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, $9, $10, $11, $12, $13)
		 RETURNING `+bookingColumns,
		resourceID, unitID, userID, clubID, startDate, endDate,
		req.GuestName, req.GuestEmail, req.GuestPhone,
		req.BoatLengthM, req.BoatBeamM, req.BoatDraftM, req.Notes,
	)
	if err = scanBooking(row, &b); err != nil {
		h.log.Error().Err(err).Msg("failed to create booking")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, b)
}

// findBestFitUnit finds the smallest slip-backed unit that fits the boat and is available for the date range.
func (h *BookingsHandler) findBestFitUnit(ctx context.Context, resourceID, clubID string, start, end time.Time, boatLen, boatBeam, boatDraft float64) (*string, error) {
	rows, err := h.db.Query(ctx,
		`SELECT ru.id, s.length_m, s.width_m, s.depth_m
		 FROM resource_units ru
		 JOIN slips s ON s.id = ru.slip_id
		 WHERE ru.resource_id = $1 AND ru.is_active = true
		 AND s.length_m >= $2 AND s.width_m >= $3 AND s.depth_m >= $4
		 AND NOT EXISTS (
		     SELECT 1 FROM bookings b
		     WHERE b.resource_unit_id = ru.id
		     AND b.status IN ('pending', 'confirmed')
		     AND b.start_date < $6 AND b.end_date > $5
		 )
		 ORDER BY (s.length_m * s.width_m * s.depth_m) ASC`,
		resourceID, boatLen, boatBeam, boatDraft, start, end,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Also search shared slips with active share windows
	sharedRows, err := h.db.Query(ctx,
		`SELECT ru.id, s.length_m, s.width_m, s.depth_m
		 FROM resource_units ru
		 JOIN slips s ON s.id = ru.slip_id
		 JOIN resources r ON r.id = ru.resource_id
		 JOIN slip_assignments sa ON sa.slip_id = s.id AND sa.released_at IS NULL
		 JOIN slip_shares ss ON ss.slip_assignment_id = sa.id AND ss.status = 'active'
		 WHERE r.club_id = $1 AND r.type = 'shared_slip' AND ru.is_active = true
		 AND s.length_m >= $2 AND s.width_m >= $3 AND s.depth_m >= $4
		 AND ss.available_from <= $5 AND ss.available_to >= $6
		 AND NOT EXISTS (
		     SELECT 1 FROM bookings b
		     WHERE b.resource_unit_id = ru.id
		     AND b.status IN ('pending', 'confirmed')
		     AND b.start_date < $6 AND b.end_date > $5
		 )
		 ORDER BY (s.length_m * s.width_m * s.depth_m) ASC`,
		clubID, boatLen, boatBeam, boatDraft, start, end,
	)
	if err != nil {
		rows.Close()
		return nil, err
	}
	defer sharedRows.Close()

	type candidate struct {
		id     string
		volume float64
	}

	var best *candidate
	for _, r := range []pgx.Rows{rows, sharedRows} {
		for r.Next() {
			var id string
			var l, w, d float64
			if err := r.Scan(&id, &l, &w, &d); err != nil {
				return nil, err
			}
			vol := l * w * d
			if best == nil || vol < best.volume {
				best = &candidate{id: id, volume: vol}
			}
		}
		if err := r.Err(); err != nil {
			return nil, err
		}
	}

	if best == nil {
		return nil, nil
	}
	return &best.id, nil
}

// findAvailableUnit finds any available unit for the resource and date range.
func (h *BookingsHandler) findAvailableUnit(ctx context.Context, resourceID string, start, end time.Time) (*string, error) {
	var unitID string
	err := h.db.QueryRow(ctx,
		`SELECT ru.id FROM resource_units ru
		 WHERE ru.resource_id = $1 AND ru.is_active = true
		 AND NOT EXISTS (
		     SELECT 1 FROM bookings b
		     WHERE b.resource_unit_id = ru.id
		     AND b.status IN ('pending', 'confirmed')
		     AND b.start_date < $3 AND b.end_date > $2
		 )
		 ORDER BY ru.sort_order ASC
		 LIMIT 1`,
		resourceID, start, end,
	).Scan(&unitID)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &unitID, nil
}

func (h *BookingsHandler) HandleGetBooking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	bookingID := chi.URLParam(r, "bookingID")
	if bookingID == "" {
		Error(w, http.StatusBadRequest, "missing booking ID")
		return
	}

	var b booking
	err := scanBooking(h.db.QueryRow(ctx,
		`SELECT b.id, b.resource_id, b.resource_unit_id, b.user_id, b.club_id, b.start_date, b.end_date, b.status,
		        b.guest_name, b.guest_email, b.guest_phone, b.payment_id, b.boat_length_m, b.boat_beam_m, b.boat_draft_m,
		        b.notes, b.created_at, b.updated_at
		 FROM bookings b
		 JOIN clubs c ON c.id = b.club_id
		 WHERE b.id = $1 AND c.slug = $2`,
		bookingID, h.config.ClubSlug,
	), &b)
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

	query := `SELECT ` + bookingColumns + `
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
		if err := scanBooking(rows, &b); err != nil {
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
	err := scanBooking(h.db.QueryRow(ctx,
		`SELECT `+bookingColumns+`
		 FROM bookings WHERE id = $1 AND club_id = $2`,
		bookingID, claims.ClubID,
	), &b)
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
	isStyre := hasRole(claims.Roles, "board") || hasRole(claims.Roles, "harbor_master")
	if !isOwner && !isStyre {
		Error(w, http.StatusForbidden, "only the booking owner or board can cancel")
		return
	}

	if b.Status == "cancelled" {
		Error(w, http.StatusConflict, "booking is already cancelled")
		return
	}

	oldStatus := b.Status
	err = scanBooking(h.db.QueryRow(ctx,
		`UPDATE bookings SET status = 'cancelled', updated_at = now()
		 WHERE id = $1
		 RETURNING `+bookingColumns,
		bookingID,
	), &b)
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
	err = scanBooking(h.db.QueryRow(ctx,
		`UPDATE bookings SET status = 'confirmed', updated_at = now()
		 WHERE id = $1
		 RETURNING `+bookingColumns,
		bookingID,
	), &b)
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

	pg := shared.ParsePagination(r, 50, 200)

	statusFilter := r.URL.Query().Get("status")
	resourceTypeFilter := r.URL.Query().Get("resource_type")
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	query := `SELECT b.id, b.resource_id, b.resource_unit_id, b.user_id, b.club_id, b.start_date, b.end_date, b.status,
	                 b.guest_name, b.guest_email, b.guest_phone, b.payment_id,
	                 b.boat_length_m, b.boat_beam_m, b.boat_draft_m,
	                 b.notes, b.created_at, b.updated_at,
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
	query += fmt.Sprintf(` LIMIT $%d OFFSET $%d`, argIdx, argIdx+1)
	args = append(args, pg.Limit, pg.Offset)

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
			&ba.ID, &ba.ResourceID, &ba.ResourceUnitID, &ba.UserID, &ba.ClubID,
			&ba.StartDate, &ba.EndDate, &ba.Status,
			&ba.GuestName, &ba.GuestEmail, &ba.GuestPhone, &ba.PaymentID,
			&ba.BoatLengthM, &ba.BoatBeamM, &ba.BoatDraftM,
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

	JSON(w, http.StatusOK, shared.NewPaginatedResponse(bookings, len(bookings), pg))
}

func hasRole(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}
