package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func newTestOrdersHandler(t *testing.T) *OrdersHandler {
	t.Helper()
	cfg := testConfig()
	log := zerolog.Nop()
	return NewOrdersHandler(nil, cfg, log)
}

func TestHandleCreateOrderInvalidBody(t *testing.T) {
	h := newTestOrdersHandler(t)

	r := setupTestRouter(routeConfig{
		method:  http.MethodPost,
		pattern: "/orders",
		handler: h.HandleCreateOrder,
	})

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleCreateOrderEmptyLines(t *testing.T) {
	h := newTestOrdersHandler(t)

	r := setupTestRouter(routeConfig{
		method:  http.MethodPost,
		pattern: "/orders",
		handler: h.HandleCreateOrder,
	})

	body := `{"lines":[]}`
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "at least one line item is required" {
		t.Errorf("expected error 'at least one line item is required', got %q", resp.Error)
	}
}
