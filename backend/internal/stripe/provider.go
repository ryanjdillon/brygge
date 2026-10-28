package stripe

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/payments"
)

// ProviderName identifies Stripe in a payments.Registry.
const ProviderName = "stripe"

// maxProductNameRunes keeps the line item on the hosted page readable; Stripe
// itself accepts longer names. Counted in runes so a cut never splits æ/ø/å.
const maxProductNameRunes = 120

// maxIntentDescriptionRunes is Stripe's limit on a PaymentIntent
// description.
const maxIntentDescriptionRunes = 1000

// sessionLifetime is how long a Checkout Session can be paid. Stripe accepts
// 30 minutes to 24 hours; an hour covers a payer who is slow with their card
// without leaving a stale page payable all day.
const sessionLifetime = 60 * time.Minute

// ErrNotCancellable is returned by CancelPayment for a session whose payment
// was already captured: it can only be refunded.
var ErrNotCancellable = errors.New("stripe: payment already captured")

// PaymentProvider adapts the client to payments.Provider using hosted
// Checkout with manual capture: a completed session is an authorization,
// which the webhook captures or voids.
type PaymentProvider struct {
	client   *Client
	clubSlug string
	now      func() time.Time
}

var (
	_ payments.Provider  = (*PaymentProvider)(nil)
	_ payments.Canceller = (*PaymentProvider)(nil)
)

// NewPaymentProvider wraps a client as a payments.Provider. clubSlug is
// recorded in the payment's metadata, so a payment in the shared Stripe
// Dashboard can be traced to its deployment.
func NewPaymentProvider(client *Client, clubSlug string) *PaymentProvider {
	return &PaymentProvider{client: client, clubSlug: clubSlug, now: time.Now}
}

// Name implements payments.Provider.
func (p *PaymentProvider) Name() string { return ProviderName }

// CreatePayment creates a card-only Checkout Session for the attempt. The
// attempt id is the idempotency key, so a create retried after a lost answer
// returns the same session instead of a second payable page.
func (p *PaymentProvider) CreatePayment(ctx context.Context, req payments.Request) (payments.Result, error) {
	if req.AttemptID == "" {
		return payments.Result{}, errors.New("stripe: payment request without attempt id")
	}
	s, err := p.client.CreateCheckoutSession(ctx, p.sessionParams(req), req.AttemptID)
	if err != nil {
		return payments.Result{}, err
	}
	if s.ID == "" || s.URL == "" {
		return payments.Result{}, fmt.Errorf("stripe: checkout session without id or url")
	}
	return payments.Result{RedirectURL: s.URL, Reference: s.ID}, nil
}

// sessionParams maps a request onto the Checkout Session form. Only card is
// enabled (Apple Pay and Google Pay are card wallets), rather than automatic
// payment methods, so a Dashboard toggle cannot add a method the webhook does
// not handle. The order token stays out of metadata: it is the pay link's
// capability and the Dashboard is not.
func (p *PaymentProvider) sessionParams(req payments.Request) url.Values {
	v := url.Values{}
	v.Set("mode", "payment")
	v.Set("payment_method_types[]", "card")
	v.Set("line_items[0][quantity]", "1")
	v.Set("line_items[0][price_data][currency]", strings.ToLower(req.Currency))
	v.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(req.AmountOre, 10))
	v.Set("line_items[0][price_data][product_data][name]", truncateRunes(req.Description, maxProductNameRunes))
	if email, ok := bareEmail(req.PayerEmail); ok {
		v.Set("customer_email", email)
	}
	v.Set("payment_intent_data[capture_method]", "manual")
	v.Set("payment_intent_data[description]", truncateRunes(req.Description, maxIntentDescriptionRunes))
	for k, val := range p.metadata(req) {
		v.Set("metadata["+k+"]", val)
		v.Set("payment_intent_data[metadata]["+k+"]", val)
	}
	v.Set("success_url", req.ReturnURL)
	v.Set("cancel_url", req.CancelURL)
	v.Set("expires_at", strconv.FormatInt(p.now().Add(sessionLifetime).Unix(), 10))
	return v
}

// metadata is copied onto both the session and its PaymentIntent, so a
// payment_intent.* event can name its attempt without a session lookup.
func (p *PaymentProvider) metadata(req payments.Request) map[string]string {
	return map[string]string{
		"attempt_id": req.AttemptID,
		"order_id":   req.OrderID,
		"club_slug":  p.clubSlug,
	}
}

// Refund is not supported yet; BRY-446 implements Stripe refunds here.
func (p *PaymentProvider) Refund(context.Context, string, int64, string) (string, error) {
	return "", payments.ErrNotSupported
}

// CancelPayment makes the session at reference unable to take money: an
// authorized PaymentIntent is voided, an open session is expired. A session
// that is already expired or whose PaymentIntent is already cancelled needs
// nothing; a captured one returns ErrNotCancellable.
func (p *PaymentProvider) CancelPayment(ctx context.Context, reference string) error {
	s, err := p.client.GetCheckoutSession(ctx, reference)
	if err != nil {
		return err
	}
	if pi := s.PaymentIntent; pi != nil && pi.Object != nil {
		switch pi.Object.Status {
		case PaymentIntentRequiresCapture:
			return p.client.CancelPaymentIntent(ctx, pi.ID)
		case PaymentIntentSucceeded:
			return ErrNotCancellable
		}
	}
	if s.Status == SessionOpen {
		return p.client.ExpireCheckoutSession(ctx, s.ID)
	}
	return nil
}

// bareEmail returns s when it is a plain address. Stripe rejects the whole
// session on an invalid customer_email, so anything else (a display-name
// form, a typo in the member register) is left out and the payer types it
// on the hosted page instead.
func bareEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return "", false
	}
	return s, true
}

// truncateRunes shortens s to at most n runes.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}
