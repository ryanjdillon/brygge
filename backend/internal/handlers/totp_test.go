package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

func newTestTOTPHandler(t *testing.T) *TOTPHandler {
	t.Helper()
	cfg := testConfig()
	cfg.TOTPEncryptionKey = "" // no encryption key — setup/confirm/verify will return 503
	log := zerolog.Nop()
	return NewTOTPHandler(nil, cfg, nil, nil, log)
}

func TestHandleTOTPSetupUnauthenticated(t *testing.T) {
	h := newTestTOTPHandler(t)

	r := setupAuthenticatedRouter(http.MethodPost, "/totp/setup", h.HandleSetup)

	req := httptest.NewRequest(http.MethodPost, "/totp/setup", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleTOTPSetupAuthenticated(t *testing.T) {
	h := newTestTOTPHandler(t)

	r := setupAuthenticatedRouter(http.MethodPost, "/totp/setup", h.HandleSetup)
	token := generateTestToken("user-1", "club-1", []string{"admin"})

	req := httptest.NewRequest(http.MethodPost, "/totp/setup", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp totpSetupResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Secret == "" {
		t.Error("expected non-empty secret")
	}
	if resp.QRURL == "" {
		t.Error("expected non-empty QR URL")
	}
	if !strings.HasPrefix(resp.QRURL, "otpauth://totp/") {
		t.Errorf("QR URL = %q, want otpauth://totp/ prefix", resp.QRURL)
	}
}

func TestHandleTOTPConfirmMissingEncKey(t *testing.T) {
	h := newTestTOTPHandler(t) // no enc key

	r := setupAuthenticatedRouter(http.MethodPost, "/totp/confirm", h.HandleConfirm)
	token := generateTestToken("user-1", "club-1", []string{"admin"})

	body := `{"code":"123456","secret":"JBSWY3DPEHPK3PXP"}`
	req := httptest.NewRequest(http.MethodPost, "/totp/confirm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

func TestHandleTOTPConfirmMissingFields(t *testing.T) {
	cfg := testConfig()
	cfg.TOTPEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	log := zerolog.Nop()
	h := NewTOTPHandler(nil, cfg, nil, nil, log)

	r := setupAuthenticatedRouter(http.MethodPost, "/totp/confirm", h.HandleConfirm)
	token := generateTestToken("user-1", "club-1", []string{"admin"})

	tests := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing secret", `{"code":"123456"}`},
		{"missing code", `{"secret":"JBSWY3DPEHPK3PXP"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/totp/confirm", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", authHeader(token))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleTOTPVerifyMissingCode(t *testing.T) {
	cfg := testConfig()
	cfg.TOTPEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	log := zerolog.Nop()
	h := NewTOTPHandler(nil, cfg, nil, nil, log)

	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Post("/totp/verify", h.HandleVerify)
	})
	token := generateTestToken("user-1", "club-1", []string{"admin"})

	req := httptest.NewRequest(http.MethodPost, "/totp/verify", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Session ID not in context (JWT auth, not session) — should get "session required"
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}
