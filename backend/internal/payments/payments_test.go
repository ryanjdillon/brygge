package payments_test

import (
	"context"
	"testing"

	"github.com/brygge-klubb/brygge/internal/payments"
)

type stubProvider struct {
	name    string
	version int
}

func (s stubProvider) Name() string { return s.name }

func (stubProvider) CreatePayment(context.Context, payments.Request) (payments.Result, error) {
	return payments.Result{}, nil
}

func (stubProvider) Refund(context.Context, string, int64, string) (string, error) {
	return "", payments.ErrNotSupported
}

func TestRegistry(t *testing.T) {
	first := stubProvider{name: "alpha"}
	second := stubProvider{name: "beta"}

	tests := []struct {
		name           string
		reg            *payments.Registry
		wantConfigured bool
		lookup         string
		want           payments.Provider
	}{
		{"nil registry", nil, false, "alpha", nil},
		{"empty registry", payments.NewRegistry(), false, "alpha", nil},
		{"nil entries skipped", payments.NewRegistry(nil), false, "alpha", nil},
		{"found", payments.NewRegistry(first, second), true, "beta", second},
		{"unknown name", payments.NewRegistry(first), true, "beta", nil},
		{"later duplicate wins", payments.NewRegistry(first, stubProvider{name: "alpha", version: 2}), true, "alpha", stubProvider{name: "alpha", version: 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.reg.Configured(); got != tc.wantConfigured {
				t.Errorf("Configured() = %v, want %v", got, tc.wantConfigured)
			}
			got, ok := tc.reg.Get(tc.lookup)
			if ok != (tc.want != nil) || got != tc.want {
				t.Errorf("Get(%q) = %v, %v; want %v", tc.lookup, got, ok, tc.want)
			}
		})
	}
}

func TestRegistryNames(t *testing.T) {
	tests := []struct {
		name string
		reg  *payments.Registry
		want []string
	}{
		{"nil registry", nil, nil},
		{"empty registry", payments.NewRegistry(), []string{}},
		{"sorted", payments.NewRegistry(stubProvider{name: "vipps"}, stubProvider{name: "stripe"}), []string{"stripe", "vipps"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.reg.Names()
			if len(got) != len(tc.want) {
				t.Fatalf("Names() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Names() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
