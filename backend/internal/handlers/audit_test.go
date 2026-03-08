package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
)

func newTestAuditHandler(t *testing.T) *AuditHandler {
	t.Helper()
	cfg := testConfig()
	log := zerolog.Nop()
	auditSvc := audit.NewService(nil, log)
	return NewAuditHandler(nil, auditSvc, cfg, log)
}

func TestHandleListAuditLogUnauthorized(t *testing.T) {
	h := newTestAuditHandler(t)

	r := setupAuthenticatedRouter(http.MethodGet, "/admin/audit", h.HandleListAuditLog)

	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleListAuditLogRequiresRole(t *testing.T) {
	h := newTestAuditHandler(t)

	r := setupRoleProtectedRouter(http.MethodGet, "/admin/audit", h.HandleListAuditLog, "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"member"})
	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}
