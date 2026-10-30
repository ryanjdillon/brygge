package accounting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A failed read must never look like "no price item" or "no alias": the
// lookups behind decideVippsCharge propagate their errors, so a charge is not
// classified from a partial picture.

// brokenPriceItems makes the second of two 800 kr price items (a different
// category from the first) fail to scan: the partial list holds only guest,
// which would read as a single category and book to 3200.
func (f *autoAssignFixture) brokenPriceItems(t *testing.T) {
	t.Helper()
	f.priceItem(t, "guest", "Aaa guest synthetic", 80000, "day", "all")
	f.priceItem(t, "service", "Zzz service synthetic", 80000, "once", "all")
	f.exec(t, `ALTER TABLE price_items ALTER COLUMN unit DROP NOT NULL`)
	f.exec(t, `UPDATE price_items SET unit = NULL WHERE club_id = $1 AND name = 'Zzz service synthetic'`, f.club.ID)
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestPriceArticleCandidatesReturnsLookupErrors(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.brokenPriceItems(t)

	cands, err := f.svc.priceArticleCandidates(context.Background(), f.club.ID, 800)
	if err == nil || cands != nil || !strings.Contains(err.Error(), "price item") {
		t.Errorf("scan failure: cands = %v, err = %v, want an error and no partial list", cands, err)
	}
	if _, err := f.svc.priceArticleCandidates(cancelled(), f.club.ID, 800); err == nil {
		t.Errorf("a cancelled context must be an error, not an empty list")
	}
	if _, _, err := f.svc.matchVippsAmountToPriceArticle(context.Background(), f.club.ID, 800); err == nil {
		t.Errorf("the legacy price-article matcher swallowed the lookup error")
	}
	f.exec(t, `ALTER TABLE price_items RENAME TO price_items_gone`)
	if _, err := f.svc.priceArticleCandidates(context.Background(), f.club.ID, 800); err == nil {
		t.Errorf("a failing query must be an error")
	}
}

func TestDecideVippsChargeSurfacesPriceItemErrors(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.brokenPriceItems(t)
	cfg := vippsAutoAssignConfig{enabled: true}

	d, err := f.svc.decideVippsCharge(context.Background(), f.club.ID, cfg, "Nobody Known", "", 800, time.Now())
	if err == nil || d != nil {
		t.Fatalf("decide = %+v, %v; a partial price list proposed an account instead of failing", d, err)
	}
	if !strings.Contains(err.Error(), "loading price items") {
		t.Errorf("err = %v, want it to name the price items", err)
	}
}

// Auto-assign reports the charge as skipped with the error and books nothing.
func TestAutoAssignDoesNotBookOnAPriceItemError(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.brokenPriceItems(t)
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, CustomerName: "Walk Up"})

	res := f.run(t)
	if len(res.Assigned) != 0 || len(res.SkippedDetails) != 1 || res.NeedsReview != 1 {
		t.Fatalf("result = %+v, want the charge skipped with its error", res)
	}
	if f.rowEntry(t, row) != "" {
		t.Errorf("charge booked from a partial price list")
	}
}

func TestPayerLookupsReturnErrors(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()

	if id, err := aliasMember(ctx, f.club.Pool, f.club.ID, "No Alias"); id != "" || err != nil {
		t.Errorf("no alias = %q, %v; want empty and no error", id, err)
	}
	if _, err := aliasMember(cancelled(), f.club.Pool, f.club.ID, "Any Name"); err == nil {
		t.Errorf("alias lookup swallowed a cancelled context")
	}
	if _, _, err := f.svc.resolveCustomerToMemberPass(cancelled(), f.club.ID, "Any Name", ""); err == nil {
		t.Errorf("member resolution swallowed a cancelled context")
	}
	if _, _, err := f.svc.resolveStrongMember(cancelled(), f.club.ID, nil, "Any Name"); err == nil {
		t.Errorf("strong resolution swallowed a cancelled context")
	}

	// The KID outranks an alias, so a failing KID lookup must not fall
	// through to a name or alias match.
	f.exec(t, `ALTER TABLE invoices RENAME TO invoices_gone`)
	_, _, err := f.svc.resolveCustomerToMemberPass(ctx, f.club.ID, "Any Name", "KID 1234567890123")
	if err == nil || !strings.Contains(err.Error(), "KID") {
		t.Errorf("KID lookup failure = %v, want an error naming the KID", err)
	}
	// With the table back a KID nobody carries is no error: it falls through.
	f.exec(t, `ALTER TABLE invoices_gone RENAME TO invoices`)
	if id, _, err := f.svc.resolveCustomerToMemberPass(ctx, f.club.ID, "Any Name", "KID 1234567890123"); err != nil || id != "" {
		t.Errorf("unknown KID = %q, %v; want no member and no error", id, err)
	}

	f.exec(t, `ALTER TABLE vipps_customer_aliases RENAME TO vipps_customer_aliases_gone`)
	if _, err := aliasMember(ctx, f.club.Pool, f.club.ID, "Any Name"); err == nil {
		t.Errorf("a failing alias query must be an error")
	}
	if _, err := resolvePayer(ctx, f.club.Pool, f.club.ID, "Any Name"); err == nil {
		t.Errorf("resolvePayer swallowed the alias error")
	}
}

// The bank queue's suggestions fail rather than offer a partial price list.
func TestRankAccountSuggestionsReturnsPriceItemErrors(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.brokenPriceItems(t)
	if _, err := f.svc.rankAccountSuggestions(context.Background(), f.club.ID, 800, "desc", "Payer", true); err == nil {
		t.Errorf("suggestions swallowed the price item error")
	}
}
