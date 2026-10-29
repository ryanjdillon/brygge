package vipps

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/brygge-klubb/brygge/internal/payments"
)

// ProviderName identifies Vipps in a payments.Registry.
const ProviderName = "vipps"

// maxDescriptionLen is the ePayment API's limit on paymentDescription.
const maxDescriptionLen = 100

// PaymentProvider adapts the ePayment client to payments.Provider.
type PaymentProvider struct {
	client *Client
}

var (
	_ payments.Provider  = (*PaymentProvider)(nil)
	_ payments.Canceller = (*PaymentProvider)(nil)
)

// NewPaymentProvider wraps an ePayment client as a payments.Provider.
func NewPaymentProvider(client *Client) *PaymentProvider {
	return &PaymentProvider{client: client}
}

// Name implements payments.Provider.
func (p *PaymentProvider) Name() string { return ProviderName }

// CreatePayment starts a WEB_REDIRECT ePayment under a fresh reference. The
// reference doubles as the idempotency key: Vipps rejects a reused reference,
// so a retried attempt is a new payment rather than a replay. Guests are asked
// to share their name, which the CAPTURED webhook copies onto the order;
// members are not, to spare them the consent step.
func (p *PaymentProvider) CreatePayment(ctx context.Context, req payments.Request) (payments.Result, error) {
	ref := newReference()
	desc := req.Description
	if len(desc) > maxDescriptionLen {
		desc = desc[:maxDescriptionLen]
	}
	var profile *Profile
	if req.IsGuest {
		profile = &Profile{Scope: "name"}
	}
	res, err := p.client.CreatePayment(ctx, ref, CreatePaymentRequest{
		Amount:             Amount{Currency: req.Currency, Value: req.AmountOre},
		Reference:          ref,
		ReturnURL:          req.ReturnURL,
		PaymentDescription: desc,
		UserFlow:           "WEB_REDIRECT",
		Profile:            profile,
	})
	if err != nil {
		return payments.Result{}, err
	}
	return payments.Result{RedirectURL: res.RedirectURL, Reference: ref}, nil
}

// CancelPayment implements payments.Canceller.
func (p *PaymentProvider) CancelPayment(ctx context.Context, reference string) error {
	return p.client.CancelPayment(ctx, reference)
}

// newReference returns a globally unique, Vipps-safe payment reference
// ([a-zA-Z0-9-], 8–50 chars).
func newReference() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "chk-" + hex.EncodeToString(b[:])
}
