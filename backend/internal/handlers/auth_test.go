package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
)

func newTestAuthHandler() *AuthHandler {
	return NewAuthHandler(nil, testConfig(), zerolog.Nop())
}

func TestHandleMeUnauthenticated(t *testing.T) {
	h := newTestAuthHandler()

	r := setupAuthenticatedRouter(http.MethodGet, "/auth/me", h.HandleMe)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleMeAuthenticated(t *testing.T) {
	h := newTestAuthHandler()

	r := setupAuthenticatedRouter(http.MethodGet, "/auth/me", h.HandleMe)

	token := generateTestToken("user-456", "club-789", []string{"member", "board"})

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
	if len(resp.Roles) != 2 || resp.Roles[0] != "member" || resp.Roles[1] != "board" {
		t.Errorf("expected roles [member, board], got %v", resp.Roles)
	}
}
