// Package stripe is a small hand-rolled client for the parts of the Stripe API
// Brygge uses: hosted Checkout Sessions, capturing or voiding their
// PaymentIntents, reading the balance and its payouts for the books, and
// verifying webhook signatures. The surface is a handful of form-encoded
// calls, which is cheaper to own than the stripe-go module.
package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// DefaultBaseURL is the Stripe API origin; test and live mode are chosen by
// the secret key, not the URL.
const DefaultBaseURL = "https://api.stripe.com"

// APIVersion pins the request and response shapes this client was written
// against, so an account-level upgrade in the Dashboard cannot change them.
// Webhook payloads follow the endpoint's own version, set when the endpoint
// is created; it should match.
const APIVersion = "2024-06-20"

// maxResponseBody caps how much of a Stripe response is buffered.
const maxResponseBody = 1 << 20

// maxListResponseBody caps a list page. Each balance transaction in a page
// carries its expanded source object, and a charge alone runs to several KiB.
const maxListResponseBody = 8 << 20

// listPageSize is the page size for list calls whose items are expanded.
const listPageSize = 50

// Client calls the Stripe API with one account's secret key. Safe for
// concurrent use.
type Client struct {
	baseURL   string
	secretKey string
	log       zerolog.Logger
	http      *http.Client
}

// New returns a Client for baseURL (DefaultBaseURL outside tests).
func New(baseURL, secretKey string, log zerolog.Logger) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		secretKey: secretKey,
		log:       log.With().Str("component", "stripe").Logger(),
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// APIError is a non-2xx answer from Stripe.
type APIError struct {
	Status  int
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("stripe %d %s/%s: %s", e.Status, e.Type, e.Code, e.Message)
}

// codeUnexpectedState is Stripe's answer to an operation the object's
// current status does not allow, such as capturing a captured PaymentIntent.
const codeUnexpectedState = "payment_intent_unexpected_state"

// CreateCheckoutSession creates a hosted Checkout Session from form-encoded
// parameters. idempotencyKey makes a retried create return the same session.
func (c *Client) CreateCheckoutSession(ctx context.Context, params url.Values, idempotencyKey string) (*CheckoutSession, error) {
	var s CheckoutSession
	if err := c.do(ctx, http.MethodPost, "/v1/checkout/sessions", idempotencyKey, params, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetCheckoutSession retrieves a session with its PaymentIntent expanded.
func (c *Client) GetCheckoutSession(ctx context.Context, id string) (*CheckoutSession, error) {
	var s CheckoutSession
	path := "/v1/checkout/sessions/" + url.PathEscape(id) + "?expand[]=payment_intent"
	if err := c.do(ctx, http.MethodGet, path, "", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ExpireCheckoutSession closes an open session so it can no longer be paid.
func (c *Client) ExpireCheckoutSession(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/checkout/sessions/"+url.PathEscape(id)+"/expire", "", url.Values{}, nil)
}

// GetPaymentIntent retrieves a PaymentIntent.
func (c *Client) GetPaymentIntent(ctx context.Context, id string) (*PaymentIntent, error) {
	var pi PaymentIntent
	if err := c.do(ctx, http.MethodGet, "/v1/payment_intents/"+url.PathEscape(id), "", nil, &pi); err != nil {
		return nil, err
	}
	return &pi, nil
}

// CapturePaymentIntent takes the full authorized amount. A PaymentIntent that
// is already captured counts as success, so a capture whose answer was lost
// can be repeated safely.
func (c *Client) CapturePaymentIntent(ctx context.Context, id, idempotencyKey string) error {
	err := c.do(ctx, http.MethodPost, "/v1/payment_intents/"+url.PathEscape(id)+"/capture", idempotencyKey, url.Values{}, nil)
	var apiErr *APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.Code != codeUnexpectedState {
		return err
	}
	pi, getErr := c.GetPaymentIntent(ctx, id)
	if getErr == nil && pi.Status == PaymentIntentSucceeded {
		return nil
	}
	return err
}

// CancelPaymentIntent voids an uncaptured PaymentIntent, releasing the hold.
func (c *Client) CancelPaymentIntent(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/payment_intents/"+url.PathEscape(id)+"/cancel", "", url.Values{}, nil)
}

// ListBalanceTransactions returns one page, newest first, of the balance
// transactions created at or after createdGTE, each with its source object
// expanded. startingAfter is the last id of the previous page ("" for the
// first).
func (c *Client) ListBalanceTransactions(ctx context.Context, createdGTE time.Time, startingAfter string) (*BalanceTransactionPage, error) {
	q := url.Values{}
	q.Set("created[gte]", strconv.FormatInt(createdGTE.Unix(), 10))
	return c.listBalanceTransactions(ctx, q, startingAfter)
}

// ListPayoutTransactions returns one page of the balance transactions paid
// out by an automatic payout, the payout's own included. Stripe answers this
// only for automatic payouts.
func (c *Client) ListPayoutTransactions(ctx context.Context, payoutID, startingAfter string) (*BalanceTransactionPage, error) {
	q := url.Values{}
	q.Set("payout", payoutID)
	return c.listBalanceTransactions(ctx, q, startingAfter)
}

func (c *Client) listBalanceTransactions(ctx context.Context, q url.Values, startingAfter string) (*BalanceTransactionPage, error) {
	q.Set("limit", strconv.Itoa(listPageSize))
	q.Add("expand[]", "data.source")
	if startingAfter != "" {
		q.Set("starting_after", startingAfter)
	}
	var page BalanceTransactionPage
	if err := c.doCapped(ctx, http.MethodGet, "/v1/balance_transactions?"+q.Encode(), "", nil, &page, maxListResponseBody); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetPayout retrieves a payout.
func (c *Client) GetPayout(ctx context.Context, id string) (*Payout, error) {
	var p Payout
	if err := c.do(ctx, http.MethodGet, "/v1/payouts/"+url.PathEscape(id), "", nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// do sends one API call. form (nil: no body) is sent form-encoded; out (nil:
// ignored) receives the decoded 2xx body. The secret key is never logged.
func (c *Client) do(ctx context.Context, method, path, idempotencyKey string, form url.Values, out any) error {
	return c.doCapped(ctx, method, path, idempotencyKey, form, out, maxResponseBody)
}

// doCapped is do with the response buffered up to maxBody bytes.
func (c *Client) doCapped(ctx context.Context, method, path, idempotencyKey string, form url.Values, out any, maxBody int64) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("stripe %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Stripe-Version", APIVersion)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("stripe %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("stripe %s %s: read response: %w", method, path, err)
	}

	c.log.Debug().
		Str("method", method).
		Str("path", path).
		Int("status", resp.StatusCode).
		Str("idempotency_key", idempotencyKey).
		Dur("latency_ms", time.Since(start)).
		Msg("stripe api call")

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("stripe %s %s decode: %w", method, path, err)
		}
	}
	return nil
}

func decodeAPIError(status int, raw []byte) error {
	var env struct {
		Error APIError `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Error.Type == "" {
		return &APIError{Status: status, Type: "unknown", Message: truncate(raw, 200)}
	}
	env.Error.Status = status
	return &env.Error
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
