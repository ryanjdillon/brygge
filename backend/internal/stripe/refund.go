package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/brygge-klubb/brygge/internal/payments"
)

// Refund statuses Stripe reports. A refund that needs the cardholder's bank
// to act stays pending (or requires_action) and can still fail later.
const (
	RefundPending        = "pending"
	RefundRequiresAction = "requires_action"
	RefundSucceeded      = "succeeded"
	RefundFailed         = "failed"
	RefundCanceled       = "canceled"
)

// refundListLimit is Stripe's largest page. A payment with more refunds than
// this is not something a club does by hand.
const refundListLimit = 100

// Refund is the part of a Refund object Brygge reads, from the API or from a
// webhook event.
type Refund struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
	// FailureReason is Stripe's code for why a failed refund failed
	// (lost_or_stolen_card, expired_or_canceled_card, ...).
	FailureReason string            `json:"failure_reason"`
	Charge        ObjectID          `json:"charge"`
	PaymentIntent ObjectID          `json:"payment_intent"`
	Metadata      map[string]string `json:"metadata"`
}

// RefundPage is one page of a refund list.
type RefundPage struct {
	Data    []Refund `json:"data"`
	HasMore bool     `json:"has_more"`
}

// BryggeStatus maps Stripe's refund status onto payment_refunds.status:
// pending and requires_action are still open, failed and canceled mean the
// money did not go back. A status this code does not know stays open.
func (r Refund) BryggeStatus() string {
	switch r.Status {
	case RefundSucceeded:
		return payments.RefundSucceeded
	case RefundFailed, RefundCanceled:
		return payments.RefundFailed
	}
	return payments.RefundRequested
}

// CreateRefund creates a refund from form-encoded parameters. The
// idempotency key makes a repeated call return the same refund; a refund is
// never re-sent under a different key.
func (c *Client) CreateRefund(ctx context.Context, params url.Values, idempotencyKey string) (*Refund, error) {
	var r Refund
	if err := c.do(ctx, http.MethodPost, "/v1/refunds", idempotencyKey, params, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRefundsByPaymentIntent returns the refunds of a PaymentIntent.
func (c *Client) ListRefundsByPaymentIntent(ctx context.Context, paymentIntentID string) ([]Refund, error) {
	return c.listRefunds(ctx, "payment_intent", paymentIntentID)
}

// ListRefundsByCharge returns the refunds of a charge, which is what a
// refund event names.
func (c *Client) ListRefundsByCharge(ctx context.Context, chargeID string) ([]Refund, error) {
	return c.listRefunds(ctx, "charge", chargeID)
}

// refundListMaxPages caps how far a refund list is followed. A list cut off
// there is an error, never a shorter answer: FindRefund must not report "not
// found" for a refund on a page it did not read.
const refundListMaxPages = 10

// errRefundListTruncated is a refund list with more pages than the cap.
var errRefundListTruncated = errors.New("stripe: refund list has more pages than are read")

func (c *Client) listRefunds(ctx context.Context, filter, id string) ([]Refund, error) {
	var all []Refund
	startingAfter := ""
	for range refundListMaxPages {
		q := url.Values{}
		q.Set(filter, id)
		q.Set("limit", strconv.Itoa(refundListLimit))
		if startingAfter != "" {
			q.Set("starting_after", startingAfter)
		}
		var page RefundPage
		if err := c.do(ctx, http.MethodGet, "/v1/refunds?"+q.Encode(), "", nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if !page.HasMore {
			return all, nil
		}
		if len(page.Data) == 0 {
			break
		}
		startingAfter = page.Data[len(page.Data)-1].ID
	}
	return nil, errRefundListTruncated
}

// ValidPaymentIntentID reports whether id looks like a Stripe PaymentIntent
// id, safe to put in a Dashboard link.
func ValidPaymentIntentID(id string) bool {
	if !strings.HasPrefix(id, "pi_") || len(id) <= 3 {
		return false
	}
	for _, r := range id[3:] {
		if r != '_' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// IsRefundEvent reports whether an event type is one of the refund events:
// charge.refunded (its object is the Charge) and charge.refund.updated (its
// object is the Refund).
func IsRefundEvent(eventType string) bool {
	return eventType == EventChargeRefunded || eventType == EventChargeRefundUpdated
}

// RefundEventTarget returns the charge and PaymentIntent a refund event is
// about. The payload is not trusted to have any particular shape; ok is false
// when it names no charge.
func RefundEventTarget(evt *Event) (chargeID, paymentIntentID string, ok bool) {
	var obj struct {
		ID            string   `json:"id"`
		Charge        ObjectID `json:"charge"`
		PaymentIntent ObjectID `json:"payment_intent"`
	}
	if err := json.Unmarshal(evt.Data.Object, &obj); err != nil {
		return "", "", false
	}
	chargeID = string(obj.Charge)
	if evt.Type == EventChargeRefunded {
		chargeID = obj.ID
	}
	return chargeID, string(obj.PaymentIntent), chargeID != ""
}

// refundKeyPrefix starts the Idempotency-Key of a refund. The key is
// derived from the request key alone: a refund repeated under it returns the
// same refund, whatever else about the call changed.
const refundKeyPrefix = "refund-"

// Refund implements payments.Refunder. The reference is the Checkout
// Session id; its PaymentIntent is what Stripe refunds.
func (p *PaymentProvider) Refund(ctx context.Context, req payments.RefundRequest) (payments.RefundResult, error) {
	piID, err := p.paymentIntentID(ctx, req.Reference)
	if err != nil {
		// Nothing was sent, so the refund certainly does not exist.
		return payments.RefundResult{}, &payments.RefundRejectedError{
			Code: "payment_lookup_failed", Message: "the payment could not be looked up at Stripe", Err: err,
		}
	}
	r, err := p.client.CreateRefund(ctx, p.refundParams(req, piID), refundKeyPrefix+req.RequestKey)
	if err != nil {
		return payments.RefundResult{}, classifyRefundError(err)
	}
	return refundResult(*r, piID), nil
}

// FindRefund implements payments.Refunder: the refund made for a request
// key, found by the key Brygge put in its metadata.
func (p *PaymentProvider) FindRefund(ctx context.Context, reference, requestKey string) (payments.RefundResult, bool, error) {
	piID, err := p.paymentIntentID(ctx, reference)
	if err != nil {
		return payments.RefundResult{}, false, err
	}
	refunds, err := p.client.ListRefundsByPaymentIntent(ctx, piID)
	if err != nil {
		return payments.RefundResult{}, false, err
	}
	for _, r := range refunds {
		if r.Metadata[RefundRequestKeyMetadata] == requestKey {
			return refundResult(r, piID), true, nil
		}
	}
	return payments.RefundResult{}, false, nil
}

// RefundRequestKeyMetadata is the metadata key that carries a refund's
// request key, which the webhook and the balance ingest match it by.
const RefundRequestKeyMetadata = "refund_request_key"

func (p *PaymentProvider) paymentIntentID(ctx context.Context, sessionID string) (string, error) {
	s, err := p.client.GetCheckoutSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if s.PaymentIntent == nil || s.PaymentIntent.ID == "" {
		return "", errors.New("stripe: checkout session has no payment intent")
	}
	return s.PaymentIntent.ID, nil
}

func (p *PaymentProvider) refundParams(req payments.RefundRequest, paymentIntentID string) url.Values {
	v := url.Values{}
	v.Set("payment_intent", paymentIntentID)
	v.Set("amount", strconv.FormatInt(req.AmountOre, 10))
	v.Set("reason", "requested_by_customer")
	v.Set("metadata["+RefundRequestKeyMetadata+"]", req.RequestKey)
	v.Set("metadata[order_id]", req.OrderID)
	v.Set("metadata[attempt_id]", req.AttemptID)
	v.Set("metadata[club_slug]", p.clubSlug)
	return v
}

func refundResult(r Refund, paymentIntentID string) payments.RefundResult {
	res := payments.RefundResult{
		ProviderRefundID: r.ID,
		Status:           r.BryggeStatus(),
		FailureReason:    r.FailureReason,
		PaymentRef:       string(r.PaymentIntent),
		ChargeRef:        string(r.Charge),
	}
	if res.PaymentRef == "" {
		res.PaymentRef = paymentIntentID
	}
	return res
}

// classifyRefundError separates a refund Stripe refused from one whose
// outcome is unknown (PAY-12). Only a parseable Stripe error object (it has a
// type) answered with a 4xx that does not mean "try again" is a refusal: an
// unparseable body may come from a proxy or an edge in front of a request
// that did arrive, and a timeout (408), a rate limit or lock timeout (429), a
// conflict (409), an idempotency refusal, a key still in use and every 5xx
// leave the first call's outcome unknown.
func classifyRefundError(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || isIdempotencyError(err) || apiErr.Type == typeUnparseable {
		return err
	}
	switch apiErr.Status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
		return err
	}
	if apiErr.Status < 400 || apiErr.Status >= 500 || apiErr.Code == codeLockTimeout {
		return err
	}
	code := apiErr.Code
	if code == "" {
		code = apiErr.Type
	}
	return &payments.RefundRejectedError{Code: code, Message: apiErr.Message, Err: err}
}

// codeLockTimeout is Stripe's answer to a request that met another request
// for the same object still running.
const codeLockTimeout = "lock_timeout"
