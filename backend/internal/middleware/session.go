package middleware

import (
	"context"
	"net/http"
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
				writeTOTPRequired(w, "totp_not_enrolled")
				return
			}
			if info.TOTPVerifiedAt == nil || time.Since(*info.TOTPVerifiedAt) > totpWindow {
				writeTOTPRequired(w, "totp_required")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeTOTPRequired emits a 403 with a stable JSON shape the SPA reads
// to choose between "redirect to enrollment" and "prompt for code".
func writeTOTPRequired(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(`{"error":"` + reason + `","verify_url":"/admin/verify-totp"}`))
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
		MaxAge:   30 * 24 * 60 * 60, // 30 days
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
