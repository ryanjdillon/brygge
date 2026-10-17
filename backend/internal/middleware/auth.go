package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/roles"
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

// RequireAdminViewer returns 403 unless the claims hold a role that may
// open the admin area at all (roles.CanViewAdmin). It suits endpoints
// every admin viewer needs whatever the method, such as the TOTP
// step-up that guards the rest of /admin.
func RequireAdminViewer() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if !roles.CanViewAdmin(claims.Roles) {
				writeError(w, http.StatusForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRoleOrBoardRead admits any admin viewer (roles.CanViewAdmin: a
// board role or admin) on safe methods — GET and HEAD — and otherwise
// behaves exactly like RequireRole(writeRoles...). It lets the board
// browse a page whose writes keep their existing, narrower role gate
// (BRY-372). Handlers mounted behind it on GET must not write.
func RequireRoleOrBoardRead(writeRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		write := RequireRole(writeRoles...)(next)
		read := RequireAdminViewer()(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				read.ServeHTTP(w, r)
				return
			}
			write.ServeHTTP(w, r)
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
