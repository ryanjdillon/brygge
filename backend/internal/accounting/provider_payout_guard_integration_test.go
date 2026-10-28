package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// manualAssigns runs each manual assign on row, with other as the second
// row of the two-row assigns (other is 50 kr; row is 100 kr).
func manualAssigns(f *stripeFixture) map[string]func(t *testing.T, row, other string) error {
	ctx := context.Background()
	return map[string]func(t *testing.T, row, other string) error{
		"invoice": func(t *testing.T, row, _ string) error {
			inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 10000})
			_, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID)
			return err
		},
		"invoice multi": func(t *testing.T, row, other string) error {
			inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 15000})
			_, err := f.svc.AssignMultipleBankRowsToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, []string{other, row}, inv.ID)
			return err
		},
		"account": func(_ *testing.T, row, _ string) error {
			_, err := f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, row, "3900", "revenue", "")
			return err
		},
		"account group": func(_ *testing.T, row, other string) error {
			_, err := f.svc.AssignBankRowsToAccountGroup(ctx, f.club.ID, f.club.Treasurer.ID, []string{other, row}, "3900", "")
			return err
		},
	}
}

// Every manual assign refuses a Vipps settlement and, once the club uses
// Stripe, a Stripe payout deposit, booking nothing; an ordinary row still
// books through each of them.
func TestManualAssignsRefuseProviderPayouts(t *testing.T) {
	f := newStripeFixture(t)
	markUsesStripe(t, f.club)
	imp := f.club.NewBankImport(t)
	day := time.Date(f.club.Year, 6, 3, 0, 0, 0, 0, time.UTC)
	plain := func(t *testing.T, cents int64) string {
		return f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: day, Cents: cents, Counterpart: "Ola Testmann"})
	}

	for name, assign := range manualAssigns(f) {
		for kind, desc := range map[string]string{
			ProviderPayoutVipps:  "Utb. 3000100 Vippsnr 548005",
			ProviderPayoutStripe: "STRIPE PAYOUT",
		} {
			row := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: day, Cents: 10000, Description: desc})
			other := plain(t, 5000)
			err := assign(t, row, other)
			var refusal *ProviderPayoutDepositError
			if !errors.Is(err, ErrProviderPayoutDeposit) || !errors.As(err, &refusal) || refusal.Kind != kind || refusal.RowID != row {
				t.Errorf("%s of a %s payout: %v, want ProviderPayoutDepositError for the row", name, kind, err)
			}
			if bankRowEntry(t, f.club, row) != "" || bankRowEntry(t, f.club, other) != "" {
				t.Errorf("%s of a %s payout booked a row", name, kind)
			}
		}
		row, other := plain(t, 10000), plain(t, 5000)
		if err := assign(t, row, other); err != nil {
			t.Errorf("%s of an ordinary row: %v", name, err)
		} else if bankRowEntry(t, f.club, row) == "" {
			t.Errorf("%s of an ordinary row booked nothing", name)
		}
	}
}

// Before the club uses Stripe a deposit that merely mentions it is an
// ordinary row.
func TestManualAssignOfStripeTextWithoutStripe(t *testing.T) {
	f := newStripeFixture(t)
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 10000, Description: "STRIPE PAYOUT"})
	if _, err := f.svc.AssignBankRowToAccount(context.Background(), f.club.ID, f.club.Treasurer.ID, row, "3900", "revenue", ""); err != nil {
		t.Errorf("assign: %v", err)
	}
}
