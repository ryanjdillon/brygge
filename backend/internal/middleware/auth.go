package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/brygge-klubb/brygge/internal/auth"
)

// contextKey is the unexported key under which auth.Claims live in
// request context. Both this package's session middleware and any
// future test-injected claims share this key.
type contextKey struct{}

// RequireRole returns 403 unless the request's authenticated claims
// include at least one of the named roles. Must run after a middleware
// that has already populated claims (e.g. AuthenticateSession).
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			for _, required := range roles {
				for _, have := range claims.Roles {
					if have == required {
						next.ServeHTTP(w, r)
						return
					}
				}
			}

			writeError(w, http.StatusForbidden, "insufficient permissions")
		})
	}
}

// GetClaims returns the authenticated principal's claims, or nil if
// the request is unauthenticated.
func GetClaims(ctx context.Context) *auth.Claims {
	claims, _ := ctx.Value(contextKey{}).(*auth.Claims)
	return claims
}

// WithClaims returns a copy of ctx that carries the supplied claims.
// Used by session middleware (and tests) to populate the request
// principal before downstream handlers read it.
func WithClaims(ctx context.Context, claims *auth.Claims) context.Context {
	return context.WithValue(ctx, contextKey{}, claims)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
