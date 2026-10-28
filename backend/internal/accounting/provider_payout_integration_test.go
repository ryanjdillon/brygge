package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// markUsesStripe records a Stripe payout for the club, as the first payout
// ingest does, so its Stripe payout descriptor is in force.
func markUsesStripe(t *testing.T, c *fin.Club) {
	t.Helper()
	if _, err := c.Pool.Exec(context.Background(),
		`INSERT INTO stripe_payouts (club_id, payout_id, amount_ore, currency, arrival_date, status, automatic)
		 VALUES ($1, 'po_test_marker', 100, 'nok', make_date($2, 1, 2), 'failed', true)`, c.ID, c.Year); err != nil {
		t.Fatal(err)
	}
}

func partnerIDs(t *testing.T, svc *Service, clubID, rowID string) map[string]bool {
	t.Helper()
	ps, err := svc.BankRowPartners(context.Background(), clubID, rowID)
	if err != nil {
		t.Fatalf("partners: %v", err)
	}
	out := map[string]bool{}
	for _, p := range ps {
		out[p.ID] = true
	}
	return out
}

// A Stripe payout deposit gets no partners and is no one's partner, once
// the club uses Stripe; before that the same text is an ordinary row.
func TestStripeDepositHasNoPartners(t *testing.T) {
	f := newStripeFixture(t)
	imp := f.club.NewBankImport(t)
	day := time.Date(f.club.Year, 5, 4, 0, 0, 0, 0, time.UTC)
	in := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: day, Cents: 10000, Description: "STRIPE PAYOUT",
		Counterpart: "Kari Testdotter"})
	out := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: day.AddDate(0, 0, 1), Cents: -10000,
		Description: "Synthetic return", Counterpart: "Kari Testdotter"})

	if !partnerIDs(t, f.svc, f.club.ID, in)[out] {
		t.Fatal("before the club uses Stripe the rows should be partners; the test proves nothing")
	}
	markUsesStripe(t, f.club)
	if got := partnerIDs(t, f.svc, f.club.ID, in); len(got) != 0 {
		t.Errorf("Stripe deposit partners = %v, want none", got)
	}
	if got := partnerIDs(t, f.svc, f.club.ID, out); got[in] {
		t.Errorf("the Stripe deposit is suggested as a partner of %s", out)
	}
}

// A mapping rule on a provider's text never draft-books its payout
// deposit; it still books an outgoing row naming Stripe and other rows.
func TestMappingRulesSkipProviderPayouts(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	markUsesStripe(t, f.club)
	imp := f.club.NewBankImport(t)
	stripeRow := f.club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 20000, Description: "STRIPE PAYOUT"})
	vippsRow := f.club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 30000, Description: "Utb. 3000100 Vippsnr 548005"})
	stripeBill := f.club.NewBankRow(t, imp, fin.BankRowOpts{Cents: -2000, Description: "Stripe faktura"})
	for _, pattern := range []string{"%stripe%", "%vippsnr%"} {
		if _, err := f.svc.CreateRule(ctx, f.club.ID, CreateRuleInput{
			Name: "Synthetic " + pattern, Priority: 1, MatchField: "description", MatchValue: pattern,
			MatchOperator: "like", DebitAccountCode: "1920", CreditAccountCode: "3900", MVAEligible: MVANotApplicable,
		}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := f.svc.AutoMatchImport(ctx, f.club.ID, imp, f.club.PeriodID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || bankRowEntry(t, f.club, stripeBill) == "" {
		t.Errorf("matched %d, want only the outgoing Stripe bill", n)
	}
	for _, id := range []string{stripeRow, vippsRow} {
		if bankRowEntry(t, f.club, id) != "" {
			t.Errorf("provider payout %s was booked by a mapping rule", id)
		}
	}
}

// Bank auto-assign leaves a Stripe payout deposit alone even when its
// counterpart and amount would settle a member's faktura.
func TestBankAutoAssignSkipsStripePayout(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Payer")
	f.faktura(t, m, 45000)
	row := f.deposit(t, "Synthetic Payer", 45000, fin.BankRowOpts{Description: "STRIPE PAYOUT"})

	preview, err := f.svc.PreviewBankAutoAssign(ctx, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.WouldAssign != 1 {
		t.Fatalf("preview before Stripe use = %+v, want the row assignable; the test proves nothing", preview)
	}
	markUsesStripe(t, f.club)
	res := f.run(t)
	if len(res.Assigned) != 0 || f.entry(t, row) != "" {
		t.Errorf("auto-assign = %+v, want the Stripe deposit left alone", res)
	}
}

// A Stripe payout deposit gets no price_item account suggestion.
func TestPriceSuggestionsSkipStripePayout(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	seedPriceItem(t, f.club, "service", "Slipping stor båt", "all", "once", 1600, 1, true)
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 160000, Description: "STRIPE PAYOUT"})

	priceItem := func() bool {
		res, err := f.svc.BankRowSuggestionsFor(ctx, f.club.ID, row)
		if err != nil {
			t.Fatalf("suggestions: %v", err)
		}
		for _, a := range res.Accounts {
			if a.Reason == "price_item" {
				return true
			}
		}
		return false
	}
	if !priceItem() {
		t.Fatal("before Stripe use the row should get a price_item suggestion; the test proves nothing")
	}
	markUsesStripe(t, f.club)
	if priceItem() {
		t.Error("Stripe payout got a price_item suggestion")
	}
}

// The Tildel list names each row's payout provider.
func TestListMarksProviderPayouts(t *testing.T) {
	f := newStripeFixture(t)
	markUsesStripe(t, f.club)
	imp := f.club.NewBankImport(t)
	stripeRow := f.club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 20000, Description: "STRIPE PAYOUT"})
	vippsRow := f.club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 30000, Description: "Utb. 3000100 Vippsnr 548005"})
	plain := f.club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 40000, Counterpart: "Ola Testmann"})

	rows, err := f.svc.ListUnmatchedBankRows(context.Background(), f.club.ID, "", "", 0, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]BankRowSummary{}
	for _, r := range rows {
		got[r.ID] = r
	}
	if r := got[stripeRow]; r.ProviderPayout == nil || *r.ProviderPayout != ProviderPayoutStripe || r.ProviderPayoutReason != stripeReasonNoPayout || !r.ProviderPayoutByHand || r.VippsPayout {
		t.Errorf("Stripe row = %+v", r)
	}
	if r := got[vippsRow]; r.ProviderPayout == nil || *r.ProviderPayout != ProviderPayoutVipps || !r.VippsPayout || r.ProviderPayoutReason != "" {
		t.Errorf("Vipps row = %+v", r)
	}
	if r := got[plain]; r.ProviderPayout != nil {
		t.Errorf("plain row = %+v, want no provider", r)
	}
}
