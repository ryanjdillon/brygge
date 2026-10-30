package accounting

import (
	"context"
	"math"
	"testing"
)

func balanceOf(lines []ReportLine, code string) float64 {
	for _, l := range lines {
		if l.AccountCode == code {
			return l.Amount
		}
	}
	return 0
}

// The balanse is cumulative, so a reference year must not feed a later
// year's: the opening balance carries those. Picking the reference year
// itself still shows it, and unmarking brings it back.
func TestBalanceSheetSkipsOtherReferencePeriods(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	f.createPosted(t, f.refPeriod, f.refYear, refLine{code: "1920", debit: 1000}, refLine{code: "1500", credit: 1000})
	f.createPosted(t, f.club.PeriodID, f.club.Year, refLine{code: "1920", debit: 500}, refLine{code: "3100", credit: 500})

	assets := func(periodID string) (bank, receivables float64) {
		t.Helper()
		bs, err := f.svc.BalanceSheet(ctx, f.club.ID, periodID, false)
		if err != nil {
			t.Fatalf("balance sheet: %v", err)
		}
		return balanceOf(bs.Assets, "1920"), balanceOf(bs.Assets, "1500")
	}

	if bank, recv := assets(f.club.PeriodID); bank != 1500 || recv != -1000 {
		t.Fatalf("unmarked current-year balanse: 1920 %v, 1500 %v; want 1500 and -1000 (the earlier year carries)", bank, recv)
	}

	f.mark(t, true)
	if bank, recv := assets(f.club.PeriodID); bank != 500 || recv != 0 {
		t.Errorf("current-year balanse: 1920 %v, 1500 %v; want 500 and 0 (the reference year left out)", bank, recv)
	}
	if bank, recv := assets(f.refPeriod); bank != 1000 || recv != -1000 {
		t.Errorf("reference-year balanse: 1920 %v, 1500 %v; want its own 1000 and -1000", bank, recv)
	}

	f.mark(t, false)
	if bank, _ := assets(f.club.PeriodID); bank != 1500 {
		t.Errorf("after unmarking 1920 = %v, want 1500", bank)
	}
}

// Momskomp's incomplete-data warning counts, in a reference year, only the
// drafts the guard would let post: the year's Vipps drafts stay drafts.
func TestMomskompDraftWarningInReferenceYear(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	// A Vipps charge draft with a fee line on 7700 and the clearing line.
	if _, err := f.create(f.club, f.refPeriod, f.refYear, "vipps",
		refLine{code: "1920", debit: 90}, refLine{code: "7700", debit: 10}, refLine{code: "2900", credit: 100}); err != nil {
		t.Fatalf("vipps draft: %v", err)
	}
	f.mark(t, true)

	got, err := f.svc.Momskompensasjon(ctx, f.club.ID, f.refPeriod, "simplified")
	if err != nil {
		t.Fatalf("momskomp: %v", err)
	}
	if got.HasDraftEntries {
		t.Errorf("a Vipps draft with a 7700 line and a 2900 line marked the reference year incomplete")
	}

	f.mark(t, false)
	expense, err := f.create(f.club, f.refPeriod, f.refYear, "", twoLines("6200", "1920")...)
	if err != nil {
		t.Fatalf("expense draft: %v", err)
	}
	f.mark(t, true)
	got, err = f.svc.Momskompensasjon(ctx, f.club.ID, f.refPeriod, "simplified")
	if err != nil {
		t.Fatalf("momskomp: %v", err)
	}
	if !got.HasDraftEntries {
		t.Errorf("an expense-shaped draft (%s) did not mark the reference year incomplete", expense.ID)
	}

	// An ordinary year keeps the old rule: any draft counts.
	f.mark(t, false)
	got, err = f.svc.Momskompensasjon(ctx, f.club.ID, f.refPeriod, "simplified")
	if err != nil || !got.HasDraftEntries {
		t.Errorf("unmarked year with drafts: HasDraftEntries = %v (%v), want true", got != nil && got.HasDraftEntries, err)
	}
}

// Momskomp reads a reference year as any other: its posted costs count.
func TestMomskompReadsReferenceYearCosts(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	f.createPosted(t, f.refPeriod, f.refYear, twoLines("6200", "1920")...)
	f.mark(t, true)
	// A cost booked after marking is part of the figure.
	if _, err := f.create(f.club, f.refPeriod, f.refYear, "", twoLines("6500", "1920")...); err != nil {
		t.Fatalf("cost draft in the reference year: %v", err)
	}

	got, err := f.svc.Momskompensasjon(ctx, f.club.ID, f.refPeriod, "simplified")
	if err != nil {
		t.Fatalf("momskomp: %v", err)
	}
	if math.Abs(got.TotalOperatingCosts-100) > 0.001 {
		t.Errorf("reference-year costs = %.2f, want 100 (the posted one only)", got.TotalOperatingCosts)
	}
}
