package accounting

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A Vipps charge has no bank row, so its candidate list must come from the
// amount alone: the open, unpaid fakturas of exactly that amount, of the
// charge's own club, narrowed by q and capped by limit.
func TestPotentialInvoicesForVippsCharge(t *testing.T) {
	testutil.SkipIfNoDB(t)
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	other := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))

	match := club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	second := club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	club.NewInvoice(t, fin.InvoiceOpts{Cents: 45100})
	club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Paid: true})
	club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Status: "voided"})
	foreign := other.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})

	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Kari Testdotter"})
	fee := club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: 45000})

	got, err := svc.PotentialInvoicesForVippsCharge(ctx, club.ID, charge, "", 0)
	if err != nil {
		t.Fatalf("potential invoices: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range got {
		ids[s.InvoiceID] = true
	}
	if len(got) != 2 || !ids[match.ID] || !ids[second.ID] || ids[foreign.ID] {
		t.Fatalf("candidates = %+v, want the club's two open 450 kr fakturas", got)
	}

	got, err = svc.PotentialInvoicesForVippsCharge(ctx, club.ID, charge, "", 1)
	if err != nil || len(got) != 1 {
		t.Errorf("limit 1: %d items, err %v", len(got), err)
	}
	got, err = svc.PotentialInvoicesForVippsCharge(ctx, club.ID, charge, strconv.Itoa(match.Number), 0)
	if err != nil || len(got) != 1 || got[0].InvoiceID != match.ID {
		t.Errorf("q by number: %+v, err %v", got, err)
	}

	if _, err := svc.PotentialInvoicesForVippsCharge(ctx, other.ID, charge, "", 0); !errors.Is(err, ErrVippsChargeNotFound) {
		t.Errorf("another club's charge: err = %v, want ErrVippsChargeNotFound", err)
	}
	if _, err := svc.PotentialInvoicesForVippsCharge(ctx, club.ID, fee, "", 0); !errors.Is(err, ErrVippsChargeNotFound) {
		t.Errorf("a Vipps fee row: err = %v, want ErrVippsChargeNotFound", err)
	}

	// A zero-amount charge matches nothing; it must not fall through to
	// fakturas of amount 0.
	zero := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 0})
	if got, err := svc.PotentialInvoicesForVippsCharge(ctx, club.ID, zero, "", 0); err != nil || got == nil || len(got) != 0 {
		t.Errorf("zero-amount charge: %+v, err %v, want empty list", got, err)
	}
}
