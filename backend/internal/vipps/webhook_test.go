package vipps_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

const (
	testSecret = "super-secret-webhook-key"
	testDate   = "Thu, 30 Mar 2023 08:38:32 GMT"
)

// vippsAuth builds the Authorization value and x-ms-content-sha256 the way
// Vipps signs a webhook, so tests exercise the real scheme rather than the
// plaintext model the endpoint used before BRY-200.
func vippsAuth(requestURI, host string, body []byte, secret, date string) (auth, contentHash string) {
	sum := sha256.Sum256(body)
	contentHash = base64.StdEncoding.EncodeToString(sum[:])
	stringToSign := "POST\n" + requestURI + "\n" + date + ";" + host + ";" + contentHash
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	auth = "HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=" + sig
	return auth, contentHash
}

// signedRequest returns a POST webhook request signed with secret over body.
func signedRequest(body []byte, secret string) *http.Request {
	req := httptest.NewRequest("POST", "/vipps/webhooks", bytes.NewReader(body))
	auth, hash := vippsAuth(req.URL.RequestURI(), req.Host, body, secret, testDate)
	req.Header.Set("x-ms-date", testDate)
	req.Header.Set("x-ms-content-sha256", hash)
	req.Header.Set("Authorization", auth)
	return req
}

var validPayload = []byte(`{"name":"epayments.payment.captured","reference":"order-99","idempotencyKey":"uuid-abc-123","timestamp":"2026-06-20T10:00:00Z","amount":{"currency":"NOK","value":15000}}`)

// TestParseAndVerify_GoldenVector pins the canonical string construction to a
// signature computed independently of the production code. If the string-to-sign
// format drifts (verb, path, separators, header order) this literal stops
// matching even when the self-signing helper below still agrees with the code.
func TestParseAndVerify_GoldenVector(t *testing.T) {
	req := httptest.NewRequest("POST", "/vipps/webhooks", bytes.NewReader(validPayload))
	req.Host = "example.com"
	req.Header.Set("x-ms-date", testDate)
	req.Header.Set("x-ms-content-sha256", "NJD81qgYGGNuFK3SrNfs4jpSNxU0h3qrUDN5Cwx47MM=")
	req.Header.Set("Authorization",
		"HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=+XiP5lM8rkSTqYrFqCiHIiO2gn0sCC4Efqpgh6kwA6Q=")

	evt, _, err := vipps.ParseAndVerify(req, testSecret)
	if err != nil {
		t.Fatalf("golden vector should verify, got: %v", err)
	}
	if evt.Reference != "order-99" {
		t.Errorf("reference = %q, want order-99", evt.Reference)
	}
}

func TestParseAndVerify_Valid(t *testing.T) {
	evt, raw, err := vipps.ParseAndVerify(signedRequest(validPayload, testSecret), testSecret)
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
	if len(raw) == 0 {
		t.Error("raw body should not be empty")
	}
}

func TestParseAndVerify_TamperedBody(t *testing.T) {
	// Sign the original, then deliver a different body under the same headers.
	req := httptest.NewRequest("POST", "/vipps/webhooks",
		bytes.NewReader([]byte(`{"name":"epayments.payment.captured","reference":"attacker","idempotencyKey":"k","timestamp":"2026-06-20T10:00:00Z"}`)))
	auth, hash := vippsAuth(req.URL.RequestURI(), req.Host, validPayload, testSecret, testDate)
	req.Header.Set("x-ms-date", testDate)
	req.Header.Set("x-ms-content-sha256", hash)
	req.Header.Set("Authorization", auth)

	if _, _, err := vipps.ParseAndVerify(req, testSecret); err == nil {
		t.Fatal("expected rejection for tampered body, got nil")
	}
}

func TestParseAndVerify_WrongSecret(t *testing.T) {
	// Signed with a secret the verifier does not hold.
	if _, _, err := vipps.ParseAndVerify(signedRequest(validPayload, "attacker-secret"), testSecret); err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

func TestParseAndVerify_CorruptSignature(t *testing.T) {
	req := signedRequest(validPayload, testSecret)
	req.Header.Set("Authorization",
		"HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=deadbeef")
	if _, _, err := vipps.ParseAndVerify(req, testSecret); err == nil {
		t.Fatal("expected error for corrupt signature, got nil")
	}
}

func TestParseAndVerify_PlaintextAuthRejected(t *testing.T) {
	// The pre-BRY-200 model sent the raw secret as the Authorization header.
	req := httptest.NewRequest("POST", "/vipps/webhooks", bytes.NewReader(validPayload))
	req.Header.Set("Authorization", testSecret)
	if _, _, err := vipps.ParseAndVerify(req, testSecret); err == nil {
		t.Fatal("expected rejection of plaintext Authorization, got nil")
	}
}

func TestParseAndVerify_MissingContentHashHeader(t *testing.T) {
	req := signedRequest(validPayload, testSecret)
	req.Header.Del("x-ms-content-sha256")
	if _, _, err := vipps.ParseAndVerify(req, testSecret); err == nil {
		t.Fatal("expected error for missing content hash header, got nil")
	}
}

func TestParseAndVerify_EmptySecret(t *testing.T) {
	if _, _, err := vipps.ParseAndVerify(signedRequest(validPayload, testSecret), ""); err == nil {
		t.Fatal("expected error when secret not configured, got nil")
	}
}

func TestParseAndVerify_InvalidJSON(t *testing.T) {
	// Correctly signed, but the body is not a JSON event.
	if _, _, err := vipps.ParseAndVerify(signedRequest([]byte("not json"), testSecret), testSecret); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestEventConstants(t *testing.T) {
	events := []string{
		vipps.EventPaymentCreated,
		vipps.EventPaymentAuthorized,
		vipps.EventPaymentCaptured,
		vipps.EventPaymentCancelled,
		vipps.EventPaymentAborted,
		vipps.EventPaymentExpired,
		vipps.EventPaymentTerminated,
		vipps.EventPaymentRefunded,
	}
	for _, e := range events {
		if e == "" {
			t.Error("event constant must not be empty")
		}
	}
}
