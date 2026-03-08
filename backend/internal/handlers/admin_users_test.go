package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func newTestAdminUsersHandler(t *testing.T) *AdminUsersHandler {
	t.Helper()
	cfg := testConfig()
	log := zerolog.Nop()
	return NewAdminUsersHandler(nil, cfg, log)
}

func TestHandleListUsersUnauthenticated(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/users", h.HandleListUsers, "styre", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleListUsersForbidden(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/users", h.HandleListUsers, "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleGetUserUnauthenticated(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/users/{userID}", h.HandleGetUser, "styre", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/users/some-id", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleGetUserForbidden(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/users/{userID}", h.HandleGetUser, "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/admin/users/some-id", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateUserRolesUnauthenticated(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/users/{userID}/roles", h.HandleUpdateUserRoles, "styre", "admin")

	body := `{"roles":["member"]}`
	req := httptest.NewRequest(http.MethodPut, "/admin/users/some-id/roles", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateUserRolesForbidden(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/users/{userID}/roles", h.HandleUpdateUserRoles, "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	body := `{"roles":["member"]}`
	req := httptest.NewRequest(http.MethodPut, "/admin/users/some-id/roles", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateUserRolesInvalidBody(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/users/{userID}/roles", h.HandleUpdateUserRoles, "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"styre"})

	req := httptest.NewRequest(http.MethodPut, "/admin/users/some-id/roles", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateUserRolesInvalidRole(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/users/{userID}/roles", h.HandleUpdateUserRoles, "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"styre"})

	body := `{"roles":["member","superadmin"]}`
	req := httptest.NewRequest(http.MethodPut, "/admin/users/some-id/roles", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "invalid role: superadmin" {
		t.Errorf("expected error 'invalid role: superadmin', got %q", resp.Error)
	}
}

func TestHandleDeleteUserUnauthenticated(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodDelete, "/admin/users/{userID}", h.HandleDeleteUser, "styre", "admin")

	req := httptest.NewRequest(http.MethodDelete, "/admin/users/some-id", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteUserForbidden(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodDelete, "/admin/users/{userID}", h.HandleDeleteUser, "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodDelete, "/admin/users/some-id", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteUserSelfDeletion(t *testing.T) {
	h := newTestAdminUsersHandler(t)
	r := setupRoleProtectedRouter(http.MethodDelete, "/admin/users/{userID}", h.HandleDeleteUser, "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"admin"})

	req := httptest.NewRequest(http.MethodDelete, "/admin/users/user-1", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "cannot delete your own account via admin" {
		t.Errorf("expected self-deletion error, got %q", resp.Error)
	}
}
