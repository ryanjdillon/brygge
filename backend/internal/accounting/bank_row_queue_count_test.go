package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// The Tildel badge, the tab's year dropdown and the list itself are one set of
// rows (tildelScope): dismissed rows, rows a refund pair has booked and a
// pending refund (a dismissed row) are out; ordinary rows and provider payout
// deposits, which the tab lists, are in.
func TestTildelCountsMatchTheList(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	markUsesStripe(t, club)
	imp := club.NewBankImport(t)
	actor := club.Treasurer.ID

	day := func(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC) }
	prev := club.Year - 1

	// In Tildel: two ordinary rows (one in the previous year), a Vipps
	// settlement and a Stripe payout deposit.
	club.NewBankRow(t, imp, fin.BankRowOpts{Date: day(club.Year, 6, 1), Cents: 45000})
	club.NewBankRow(t, imp, fin.BankRowOpts{Date: day(prev, 12, 20), Cents: -12000})
	club.NewBankRow(t, imp, fin.BankRowOpts{Date: day(club.Year, 6, 2), Cents: 30000, Description: "Utb. 2000001 Vippsnr 123456"})
	club.NewBankRow(t, imp, fin.BankRowOpts{Date: day(club.Year, 6, 3), Cents: 80000, Description: "STRIPE PAYOUT"})

	// Out: a row dismissed for good, and a pending refund (dismissed with a
	// refund reason, no outbound transfer paired yet).
	gone := club.NewBankRow(t, imp, fin.BankRowOpts{Date: day(club.Year, 6, 4), Cents: 1000})
	pending := club.NewBankRow(t, imp, fin.BankRowOpts{Date: day(club.Year, 6, 5), Cents: 20000, KID: "1234567"})
	for id, reason := range map[string]string{gone: "bank_fee", pending: "double_payment"} {
		if err := svc.DismissBankRow(ctx, club.ID, actor, id, reason); err != nil {
			t.Fatalf("dismiss %s: %v", reason, err)
		}
	}

	// Out: a refund pair, both rows booked by PairRefund.
	inbound := club.NewBankRow(t, imp, fin.BankRowOpts{Date: day(club.Year, 6, 6), Cents: 50000, KID: "7654321"})
	outbound := club.NewBankRow(t, imp, fin.BankRowOpts{Date: day(club.Year, 6, 7), Cents: -50000})
	if err := svc.DismissBankRow(ctx, club.ID, actor, inbound, "double_payment"); err != nil {
		t.Fatalf("dismiss inbound: %v", err)
	}
	if _, _, err := svc.PairRefund(ctx, club.ID, actor, inbound, outbound); err != nil {
		t.Fatalf("pair refund: %v", err)
	}

	for _, year := range []int{0, club.Year, prev, club.Year + 1} {
		rows, err := svc.ListUnmatchedBankRows(ctx, club.ID, "", "", year, 200, 0)
		if err != nil {
			t.Fatalf("list year %d: %v", year, err)
		}
		n, err := svc.CountUnmatchedBankRows(ctx, club.ID, year)
		if err != nil {
			t.Fatalf("count year %d: %v", year, err)
		}
		if n != len(rows) {
			t.Errorf("year %d: badge counts %d rows, the tab lists %d", year, n, len(rows))
		}
	}

	all, err := svc.CountUnmatchedBankRows(ctx, club.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if all != 4 {
		t.Errorf("count = %d, want 4 (two ordinary rows, a Vipps and a Stripe payout)", all)
	}

	byYear, err := svc.CountUnmatchedBankRowsByYear(ctx, club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if byYear[club.Year] != 3 || byYear[prev] != 1 || len(byYear) != 2 {
		t.Errorf("by year = %v, want {%d: 3, %d: 1}", byYear, club.Year, prev)
	}

	// The refund tab owns the pending refund; the two never overlap.
	refunds, err := svc.CountPendingRefunds(ctx, club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refunds != 1 {
		t.Errorf("pending refunds = %d, want 1", refunds)
	}
}
