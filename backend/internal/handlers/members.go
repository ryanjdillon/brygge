package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
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

type memberProfile struct {
	ID         string    `json:"id"`
	ClubID     string    `json:"club_id"`
	Email      string    `json:"email"`
	FullName   string    `json:"full_name"`
	Phone      string    `json:"phone"`
	Address    string    `json:"address_line"`
	PostalCode string    `json:"postal_code"`
	City       string    `json:"city"`
	IsLocal    bool      `json:"is_local"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type updateProfileRequest struct {
	FullName   *string `json:"full_name,omitempty"`
	Phone      *string `json:"phone,omitempty"`
	Address    *string `json:"address_line,omitempty"`
	PostalCode *string `json:"postal_code,omitempty"`
	City       *string `json:"city,omitempty"`
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
	FullName string  `json:"full_name"`
	Phone    *string `json:"phone,omitempty"`
	Email    *string `json:"email,omitempty"`
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
		`SELECT id, club_id, email, full_name, phone, address_line, postal_code, city, is_local, created_at, updated_at
		 FROM users WHERE id = $1 AND club_id = $2`,
		claims.UserID, claims.ClubID,
	).Scan(
		&p.ID, &p.ClubID, &p.Email, &p.FullName, &p.Phone,
		&p.Address, &p.PostalCode, &p.City, &p.IsLocal,
		&p.CreatedAt, &p.UpdatedAt,
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
		`SELECT id, club_id, email, full_name, phone, address_line, postal_code, city, is_local, created_at, updated_at
		 FROM users WHERE id = $1 AND club_id = $2`,
		claims.UserID, claims.ClubID,
	).Scan(
		&current.ID, &current.ClubID, &current.Email, &current.FullName, &current.Phone,
		&current.Address, &current.PostalCode, &current.City, &current.IsLocal,
		&current.CreatedAt, &current.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch current profile for update")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if req.FullName != nil {
		current.FullName = *req.FullName
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

	// full_name became a generated column in DIL-228; we now write
	// first/last directly. Until /me starts shipping these fields
	// (DIL-229), the request still arrives as a single full_name and
	// we split on the last space to populate both columns.
	first, last := splitFullName(current.FullName)

	var p memberProfile
	err = h.db.QueryRow(ctx,
		`UPDATE users
		 SET first_name = $3, last_name = $4, phone = $5, address_line = $6, postal_code = $7, city = $8, updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, club_id, email, full_name, phone, address_line, postal_code, city, is_local, created_at, updated_at`,
		claims.UserID, claims.ClubID,
		first, last, current.Phone, current.Address, current.PostalCode, current.City,
	).Scan(
		&p.ID, &p.ClubID, &p.Email, &p.FullName, &p.Phone,
		&p.Address, &p.PostalCode, &p.City, &p.IsLocal,
		&p.CreatedAt, &p.UpdatedAt,
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
		`SELECT u.full_name, u.phone, u.email
		 FROM users u
		 JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		 WHERE u.club_id = $1
		 AND ur.role IN ('member', 'slip_holder', 'board', 'harbor_master', 'treasurer', 'admin')
		 GROUP BY u.id, u.full_name, u.phone, u.email
		 ORDER BY u.full_name
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
		if err := rows.Scan(&e.FullName, &phone, &email); err != nil {
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
