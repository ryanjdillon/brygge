// Package payments is the provider-neutral seam between checkout orders and
// the services that charge them. Handlers decide what is owed and where the
// payer returns; a Provider turns that into a hosted payment page. Marking an
// order paid stays outside this package: each provider's signed webhook feeds
// the same order-settlement code in the handlers package.
package payments

import (
	"context"
	"errors"
	"sort"
)

// ErrNotSupported is returned by a Provider for an operation it does not
// offer, so callers can tell "not available here" apart from a failed call.
var ErrNotSupported = errors.New("payments: operation not supported by provider")

// Request describes one payment attempt for a checkout order.
type Request struct {
	OrderID string
	// AmountOre is the total to charge in the currency's minor unit.
	AmountOre int64
	Currency  string
	// Description is shown to the payer; providers shorten it to their own
	// limit, so callers pass the full text.
	Description string
	// IsGuest marks an order with no linked member. Providers that can ask the
	// payer for their name do so only then, so the club learns who paid.
	IsGuest bool
	// ReturnURL is where the payer's browser lands after paying or aborting.
	// Providers with a separate cancel destination send a completed payment
	// here and an abandoned one to CancelURL.
	ReturnURL string
	// CancelURL is where a payer who backs out of the payment page lands.
	CancelURL string
	// AttemptID is the payment_attempts row this payment belongs to. It is
	// unique per attempt, so a provider that supports idempotency keys can
	// use it to make a retried create return the same payment.
	AttemptID string
	// PayerEmail is the linked member's email ("" for guests), for
	// providers that prefill it and send their receipt there.
	PayerEmail string
}

// Result is a created payment: the short-lived page to send the payer to, and
// the provider's reference that its webhooks will carry back.
type Result struct {
	RedirectURL string
	Reference   string
}

// Provider creates and refunds payments with one payment service.
type Provider interface {
	// Name is the stable identifier callers select the provider by.
	Name() string
	CreatePayment(ctx context.Context, req Request) (Result, error)
	// Refund returns money for a captured payment and the provider's id for
	// the refund, or ErrNotSupported.
	Refund(ctx context.Context, reference string, amountOre int64, reason string) (providerRefundID string, err error)
}

// Canceller is implemented by providers that can void a payment which is
// authorized but not yet captured.
type Canceller interface {
	CancelPayment(ctx context.Context, reference string) error
}

// Registry holds the providers this deployment has credentials for. A nil
// *Registry is valid and has none configured, so an instance without any
// payment credentials needs no special casing.
type Registry struct {
	byName map[string]Provider
}

// NewRegistry returns a registry of the given providers. Nil entries are
// skipped; a later provider with the same name replaces an earlier one.
func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{byName: make(map[string]Provider, len(providers))}
	for _, p := range providers {
		if p != nil {
			r.byName[p.Name()] = p
		}
	}
	return r
}

// Names returns the registered provider names in sorted order.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Get returns the provider registered under name.
func (r *Registry) Get(name string) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.byName[name]
	return p, ok
}

// Configured reports whether any provider is available for online payment.
func (r *Registry) Configured() bool {
	return r != nil && len(r.byName) > 0
}
