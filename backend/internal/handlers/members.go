package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/shared"
)

type MembersHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewMembersHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	log zerolog.Logger,
) *MembersHandler {
	return &MembersHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "members").Logger(),
	}
}

// supportedUILocales is the set of UI language codes a member may pick.
// Keep aligned with the clubs/users CHECK constraints (migration 000047)
// and frontend/src/locales/*.json.
var supportedUILocales = map[string]bool{
	"nb": true, "nn": true, "en": true, "de": true,
	"fr": true, "it": true, "nl": true, "pl": true,
}

type memberProfile struct {
	ID              string `json:"id"`
	ClubID          string `json:"club_id"`
	Email           string `json:"email"`
	FirstName       string `json:"first_name"`
	LastName        string `json:"last_name"`
	FullName        string `json:"full_name"`
	Phone           string `json:"phone"`
	Address         string `json:"address_line"`
	PostalCode      string `json:"postal_code"`
	City            string `json:"city"`
	IsLocal         bool   `json:"is_local"`
	HideInDirectory bool   `json:"hide_in_directory"`
	// PreferredLanguage is nil when the member hasn't chosen one
	// (→ the UI follows the club default).
	PreferredLanguage *string   `json:"preferred_language"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type updateProfileRequest struct {
	FirstName       *string `json:"first_name,omitempty"`
	LastName        *string `json:"last_name,omitempty"`
	FullName        *string `json:"full_name,omitempty"` // kept for backward compat; split on last space if first/last absent
	Phone           *string `json:"phone,omitempty"`
	Address         *string `json:"address_line,omitempty"`
	PostalCode      *string `json:"postal_code,omitempty"`
	City            *string `json:"city,omitempty"`
	HideInDirectory *bool   `json:"hide_in_directory,omitempty"`
	// PreferredLanguage: a supported code sets it; "" clears it
	// (revert to club default); omitted leaves it unchanged.
	PreferredLanguage *string `json:"preferred_language,omitempty"`
}

type boat struct {
	ID                    string     `json:"id"`
	UserID                string     `json:"user_id"`
	ClubID                string     `json:"club_id"`
	Name                  string     `json:"name"`
	Type                  string     `json:"type"`
	Manufacturer          string     `json:"manufacturer"`
	Model                 string     `json:"model"`
	LengthM               *float64   `json:"length_m,omitempty"`
	BeamM                 *float64   `json:"beam_m,omitempty"`
	DraftM                *float64   `json:"draft_m,omitempty"`
	WeightKg              *float64   `json:"weight_kg,omitempty"`
	RegistrationNumber    string     `json:"registration_number"`
	MMSI                  string     `json:"mmsi"`
	CallSign              string     `json:"call_sign"`
	BoatModelID           *string    `json:"boat_model_id,omitempty"`
	MeasurementsConfirmed bool       `json:"measurements_confirmed"`
	ConfirmedBy           *string    `json:"confirmed_by,omitempty"`
	ConfirmedAt           *time.Time `json:"confirmed_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	Slip                  *boatSlip  `json:"slip,omitempty"`
}

type boatSlip struct {
	SlipID         string `json:"slip_id"`
	Section        string `json:"section"`
	Number         string `json:"number"`
	AssignmentType string `json:"assignment_type"`
}

type createBoatRequest struct {
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Manufacturer       string   `json:"manufacturer"`
	Model              string   `json:"model"`
	LengthM            *float64 `json:"length_m,omitempty"`
	BeamM              *float64 `json:"beam_m,omitempty"`
	DraftM             *float64 `json:"draft_m,omitempty"`
	WeightKg           *float64 `json:"weight_kg,omitempty"`
	RegistrationNumber string   `json:"registration_number"`
	MMSI               string   `json:"mmsi"`
	CallSign           string   `json:"call_sign"`
	BoatModelID        *string  `json:"boat_model_id,omitempty"`
}

type updateBoatRequest struct {
	Name               *string  `json:"name,omitempty"`
	Type               *string  `json:"type,omitempty"`
	Manufacturer       *string  `json:"manufacturer,omitempty"`
	Model              *string  `json:"model,omitempty"`
	LengthM            *float64 `json:"length_m,omitempty"`
	BeamM              *float64 `json:"beam_m,omitempty"`
	DraftM             *float64 `json:"draft_m,omitempty"`
	WeightKg           *float64 `json:"weight_kg,omitempty"`
	RegistrationNumber *string  `json:"registration_number,omitempty"`
	MMSI               *string  `json:"mmsi,omitempty"`
	CallSign           *string  `json:"call_sign,omitempty"`
	BoatModelID        *string  `json:"boat_model_id,omitempty"`
}

type memberSlip struct {
	SlipID     string   `json:"slip_id"`
	Number     string   `json:"number"`
	Section    string   `json:"section"`
	LengthM    *float64 `json:"length_m,omitempty"`
	WidthM     *float64 `json:"width_m,omitempty"`
	DepthM     *float64 `json:"depth_m,omitempty"`
	Status     string   `json:"status"`
	AssignedAt string   `json:"assigned_at"`
}

type reportIssueRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

type directoryEntry struct {
	FirstName string  `json:"first_name"`
	LastName  string  `json:"last_name"`
	FullName  string  `json:"full_name"`
	Phone     *string `json:"phone,omitempty"`
	Email     *string `json:"email,omitempty"`
}

func (h *MembersHandler) HandleGetMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var p memberProfile
	err := h.db.QueryRow(ctx,
		`SELECT id, club_id, email, first_name, last_name, full_name, phone, address_line, postal_code, city, is_local, hide_in_directory, preferred_language, created_at, updated_at
		 FROM users WHERE id = $1 AND club_id = $2`,
		claims.UserID, claims.ClubID,
	).Scan(
		&p.ID, &p.ClubID, &p.Email, &p.FirstName, &p.LastName, &p.FullName, &p.Phone,
		&p.Address, &p.PostalCode, &p.City, &p.IsLocal, &p.HideInDirectory,
		&p.PreferredLanguage, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch member profile")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, p)
}

func (h *MembersHandler) HandleUpdateMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var current memberProfile
	err := h.db.QueryRow(ctx,
		`SELECT id, club_id, email, first_name, last_name, full_name, phone, address_line, postal_code, city, is_local, hide_in_directory, preferred_language, created_at, updated_at
		 FROM users WHERE id = $1 AND club_id = $2`,
		claims.UserID, claims.ClubID,
	).Scan(
		&current.ID, &current.ClubID, &current.Email, &current.FirstName, &current.LastName, &current.FullName, &current.Phone,
		&current.Address, &current.PostalCode, &current.City, &current.IsLocal, &current.HideInDirectory,
		&current.PreferredLanguage, &current.CreatedAt, &current.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch current profile for update")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if req.FirstName != nil {
		current.FirstName = strings.TrimSpace(*req.FirstName)
	}
	if req.LastName != nil {
		current.LastName = strings.TrimSpace(*req.LastName)
	}
	// Backward compat: if only full_name is sent, split on last space.
	if req.FirstName == nil && req.LastName == nil && req.FullName != nil {
		current.FirstName, current.LastName = splitFullName(*req.FullName)
	}
	if req.Phone != nil {
		current.Phone = *req.Phone
	}
	if req.Address != nil {
		current.Address = *req.Address
	}
	if req.PostalCode != nil {
		current.PostalCode = *req.PostalCode
	}
	if req.City != nil {
		current.City = *req.City
	}
	if req.HideInDirectory != nil {
		current.HideInDirectory = *req.HideInDirectory
	}
	if req.PreferredLanguage != nil {
		v := strings.TrimSpace(*req.PreferredLanguage)
		switch {
		case v == "":
			current.PreferredLanguage = nil // clear → follow club default
		case !supportedUILocales[v]:
			Error(w, http.StatusBadRequest, "unsupported language")
			return
		default:
			current.PreferredLanguage = &v
		}
	}

	var p memberProfile
	err = h.db.QueryRow(ctx,
		`UPDATE users
		 SET first_name = $3, last_name = $4, phone = $5, address_line = $6, postal_code = $7, city = $8, hide_in_directory = $9, preferred_language = $10, updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, club_id, email, first_name, last_name, full_name, phone, address_line, postal_code, city, is_local, hide_in_directory, preferred_language, created_at, updated_at`,
		claims.UserID, claims.ClubID,
		current.FirstName, current.LastName, current.Phone, current.Address, current.PostalCode, current.City, current.HideInDirectory, current.PreferredLanguage,
	).Scan(
		&p.ID, &p.ClubID, &p.Email, &p.FirstName, &p.LastName, &p.FullName, &p.Phone,
		&p.Address, &p.PostalCode, &p.City, &p.IsLocal, &p.HideInDirectory,
		&p.PreferredLanguage, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update member profile")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, p)
}

func (h *MembersHandler) HandleListMyBoats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT b.id, b.user_id, b.club_id, b.name, b.type, b.manufacturer, b.model,
		        b.length_m, b.beam_m, b.draft_m, b.weight_kg, b.registration_number,
		        b.mmsi, b.call_sign,
		        b.boat_model_id, b.measurements_confirmed, b.confirmed_by, b.confirmed_at,
		        b.created_at, b.updated_at,
		        sa.slip_id, s.section, s.number, sa.assignment_type::text
		 FROM boats b
		 LEFT JOIN slip_assignments sa
		        ON sa.boat_id = b.id AND sa.released_at IS NULL
		 LEFT JOIN slips s ON s.id = sa.slip_id
		 WHERE b.user_id = $1 AND b.club_id = $2
		 ORDER BY b.name`,
		claims.UserID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list boats")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	boats := make([]boat, 0)
	for rows.Next() {
		var b boat
		var slipID, slipSection, slipNumber, slipType *string
		if err := rows.Scan(
			&b.ID, &b.UserID, &b.ClubID, &b.Name, &b.Type, &b.Manufacturer, &b.Model,
			&b.LengthM, &b.BeamM, &b.DraftM, &b.WeightKg, &b.RegistrationNumber,
			&b.MMSI, &b.CallSign,
			&b.BoatModelID, &b.MeasurementsConfirmed, &b.ConfirmedBy, &b.ConfirmedAt,
			&b.CreatedAt, &b.UpdatedAt,
			&slipID, &slipSection, &slipNumber, &slipType,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan boat")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if slipID != nil {
			b.Slip = &boatSlip{
				SlipID:         *slipID,
				Section:        deref(slipSection),
				Number:         deref(slipNumber),
				AssignmentType: deref(slipType),
			}
		}
		boats = append(boats, b)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating boat rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, boats)
}

func (h *MembersHandler) HandleCreateBoat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createBoatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	b, err := createBoatForUser(ctx, h.db, h.log,
		claims.UserID, claims.ClubID, claims.UserID, req, false)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusCreated, b)
}

func applyBoatUpdates(current *boat, req updateBoatRequest) {
	if req.Name != nil {
		current.Name = *req.Name
	}
	if req.Type != nil {
		current.Type = *req.Type
	}
	if req.Manufacturer != nil {
		current.Manufacturer = *req.Manufacturer
	}
	if req.Model != nil {
		current.Model = *req.Model
	}
	if req.LengthM != nil {
		current.LengthM = req.LengthM
	}
	if req.BeamM != nil {
		current.BeamM = req.BeamM
	}
	if req.DraftM != nil {
		current.DraftM = req.DraftM
	}
	if req.WeightKg != nil {
		current.WeightKg = req.WeightKg
	}
	if req.RegistrationNumber != nil {
		current.RegistrationNumber = *req.RegistrationNumber
	}
	if req.MMSI != nil {
		current.MMSI = *req.MMSI
	}
	if req.CallSign != nil {
		current.CallSign = *req.CallSign
	}
	if req.BoatModelID != nil {
		current.BoatModelID = req.BoatModelID
	}
}

func (h *MembersHandler) HandleUpdateBoat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	boatID := chi.URLParam(r, "boatID")
	if boatID == "" {
		Error(w, http.StatusBadRequest, "missing boat ID")
		return
	}

	var req updateBoatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	b, err := updateBoatForUser(ctx, h.db, h.log,
		boatID, claims.UserID, claims.ClubID, claims.UserID, req, false)
	if errors.Is(err, boatErrNotFound) {
		Error(w, http.StatusNotFound, "boat not found")
		return
	}
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, b)
}

func (h *MembersHandler) HandleDeleteBoat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	boatID := chi.URLParam(r, "boatID")
	if boatID == "" {
		Error(w, http.StatusBadRequest, "missing boat ID")
		return
	}

	tag, err := h.db.Exec(ctx,
		`DELETE FROM boats WHERE id = $1 AND user_id = $2 AND club_id = $3`,
		boatID, claims.UserID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete boat")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "boat not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *MembersHandler) HandleGetMySlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var s memberSlip
	err := h.db.QueryRow(ctx,
		`SELECT s.id, s.number, s.section, s.length_m, s.width_m, s.depth_m, s.status, sa.assigned_at
		 FROM slip_assignments sa
		 JOIN slips s ON s.id = sa.slip_id
		 WHERE sa.user_id = $1 AND sa.club_id = $2 AND sa.released_at IS NULL`,
		claims.UserID, claims.ClubID,
	).Scan(
		&s.SlipID, &s.Number, &s.Section,
		&s.LengthM, &s.WidthM, &s.DepthM, &s.Status,
		&s.AssignedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "no slip assigned")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch member slip")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, s)
}

func (h *MembersHandler) HandleReportIssue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req reportIssueRequest
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

	var projectID string
	err := h.db.QueryRow(ctx,
		`SELECT id FROM projects WHERE club_id = $1 AND name = 'harbor-maintenance'`,
		claims.ClubID,
	).Scan(&projectID)
	if err == pgx.ErrNoRows {
		err = h.db.QueryRow(ctx,
			`INSERT INTO projects (club_id, name, description, created_by)
			 VALUES ($1, 'harbor-maintenance', 'Harbor maintenance issues reported by members', $2)
			 RETURNING id`,
			claims.ClubID, claims.UserID,
		).Scan(&projectID)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to create harbor-maintenance project")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else if err != nil {
		h.log.Error().Err(err).Msg("failed to find harbor-maintenance project")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var taskID string
	var createdAt time.Time
	err = h.db.QueryRow(ctx,
		`INSERT INTO tasks (project_id, club_id, title, description, status, priority, created_by)
		 VALUES ($1, $2, $3, $4, 'todo', $5, $6)
		 RETURNING id, created_at`,
		projectID, claims.ClubID, req.Title, req.Description, priority, claims.UserID,
	).Scan(&taskID, &createdAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create maintenance task")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, map[string]any{
		"id":         taskID,
		"title":      req.Title,
		"status":     "todo",
		"priority":   priority,
		"created_at": createdAt,
	})
}

func (h *MembersHandler) HandleGetDirectory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	pg := shared.ParsePagination(r, 100, 500)

	rows, err := h.db.Query(ctx,
		`SELECT u.first_name, u.last_name, u.full_name, u.phone, u.email
		 FROM users u
		 JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		 WHERE u.club_id = $1
		 AND u.hide_in_directory = FALSE
		 AND ur.role IN ('member', 'slip_holder', 'board', 'harbor_master', 'treasurer', 'admin')
		 GROUP BY u.id, u.first_name, u.last_name, u.full_name, u.phone, u.email
		 ORDER BY u.last_name, u.first_name
		 LIMIT $2 OFFSET $3`,
		claims.ClubID, pg.Limit, pg.Offset,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list directory")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	entries := make([]directoryEntry, 0)
	for rows.Next() {
		var e directoryEntry
		var phone, email string
		if err := rows.Scan(&e.FirstName, &e.LastName, &e.FullName, &phone, &email); err != nil {
			h.log.Error().Err(err).Msg("failed to scan directory entry")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if phone != "" {
			e.Phone = &phone
		}
		if email != "" {
			e.Email = &email
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating directory rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, shared.NewPaginatedResponse(entries, len(entries), pg))
}

type dashboardResponse struct {
	MembershipStatus     string    `json:"membershipStatus"`
	QueuePosition        *int      `json:"queuePosition"`
	QueueTotal           *int      `json:"queueTotal"`
	Slip                 *dashSlip `json:"slip"`
	UpcomingBookingCount int       `json:"upcomingBookingsCount"`
}

type dashSlip struct {
	Number   string `json:"number"`
	Location string `json:"location"`
}

type memberInvoice struct {
	ID            string  `json:"id"`
	InvoiceNumber int     `json:"invoice_number"`
	KID           string  `json:"kid_number"`
	TotalAmount   float64 `json:"total_amount"`
	IssueDate     string  `json:"issue_date"`
	DueDate       string  `json:"due_date"`
	SentAt        *string `json:"sent_at"`
	Paid          bool    `json:"paid"`
	PriceItemName string  `json:"price_item_name"`
	Description   string  `json:"description"`
	// HasPDF gates the portal "Open PDF" action — imported invoices (and
	// any not yet rendered) have no retrievable PDF, so the button is
	// hidden rather than leading to a "PDF not available" error.
	HasPDF bool `json:"has_pdf"`
}

func (h *MembersHandler) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	resp := dashboardResponse{}

	// Single query combining roles, waiting list, slip, and booking count
	var rolesArr []string
	var queuePos, queueTotal *int
	var slipNum, slipSection *string
	var bookingCount int

	err := h.db.QueryRow(ctx, `
		WITH user_roles_agg AS (
			SELECT array_agg(role) AS roles
			FROM user_roles WHERE user_id = $1 AND club_id = $2
		),
		queue AS (
			SELECT position,
				(SELECT count(*) FROM waiting_list_entries WHERE club_id = $2 AND status = 'active') AS total
			FROM waiting_list_entries WHERE user_id = $1 AND club_id = $2 AND status = 'active'
		),
		slip AS (
			SELECT s.number, s.section
			FROM slips s JOIN slip_assignments sa ON sa.slip_id = s.id
			WHERE sa.user_id = $1 AND s.club_id = $2 AND sa.released_at IS NULL
			LIMIT 1
		),
		bookings_count AS (
			SELECT count(*) AS cnt FROM bookings
			WHERE user_id = $1 AND club_id = $2 AND status != 'cancelled' AND end_date >= now()
		)
		SELECT
			COALESCE((SELECT roles FROM user_roles_agg), '{}'),
			(SELECT position FROM queue),
			(SELECT total FROM queue),
			(SELECT number FROM slip),
			(SELECT section FROM slip),
			(SELECT cnt FROM bookings_count)
	`, claims.UserID, claims.ClubID,
	).Scan(&rolesArr, &queuePos, &queueTotal, &slipNum, &slipSection, &bookingCount)

	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to load dashboard")
		return
	}

	resp.MembershipStatus = bestRole(rolesArr)
	resp.QueuePosition = queuePos
	resp.QueueTotal = queueTotal
	if slipNum != nil {
		resp.Slip = &dashSlip{Number: *slipNum, Location: *slipSection}
	}
	resp.UpcomingBookingCount = bookingCount

	JSON(w, http.StatusOK, resp)
}

// HandleListMyInvoices returns the authenticated member's own invoices
// only — scoped to their user_id and club. Drafts the treasurer has not
// issued yet are excluded (sent_at IS NULL and not imported), as are
// voided invoices. Paid is derived from a linked payment.
func (h *MembersHandler) HandleListMyInvoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	where := ""
	switch r.URL.Query().Get("status") {
	case "paid":
		where = " AND i.payment_id IS NOT NULL"
	case "unpaid":
		where = " AND i.payment_id IS NULL"
	case "", "all":
		// no extra filter
	default:
		Error(w, http.StatusBadRequest, "status must be one of: paid, unpaid, all")
		return
	}

	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			Error(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = n
	}

	q := `SELECT i.id, i.invoice_number, i.kid_number, i.total_amount,
	             i.issue_date, i.due_date, i.sent_at,
	             (i.payment_id IS NOT NULL) AS paid,
	             COALESCE(pi.name, ''),
	             COALESCE((SELECT description FROM invoice_lines WHERE invoice_id = i.id LIMIT 1), ''),
	             (i.pdf_data IS NOT NULL OR COALESCE(i.s3_key, '') <> '')
	        FROM invoices i
	        LEFT JOIN price_items pi ON pi.id = i.price_item_id
	       WHERE i.user_id = $1 AND i.club_id = $2
	         AND i.status = 'open'
	         AND (i.sent_at IS NOT NULL OR i.import_source IS NOT NULL)` + where + `
	       ORDER BY (i.payment_id IS NULL) DESC,
	                CASE WHEN i.payment_id IS NULL THEN i.due_date END ASC,
	                i.issue_date DESC`
	args := []any{claims.UserID, claims.ClubID}
	if limit > 0 {
		q += " LIMIT $3"
		args = append(args, limit)
	}

	rows, err := h.db.Query(ctx, q, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("list my invoices")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := make([]memberInvoice, 0)
	for rows.Next() {
		var mi memberInvoice
		var issue, due time.Time
		var sentAt *time.Time
		if err := rows.Scan(&mi.ID, &mi.InvoiceNumber, &mi.KID, &mi.TotalAmount,
			&issue, &due, &sentAt, &mi.Paid, &mi.PriceItemName, &mi.Description, &mi.HasPDF); err != nil {
			h.log.Error().Err(err).Msg("scan my invoice row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		mi.IssueDate = issue.Format("2006-01-02")
		mi.DueDate = due.Format("2006-01-02")
		if sentAt != nil {
			s := sentAt.Format(time.RFC3339)
			mi.SentAt = &s
		}
		out = append(out, mi)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("iterate my invoices")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, out)
}

// HandlePaymentDataUpdatedAt returns when invoice/payment data was last
// refreshed from the bank for the caller's club — i.e. the timestamp of the
// most recent bank-statement import. Invoice paid/unpaid status only changes
// when a bank import is reconciled, so this is the freshness signal shown on
// the faktura and economy surfaces. Auth-only (no role gate) so both the
// member portal and the admin views can read it. Null when no import exists.
func (h *MembersHandler) HandlePaymentDataUpdatedAt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var updatedAt *time.Time
	if err := h.db.QueryRow(ctx,
		`SELECT MAX(created_at) FROM bank_imports WHERE club_id = $1`,
		claims.ClubID,
	).Scan(&updatedAt); err != nil {
		h.log.Error().Err(err).Msg("query last bank import")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"updated_at": updatedAt})
}

func dimsMatch(a, b *float64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func bestRole(roles []string) string {
	order := []string{"admin", "board", "slip_holder", "member", "applicant"}
	roleSet := make(map[string]bool, len(roles))
	for _, r := range roles {
		roleSet[r] = true
	}
	for _, r := range order {
		if roleSet[r] {
			return r
		}
	}
	if len(roles) > 0 {
		return roles[0]
	}
	return "member"
}
