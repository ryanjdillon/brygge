package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// DemoUser defines a test user available via demo auth.
type DemoUser struct {
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	Roles       []string `json:"roles"`
	Description string   `json:"description"`
}

// demoUsers matches the seed data from cmd/seed/main.go.
var demoUsers = []DemoUser{
	{Email: "admin@brygge.local", Name: "Admin Bruker", Roles: []string{"admin", "board", "member"}, Description: "Full admin access"},
	{Email: "slip-member@brygge.local", Name: "Kari Sjømann", Roles: []string{"member"}, Description: "Member with slip (harbor membership + slip A1)"},
	{Email: "wl-member@brygge.local", Name: "Per Venansen", Roles: []string{"member"}, Description: "Member on waiting list (#2)"},
	{Email: "member@brygge.local", Name: "Medlem Hansen", Roles: []string{"member"}, Description: "Regular member (waiting list #7)"},
}

type DemoAuthHandler struct {
	db       *pgxpool.Pool
	config   *config.Config
	sessions *auth.SessionService
	log      zerolog.Logger
}

func NewDemoAuthHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	sessions *auth.SessionService,
	log zerolog.Logger,
) *DemoAuthHandler {
	return &DemoAuthHandler{
		db:       db,
		config:   cfg,
		sessions: sessions,
		log:      log.With().Str("handler", "demo_auth").Logger(),
	}
}

// HandleListDemoUsers returns the available demo users.
func (h *DemoAuthHandler) HandleListDemoUsers(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, demoUsers)
}

type demoLoginRequest struct {
	Email string `json:"email"`
}

// HandleDemoLogin creates a session for a demo user without email/password verification.
func (h *DemoAuthHandler) HandleDemoLogin(w http.ResponseWriter, r *http.Request) {
	var req demoLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" {
		Error(w, http.StatusBadRequest, "email is required")
		return
	}

	// Verify the email is in the allowed demo user list
	allowed := false
	for _, u := range demoUsers {
		if u.Email == req.Email {
			allowed = true
			break
		}
	}
	if !allowed {
		Error(w, http.StatusForbidden, "email is not a demo user")
		return
	}

	// Look up user in database
	var userID, clubID string
	err := h.db.QueryRow(r.Context(),
		`SELECT u.id, u.club_id FROM users u
		 JOIN clubs c ON c.id = u.club_id
		 WHERE u.email = $1 AND c.slug = $2`,
		req.Email, h.config.ClubSlug,
	).Scan(&userID, &clubID)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "demo user not found — run seed first")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to look up demo user")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Create session
	sessionID, err := h.sessions.CreateSession(r.Context(), userID, clubID, r.RemoteAddr, r.UserAgent())
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create demo session")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	middleware.SetSessionCookie(w, sessionID, secure)

	h.log.Info().Str("email", req.Email).Str("user_id", userID).Msg("demo login")

	JSON(w, http.StatusOK, map[string]string{
		"user_id": userID,
		"email":   req.Email,
		"message": "demo session created",
	})
}
