package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/devtoken"
)

type devTokenKey struct{}

// RequireDevToken authenticates a request solely by a developer bearer token
// (BRY-115) and ignores any session cookie. On success it injects claims for
// the token's creator and club (no roles — the token grants only its scope,
// enforced by which routes mount this middleware) plus the token id, so the
// handler can audit which credential ran the query.
func RequireDevToken(svc *devtoken.Service, scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			plain, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || strings.TrimSpace(plain) == "" {
				writeError(w, http.StatusUnauthorized, "developer bearer token required")
				return
			}
			tok, err := svc.Authenticate(r.Context(), strings.TrimSpace(plain), scope)
			if errors.Is(err, devtoken.ErrInvalid) {
				writeError(w, http.StatusUnauthorized, "invalid or expired developer token")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "token verification failed")
				return
			}

			claims := &auth.Claims{ClubID: tok.ClubID}
			if tok.CreatedBy != nil {
				claims.UserID = *tok.CreatedBy
			}
			ctx := WithClaims(r.Context(), claims)
			ctx = context.WithValue(ctx, devTokenKey{}, tok.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetDevTokenID returns the id of the developer token that authenticated the
// request, or "" for any other auth path.
func GetDevTokenID(ctx context.Context) string {
	id, _ := ctx.Value(devTokenKey{}).(string)
	return id
}

// DevTokenKey is a rate-limiter key function that buckets by token id, so a
// leaked token is throttled independently of the caller's IP. Must run after
// RequireDevToken.
func DevTokenKey(r *http.Request) string {
	return "devtoken:" + GetDevTokenID(r.Context())
}
