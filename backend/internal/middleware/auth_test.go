package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brygge-klubb/brygge/internal/auth"
)

func noopHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func TestRequireRoleAllowed(t *testing.T) {
	claims := &auth.Claims{
		UserID: "user-1",
		ClubID: "club-1",
		Roles:  []string{"board"},
	}

	called := false
	handler := RequireRole("board")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithClaims(req.Context(), claims))
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
	req = req.WithContext(WithClaims(req.Context(), claims))
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
	handler := RequireRole("board", "admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !called {
		t.Error("next handler was not called")
	}
}

func TestRequireRoleNoClaims(t *testing.T) {
	handler := RequireRole("admin")(noopHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestGetClaimsNil(t *testing.T) {
	ctx := context.Background()
	claims := GetClaims(ctx)
	if claims != nil {
		t.Errorf("GetClaims() = %v, want nil", claims)
	}
}

func TestWithClaimsRoundtrip(t *testing.T) {
	want := &auth.Claims{UserID: "u", ClubID: "c", Roles: []string{"r"}}
	ctx := WithClaims(context.Background(), want)
	got := GetClaims(ctx)
	if got == nil || got.UserID != "u" {
		t.Fatalf("GetClaims after WithClaims = %v, want %v", got, want)
	}
}
