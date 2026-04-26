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

	var fullName, email string
	if h.db != nil {
		_ = h.db.QueryRow(r.Context(),
			`SELECT full_name, email FROM users WHERE id = $1`, claims.UserID,
		).Scan(&fullName, &email)
	}

	resp := meResponse{
		UserID:   claims.UserID,
		ClubID:   claims.ClubID,
		Roles:    claims.Roles,
		FullName: fullName,
		Email:    email,
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
	FullName       string     `json:"full_name"`
	Email          string     `json:"email"`
	TOTPEnabled    bool       `json:"totp_enabled"`
	TOTPVerifiedAt *time.Time `json:"totp_verified_at,omitempty"`
}
