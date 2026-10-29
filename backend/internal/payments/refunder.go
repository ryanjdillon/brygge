package payments

import (
	"context"
	"errors"
	"fmt"
)

// Refunder is implemented by providers that can return money for a captured
// payment. It is optional, like Canceller: a provider that does not
// implement it (Vipps, until BRY-14) has its refund button hidden and the
// refund endpoint answers not_supported.
type Refunder interface {
	// Refund asks the provider to return AmountOre of the payment at
	// req.Reference. The request key makes it idempotent at the provider: a
	// call repeated with the same key never refunds twice, and a refund must
	// never be re-sent under a different key (use FindRefund to learn what a
	// call whose answer was lost did).
	//
	// A *RefundRejectedError means the provider certainly did not create the
	// refund. Any other error is uncertain: the refund may exist.
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
	// FindRefund returns the refund the provider made for a request key on
	// the payment at reference, with false when it has none.
	FindRefund(ctx context.Context, reference, requestKey string) (RefundResult, bool, error)
}

// RefundRequest describes one refund of a captured payment.
type RefundRequest struct {
	// Reference is the provider's reference of the payment attempt (a Stripe
	// Checkout Session id).
	Reference  string
	AmountOre  int64
	Currency   string
	RequestKey string
	// OrderID and AttemptID travel with the refund so it can be traced in
	// the provider's dashboard and matched when its webhook arrives.
	OrderID   string
	AttemptID string
}

// Refund states, in Brygge's vocabulary (payment_refunds.status).
const (
	RefundRequested = "requested"
	RefundSucceeded = "succeeded"
	RefundFailed    = "failed"
)

// RefundResult is a refund as the provider reports it.
type RefundResult struct {
	ProviderRefundID string
	// Status is RefundRequested while the provider has not finished it.
	Status string
	// FailureReason is the provider's word for why a failed refund failed.
	FailureReason string
	// PaymentRef and ChargeRef are the provider's ids of the payment and
	// the charge the refund returns money for.
	PaymentRef string
	ChargeRef  string
}

// RefundRejectedError is a refund the provider certainly did not make: it
// refused the request, or the request never left. The row is marked failed
// and the person may try again with a new request.
type RefundRejectedError struct {
	// Code is the provider's error code when it gave one.
	Code    string
	Message string
	Err     error
}

func (e *RefundRejectedError) Error() string {
	return fmt.Sprintf("payments: refund rejected: %s", e.Message)
}

func (e *RefundRejectedError) Unwrap() error { return e.Err }

// IsRefundRejected reports whether err is a refund the provider certainly
// did not make.
func IsRefundRejected(err error) (*RefundRejectedError, bool) {
	var rej *RefundRejectedError
	return rej, errors.As(err, &rej)
}

// RefunderFor returns the Refunder registered under name, if it is one.
func (r *Registry) RefunderFor(name string) (Refunder, bool) {
	p, ok := r.Get(name)
	if !ok {
		return nil, false
	}
	ref, ok := p.(Refunder)
	return ref, ok
}

// RefundProviders returns the names of the registered providers that can
// refund, sorted.
func (r *Registry) RefundProviders() []string {
	var names []string
	for _, n := range r.Names() {
		if _, ok := r.RefunderFor(n); ok {
			names = append(names, n)
		}
	}
	return names
}
