package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func newTestPriceItemsHandler(t *testing.T) *PriceItemsHandler {
	t.Helper()
	cfg := testConfig()
	log := zerolog.Nop()
	return NewPriceItemsHandler(nil, cfg, log)
}

func TestHandleListAdminPricingUnauthenticated(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/pricing", h.HandleListAdmin, "admin", "treasurer")

	req := httptest.NewRequest(http.MethodGet, "/admin/pricing", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleListAdminPricingForbidden(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/pricing", h.HandleListAdmin, "admin", "treasurer")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/admin/pricing", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleCreatePriceItemUnauthenticated(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/pricing", h.HandleCreate, "admin", "treasurer")

	body := `{"name":"Test","category":"guest","amount":100}`
	req := httptest.NewRequest(http.MethodPost, "/admin/pricing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleCreatePriceItemInvalidBody(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/pricing", h.HandleCreate, "admin", "treasurer")

	token := generateTestToken("user-1", "club-1", []string{"admin"})

	req := httptest.NewRequest(http.MethodPost, "/admin/pricing", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleCreatePriceItemMissingName(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/pricing", h.HandleCreate, "admin", "treasurer")

	token := generateTestToken("user-1", "club-1", []string{"admin"})

	body := `{"category":"guest","amount":100}`
	req := httptest.NewRequest(http.MethodPost, "/admin/pricing", strings.NewReader(body))
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
	if resp.Error != "name is required" {
		t.Errorf("expected error 'name is required', got %q", resp.Error)
	}
}

func TestHandleCreatePriceItemInvalidCategory(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/pricing", h.HandleCreate, "admin", "treasurer")

	token := generateTestToken("user-1", "club-1", []string{"admin"})

	body := `{"name":"Test","category":"invalid_cat","amount":100}`
	req := httptest.NewRequest(http.MethodPost, "/admin/pricing", strings.NewReader(body))
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
	if resp.Error != "invalid category" {
		t.Errorf("expected error 'invalid category', got %q", resp.Error)
	}
}

func TestHandleCreatePriceItemNegativeAmount(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/pricing", h.HandleCreate, "admin", "treasurer")

	token := generateTestToken("user-1", "club-1", []string{"admin"})

	body := `{"name":"Test","category":"guest","amount":-50}`
	req := httptest.NewRequest(http.MethodPost, "/admin/pricing", strings.NewReader(body))
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
	if resp.Error != "amount must be non-negative" {
		t.Errorf("expected error 'amount must be non-negative', got %q", resp.Error)
	}
}

func TestHandleUpdatePriceItemUnauthenticated(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/pricing/{itemID}", h.HandleUpdate, "admin", "treasurer")

	body := `{"name":"Test","category":"guest","amount":100}`
	req := httptest.NewRequest(http.MethodPut, "/admin/pricing/some-id", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdatePriceItemInvalidBody(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/pricing/{itemID}", h.HandleUpdate, "admin", "treasurer")

	token := generateTestToken("user-1", "club-1", []string{"admin"})

	req := httptest.NewRequest(http.MethodPut, "/admin/pricing/some-id", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdatePriceItemValidation(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/pricing/{itemID}", h.HandleUpdate, "admin", "treasurer")

	token := generateTestToken("user-1", "club-1", []string{"admin"})

	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{"missing name", `{"category":"guest","amount":100}`, "name is required"},
		{"invalid category", `{"name":"Test","category":"bad","amount":100}`, "invalid category"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/admin/pricing/some-id", strings.NewReader(tt.body))
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
			if resp.Error != tt.expected {
				t.Errorf("expected error %q, got %q", tt.expected, resp.Error)
			}
		})
	}
}

func TestHandleDeletePriceItemUnauthenticated(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodDelete, "/admin/pricing/{itemID}", h.HandleDelete, "admin", "treasurer")

	req := httptest.NewRequest(http.MethodDelete, "/admin/pricing/some-id", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleDeletePriceItemForbidden(t *testing.T) {
	h := newTestPriceItemsHandler(t)
	r := setupRoleProtectedRouter(http.MethodDelete, "/admin/pricing/{itemID}", h.HandleDelete, "admin", "treasurer")

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodDelete, "/admin/pricing/some-id", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}
