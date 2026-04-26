package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// testConfig returns a Config sufficient for handler unit tests. No real
// JWT or Vipps state — those code paths were removed in DIL-28.
func testConfig() *config.Config {
	return &config.Config{
		Port:        8080,
		DatabaseURL: "postgres://test:test@localhost:5432/test?sslmode=disable",
		RedisURL:    "redis://localhost:6379/0",
		ClubSlug:    "test-club",
	}
}

// generateTestToken builds an opaque token that the test-only auth
// middleware decodes back into auth.Claims. It is NOT a real JWT; it
// simply preserves the call-site API of the pre-DIL-28 helpers so we
// don't have to rewrite every authenticated test.
func generateTestToken(userID, clubID string, roles []string) string {
	claims := auth.Claims{UserID: userID, ClubID: clubID, Roles: roles}
	b, err := json.Marshal(claims)
	if err != nil {
		panic("marshalling test claims: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(b)
}

func authHeader(token string) string {
	return "Bearer " + token
}

// testAuthMiddleware decodes the opaque token from generateTestToken
// out of the Authorization header and injects the resulting claims
// into request context. Unauthenticated → 401.
func testAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok {
			http.Error(w, `{"error":"invalid authorization header"}`, http.StatusUnauthorized)
			return
		}
		raw, err := base64.URLEncoding.DecodeString(token)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		var claims auth.Claims
		if err := json.Unmarshal(raw, &claims); err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		ctx := middleware.WithClaims(r.Context(), &claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type routeConfig struct {
	method      string
	pattern     string
	handler     http.HandlerFunc
	middlewares []func(http.Handler) http.Handler
}

func setupTestRouter(routes ...routeConfig) *chi.Mux {
	r := chi.NewRouter()
	for _, route := range routes {
		subrouter := chi.NewRouter()
		for _, mw := range route.middlewares {
			subrouter.Use(mw)
		}
		subrouter.Method(route.method, "/", route.handler)
		r.Mount(route.pattern, subrouter)
	}
	return r
}

func setupAuthenticatedRouter(method, pattern string, handler http.HandlerFunc) *chi.Mux {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Method(method, pattern, handler)
	})
	return r
}

func setupRoleProtectedRouter(method, pattern string, handler http.HandlerFunc, roles ...string) *chi.Mux {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Use(middleware.RequireRole(roles...))
		r.Method(method, pattern, handler)
	})
	return r
}

// withTestClaims directly injects claims into a context (no header
// decoding). Useful for tests that build a request manually rather
// than going through a router.
func withTestClaims(ctx context.Context, userID, clubID string, roles []string) context.Context {
	return middleware.WithClaims(ctx, &auth.Claims{UserID: userID, ClubID: clubID, Roles: roles})
}
