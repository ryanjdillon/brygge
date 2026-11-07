package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/roles"
)

const magicLinkExpiry = 15 * time.Minute

// magicLinkRetention is how long a magic-link row is kept past its expiry.
// The row holds the address the link was sent to and the token, and a link
// can only be consumed before it expires, so this also bounds how long a
// consumed one is kept.
const magicLinkRetention = 24 * time.Hour

// MailPrincipalEnsurer creates a user's Stalwart principal if it is missing.
// *mail.UserProvisioner is the implementation.
type MailPrincipalEnsurer interface {
	EnsureUserPrincipal(ctx context.Context, userID, email string) (string, error)
}

type MagicLinkHandler struct {
	db       *pgxpool.Pool
	config   *config.Config
	email    email.Sender
	sessions *auth.SessionService
	log      zerolog.Logger

	// MailProvisioner, when set, lazily ensures a Stalwart principal
	// exists for the user on first successful login (DIL-321).
	// Detached goroutine — failures are logged but don't block login.
	// Applicant-only users are skipped: they are not members yet.
	MailProvisioner MailPrincipalEnsurer
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
	var userID, clubID string
	err := h.db.QueryRow(ctx,
		`SELECT id, club_id FROM users WHERE email = $1 AND club_id = (SELECT id FROM clubs WHERE slug = $2)
		   AND kind = 'person' AND deactivated_at IS NULL`,
		req.Email, h.config.ClubSlug,
	).Scan(&userID, &clubID)

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

	loginURL, err := issueMagicLink(ctx, h.db, h.config, clubID, req.Email)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to store magic link")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Send email
	if h.email != nil {
		locale := email.DetectLocale(r)
		subject := email.MagicLinkSubject(locale, h.config.ClubName, time.Now())
		htmlBody := email.MagicLinkBody(locale, h.config.ClubName, h.config.Domain, loginURL)
		// The club is CLUB_SLUG's, which the send log resolves itself.
		mailCtx := email.WithSendMeta(ctx, email.SendMeta{Kind: email.KindMagicLink, UserID: userID})
		if err := h.email.Send(mailCtx, req.Email, subject, htmlBody); err != nil {
			h.log.Error().Err(err).Str("email", req.Email).Msg("failed to send magic link email")
			// Don't fail the request — the link is stored, user can retry
		}
	} else {
		h.log.Warn().Str("email", req.Email).Msg("magic link created but email delivery disabled (no SMTP_HOST)")
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
		`SELECT id FROM users WHERE email = $1 AND club_id = $2
		   AND kind = 'person' AND deactivated_at IS NULL`,
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

	middleware.SetSessionCookie(w, sessionID, cookieSecure(h.config))

	// Lazy Stalwart provisioning (DIL-321). Detached so login
	// latency isn't affected; reconciler picks up the new principal
	// at most 5 min later if it was needed before this goroutine
	// finished. An applicant has not paid and gets no club mailbox; one
	// whose roles cannot be read is skipped too, and is provisioned on a
	// later login.
	if h.MailProvisioner != nil && h.mayProvisionMail(ctx, userID, clubID) {
		// WithoutCancel keeps the request's trace/log values while
		// outliving the response that ends the request context.
		detached := context.WithoutCancel(r.Context())
		go func(uid, email string) {
			bg, cancel := context.WithTimeout(detached, 30*time.Second)
			defer cancel()
			if _, err := h.MailProvisioner.EnsureUserPrincipal(bg, uid, email); err != nil {
				h.log.Warn().Err(err).Str("user_id", uid).Msg("stalwart provisioning failed")
			}
		}(userID, linkEmail)
	}

	// Redirect to frontend
	http.Redirect(w, r, h.config.FrontendURL+"/portal", http.StatusFound)
}

// mayProvisionMail reports whether a login may create the user's Stalwart
// principal: not for an applicant-only user, and not when the roles cannot be
// read.
func (h *MagicLinkHandler) mayProvisionMail(ctx context.Context, userID, clubID string) bool {
	var userRoles []string
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(array_agg(role::text), '{}') FROM user_roles WHERE user_id = $1 AND club_id = $2`,
		userID, clubID,
	).Scan(&userRoles); err != nil {
		h.log.Warn().Err(err).Str("user_id", userID).Msg("stalwart provisioning skipped: roles unreadable")
		return false
	}
	return !roles.IsApplicantOnly(userRoles)
}

// issueMagicLink stores a single-use login link for email in clubID and
// returns its URL. The URL is the backend verify endpoint: it consumes the
// token, sets the session cookie and redirects to /portal in one request.
// email must be the address as stored on the user, which verify matches
// exactly.
func issueMagicLink(ctx context.Context, db *pgxpool.Pool, cfg *config.Config, clubID, email string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generate magic link token: %w", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO magic_links (token, email, club_id, expires_at) VALUES ($1, $2, $3, $4)`,
		token, email, clubID, time.Now().Add(magicLinkExpiry),
	); err != nil {
		return "", fmt.Errorf("store magic link: %w", err)
	}
	return fmt.Sprintf("%s/api/v1/auth/verify?token=%s", cfg.FrontendURL, token), nil
}

// HandleSessionLogout destroys the current session and clears the cookie.
func (h *MagicLinkHandler) HandleSessionLogout(w http.ResponseWriter, r *http.Request) {
	sessionID := middleware.GetSessionID(r.Context())
	if sessionID != "" && h.sessions != nil {
		if err := h.sessions.DeleteSession(r.Context(), sessionID); err != nil {
			h.log.Error().Err(err).Msg("failed to delete session on logout")
		}
	}
	middleware.ClearSessionCookie(w, cookieSecure(h.config))
	JSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// cookieSecure decides the session cookie's Secure flag from the configured
// public origin rather than the request: X-Forwarded-Proto is client-settable
// unless a proxy overwrites it, and r.TLS is nil behind a TLS-terminating
// proxy. Plain http is only expected for local development.
func cookieSecure(cfg *config.Config) bool {
	return cfg != nil && strings.HasPrefix(cfg.FrontendURL, "https://")
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

// PurgeExpired deletes magic links that expired more than magicLinkRetention
// ago, used or not. Run by the hourly sweep.
func (h *MagicLinkHandler) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := h.db.Exec(ctx,
		`DELETE FROM magic_links WHERE expires_at < now() - $1::interval`, magicLinkRetention)
	if err != nil {
		return 0, fmt.Errorf("purge expired magic links: %w", err)
	}
	return tag.RowsAffected(), nil
}
