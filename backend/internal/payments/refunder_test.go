package payments_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/brygge-klubb/brygge/internal/payments"
)

type refundingProvider struct{ stubProvider }

func (refundingProvider) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	return payments.RefundResult{}, nil
}

func (refundingProvider) FindRefund(context.Context, string, string) (payments.RefundResult, bool, error) {
	return payments.RefundResult{}, false, nil
}

func TestRegistryRefunders(t *testing.T) {
	reg := payments.NewRegistry(stubProvider{name: "vipps"}, refundingProvider{stubProvider{name: "stripe"}})
	if _, ok := reg.RefunderFor("stripe"); !ok {
		t.Error("stripe is not a Refunder")
	}
	if _, ok := reg.RefunderFor("vipps"); ok {
		t.Error("a provider without Refund is a Refunder")
	}
	if _, ok := reg.RefunderFor("unknown"); ok {
		t.Error("an unknown provider is a Refunder")
	}
	if got := reg.RefundProviders(); len(got) != 1 || got[0] != "stripe" {
		t.Errorf("RefundProviders() = %v, want [stripe]", got)
	}
	var none *payments.Registry
	if _, ok := none.RefunderFor("stripe"); ok || len(none.RefundProviders()) != 0 {
		t.Error("a nil registry has refunders")
	}
}

func TestRefundRejectedError(t *testing.T) {
	err := fmt.Errorf("sending: %w", &payments.RefundRejectedError{Code: "charge_disputed", Message: "disputed"})
	rej, ok := payments.IsRefundRejected(err)
	if !ok || rej.Code != "charge_disputed" {
		t.Errorf("IsRefundRejected(%v) = %v, %v", err, rej, ok)
	}
	if _, ok := payments.IsRefundRejected(errors.New("timeout")); ok {
		t.Error("a plain error is a rejection")
	}
}
