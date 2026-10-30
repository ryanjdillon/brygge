package vipps

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Event names as they appear in the delivered webhook payload's `name` field:
// bare uppercase state names. NOTE these differ from the ".v1"-suffixed event
// TYPES used to register a webhook (see registrationEvents) — the payload does
// NOT echo the dotted registration type. Confirmed against a live test delivery
// (name="AUTHORIZED") and
// https://developer.vippsmobilepay.com/docs/APIs/epayment-api/api-guide/webhooks/
const (
	EventPaymentCreated    = "CREATED"
	EventPaymentAuthorized = "AUTHORIZED"
	EventPaymentCaptured   = "CAPTURED"
	EventPaymentCancelled  = "CANCELLED"
	EventPaymentAborted    = "ABORTED"
	EventPaymentExpired    = "EXPIRED"
	EventPaymentTerminated = "TERMINATED"
	EventPaymentRefunded   = "REFUNDED"
)

// WebhookEventNames lists the event names above. The webhook's failure metric
// reports these under their own name and every other name as "other", so its
// label set stays closed.
var WebhookEventNames = []string{
	EventPaymentCreated, EventPaymentAuthorized, EventPaymentCaptured,
	EventPaymentCancelled, EventPaymentAborted, EventPaymentExpired,
	EventPaymentTerminated, EventPaymentRefunded,
}

// maxWebhookBody caps how much of the request body we buffer for hashing.
const maxWebhookBody = 1 << 20

// WebhookEvent is the payload Vipps POSTs to the registered webhook URL.
// IdempotencyKey is stable per event and used for deduplication.
type WebhookEvent struct {
	Name           string          `json:"name"`
	Reference      string          `json:"reference"`
	Timestamp      time.Time       `json:"timestamp"`
	IdempotencyKey string          `json:"idempotencyKey"`
	PSPReference   string          `json:"pspReference,omitempty"`
	Amount         *Amount         `json:"amount,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
}

// ParseAndVerify reads the request body, verifies the Vipps HMAC-SHA256
// webhook signature against the registration secret, and decodes the event.
//
// Vipps signs each webhook the same way Azure Event Grid does. The receiver
// recomputes the signature over a canonical string and compares it
// constant-time:
//
//	contentHash  = base64(SHA256(rawBody))
//	stringToSign = "POST\n" + pathAndQuery + "\n" +
//	               xMsDate + ";" + host + ";" + contentHash
//	signature    = base64(HMAC-SHA256(secret, stringToSign))
//
// The signature arrives in the Authorization header:
//
//	HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=<base64>
//
// The HMAC key is the registration secret used as raw UTF-8 bytes (matching
// Vipps' own JS/.NET samples), not base64-decoded. See
// https://developer.vippsmobilepay.com/docs/APIs/webhooks-api/request-authentication/
func ParseAndVerify(r *http.Request, secret string) (*WebhookEvent, []byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		return nil, nil, fmt.Errorf("reading webhook body: %w", err)
	}

	if err := verifySignature(r, body, secret); err != nil {
		return nil, body, err
	}

	var evt WebhookEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, body, fmt.Errorf("decoding webhook event: %w", err)
	}
	return &evt, body, nil
}

// verifySignature validates the Vipps HMAC-SHA256 webhook signature over body.
// It returns nil only when the x-ms-content-sha256 header matches the body hash
// AND the Authorization signature matches a locally recomputed one — the first
// check binds the signature to the exact bytes we received, so a tampered body
// is rejected before the HMAC compare.
func verifySignature(r *http.Request, body []byte, secret string) error {
	if secret == "" {
		return fmt.Errorf("webhook secret not configured")
	}

	// Vipps sends the signature in both x-vipps-authorization and Authorization.
	// Prefer the former: reverse proxies and API gateways commonly strip or
	// overwrite Authorization, which is why Vipps added the x-vipps- variant.
	authHeader := r.Header.Get("x-vipps-authorization")
	if authHeader == "" {
		authHeader = r.Header.Get("Authorization")
	}
	sig, err := signatureFromAuthHeader(authHeader)
	if err != nil {
		return err
	}

	sum := sha256.Sum256(body)
	contentHash := base64.StdEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("x-ms-content-sha256")), []byte(contentHash)) != 1 {
		return fmt.Errorf("webhook content hash mismatch")
	}

	// pathAndQuery and host must match the callback URL registered with Vipps.
	// Behind a reverse proxy that rewrites either, register the externally
	// visible path/host so the string-to-sign reconstructs identically here.
	stringToSign := "POST\n" + r.URL.RequestURI() + "\n" +
		r.Header.Get("x-ms-date") + ";" + r.Host + ";" + contentHash

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return fmt.Errorf("webhook signature mismatch")
	}
	return nil
}

// signatureFromAuthHeader extracts the base64 Signature from a Vipps
// Authorization header of the form:
//
//	HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=<base64>
func signatureFromAuthHeader(auth string) (string, error) {
	const scheme = "HMAC-SHA256 "
	if !strings.HasPrefix(auth, scheme) {
		return "", fmt.Errorf("webhook authorization: missing HMAC-SHA256 scheme")
	}
	for _, part := range strings.Split(strings.TrimPrefix(auth, scheme), "&") {
		if v, ok := strings.CutPrefix(part, "Signature="); ok {
			if v == "" {
				return "", fmt.Errorf("webhook authorization: empty signature")
			}
			return v, nil
		}
	}
	return "", fmt.Errorf("webhook authorization: no Signature parameter")
}
