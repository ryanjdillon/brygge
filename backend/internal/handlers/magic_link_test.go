package handlers

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/email"
)

func newTestMagicLinkHandler(t *testing.T) (*MagicLinkHandler, *email.MockSender) {
	t.Helper()
	cfg := testConfig()
	cfg.FrontendURL = "http://localhost:5173"
	log := zerolog.Nop()
	mock := &email.MockSender{}
	h := NewMagicLinkHandler(nil, cfg, mock, log)
	return h, mock
}

func TestHandleRequestMagicLinkMissingEmail(t *testing.T) {
	h, _ := newTestMagicLinkHandler(t)

	r := chi.NewRouter()
	r.Post("/auth/magic-link", h.HandleRequestMagicLink)

	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleRequestMagicLinkInvalidBody(t *testing.T) {
	h, _ := newTestMagicLinkHandler(t)

	r := chi.NewRouter()
	r.Post("/auth/magic-link", h.HandleRequestMagicLink)

	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestHandleVerifyMagicLinkNoEmailSender(t *testing.T) {
	cfg := testConfig()
	cfg.FrontendURL = "http://localhost:5173"
	log := zerolog.Nop()
	// nil email sender — handler should still work
	h := NewMagicLinkHandler(nil, cfg, nil, log)

	r := chi.NewRouter()
	r.Post("/auth/magic-link", h.HandleRequestMagicLink)

	// With nil db this will panic on QueryRow, but missing email returns 400 before hitting db
	body := `{"email":""}`
	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleVerifyMagicLinkMissingToken(t *testing.T) {
	h, _ := newTestMagicLinkHandler(t)

	r := chi.NewRouter()
	r.Get("/auth/verify", h.HandleVerifyMagicLink)

	req := httptest.NewRequest(http.MethodGet, "/auth/verify", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "token is required" {
		t.Errorf("error = %q, want %q", resp.Error, "token is required")
	}
}

// NOTE: TestHandleVerifyMagicLinkInvalidToken and TestHandleRequestMagicLinkUnknownEmail
// require a real database (pgx panics on nil pool). These are covered by integration tests.
// Unit tests here cover validation paths that don't hit the DB.

func TestGenerateToken(t *testing.T) {
	token1, err := generateToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be base64url encoded, 32 bytes → 43 chars without padding
	if len(token1) != 43 {
		t.Errorf("token length = %d, want 43", len(token1))
	}

	// Should decode to 32 bytes
	decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(token1)
	if err != nil {
		t.Fatalf("failed to decode token: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("decoded length = %d, want 32", len(decoded))
	}

	// Two tokens should be different
	token2, err := generateToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token1 == token2 {
		t.Error("two generated tokens should not be equal")
	}
}
