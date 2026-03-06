package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
)

func newTestJWTService(secret string) *auth.JWTService {
	return auth.NewJWTService(&config.Config{
		JWTSecret:        secret,
		JWTAccessExpiry:  15 * time.Minute,
		JWTRefreshExpiry: 7 * 24 * time.Hour,
	})
}

func noopHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func generateTestToken(t *testing.T, svc *auth.JWTService, userID, clubID string, roles []string) string {
	t.Helper()
	token, err := svc.GenerateAccessToken(userID, clubID, roles)
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}
	return token
}

func TestAuthenticateValidToken(t *testing.T) {
	svc := newTestJWTService("test-secret")
	token := generateTestToken(t, svc, "user-1", "club-1", []string{"member"})

	var gotClaims *auth.Claims
	handler := Authenticate(svc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = GetClaims(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotClaims == nil {
		t.Fatal("claims not found in context")
	}
	if gotClaims.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", gotClaims.UserID, "user-1")
	}
}

func TestAuthenticateMissingHeader(t *testing.T) {
	svc := newTestJWTService("test-secret")
	handler := Authenticate(svc)(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAuthenticateInvalidToken(t *testing.T) {
	svc := newTestJWTService("test-secret")
	handler := Authenticate(svc)(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer garbage-token-value")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAuthenticateMalformedHeader(t *testing.T) {
	svc := newTestJWTService("test-secret")
	handler := Authenticate(svc)(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestRequireRoleAllowed(t *testing.T) {
	claims := &auth.Claims{
		UserID: "user-1",
		ClubID: "club-1",
		Roles:  []string{"styre"},
	}

	called := false
	handler := RequireRole("styre")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, claims)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !called {
		t.Error("next handler was not called")
	}
}

func TestRequireRoleForbidden(t *testing.T) {
	claims := &auth.Claims{
		UserID: "user-1",
		ClubID: "club-1",
		Roles:  []string{"member"},
	}

	handler := RequireRole("admin")(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, claims)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}

	var body map[string]string
	json.NewDecoder(rr.Body).Decode(&body)
	if body["error"] == "" {
		t.Error("expected error message in response body")
	}
}

func TestRequireRoleMultipleRoles(t *testing.T) {
	claims := &auth.Claims{
		UserID: "user-1",
		ClubID: "club-1",
		Roles:  []string{"admin"},
	}

	called := false
	handler := RequireRole("styre", "admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, claims)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !called {
		t.Error("next handler was not called")
	}
}

func TestGetClaimsNil(t *testing.T) {
	ctx := context.Background()
	claims := GetClaims(ctx)
	if claims != nil {
		t.Errorf("GetClaims() = %v, want nil", claims)
	}
}
