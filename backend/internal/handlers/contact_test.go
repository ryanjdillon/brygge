package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func newTestContactHandler(t *testing.T) *ContactHandler {
	t.Helper()
	cfg := testConfig()
	log := zerolog.Nop()
	return NewContactHandler(cfg, log)
}

func TestHandleContactFormValid(t *testing.T) {
	h := newTestContactHandler(t)

	body := `{"name":"Ola Nordmann","email":"ola@example.com","subject":"Test","message":"Hello there"}`
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleContactForm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "received" {
		t.Errorf("expected status 'received', got %q", resp["status"])
	}
}

func TestHandleContactFormMissingFields(t *testing.T) {
	h := newTestContactHandler(t)

	tests := []struct {
		name string
		body string
	}{
		{"missing name", `{"email":"a@b.com","subject":"Hi","message":"Hello"}`},
		{"missing email", `{"name":"Ola","subject":"Hi","message":"Hello"}`},
		{"missing subject", `{"name":"Ola","email":"a@b.com","message":"Hello"}`},
		{"missing message", `{"name":"Ola","email":"a@b.com","subject":"Hi"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.HandleContactForm(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}

			var resp errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Error == "" {
				t.Error("expected error message in response")
			}
		})
	}
}

func TestHandleContactFormEmptyBody(t *testing.T) {
	h := newTestContactHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleContactForm(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestHandleContactFormInvalidJSON(t *testing.T) {
	h := newTestContactHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleContactForm(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}
