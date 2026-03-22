package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

const magicLinkExpiry = 15 * time.Minute

type MagicLinkHandler struct {
	db       *pgxpool.Pool
	config   *config.Config
	email    email.Sender
	sessions *auth.SessionService
	log      zerolog.Logger
}

func NewMagicLinkHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	emailClient email.Sender,
	sessions *auth.SessionService,
	log zerolog.Logger,
) *MagicLinkHandler {
	return &MagicLinkHandler{
		db:       db,
		config:   cfg,
		email:    emailClient,
		sessions: sessions,
		log:      log.With().Str("handler", "magic_link").Logger(),
	}
}

type magicLinkRequest struct {
	Email string `json:"email"`
}

type magicLinkResponse struct {
	Message string `json:"message"`
}

// HandleRequestMagicLink sends a login link to the user's email.
// Always returns 200 regardless of whether the email exists.
func (h *MagicLinkHandler) HandleRequestMagicLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req magicLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" {
		Error(w, http.StatusBadRequest, "email is required")
		return
	}

	// Always return same response to avoid email enumeration
	resp := magicLinkResponse{Message: "If this email is registered, a login link has been sent"}

	// Check if user exists
	var userID string
	err := h.db.QueryRow(ctx,
		`SELECT id FROM users WHERE email = $1 AND club_id = (SELECT id FROM clubs WHERE slug = $2)`,
		req.Email, h.config.ClubSlug,
	).Scan(&userID)

	if err == pgx.ErrNoRows {
		// User doesn't exist — return 200 without sending email
		JSON(w, http.StatusOK, resp)
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to look up user for magic link")
		// Still return 200 to avoid leaking info
		JSON(w, http.StatusOK, resp)
		return
	}

	// Generate token
	token, err := generateToken()
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate magic link token")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Store magic link
	expiresAt := time.Now().Add(magicLinkExpiry)
	_, err = h.db.Exec(ctx,
		`INSERT INTO magic_links (token, email, club_id, expires_at)
		 VALUES ($1, $2, (SELECT id FROM clubs WHERE slug = $3), $4)`,
		token, req.Email, h.config.ClubSlug, expiresAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to store magic link")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Send email
	if h.email != nil {
		loginURL := fmt.Sprintf("%s/auth/verify?token=%s", h.config.FrontendURL, token)
		htmlBody := fmt.Sprintf(
			`<p>Klikk lenken under for å logge inn:</p><p><a href="%s">Logg inn</a></p><p>Lenken er gyldig i 15 minutter.</p>`,
			loginURL,
		)
		if err := h.email.Send(ctx, req.Email, "Logg inn", htmlBody); err != nil {
			h.log.Error().Err(err).Str("email", req.Email).Msg("failed to send magic link email")
			// Don't fail the request — the link is stored, user can retry
		}
	} else {
		h.log.Warn().Str("email", req.Email).Msg("magic link created but email delivery disabled (no RESEND_API_KEY)")
	}

	JSON(w, http.StatusOK, resp)
}

// HandleVerifyMagicLink validates a magic link token and returns user info.
// In the session-based flow (DIL-26), this will create a session and set a cookie.
// For now, it returns the user info as JSON (to be wired with session creation).
func (h *MagicLinkHandler) HandleVerifyMagicLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	token := r.URL.Query().Get("token")
	if token == "" {
		Error(w, http.StatusBadRequest, "token is required")
		return
	}

	// Atomically consume the token
	var linkEmail, clubID string
	err := h.db.QueryRow(ctx,
		`UPDATE magic_links SET used = true
		 WHERE token = $1 AND used = false AND expires_at > NOW()
		 RETURNING email, club_id`,
		token,
	).Scan(&linkEmail, &clubID)

	if err == pgx.ErrNoRows {
		Error(w, http.StatusBadRequest, "invalid or expired login link")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to consume magic link")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Look up user
	var userID string
	err = h.db.QueryRow(ctx,
		`SELECT id FROM users WHERE email = $1 AND club_id = $2`,
		linkEmail, clubID,
	).Scan(&userID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to look up user after magic link verification")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Create session
	ip := r.RemoteAddr
	ua := r.UserAgent()
	sessionID, err := h.sessions.CreateSession(ctx, userID, clubID, ip, ua)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create session")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	middleware.SetSessionCookie(w, sessionID, secure)

	// Redirect to frontend
	http.Redirect(w, r, h.config.FrontendURL+"/portal", http.StatusFound)
}

// HandleSessionLogout destroys the current session and clears the cookie.
func (h *MagicLinkHandler) HandleSessionLogout(w http.ResponseWriter, r *http.Request) {
	sessionID := middleware.GetSessionID(r.Context())
	if sessionID != "" && h.sessions != nil {
		if err := h.sessions.DeleteSession(r.Context(), sessionID); err != nil {
			h.log.Error().Err(err).Msg("failed to delete session on logout")
		}
	}
	middleware.ClearSessionCookie(w)
	JSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}
