package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

func newTestAuthHandler(t *testing.T) (*AuthHandler, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	cfg := testConfig()
	log := zerolog.Nop()

	h := NewAuthHandler(nil, rdb, testJWTService(), testVippsClient(), cfg, log)
	return h, mr
}

func TestHandleVippsLogin(t *testing.T) {
	h, _ := newTestAuthHandler(t)

	r := chi.NewRouter()
	r.Get("/auth/vipps/login", h.HandleVippsLogin)

	req := httptest.NewRequest(http.MethodGet, "/auth/vipps/login", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected status %d, got %d", http.StatusTemporaryRedirect, rec.Code)
	}

	location := rec.Header().Get("Location")
	if location == "" {
		t.Fatal("expected Location header to be set")
	}
	if !strings.Contains(location, "test-vipps-client-id") {
		t.Errorf("expected Location to contain client_id, got %s", location)
	}
	if !strings.Contains(location, "apitest.vipps.no") {
		t.Errorf("expected Location to use test base URL, got %s", location)
	}
}

func TestHandleEmailRegisterMissingFields(t *testing.T) {
	h, _ := newTestAuthHandler(t)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing password", `{"email":"a@b.com","full_name":"Test"}`},
		{"missing email", `{"password":"secret","full_name":"Test"}`},
		{"missing full_name", `{"email":"a@b.com","password":"secret"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.HandleEmailRegister(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}

			var resp errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Error == "" {
				t.Error("expected error message in response")
			}
		})
	}
}

func TestHandleEmailLoginMissingFields(t *testing.T) {
	h, _ := newTestAuthHandler(t)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing password", `{"email":"a@b.com"}`},
		{"missing email", `{"password":"secret"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.HandleEmailLogin(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}
		})
	}
}

func TestHandleRefreshTokenMissing(t *testing.T) {
	h, _ := newTestAuthHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleRefreshToken(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !strings.Contains(resp.Error, "refresh_token") {
		t.Errorf("expected error about refresh_token, got %q", resp.Error)
	}
}

func TestHandleRefreshTokenRevoked(t *testing.T) {
	h, mr := newTestAuthHandler(t)

	svc := testJWTService()
	refreshToken, err := svc.GenerateRefreshToken("user-123")
	if err != nil {
		t.Fatalf("failed to generate refresh token: %v", err)
	}

	mr.Set(revokedPrefix+refreshToken, "1")

	body := `{"refresh_token":"` + refreshToken + `"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleRefreshToken(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !strings.Contains(resp.Error, "revoked") {
		t.Errorf("expected error about revoked token, got %q", resp.Error)
	}
}

func TestHandleMeUnauthenticated(t *testing.T) {
	h, _ := newTestAuthHandler(t)

	r := setupAuthenticatedRouter(http.MethodGet, "/auth/me", h.HandleMe)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleMeAuthenticated(t *testing.T) {
	h, _ := newTestAuthHandler(t)

	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Use(middleware.Authenticate(jwtSvc))
	r.Get("/auth/me", h.HandleMe)

	token := generateTestToken("user-456", "club-789", []string{"member", "styre"})

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}

	var resp meResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.UserID != "user-456" {
		t.Errorf("expected user_id 'user-456', got %q", resp.UserID)
	}
	if resp.ClubID != "club-789" {
		t.Errorf("expected club_id 'club-789', got %q", resp.ClubID)
	}
	if len(resp.Roles) != 2 || resp.Roles[0] != "member" || resp.Roles[1] != "styre" {
		t.Errorf("expected roles [member, styre], got %v", resp.Roles)
	}
}
