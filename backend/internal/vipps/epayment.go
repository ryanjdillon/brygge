package vipps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Amount represents a monetary value in the smallest currency unit (øre for NOK).
type Amount struct {
	Currency string `json:"currency"`
	Value    int64  `json:"value"` // øre (1 NOK = 100 øre)
}

// PaymentMethod selects how the payer pays. The ePayment API requires it.
type PaymentMethod struct {
	Type string `json:"type"` // WALLET | CARD
}

// CreatePaymentRequest is the body for POST /epayment/v1/payments.
type CreatePaymentRequest struct {
	Amount             Amount        `json:"amount"`
	PaymentMethod      PaymentMethod `json:"paymentMethod"`
	Reference          string        `json:"reference"`
	ReturnURL          string        `json:"returnUrl"`
	PaymentDescription string        `json:"paymentDescription,omitempty"`
	UserFlow           string        `json:"userFlow,omitempty"` // WEB_REDIRECT | PUSH_MESSAGE | QR | NATIVE_REDIRECT
}

// PaymentResponse is the body returned by CreatePayment.
type PaymentResponse struct {
	Reference   string `json:"reference"`
	RedirectURL string `json:"redirectUrl"`
}

// PaymentDetails is the body returned by GetPayment.
type PaymentDetails struct {
	Reference string `json:"reference"`
	State     string `json:"state"` // CREATED | AUTHORIZED | TERMINATED | ABORTED | EXPIRED | CANCELLED | CAPTURED | REFUNDED | PARTIALLY_REFUNDED | PARTIALLY_CAPTURED
	Amount    Amount `json:"amount"`
}

// CaptureRequest is the body for POST /epayment/v1/payments/{ref}/capture.
type CaptureRequest struct {
	ModificationAmount Amount `json:"modificationAmount"`
}

// CreatePayment initiates a new ePayment and returns the checkout redirect URL.
// idempotencyKey should be a stable UUID per logical payment attempt.
func (c *Client) CreatePayment(ctx context.Context, idempotencyKey string, req CreatePaymentRequest) (*PaymentResponse, error) {
	if req.Amount.Currency == "" {
		req.Amount.Currency = "NOK"
	}
	if req.UserFlow == "" {
		req.UserFlow = "WEB_REDIRECT"
	}
	if req.PaymentMethod.Type == "" {
		req.PaymentMethod.Type = "WALLET"
	}
	var out PaymentResponse
	if err := c.do(ctx, http.MethodPost, "/epayment/v1/payments", idempotencyKey, req, &out); err != nil {
		return nil, fmt.Errorf("create payment %s: %w", req.Reference, err)
	}
	return &out, nil
}

// GetPaymentRaw fetches a payment by reference as raw JSON, preserving fields
// (e.g. the aggregate authorized/captured/refunded breakdown) that the typed
// PaymentDetails drops. Used by operator diagnostics.
func (c *Client) GetPaymentRaw(ctx context.Context, reference string) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/epayment/v1/payments/"+reference, "", nil, &out); err != nil {
		return nil, fmt.Errorf("get payment %s: %w", reference, err)
	}
	return out, nil
}

// GetPayment fetches the current state of a payment by reference.
func (c *Client) GetPayment(ctx context.Context, reference string) (*PaymentDetails, error) {
	var out PaymentDetails
	if err := c.do(ctx, http.MethodGet, "/epayment/v1/payments/"+reference, "", nil, &out); err != nil {
		return nil, fmt.Errorf("get payment %s: %w", reference, err)
	}
	return &out, nil
}

// CapturePayment captures a previously authorized payment.
// amountOere must equal the authorized amount (partial capture not yet used).
func (c *Client) CapturePayment(ctx context.Context, idempotencyKey, reference string, amountOere int64) error {
	req := CaptureRequest{
		ModificationAmount: Amount{Currency: "NOK", Value: amountOere},
	}
	return c.do(ctx, http.MethodPost,
		"/epayment/v1/payments/"+reference+"/capture",
		idempotencyKey, req, nil)
}

// CancelPayment cancels an authorized (not yet captured) payment.
func (c *Client) CancelPayment(ctx context.Context, reference string) error {
	return c.do(ctx, http.MethodPost,
		"/epayment/v1/payments/"+reference+"/cancel",
		"", nil, nil)
}
