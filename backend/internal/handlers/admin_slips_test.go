package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func newTestAdminSlipsHandler(t *testing.T) *AdminSlipsHandler {
	t.Helper()
	cfg := testConfig()
	log := zerolog.Nop()
	return NewAdminSlipsHandler(nil, cfg, log)
}

func TestHandleListSlipsUnauthenticated(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/slips", h.HandleListSlips, "styre", "harbour_master")

	req := httptest.NewRequest(http.MethodGet, "/admin/slips", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleListSlipsForbidden(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/slips", h.HandleListSlips, "styre", "harbour_master")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/admin/slips", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleGetSlipUnauthenticated(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/slips/{slipID}", h.HandleGetSlip, "styre", "harbour_master")

	req := httptest.NewRequest(http.MethodGet, "/admin/slips/some-id", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleGetSlipForbidden(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/slips/{slipID}", h.HandleGetSlip, "styre", "harbour_master")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/admin/slips/some-id", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleCreateSlipUnauthenticated(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/slips", h.HandleCreateSlip, "styre", "harbour_master")

	body := `{"number":"A1","section":"A"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/slips", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleCreateSlipForbidden(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/slips", h.HandleCreateSlip, "styre", "harbour_master")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	body := `{"number":"A1","section":"A"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/slips", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateSlipUnauthenticated(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/slips/{slipID}", h.HandleUpdateSlip, "styre", "harbour_master")

	req := httptest.NewRequest(http.MethodPut, "/admin/slips/some-id", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateSlipForbidden(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/slips/{slipID}", h.HandleUpdateSlip, "styre", "harbour_master")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodPut, "/admin/slips/some-id", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleAssignSlipUnauthenticated(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/slips/{slipID}/assign", h.HandleAssignSlip, "styre", "harbour_master")

	body := `{"user_id":"user-2"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/slips/some-id/assign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleAssignSlipForbidden(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/slips/{slipID}/assign", h.HandleAssignSlip, "styre", "harbour_master")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	body := `{"user_id":"user-2"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/slips/some-id/assign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleReleaseSlipUnauthenticated(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/slips/{slipID}/release", h.HandleReleaseSlip, "styre", "harbour_master")

	req := httptest.NewRequest(http.MethodPost, "/admin/slips/some-id/release", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleReleaseSlipForbidden(t *testing.T) {
	h := newTestAdminSlipsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/slips/{slipID}/release", h.HandleReleaseSlip, "styre", "harbour_master")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodPost, "/admin/slips/some-id/release", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}
