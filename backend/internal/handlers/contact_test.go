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

func TestHandleContactFormInvalidEmail(t *testing.T) {
	h := newTestContactHandler(t)

	tests := []struct {
		name  string
		email string
	}{
		{"no at sign", "invalid"},
		{"no domain", "user@"},
		{"no tld", "user@domain"},
		{"no local part", "@domain.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"name":"Test","email":"` + tt.email + `","subject":"Hi","message":"This is a valid message."}`
			req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.HandleContactForm(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
			}

			var resp errorResponse
			json.NewDecoder(rec.Body).Decode(&resp)
			if !strings.Contains(resp.Error, "email") {
				t.Errorf("expected error about email, got %q", resp.Error)
			}
		})
	}
}

func TestHandleContactFormMessageTooShort(t *testing.T) {
	h := newTestContactHandler(t)

	body := `{"name":"Test","email":"user@example.com","subject":"Hi","message":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleContactForm(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp errorResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "10 characters") {
		t.Errorf("expected error about 10 characters, got %q", resp.Error)
	}
}

func TestIsValidEmail(t *testing.T) {
	tests := []struct {
		email string
		valid bool
	}{
		{"user@example.com", true},
		{"a@b.no", true},
		{"name+tag@domain.co.uk", true},
		{"invalid", false},
		{"@domain.com", false},
		{"user@", false},
		{"user@domain", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			got := isValidEmail(tt.email)
			if got != tt.valid {
				t.Errorf("isValidEmail(%q) = %v, want %v", tt.email, got, tt.valid)
			}
		})
	}
}
