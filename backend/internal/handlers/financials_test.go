package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func newTestFinancialsHandler(t *testing.T) *FinancialsHandler {
	t.Helper()
	cfg := testConfig()
	log := zerolog.Nop()
	return NewFinancialsHandler(nil, cfg, log)
}

func TestHandleGetFinancialSummaryUnauthenticated(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/summary", h.HandleGetFinancialSummary, "treasurer", "styre", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/summary", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleGetFinancialSummaryForbidden(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/summary", h.HandleGetFinancialSummary, "treasurer", "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/summary", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleGetFinancialSummaryInvalidYear(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/summary", h.HandleGetFinancialSummary, "treasurer", "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"treasurer"})

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/summary?year=abc", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleListPaymentsUnauthenticated(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/payments", h.HandleListPayments, "treasurer", "styre", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/payments", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleListPaymentsForbidden(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/payments", h.HandleListPayments, "treasurer", "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/payments", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleListPaymentsInvalidYear(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/payments", h.HandleListPayments, "treasurer", "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"treasurer"})

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/payments?year=xyz", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleGetPaymentDetailsUnauthenticated(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/payments/{paymentID}", h.HandleGetPaymentDetails, "treasurer", "styre", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/payments/some-id", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleExportCSVUnauthenticated(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/export", h.HandleExportCSV, "treasurer", "styre", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/export", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleExportCSVInvalidDateFormats(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/export", h.HandleExportCSV, "treasurer", "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"treasurer"})

	tests := []struct {
		name  string
		query string
	}{
		{"invalid year", "?year=abc"},
		{"invalid start date", "?start=not-a-date"},
		{"invalid end date", "?end=not-a-date"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/financials/export"+tt.query, nil)
			req.Header.Set("Authorization", authHeader(token))
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleGenerateInvoiceUnauthenticated(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/financials/invoices", h.HandleGenerateInvoice, "treasurer", "styre", "admin")

	req := httptest.NewRequest(http.MethodPost, "/admin/financials/invoices", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleGenerateInvoiceInvalidBody(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/financials/invoices", h.HandleGenerateInvoice, "treasurer", "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"treasurer"})

	req := httptest.NewRequest(http.MethodPost, "/admin/financials/invoices", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleGenerateInvoiceMissingFields(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/financials/invoices", h.HandleGenerateInvoice, "treasurer", "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"treasurer"})

	tests := []struct {
		name string
		body string
	}{
		{"missing user_id", `{"type":"dues","amount":500,"due_date":"2026-06-01"}`},
		{"missing type", `{"user_id":"u1","amount":500,"due_date":"2026-06-01"}`},
		{"zero amount", `{"user_id":"u1","type":"dues","amount":0,"due_date":"2026-06-01"}`},
		{"negative amount", `{"user_id":"u1","type":"dues","amount":-100,"due_date":"2026-06-01"}`},
		{"missing due_date", `{"user_id":"u1","type":"dues","amount":500}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/financials/invoices", strings.NewReader(tt.body))
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
			if resp.Error == "" {
				t.Error("expected non-empty error message")
			}
		})
	}
}

func TestHandleGenerateInvoiceInvalidDueDate(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/financials/invoices", h.HandleGenerateInvoice, "treasurer", "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"treasurer"})

	body := `{"user_id":"u1","type":"dues","amount":500,"due_date":"not-a-date"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/financials/invoices", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleListOverdueUnauthenticated(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/overdue", h.HandleListOverdue, "treasurer", "styre", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/overdue", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleListOverdueForbidden(t *testing.T) {
	h := newTestFinancialsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/overdue", h.HandleListOverdue, "treasurer", "styre", "admin")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/admin/financials/overdue", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}
