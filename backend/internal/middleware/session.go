package middleware

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/auth"
)

const (
	sessionCookieName = "brygge_session"
	totpWindow        = 12 * time.Hour
)

// sessionContextKey stores the session ID in context for logout/TOTP stamping.
type sessionContextKey struct{}

// sessionInfoContextKey stores per-session TOTP state for handlers
// to read (e.g. /me).
type sessionInfoContextKey struct{}

// AuthenticateSession validates the session cookie and injects claims into context.
func AuthenticateSession(sessionService *auth.SessionService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil || cookie.Value == "" {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			claims, info, err := sessionService.ValidateSession(r.Context(), cookie.Value)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid or expired session")
				return
			}

			ctx := context.WithValue(r.Context(), contextKey{}, claims)
			ctx = context.WithValue(ctx, sessionContextKey{}, cookie.Value)
			ctx = context.WithValue(ctx, sessionInfoContextKey{}, info)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalSessionAuth extracts session claims if present but does not require authentication.
func OptionalSessionAuth(sessionService *auth.SessionService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil || cookie.Value == "" {
				next.ServeHTTP(w, r)
				return
			}

			claims, info, err := sessionService.ValidateSession(r.Context(), cookie.Value)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), contextKey{}, claims)
			ctx = context.WithValue(ctx, sessionContextKey{}, cookie.Value)
			ctx = context.WithValue(ctx, sessionInfoContextKey{}, info)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdminTOTP gates a route group on a recent TOTP verification.
// Returns 403 totp_required when the session has never verified, when
// the user has no TOTP enrolled, or when the last verify is older than
// the 12-hour step-up window. Must run after AuthenticateSession.
func RequireAdminTOTP(sessionService *auth.SessionService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := GetSessionInfo(r.Context())
			if info == nil {
				// No session info in context = AuthenticateSession didn't run.
				// Fall back to a fresh DB read so this middleware works
				// independently if anyone wires it that way.
				sessionID := GetSessionID(r.Context())
				if sessionID == "" {
					writeError(w, http.StatusUnauthorized, "authentication required")
					return
				}
				_, fresh, err := sessionService.ValidateSession(r.Context(), sessionID)
				if err != nil {
					writeError(w, http.StatusUnauthorized, "invalid session")
					return
				}
				info = fresh
			}

			if !info.TOTPEnabled {
				writeTOTPRequired(w, r, "totp_not_enrolled")
				return
			}
			if info.TOTPVerifiedAt == nil || time.Since(*info.TOTPVerifiedAt) > totpWindow {
				writeTOTPRequired(w, r, "totp_required")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// freshTOTPWindow is the package-level default that RequireFreshTOTP
// (no-arg form) falls back to. It's set once at server startup via
// SetFreshTOTPWindow so route registration doesn't need to pass the
// duration at every call site. See DIL-344.
var freshTOTPWindow = 10 * time.Minute

// SetFreshTOTPWindow installs the configured default. Call once during
// server startup; safe to read concurrently afterward because no other
// code path mutates the value.
func SetFreshTOTPWindow(window time.Duration) {
	if window > 0 {
		freshTOTPWindow = window
	}
}

// FreshTOTPWindow returns the configured default — useful for surfacing
// the value to the SPA on /session/me so the countdown stays in sync.
func FreshTOTPWindow() time.Duration { return freshTOTPWindow }

// RequireFreshTOTPDefault gates a route on the package default window,
// avoiding the duplicated `10 * time.Minute` literal at every call
// site. Prefer this for production routes; use RequireFreshTOTP only
// when a specific route genuinely needs a different window.
func RequireFreshTOTPDefault() func(http.Handler) http.Handler {
	return RequireFreshTOTP(freshTOTPWindow)
}

// RequireFreshTOTP gates a route on a TOTP verification within a short
// (per-action) window — typically 5–15 minutes for high-blast-radius
// operations like role grants, account deletion, or bank-account
// changes. Returns `403 totp_fresh_required` distinct from
// RequireAdminTOTP's `totp_required`, so the SPA can render an
// in-context modal instead of a full-page redirect. Must run after
// AuthenticateSession.
func RequireFreshTOTP(window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsFreshTOTP(r.Context(), window) {
				writeTOTPFreshRequired(w, r, window)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IsFreshTOTP reports whether the current session has TOTP verified
// within the supplied window. Useful for handlers that need to gate
// only on certain field changes (where the middleware can't inspect
// the request body).
func IsFreshTOTP(ctx context.Context, window time.Duration) bool {
	info := GetSessionInfo(ctx)
	if info == nil || !info.TOTPEnabled || info.TOTPVerifiedAt == nil {
		return false
	}
	return time.Since(*info.TOTPVerifiedAt) <= window
}

// isBrowserNavigation reports whether the request is a top-level
// browser navigation (a new tab / address bar / anchor click) rather
// than an XHR/fetch from the SPA. Such requests aren't intercepted by
// the SPA's HTTP client, so a JSON 403 would just render as raw text
// in the tab (e.g. opening an inline faktura PDF on a lapsed step-up).
func isBrowserNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html") && !strings.Contains(accept, "application/json")
}

// redirectToVerify sends a browser navigation to the TOTP verify page,
// preserving the original URL in ?next= so the user lands back on it
// after verifying.
func redirectToVerify(w http.ResponseWriter, r *http.Request) {
	dest := "/admin/verify-totp?next=" + url.QueryEscape(r.URL.RequestURI())
	http.Redirect(w, r, dest, http.StatusFound)
}

// writeTOTPRequired emits a 403 with a stable JSON shape the SPA reads
// to choose between "redirect to enrollment" and "prompt for code". For
// a top-level browser navigation (no SPA interceptor) it instead 302s
// to the verify page so the user doesn't see raw JSON.
func writeTOTPRequired(w http.ResponseWriter, r *http.Request, reason string) {
	if isBrowserNavigation(r) {
		redirectToVerify(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(`{"error":"` + reason + `","verify_url":"/admin/verify-totp"}`))
}

// writeTOTPFreshRequired emits a 403 the SPA decodes to mount the
// in-context per-action modal (instead of full-page redirecting). A
// top-level browser navigation gets the full-page verify redirect
// instead, for the same reason as writeTOTPRequired.
func writeTOTPFreshRequired(w http.ResponseWriter, r *http.Request, window time.Duration) {
	if isBrowserNavigation(r) {
		redirectToVerify(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	body := `{"error":"totp_fresh_required","window_seconds":` +
		strconv.Itoa(int(window.Seconds())) + `}`
	w.Write([]byte(body))
}

// GetSessionID returns the session ID from context, or empty string.
func GetSessionID(ctx context.Context) string {
	id, _ := ctx.Value(sessionContextKey{}).(string)
	return id
}

// GetSessionInfo returns the SessionInfo populated by AuthenticateSession,
// or nil for unauthenticated requests / contexts where it wasn't set.
func GetSessionInfo(ctx context.Context) *auth.SessionInfo {
	info, _ := ctx.Value(sessionInfoContextKey{}).(*auth.SessionInfo)
	return info
}

// SetSessionCookie sets the session cookie on the response.
func SetSessionCookie(w http.ResponseWriter, sessionID string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60, // matches auth.sessionAbsoluteCap (7d); server still enforces the real expiry
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}
