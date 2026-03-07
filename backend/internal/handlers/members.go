package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
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

	var p memberProfile
	err = h.db.QueryRow(ctx,
		`UPDATE users
		 SET full_name = $3, phone = $4, address_line = $5, postal_code = $6, city = $7, updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, club_id, email, full_name, phone, address_line, postal_code, city, is_local, created_at, updated_at`,
		claims.UserID, claims.ClubID,
		current.FullName, current.Phone, current.Address, current.PostalCode, current.City,
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
		`SELECT id, user_id, club_id, name, type, manufacturer, model,
		        length_m, beam_m, draft_m, weight_kg, registration_number,
		        boat_model_id, measurements_confirmed, confirmed_by, confirmed_at,
		        created_at, updated_at
		 FROM boats WHERE user_id = $1 AND club_id = $2
		 ORDER BY name`,
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
		if err := rows.Scan(
			&b.ID, &b.UserID, &b.ClubID, &b.Name, &b.Type, &b.Manufacturer, &b.Model,
			&b.LengthM, &b.BeamM, &b.DraftM, &b.WeightKg, &b.RegistrationNumber,
			&b.BoatModelID, &b.MeasurementsConfirmed, &b.ConfirmedBy, &b.ConfirmedAt,
			&b.CreatedAt, &b.UpdatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan boat")
			Error(w, http.StatusInternalServerError, "internal error")
			return
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

	// Determine if measurements can be auto-confirmed from a trusted model
	confirmed := false
	if req.BoatModelID != nil {
		var mLength, mBeam, mDraft *float64
		err := h.db.QueryRow(ctx,
			`SELECT length_m, beam_m, draft_m FROM boat_models WHERE id = $1`,
			*req.BoatModelID,
		).Scan(&mLength, &mBeam, &mDraft)
		if err == nil {
			confirmed = dimsMatch(req.LengthM, mLength) &&
				dimsMatch(req.BeamM, mBeam) &&
				dimsMatch(req.DraftM, mDraft)
		}
	}

	var b boat
	err := h.db.QueryRow(ctx,
		`INSERT INTO boats (user_id, club_id, name, type, manufacturer, model,
		                    length_m, beam_m, draft_m, weight_kg, registration_number,
		                    boat_model_id, measurements_confirmed)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id, user_id, club_id, name, type, manufacturer, model,
		           length_m, beam_m, draft_m, weight_kg, registration_number,
		           boat_model_id, measurements_confirmed, confirmed_by, confirmed_at,
		           created_at, updated_at`,
		claims.UserID, claims.ClubID, req.Name, req.Type, req.Manufacturer, req.Model,
		req.LengthM, req.BeamM, req.DraftM, req.WeightKg, req.RegistrationNumber,
		req.BoatModelID, confirmed,
	).Scan(
		&b.ID, &b.UserID, &b.ClubID, &b.Name, &b.Type, &b.Manufacturer, &b.Model,
		&b.LengthM, &b.BeamM, &b.DraftM, &b.WeightKg, &b.RegistrationNumber,
		&b.BoatModelID, &b.MeasurementsConfirmed, &b.ConfirmedBy, &b.ConfirmedAt,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create boat")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, b)
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

	var current boat
	err := h.db.QueryRow(ctx,
		`SELECT id, user_id, club_id, name, type, manufacturer, model,
		        length_m, beam_m, draft_m, weight_kg, registration_number,
		        boat_model_id, measurements_confirmed, confirmed_by, confirmed_at,
		        created_at, updated_at
		 FROM boats WHERE id = $1 AND user_id = $2 AND club_id = $3`,
		boatID, claims.UserID, claims.ClubID,
	).Scan(
		&current.ID, &current.UserID, &current.ClubID, &current.Name, &current.Type,
		&current.Manufacturer, &current.Model,
		&current.LengthM, &current.BeamM, &current.DraftM, &current.WeightKg,
		&current.RegistrationNumber,
		&current.BoatModelID, &current.MeasurementsConfirmed, &current.ConfirmedBy, &current.ConfirmedAt,
		&current.CreatedAt, &current.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "boat not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch boat for update")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var req updateBoatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Track whether dimensions are changing
	oldLength, oldBeam, oldDraft := current.LengthM, current.BeamM, current.DraftM

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

	// If dimensions changed, reset confirmation
	dimsChanged := !dimsMatch(current.LengthM, oldLength) ||
		!dimsMatch(current.BeamM, oldBeam) ||
		!dimsMatch(current.DraftM, oldDraft)

	confirmed := current.MeasurementsConfirmed
	var confirmedBy *string
	var confirmedAt *time.Time

	if dimsChanged {
		// Check if new dims match the linked model (auto-re-confirm)
		reconfirmed := false
		if current.BoatModelID != nil {
			var mLength, mBeam, mDraft *float64
			mErr := h.db.QueryRow(ctx,
				`SELECT length_m, beam_m, draft_m FROM boat_models WHERE id = $1`,
				*current.BoatModelID,
			).Scan(&mLength, &mBeam, &mDraft)
			if mErr == nil {
				reconfirmed = dimsMatch(current.LengthM, mLength) &&
					dimsMatch(current.BeamM, mBeam) &&
					dimsMatch(current.DraftM, mDraft)
			}
		}
		if reconfirmed {
			confirmed = true
		} else {
			confirmed = false
			confirmedBy = nil
			confirmedAt = nil
		}
	} else {
		confirmedBy = current.ConfirmedBy
		confirmedAt = current.ConfirmedAt
	}

	var b boat
	err = h.db.QueryRow(ctx,
		`UPDATE boats
		 SET name = $4, type = $5, manufacturer = $6, model = $7,
		     length_m = $8, beam_m = $9, draft_m = $10, weight_kg = $11,
		     registration_number = $12, boat_model_id = $13,
		     measurements_confirmed = $14, confirmed_by = $15, confirmed_at = $16,
		     updated_at = now()
		 WHERE id = $1 AND user_id = $2 AND club_id = $3
		 RETURNING id, user_id, club_id, name, type, manufacturer, model,
		           length_m, beam_m, draft_m, weight_kg, registration_number,
		           boat_model_id, measurements_confirmed, confirmed_by, confirmed_at,
		           created_at, updated_at`,
		boatID, claims.UserID, claims.ClubID,
		current.Name, current.Type, current.Manufacturer, current.Model,
		current.LengthM, current.BeamM, current.DraftM, current.WeightKg,
		current.RegistrationNumber, current.BoatModelID,
		confirmed, confirmedBy, confirmedAt,
	).Scan(
		&b.ID, &b.UserID, &b.ClubID, &b.Name, &b.Type, &b.Manufacturer, &b.Model,
		&b.LengthM, &b.BeamM, &b.DraftM, &b.WeightKg, &b.RegistrationNumber,
		&b.BoatModelID, &b.MeasurementsConfirmed, &b.ConfirmedBy, &b.ConfirmedAt,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update boat")
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
		`SELECT id FROM projects WHERE club_id = $1 AND name = 'harbour-maintenance'`,
		claims.ClubID,
	).Scan(&projectID)
	if err == pgx.ErrNoRows {
		err = h.db.QueryRow(ctx,
			`INSERT INTO projects (club_id, name, description, created_by)
			 VALUES ($1, 'harbour-maintenance', 'Harbour maintenance issues reported by members', $2)
			 RETURNING id`,
			claims.ClubID, claims.UserID,
		).Scan(&projectID)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to create harbour-maintenance project")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else if err != nil {
		h.log.Error().Err(err).Msg("failed to find harbour-maintenance project")
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

	rows, err := h.db.Query(ctx,
		`SELECT u.full_name, u.phone, u.email
		 FROM users u
		 JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		 WHERE u.club_id = $1
		 AND ur.role IN ('member', 'slip_owner', 'styre', 'harbour_master', 'treasurer', 'admin')
		 GROUP BY u.id, u.full_name, u.phone, u.email
		 ORDER BY u.full_name`,
		claims.ClubID,
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

	JSON(w, http.StatusOK, entries)
}

type dashboardResponse struct {
	MembershipStatus     string       `json:"membershipStatus"`
	QueuePosition        *int         `json:"queuePosition"`
	QueueTotal           *int         `json:"queueTotal"`
	Slip                 *dashSlip    `json:"slip"`
	UpcomingBookingCount int          `json:"upcomingBookingsCount"`
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

	// Determine highest role as membership status
	roles, _ := h.getRoles(ctx, claims.UserID, claims.ClubID)
	resp.MembershipStatus = bestRole(roles)

	// Queue position
	var pos, total int
	err := h.db.QueryRow(ctx,
		`SELECT position, (SELECT count(*) FROM waiting_list_entries WHERE club_id = $2 AND status = 'active')
		 FROM waiting_list_entries WHERE user_id = $1 AND club_id = $2 AND status = 'active'`,
		claims.UserID, claims.ClubID,
	).Scan(&pos, &total)
	if err == nil {
		resp.QueuePosition = &pos
		resp.QueueTotal = &total
	}

	// Slip info
	var slipNum, slipSection string
	err = h.db.QueryRow(ctx,
		`SELECT s.number, s.section FROM slips s
		 JOIN slip_assignments sa ON sa.slip_id = s.id
		 WHERE sa.user_id = $1 AND s.club_id = $2 AND sa.released_at IS NULL`,
		claims.UserID, claims.ClubID,
	).Scan(&slipNum, &slipSection)
	if err == nil {
		resp.Slip = &dashSlip{Number: slipNum, Location: slipSection}
	}

	// Upcoming bookings count
	var count int
	_ = h.db.QueryRow(ctx,
		`SELECT count(*) FROM bookings
		 WHERE user_id = $1 AND club_id = $2 AND status != 'cancelled' AND end_date >= now()`,
		claims.UserID, claims.ClubID,
	).Scan(&count)
	resp.UpcomingBookingCount = count

	JSON(w, http.StatusOK, resp)
}

func (h *MembersHandler) getRoles(ctx context.Context, userID, clubID string) ([]string, error) {
	rows, err := h.db.Query(ctx,
		`SELECT role FROM user_roles WHERE user_id = $1 AND club_id = $2`,
		userID, clubID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
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
	order := []string{"admin", "styre", "slip_owner", "member", "applicant"}
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
