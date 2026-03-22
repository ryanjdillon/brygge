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

// AuthenticateSession validates the session cookie and injects claims into context.
func AuthenticateSession(sessionService *auth.SessionService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil || cookie.Value == "" {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			claims, _, err := sessionService.ValidateSession(r.Context(), cookie.Value)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid or expired session")
				return
			}

			ctx := context.WithValue(r.Context(), contextKey{}, claims)
			ctx = context.WithValue(ctx, sessionContextKey{}, cookie.Value)
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

			claims, _, err := sessionService.ValidateSession(r.Context(), cookie.Value)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), contextKey{}, claims)
			ctx = context.WithValue(ctx, sessionContextKey{}, cookie.Value)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdminTOTP checks that the session has a recent TOTP verification.
// Returns 403 with totp_required error if verification is missing or expired.
func RequireAdminTOTP(sessionService *auth.SessionService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sessionID := GetSessionID(r.Context())
			if sessionID == "" {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			_, totpVerifiedAt, err := sessionService.ValidateSession(r.Context(), sessionID)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid session")
				return
			}

			if totpVerifiedAt == nil || time.Since(*totpVerifiedAt) > totpWindow {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"totp_required","verify_url":"/admin/verify-totp"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetSessionID returns the session ID from context, or empty string.
func GetSessionID(ctx context.Context) string {
	id, _ := ctx.Value(sessionContextKey{}).(string)
	return id
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
