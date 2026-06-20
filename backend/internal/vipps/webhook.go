package vipps

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Event names sent by Vipps ePayment webhooks.
const (
	EventPaymentCreated   = "epayments.payment.created"
	EventPaymentReserved  = "epayments.payment.reserved"  // authorized / reserved
	EventPaymentCaptured  = "epayments.payment.captured"
	EventPaymentCancelled = "epayments.payment.cancelled"
	EventPaymentAborted   = "epayments.payment.aborted"
	EventPaymentExpired   = "epayments.payment.expired"
	EventPaymentRefunded  = "epayments.payment.refunded"
)

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

// ParseAndVerify reads the request body, verifies the Authorization header
// against the configured webhook secret, and decodes the event.
//
// Vipps sends the raw webhook secret (no "Bearer" prefix) in the
// Authorization header. Comparison is constant-time to resist timing attacks.
func ParseAndVerify(r *http.Request, secret string) (*WebhookEvent, []byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("reading webhook body: %w", err)
	}

	auth := r.Header.Get("Authorization")
	if subtle.ConstantTimeCompare([]byte(auth), []byte(secret)) != 1 {
		return nil, body, fmt.Errorf("webhook authorization mismatch")
	}

	var evt WebhookEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, body, fmt.Errorf("decoding webhook event: %w", err)
	}
	return &evt, body, nil
}
