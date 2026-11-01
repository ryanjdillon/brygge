package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An instance without Vipps credentials: the handler guard turns the
// not-configured case into a 503 before it reaches the service.
func TestHandleVippsReportSyncNotConfigured(t *testing.T) {
	h := newTestAccountingHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, "/sync/vipps-report", h.HandleVippsReportSync)

	req := httptest.NewRequest(http.MethodPost, "/sync/vipps-report", strings.NewReader(`{"date":"2026-10-25"}`))
	req.Header.Set("Authorization", authHeader(generateTestToken("u1", "c1", []string{"treasurer"})))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}
