package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/roles"
)

const magicLinkExpiry = 15 * time.Minute

// magicLinkRetention is how long a magic-link row is kept past its expiry.
// The row holds the address the link was sent to and the token's hash, and a link
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
	audit    *audit.Service // optional, may be nil in tests
	log      zerolog.Logger

	// loginFailDedupe bounds anonymous auth.login_failed rows (BRY-659);
	// nil writes every one. Set with SetLoginFailDedupe.
	loginFailDedupe *redis.Client

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
	auditService *audit.Service,
	log zerolog.Logger,
) *MagicLinkHandler {
	return &MagicLinkHandler{
		db:       db,
		config:   cfg,
		email:    emailClient,
		sessions: sessions,
		audit:    auditService,
		log:      log.With().Str("handler", "magic_link").Logger(),
	}
}

type magicLinkRequest struct {
	Email string `json:"email"`
}

type magicLinkResponse struct {
	Message string `json:"message"`
}

// SetLoginFailDedupe gives the handler the Redis client that bounds anonymous
// auth.login_failed rows to one per client IP, method and reason a minute.
func (h *MagicLinkHandler) SetLoginFailDedupe(rdb *redis.Client) { h.loginFailDedupe = rdb }

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

// HandleVerifyMagicLink answers the GET login links mailed before BRY-658,
// which pointed here. It consumes nothing: it sends the browser on to the SPA
// page the links now go to, where the member logs in with a button (POST), so
// a mail scanner that prefetches an old link cannot use it up. Links live
// magicLinkExpiry, so this route can go one release after BRY-658 ships.
func (h *MagicLinkHandler) HandleVerifyMagicLink(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, loginVerifyURL(h.config, r.URL.Query().Get("token")), http.StatusFound)
}

type magicLinkVerifyRequest struct {
	Token string `json:"token"`
}

type magicLinkVerifyResponse struct {
	Status string `json:"status"`
}

// Error codes the login verify page matches on.
const (
	codeLoginTokenInvalid = "login_token_invalid"
	codeLoginTokenUsed    = "login_token_used"
	codeLoginTokenExpired = "login_token_expired"
)

// HandleConsumeMagicLink logs in with a magic-link token sent in a JSON body:
// it consumes the token, creates the session and sets its cookie. Only a POST
// consumes a login token, so opening the link (a GET) changes nothing.
//
// Login CSRF: requireJSON (exact media type application/json, else 415) keeps
// another site from logging a visitor in as someone else, but only together
// with the single-origin CORS allow-list in newRouter (cfg.FrontendURL). A
// cross-site HTML form cannot send application/json, and a cross-origin fetch
// that does needs a preflight, which that allow-list fails. Widening
// AllowedOrigins breaks this guard. POST /join/confirm relies on the same pair.
//
// A session the request already carries is deleted once the new one exists,
// so logging in as someone else does not leave the previous login live.
//
// Audit (BRY-659): a login writes auth.login_success; a token that is
// refused writes auth.login_failed with the reason, naming the user the
// link was for when its row is still there. A missing token, a wrong
// content type or a malformed body presented no credential and writes none.
func (h *MagicLinkHandler) HandleConsumeMagicLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !requireJSON(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req magicLinkVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		loginTokenRefused(w, codeLoginTokenInvalid)
		return
	}

	// Atomically consume the token. The lookup is by hash, so it needs no
	// constant-time compare.
	var linkEmail, clubID string
	err := h.db.QueryRow(ctx,
		`UPDATE magic_links SET used = true
		 WHERE token_hash = $1 AND used = false AND expires_at > NOW()
		 RETURNING email, club_id`,
		magicLinkTokenHash(token),
	).Scan(&linkEmail, &clubID)
	if errors.Is(err, pgx.ErrNoRows) {
		code, rowEmail, rowClubID := h.refusal(ctx, token)
		h.auditLoginFailed(ctx, r, rowEmail, rowClubID, code)
		loginTokenRefused(w, code)
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to consume magic link")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var userID string
	err = h.db.QueryRow(ctx,
		`SELECT id FROM users WHERE email = $1 AND club_id = $2
		   AND kind = 'person' AND deactivated_at IS NULL`,
		linkEmail, clubID,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The user was deactivated (or never was a person) after the link
		// was sent: the link is spent and logs no one in.
		h.auditLoginFailed(ctx, r, linkEmail, clubID, codeLoginTokenInvalid)
		loginTokenRefused(w, codeLoginTokenInvalid)
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to look up user after magic link verification")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	sessionID, err := h.sessions.CreateSession(ctx, userID, clubID, r.RemoteAddr, r.UserAgent())
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create session")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	endPriorSession(ctx, r, h.sessions, sessionID, h.log)
	middleware.SetSessionCookie(w, sessionID, cookieSecure(h.config))
	auditLoginSuccess(ctx, h.audit, r, userID, clubID, loginMethodMagicLink)

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

	JSON(w, http.StatusOK, magicLinkVerifyResponse{Status: "logged_in"})
}

// refusal says why a token could not be consumed, so the page can tell the
// member whether to ask for a new link because theirs expired or was already
// used. Only the holder of the token can learn this. It also returns the
// address and club of the link's row, for the audit row; both are "" for a
// token with no row.
func (h *MagicLinkHandler) refusal(ctx context.Context, token string) (code, linkEmail, clubID string) {
	var used, expired bool
	err := h.db.QueryRow(ctx,
		`SELECT used, expires_at <= NOW(), email, club_id::text FROM magic_links WHERE token_hash = $1`,
		magicLinkTokenHash(token),
	).Scan(&used, &expired, &linkEmail, &clubID)
	switch {
	case err != nil:
		return codeLoginTokenInvalid, "", ""
	case used:
		return codeLoginTokenUsed, linkEmail, clubID
	case expired:
		return codeLoginTokenExpired, linkEmail, clubID
	default:
		return codeLoginTokenInvalid, linkEmail, clubID
	}
}

// auditLoginFailed writes auth.login_failed for a refused token. The link's
// row names an address, not a user, so the user is looked up by it, active
// or not: a deactivated member's refused link is still theirs. The address
// itself never goes in the row. With no link row (an unknown token) there is
// no one to name.
func (h *MagicLinkHandler) auditLoginFailed(ctx context.Context, r *http.Request, linkEmail, clubID, code string) {
	if h.audit == nil {
		return
	}
	var userID string
	if linkEmail != "" && clubID != "" {
		err := h.db.QueryRow(ctx,
			`SELECT id FROM users WHERE email = $1 AND club_id = $2 AND kind = 'person'`,
			linkEmail, clubID,
		).Scan(&userID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			h.log.Warn().Err(err).Msg("login audit: resolve the link's user")
		}
	}
	auditLoginFailed(ctx, h.audit, h.loginFailDedupe, r, userID, clubID, h.config.ClubSlug, loginMethodMagicLink, loginFailReason(code))
}

func loginTokenRefused(w http.ResponseWriter, code string) {
	JSON(w, http.StatusGone, map[string]string{"error": "invalid or expired login link", "code": code})
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
// returns its URL, the SPA page /login/verify (loginVerifyURL). email must be
// the address as stored on the user, which verify matches exactly. Only the
// token's hash is stored; the URL carries the token.
func issueMagicLink(ctx context.Context, db *pgxpool.Pool, cfg *config.Config, clubID, email string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generate magic link token: %w", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO magic_links (token_hash, email, club_id, expires_at) VALUES ($1, $2, $3, $4)`,
		magicLinkTokenHash(token), email, clubID, time.Now().Add(magicLinkExpiry),
	); err != nil {
		return "", fmt.Errorf("store magic link: %w", err)
	}
	return loginVerifyURL(cfg, token), nil
}

// loginVerifyURL is the SPA page a login link opens. Opening it changes
// nothing: the page logs in with a POST, so a mail scanner that prefetches
// links cannot use the token up. An empty token gives the bare page, which
// says the link is invalid.
func loginVerifyURL(cfg *config.Config, token string) string {
	if token == "" {
		return publicSiteBase(cfg) + "/login/verify"
	}
	return publicSiteBase(cfg) + "/login/verify?token=" + url.QueryEscape(token)
}

// magicLinkTokenHash is what magic_links.token_hash stores: sha256 hex, the
// encoding join tokens use, so a read of the table cannot log anyone in.
func magicLinkTokenHash(token string) string {
	return sha256Hex(token)
}

// HandleSessionLogout destroys the current session and clears the cookie,
// and writes auth.logout for the member (BRY-659). The session id stays out
// of the audit row: it is the cookie's value.
func (h *MagicLinkHandler) HandleSessionLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := middleware.GetSessionID(ctx)
	if sessionID != "" && h.sessions != nil {
		if err := h.sessions.DeleteSession(ctx, sessionID); err != nil {
			h.log.Error().Err(err).Msg("failed to delete session on logout")
		}
	}
	middleware.ClearSessionCookie(w, cookieSecure(h.config))
	if claims := middleware.GetClaims(ctx); claims != nil && h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr, audit.ActionLogout, "session", "", nil)
	}
	JSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// endPriorSession deletes the session the request's cookie names, if any and
// if it is not newID, so a login replaces the browser's previous session
// rather than leaving it valid server-side. Best effort: a failure is logged
// and the login goes ahead, since the new cookie replaces the old one anyway.
func endPriorSession(ctx context.Context, r *http.Request, sessions *auth.SessionService, newID string, log zerolog.Logger) {
	old := middleware.SessionCookieValue(r)
	if old == "" || old == newID || sessions == nil {
		return
	}
	if err := sessions.DeleteSession(ctx, old); err != nil {
		log.Warn().Err(err).Msg("failed to delete the previous session on login")
	}
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
