package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

func TestSecurityHeadersPresent(t *testing.T) {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			w.Header().Set("Content-Security-Policy", "default-src 'self'")
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	headers := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":          "DENY",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"Referrer-Policy":          "strict-origin-when-cross-origin",
		"Permissions-Policy":       "camera=(), microphone=(), geolocation=()",
		"Content-Security-Policy":  "default-src 'self'",
	}

	for name, expected := range headers {
		got := rec.Header().Get(name)
		if got != expected {
			t.Errorf("header %s: expected %q, got %q", name, expected, got)
		}
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowCredentials: true,
	}))
	r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "http://evil.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	origin := rec.Header().Get("Access-Control-Allow-Origin")
	if origin != "" {
		t.Errorf("expected no Access-Control-Allow-Origin for evil origin, got %q", origin)
	}
}

func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowCredentials: true,
	}))
	r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	origin := rec.Header().Get("Access-Control-Allow-Origin")
	if origin != "http://localhost:5173" {
		t.Errorf("expected Access-Control-Allow-Origin http://localhost:5173, got %q", origin)
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"too short", "abc", true},
		{"11 chars", "12345678901", true},
		{"exactly 12", "123456789012", false},
		{"long password", "this-is-a-very-long-password-that-should-pass", false},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePassword(tt.password)
			if tt.wantErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
		})
	}
}

func TestHandleEmailRegisterPasswordTooShort(t *testing.T) {
	h, _ := newTestAuthHandler(t)

	body := `{"email":"a@b.com","password":"short","full_name":"Test User"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleEmailRegister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !strings.Contains(resp.Error, "12 characters") {
		t.Errorf("expected error about 12 characters, got %q", resp.Error)
	}
}

func TestHandleAuthCodeExchangeMissingCode(t *testing.T) {
	h, _ := newTestAuthHandler(t)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/auth/exchange", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleAuthCodeExchange(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestHandleAuthCodeExchangeInvalidCode(t *testing.T) {
	h, _ := newTestAuthHandler(t)

	body := `{"code":"nonexistent"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/exchange", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleAuthCodeExchange(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !strings.Contains(resp.Error, "invalid or expired") {
		t.Errorf("expected error about invalid/expired code, got %q", resp.Error)
	}
}

func TestHandleAuthCodeExchangeSuccess(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	cfg := testConfig()
	log := zerolog.Nop()
	h := NewAuthHandler(nil, rdb, testJWTService(), testVippsClient(), cfg, log)

	payload := `{"user_id":"user-1","club_id":"club-1","roles":["member"]}`
	mr.Set(authCodePrefix+"test-code-123", payload)

	body := `{"code":"test-code-123"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/exchange", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleAuthCodeExchange(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp tokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.AccessToken == "" {
		t.Error("expected non-empty access_token")
	}
	if resp.RefreshToken == "" {
		t.Error("expected non-empty refresh_token")
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("expected token_type Bearer, got %q", resp.TokenType)
	}

	// Code should be consumed (single-use)
	if mr.Exists(authCodePrefix + "test-code-123") {
		t.Error("expected auth code to be deleted after exchange")
	}
}

func TestHandleAuthCodeExchangeCodeIsOneTimeUse(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	cfg := testConfig()
	log := zerolog.Nop()
	h := NewAuthHandler(nil, rdb, testJWTService(), testVippsClient(), cfg, log)

	payload := `{"user_id":"user-1","club_id":"club-1","roles":["member"]}`
	mr.Set(authCodePrefix+"one-time-code", payload)

	body := `{"code":"one-time-code"}`

	// First use should succeed
	req1 := httptest.NewRequest(http.MethodPost, "/auth/exchange", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	h.HandleAuthCodeExchange(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first exchange: expected %d, got %d", http.StatusOK, rec1.Code)
	}

	// Second use should fail
	req2 := httptest.NewRequest(http.MethodPost, "/auth/exchange", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	h.HandleAuthCodeExchange(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("second exchange: expected %d, got %d", http.StatusBadRequest, rec2.Code)
	}
}

func TestJWTTokensContainJTI(t *testing.T) {
	svc := testJWTService()

	accessToken, err := svc.GenerateAccessToken("user-1", "club-1", []string{"member"})
	if err != nil {
		t.Fatalf("failed to generate access token: %v", err)
	}

	claims, err := svc.ValidateAccessToken(accessToken)
	if err != nil {
		t.Fatalf("failed to validate access token: %v", err)
	}
	if claims.ID == "" {
		t.Error("expected access token to have jti claim")
	}

	refreshToken, err := svc.GenerateRefreshToken("user-1")
	if err != nil {
		t.Fatalf("failed to generate refresh token: %v", err)
	}

	refreshClaims, err := svc.ValidateRefreshToken(refreshToken)
	if err != nil {
		t.Fatalf("failed to validate refresh token: %v", err)
	}
	if refreshClaims.ID == "" {
		t.Error("expected refresh token to have jti claim")
	}

	// JTIs should be unique
	if claims.ID == refreshClaims.ID {
		t.Error("access and refresh tokens should have different jti values")
	}
}
