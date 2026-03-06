package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

const testJWTSecret = "test-secret-key-for-integration-tests"

func testConfig() *config.Config {
	return &config.Config{
		Port:              8080,
		DatabaseURL:       "postgres://test:test@localhost:5432/test?sslmode=disable",
		RedisURL:          "redis://localhost:6379/0",
		ClubSlug:          "test-club",
		JWTSecret:         testJWTSecret,
		JWTAccessExpiry:   15 * time.Minute,
		JWTRefreshExpiry:  7 * 24 * time.Hour,
		VippsClientID:     "test-vipps-client-id",
		VippsClientSecret: "test-vipps-secret",
		VippsCallbackURL:  "http://localhost:8080/api/v1/auth/vipps/callback",
		VippsTestMode:     true,
	}
}

func testJWTService() *auth.JWTService {
	cfg := testConfig()
	return auth.NewJWTService(cfg)
}

func testVippsClient() *auth.VippsClient {
	cfg := testConfig()
	return auth.NewVippsClient(cfg)
}

func generateTestToken(userID, clubID string, roles []string) string {
	svc := testJWTService()
	token, err := svc.GenerateAccessToken(userID, clubID, roles)
	if err != nil {
		panic("failed to generate test token: " + err.Error())
	}
	return token
}

func authHeader(token string) string {
	return "Bearer " + token
}

type routeConfig struct {
	method  string
	pattern string
	handler http.HandlerFunc
	middlewares []func(http.Handler) http.Handler
}

func setupTestRouter(routes ...routeConfig) *chi.Mux {
	r := chi.NewRouter()
	jwtSvc := testJWTService()

	for _, route := range routes {
		subrouter := chi.NewRouter()
		for _, mw := range route.middlewares {
			subrouter.Use(mw)
		}
		subrouter.Method(route.method, "/", route.handler)
		r.Mount(route.pattern, subrouter)
	}

	_ = jwtSvc
	return r
}

func setupAuthenticatedRouter(method, pattern string, handler http.HandlerFunc) *chi.Mux {
	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Method(method, pattern, handler)
	})
	return r
}

func setupRoleProtectedRouter(method, pattern string, handler http.HandlerFunc, roles ...string) *chi.Mux {
	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Use(middleware.RequireRole(roles...))
		r.Method(method, pattern, handler)
	})
	return r
}
