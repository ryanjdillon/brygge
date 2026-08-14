package vipps

import (
	"context"
	"fmt"
	"net/http"
)

// RegisterWebhookRequest is the body for POST /webhooks/v1/webhooks.
type RegisterWebhookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// RegisterWebhookResponse is returned by RegisterWebhook. The secret is shown
// only in this response and cannot be retrieved later.
type RegisterWebhookResponse struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

// Webhook is one registered webhook as returned by ListWebhooks.
type Webhook struct {
	ID     string   `json:"id"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

type listWebhooksResponse struct {
	Webhooks []Webhook `json:"webhooks"`
}

// registrationEvents returns the ePayment events to subscribe to, in the
// ".v1"-suffixed form the Webhooks API expects at registration. The delivered
// payload's `name` field uses the un-suffixed names (EventPayment*); confirm
// the exact delivered form against the Vipps test environment before relying on
// dispatch matching, since the two representations differ.
func registrationEvents() []string {
	base := []string{
		EventPaymentCreated,
		EventPaymentAuthorized,
		EventPaymentCaptured,
		EventPaymentCancelled,
		EventPaymentAborted,
		EventPaymentExpired,
		EventPaymentTerminated,
		EventPaymentRefunded,
	}
	out := make([]string, len(base))
	for i, e := range base {
		out[i] = e + ".v1"
	}
	return out
}

// RegisterWebhook registers callbackURL to receive this merchant's ePayment
// events and returns the Vipps-generated webhook id and secret. Store the
// secret as VIPPS_WEBHOOK_SECRET — it is never shown again, and losing it means
// deleting and re-registering the webhook.
func (c *Client) RegisterWebhook(ctx context.Context, callbackURL string) (*RegisterWebhookResponse, error) {
	if callbackURL == "" {
		return nil, fmt.Errorf("register webhook: empty callback URL")
	}
	req := RegisterWebhookRequest{URL: callbackURL, Events: registrationEvents()}
	var out RegisterWebhookResponse
	if err := c.do(ctx, http.MethodPost, "/webhooks/v1/webhooks", "", req, &out); err != nil {
		return nil, fmt.Errorf("register webhook %s: %w", callbackURL, err)
	}
	return &out, nil
}

// ListWebhooks returns the webhooks currently registered for this merchant.
func (c *Client) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	var out listWebhooksResponse
	if err := c.do(ctx, http.MethodGet, "/webhooks/v1/webhooks", "", nil, &out); err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	return out.Webhooks, nil
}

// DeleteWebhook removes a previously registered webhook by id.
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("delete webhook: empty id")
	}
	if err := c.do(ctx, http.MethodDelete, "/webhooks/v1/webhooks/"+id, "", nil, nil); err != nil {
		return fmt.Errorf("delete webhook %s: %w", id, err)
	}
	return nil
}
