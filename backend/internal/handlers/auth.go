package handlers

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type AuthHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewAuthHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *AuthHandler {
	return &AuthHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "auth").Logger(),
	}
}

func (h *AuthHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var firstName, lastName, fullName, email string
	var preferredLanguage *string
	clubDefaultLanguage := "nb"
	if h.db != nil {
		_ = h.db.QueryRow(r.Context(),
			`SELECT u.first_name, u.last_name, COALESCE(u.full_name, ''), u.email,
			        u.preferred_language, c.default_language
			   FROM users u JOIN clubs c ON c.id = u.club_id
			  WHERE u.id = $1`, claims.UserID,
		).Scan(&firstName, &lastName, &fullName, &email, &preferredLanguage, &clubDefaultLanguage)
	}

	resp := meResponse{
		UserID:              claims.UserID,
		ClubID:              claims.ClubID,
		Roles:               claims.Roles,
		FirstName:           firstName,
		LastName:            lastName,
		FullName:            fullName,
		Email:               email,
		PreferredLanguage:   preferredLanguage,
		ClubDefaultLanguage: clubDefaultLanguage,
	}
	if info := middleware.GetSessionInfo(r.Context()); info != nil {
		resp.TOTPEnabled = info.TOTPEnabled
		resp.TOTPVerifiedAt = info.TOTPVerifiedAt
	}

	JSON(w, http.StatusOK, resp)
}

type meResponse struct {
	UserID         string     `json:"user_id"`
	ClubID         string     `json:"club_id"`
	Roles          []string   `json:"roles"`
	FirstName      string     `json:"first_name"`
	LastName       string     `json:"last_name"`
	FullName       string     `json:"full_name"` // computed convenience; DIL-230 will drop
	Email          string     `json:"email"`
	TOTPEnabled    bool       `json:"totp_enabled"`
	TOTPVerifiedAt *time.Time `json:"totp_verified_at,omitempty"`
	// PreferredLanguage is the member's explicit UI language, or nil
	// when they've made no choice (→ fall back to the club default).
	PreferredLanguage   *string `json:"preferred_language"`
	ClubDefaultLanguage string  `json:"club_default_language"`
}
