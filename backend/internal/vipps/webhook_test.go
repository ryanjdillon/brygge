package vipps_test

import (
	"bytes"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

const testSecret = "super-secret-webhook-key"

func webhookRequest(body string, authHeader string) *httptest.ResponseRecorder {
	_ = authHeader
	return nil // not used directly; see below
}

func TestParseAndVerify_Valid(t *testing.T) {
	payload := `{
		"name": "epayments.payment.captured",
		"reference": "order-99",
		"timestamp": "2026-06-20T10:00:00Z",
		"idempotencyKey": "uuid-abc-123",
		"amount": {"currency":"NOK","value":15000}
	}`

	req := httptest.NewRequest("POST", "/vipps/webhooks", bytes.NewBufferString(payload))
	req.Header.Set("Authorization", testSecret)
	req.Header.Set("Content-Type", "application/json")

	evt, raw, err := vipps.ParseAndVerify(req, testSecret)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if evt.Name != vipps.EventPaymentCaptured {
		t.Errorf("name = %q, want %q", evt.Name, vipps.EventPaymentCaptured)
	}
	if evt.Reference != "order-99" {
		t.Errorf("reference = %q", evt.Reference)
	}
	if evt.IdempotencyKey != "uuid-abc-123" {
		t.Errorf("idempotency key = %q", evt.IdempotencyKey)
	}
	if evt.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
	_ = evt.Timestamp.UTC().Format(time.RFC3339)
	if len(raw) == 0 {
		t.Error("raw body should not be empty")
	}
}

func TestParseAndVerify_WrongSecret(t *testing.T) {
	payload := `{"name":"epayments.payment.captured","reference":"ref-1","idempotencyKey":"k1","timestamp":"2026-06-20T10:00:00Z"}`
	req := httptest.NewRequest("POST", "/vipps/webhooks", bytes.NewBufferString(payload))
	req.Header.Set("Authorization", "wrong-secret")

	_, _, err := vipps.ParseAndVerify(req, testSecret)
	if err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

func TestParseAndVerify_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/vipps/webhooks", bytes.NewBufferString("not json"))
	req.Header.Set("Authorization", testSecret)

	_, _, err := vipps.ParseAndVerify(req, testSecret)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestEventConstants(t *testing.T) {
	events := []string{
		vipps.EventPaymentCreated,
		vipps.EventPaymentReserved,
		vipps.EventPaymentCaptured,
		vipps.EventPaymentCancelled,
		vipps.EventPaymentAborted,
		vipps.EventPaymentExpired,
		vipps.EventPaymentRefunded,
	}
	for _, e := range events {
		if e == "" {
			t.Error("event constant must not be empty")
		}
	}
}
