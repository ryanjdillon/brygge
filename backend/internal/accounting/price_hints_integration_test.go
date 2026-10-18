package accounting

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func seedPriceItem(t *testing.T, club *fin.Club, category, name, audience, unit string, amount float64, sortOrder int, active bool) {
	t.Helper()
	if _, err := club.Pool.Exec(context.Background(),
		`INSERT INTO price_items (club_id, category, name, amount, unit, audience, sort_order, is_active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		club.ID, category, name, amount, unit, audience, sortOrder, active,
	); err != nil {
		t.Fatalf("seed price item %s: %v", name, err)
	}
}

// The accounts list names, per account, the active price items that book to
// it by default and the categories that map to it. Inactive items and
// categories outside the price-item vocabulary never show, and a member and
// a non-member price under one name show once.
func TestListAccountsWithPriceHints(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))

	seedPriceItem(t, club, "service", "Slipping stor båt", "all", "once", 1600, 2, true)
	seedPriceItem(t, club, "service", "Slipping liten båt", "member", "once", 800, 1, true)
	seedPriceItem(t, club, "service", "Slipping liten båt", "non_member", "once", 1200, 1, true)
	seedPriceItem(t, club, "service", "Gammel slipping", "all", "once", 500, 0, false)
	seedPriceItem(t, club, "merchandise", "Caps", "all", "once", 150, 0, true)

	accounts, err := svc.ListAccountsWithPriceHints(context.Background(), club.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byCode := map[string]AccountWithPriceHints{}
	for _, a := range accounts {
		byCode[a.Code] = a
	}

	sales := byCode["3300"]
	var names []string
	for _, it := range sales.PriceItems {
		names = append(names, it.Name)
		if it.Category != "service" {
			t.Errorf("3300 item %q has category %q, want service", it.Name, it.Category)
		}
	}
	if len(names) != 2 || names[0] != "Slipping liten båt" || names[1] != "Slipping stor båt" {
		t.Errorf("3300 price items = %v, want [Slipping liten båt Slipping stor båt]", names)
	}
	if len(sales.PriceCategories) != 1 || sales.PriceCategories[0] != "service" {
		t.Errorf("3300 price categories = %v, want [service]", sales.PriceCategories)
	}

	slip := byCode["3120"]
	if len(slip.PriceCategories) != 2 || slip.PriceCategories[0] != "slip_fee" || slip.PriceCategories[1] != "seasonal_rental" {
		t.Errorf("3120 price categories = %v, want [slip_fee seasonal_rental]", slip.PriceCategories)
	}
	bank := byCode["1920"]
	if bank.PriceItems == nil || len(bank.PriceItems) != 0 || bank.PriceCategories == nil || len(bank.PriceCategories) != 0 {
		t.Errorf("1920 hints = %v / %v, want empty arrays", bank.PriceItems, bank.PriceCategories)
	}
}

// A Vipps charge gets account suggestions from the price items its amount
// fits, the same pass bank rows get.
func TestVippsChargeSuggestionsPriceItem(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))

	seedPriceItem(t, club, "service", "Slipping stor båt", "all", "once", 1600, 1, true)
	seedPriceItem(t, club, "guest", "Gjesteplass", "all", "night", 250, 2, true)

	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, CustomerName: "Kari Testdotter"})
	res, err := svc.VippsChargeSuggestionsFor(context.Background(), club.ID, charge)
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	if len(res.Accounts) == 0 || res.Accounts[0].Code != "3300" || res.Accounts[0].ConfidenceLabel != "sannsynleg" {
		t.Fatalf("accounts = %+v, want 3300 sannsynleg first", res.Accounts)
	}
	if res.Accounts[0].WhyTooltip != "Slipping stor båt" || res.Accounts[0].Reason != "price_item" {
		t.Errorf("why = %q, reason = %q; want the price item name, price_item", res.Accounts[0].WhyTooltip, res.Accounts[0].Reason)
	}

	nights := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 75000, CustomerName: "Kari Testdotter"})
	res, err = svc.VippsChargeSuggestionsFor(context.Background(), club.ID, nights)
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	if len(res.Accounts) == 0 || res.Accounts[0].Code != "3200" || res.Accounts[0].WhyTooltip != "3x Gjesteplass" {
		t.Errorf("accounts = %+v, want 3200 for 3x Gjesteplass first", res.Accounts)
	}

	// 1 600.01 fits no price item: the default revenue cohort, all weak.
	odd := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160001, CustomerName: "Kari Testdotter"})
	res, err = svc.VippsChargeSuggestionsFor(context.Background(), club.ID, odd)
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	for _, a := range res.Accounts {
		if a.ConfidenceLabel != "svak" {
			t.Errorf("unmatched amount suggested %s as %s, want only weak defaults", a.Code, a.ConfidenceLabel)
		}
	}
}
