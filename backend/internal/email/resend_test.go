package email

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientReturnsNilForEmptyKey(t *testing.T) {
	c := NewClient("", "noreply@test.com")
	if c != nil {
		t.Error("expected nil client for empty API key")
	}
}

func TestNewClientReturnsClientForValidKey(t *testing.T) {
	c := NewClient("re_test_key", "noreply@test.com")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.fromAddress != "noreply@test.com" {
		t.Errorf("fromAddress = %q, want %q", c.fromAddress, "noreply@test.com")
	}
}

func TestSendSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer re_test_key" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer re_test_key")
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}

		var req sendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if req.From != "noreply@test.com" {
			t.Errorf("from = %q, want %q", req.From, "noreply@test.com")
		}
		if len(req.To) != 1 || req.To[0] != "user@example.com" {
			t.Errorf("to = %v, want [user@example.com]", req.To)
		}
		if req.Subject != "Test Subject" {
			t.Errorf("subject = %q, want %q", req.Subject, "Test Subject")
		}
		if req.HTML != "<p>Hello</p>" {
			t.Errorf("html = %q, want %q", req.HTML, "<p>Hello</p>")
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(sendResponse{ID: "email-123"})
	}))
	defer server.Close()

	c := &Client{
		apiKey:      "re_test_key",
		fromAddress: "noreply@test.com",
		httpClient:  server.Client(),
	}
	// Override the API URL for testing
	origURL := resendAPIURL
	defer func() { resendAPIURLOverride = "" }()
	resendAPIURLOverride = server.URL

	err := c.Send(context.Background(), "user@example.com", "Test Subject", "<p>Hello</p>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = origURL
}

func TestSendRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	c := &Client{
		apiKey:      "re_test_key",
		fromAddress: "noreply@test.com",
		httpClient:  server.Client(),
	}
	resendAPIURLOverride = server.URL
	defer func() { resendAPIURLOverride = "" }()

	err := c.Send(context.Background(), "user@example.com", "Test", "<p>Hi</p>")
	if err == nil {
		t.Fatal("expected error for 429")
	}
	if err.Error() != "resend rate limited (429)" {
		t.Errorf("error = %q, want rate limited message", err.Error())
	}
}

func TestSendAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"message": "Invalid email"})
	}))
	defer server.Close()

	c := &Client{
		apiKey:      "re_test_key",
		fromAddress: "noreply@test.com",
		httpClient:  server.Client(),
	}
	resendAPIURLOverride = server.URL
	defer func() { resendAPIURLOverride = "" }()

	err := c.Send(context.Background(), "bad", "Test", "<p>Hi</p>")
	if err == nil {
		t.Fatal("expected error for 422")
	}
}
