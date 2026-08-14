package vipps_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

func newTestClient(baseURL string) *vipps.Client {
	return vipps.New(baseURL, "test-client-id", "test-secret", "test-sub-key", "123456", zerolog.Nop())
}

// mockVippsServer starts a minimal Vipps mock that records token fetches and
// returns canned responses for the ePayment endpoints.
func mockVippsServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var tokenCalls atomic.Int32

	mux := http.NewServeMux()

	mux.HandleFunc("POST /accesstoken/get", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-" + r.Header.Get("client_id"),
			"token_type":   "Bearer",
			// Vipps sends numeric accesstoken fields as JSON strings.
			"expires_in": "3600",
		})
	})

	mux.HandleFunc("POST /epayment/v1/payments", func(w http.ResponseWriter, r *http.Request) {
		var req vipps.CreatePaymentRequest
		json.NewDecoder(r.Body).Decode(&req)
		// The ePayment API rejects a create without paymentMethod (400).
		if req.PaymentMethod.Type == "" {
			t.Errorf("CreatePayment sent no paymentMethod.type")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"reference":   req.Reference,
			"redirectUrl": "https://vipps.no/pay/" + req.Reference,
		})
	})

	mux.HandleFunc("GET /epayment/v1/payments/", func(w http.ResponseWriter, r *http.Request) {
		ref := r.URL.Path[len("/epayment/v1/payments/"):]
		json.NewEncoder(w).Encode(map[string]any{
			"reference": ref,
			"state":     "AUTHORIZED",
			"amount":    map[string]any{"currency": "NOK", "value": 15000},
		})
	})

	mux.HandleFunc("POST /epayment/v1/payments/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /webhooks/v1/webhooks", func(w http.ResponseWriter, r *http.Request) {
		var req vipps.RegisterWebhookRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":     "wh-" + req.URL,
			"secret": "generated-secret",
		})
	})

	mux.HandleFunc("GET /webhooks/v1/webhooks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"webhooks": []map[string]any{
				{"id": "wh-1", "url": "https://example.com/hook", "events": []string{"epayments.payment.captured.v1"}},
			},
		})
	})

	mux.HandleFunc("DELETE /webhooks/v1/webhooks/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &tokenCalls
}

func TestAccessTokenCached(t *testing.T) {
	srv, tokenCalls := mockVippsServer(t)
	c := newTestClient(srv.URL)
	ctx := context.Background()

	// Two back-to-back calls should only fetch the token once.
	if _, err := c.CreatePayment(ctx, "idem-1", vipps.CreatePaymentRequest{
		Reference: "ref-1",
		ReturnURL: "https://example.com/return",
		Amount:    vipps.Amount{Currency: "NOK", Value: 15000},
	}); err != nil {
		t.Fatalf("first CreatePayment: %v", err)
	}
	if _, err := c.GetPayment(ctx, "ref-1"); err != nil {
		t.Fatalf("GetPayment: %v", err)
	}

	if n := tokenCalls.Load(); n != 1 {
		t.Errorf("expected 1 token fetch, got %d", n)
	}
}

func TestCreatePayment(t *testing.T) {
	srv, _ := mockVippsServer(t)
	c := newTestClient(srv.URL)

	p, err := c.CreatePayment(context.Background(), "idem-2", vipps.CreatePaymentRequest{
		Reference:          "order-42",
		ReturnURL:          "https://example.com/return",
		Amount:             vipps.Amount{Currency: "NOK", Value: 25000},
		PaymentDescription: "Sesongplass 2026",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if p.Reference != "order-42" {
		t.Errorf("reference = %q, want %q", p.Reference, "order-42")
	}
	if p.RedirectURL == "" {
		t.Error("expected non-empty RedirectURL")
	}
}

func TestCaptureAndCancel(t *testing.T) {
	srv, _ := mockVippsServer(t)
	c := newTestClient(srv.URL)
	ctx := context.Background()

	if err := c.CapturePayment(ctx, "cap-idem", "ref-cap", 10000); err != nil {
		t.Errorf("CapturePayment: %v", err)
	}
	if err := c.CancelPayment(ctx, "ref-cancel"); err != nil {
		t.Errorf("CancelPayment: %v", err)
	}
}

func TestTokenRetryOn401(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()

	mux.HandleFunc("POST /accesstoken/get", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		// First call returns a bad token that triggers 401, second returns a good one.
		tok := "bad-token"
		if n > 1 {
			tok = "good-token"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": tok,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	mux.HandleFunc("POST /epayment/v1/payments", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer bad-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"reference": "ref-retry", "redirectUrl": "https://example.com"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv.URL)
	p, err := c.CreatePayment(context.Background(), "idem-retry", vipps.CreatePaymentRequest{
		Reference: "ref-retry",
		ReturnURL: "https://example.com/return",
		Amount:    vipps.Amount{Currency: "NOK", Value: 5000},
	})
	if err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if p.Reference != "ref-retry" {
		t.Errorf("reference = %q", p.Reference)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("expected 2 token fetches (initial + retry), got %d", n)
	}
}

func TestVerifySucceeds(t *testing.T) {
	srv, tokenCalls := mockVippsServer(t)
	c := newTestClient(srv.URL)

	res, err := c.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if n := tokenCalls.Load(); n != 1 {
		t.Errorf("expected 1 token fetch, got %d", n)
	}
	if res.BaseURL != srv.URL {
		t.Errorf("BaseURL = %q, want %q", res.BaseURL, srv.URL)
	}
	if res.MSN != "123456" {
		t.Errorf("MSN = %q, want 123456", res.MSN)
	}
	// The token must never appear verbatim in the result.
	if res.TokenMasked == "" || strings.Contains(res.TokenMasked, "test-client-id") {
		t.Errorf("TokenMasked = %q leaks or is empty", res.TokenMasked)
	}
	if !res.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt = %v is not in the future", res.ExpiresAt)
	}
}

func TestVerifyBypassesCache(t *testing.T) {
	srv, tokenCalls := mockVippsServer(t)
	c := newTestClient(srv.URL)

	if _, err := c.Verify(context.Background()); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	// A cached token would make the second handshake skip the fetch; Verify
	// must always exercise the live credential path.
	if _, err := c.Verify(context.Background()); err != nil {
		t.Fatalf("second Verify: %v", err)
	}
	if n := tokenCalls.Load(); n != 2 {
		t.Errorf("expected 2 token fetches (cache bypassed), got %d", n)
	}
}

func TestVerifyReportsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /accesstoken/get", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_client"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv.URL)
	res, err := c.Verify(context.Background())
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	// Even on failure the non-sensitive context is returned for diagnostics.
	if res.BaseURL != srv.URL || res.MSN != "123456" {
		t.Errorf("error result missing context: %+v", res)
	}
}
