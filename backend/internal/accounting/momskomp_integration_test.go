package accounting

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// dateIn formats a calendar date for entry_date.
func dateIn(year, month, day int) string {
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

// addPeriod seeds another fiscal period for the club, open, and returns its id.
func addPeriod(t *testing.T, club *fin.Club, year int) string {
	t.Helper()
	var id string
	if err := club.Pool.QueryRow(context.Background(),
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'open') RETURNING id`,
		club.ID, year,
	).Scan(&id); err != nil {
		t.Fatalf("add period %d: %v", year, err)
	}
	return id
}

// bookEntry creates (and optionally posts) a two-line entry dated in the given
// period's year and returns its id.
func bookEntry(t *testing.T, svc *Service, club *fin.Club, periodID string, year int, debit, credit string, amount float64, post bool) string {
	t.Helper()
	ctx := context.Background()
	e, err := svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      dateIn(year, 5, 2),
		Description:    "Synthetic bilag",
		CreatedBy:      club.Treasurer.ID,
		ClubID:         club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: debit, Debit: amount},
			{AccountCode: credit, Credit: amount},
		},
	})
	if err != nil {
		t.Fatalf("create entry %s/%s: %v", debit, credit, err)
	}
	if post {
		if err := svc.PostJournalEntry(ctx, club.ID, e.ID, club.Treasurer.ID); err != nil {
			t.Fatalf("post entry: %v", err)
		}
	}
	return e.ID
}

func breakdownTotal(r *MomskompReport, code string) float64 {
	for _, b := range r.BreakdownByAccount {
		if b.AccountCode == code {
			return b.TotalAmount
		}
	}
	return 0
}

// A cost account with a posted line in each of two years and a draft in the
// second must report only the selected period's posted lines.
func TestMomskompensasjonSumsOnlyThePeriodsPostedLines(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan), fin.WithYear(2026))
	p2025 := addPeriod(t, club, 2025)
	ctx := context.Background()

	bookEntry(t, svc, club, p2025, 2025, "6200", "1920", 100, true)
	bookEntry(t, svc, club, club.PeriodID, 2026, "6200", "1920", 40, true)
	bookEntry(t, svc, club, club.PeriodID, 2026, "6200", "1920", 7, false)

	got2025, err := svc.Momskompensasjon(ctx, club.ID, p2025, "simplified")
	if err != nil {
		t.Fatalf("momskomp 2025: %v", err)
	}
	if math.Abs(got2025.TotalOperatingCosts-100) > 0.001 {
		t.Errorf("2025 costs = %.2f, want 100 (not the 2026 line or the draft)", got2025.TotalOperatingCosts)
	}
	if got := breakdownTotal(got2025, "6200"); math.Abs(got-100) > 0.001 {
		t.Errorf("2025 account 6200 = %.2f, want 100", got)
	}

	got2026, err := svc.Momskompensasjon(ctx, club.ID, club.PeriodID, "simplified")
	if err != nil {
		t.Fatalf("momskomp 2026: %v", err)
	}
	if math.Abs(got2026.TotalOperatingCosts-40) > 0.001 {
		t.Errorf("2026 costs = %.2f, want 40 (not the 2025 line or the draft)", got2026.TotalOperatingCosts)
	}
	if !got2026.HasDraftEntries {
		t.Error("2026 holds a draft: HasDraftEntries should be true")
	}
}

// An account that is inactive but holds postings stays in the breakdown, as
// documentedCompensation counts its lines.
func TestMomskompensasjonKeepsInactiveCostAccountWithPostings(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	ctx := context.Background()

	bookEntry(t, svc, club, club.PeriodID, club.Year, "6200", "1920", 55, true)
	if _, err := pool.Exec(ctx, `UPDATE accounts SET is_active = false WHERE club_id = $1 AND code = '6200'`, club.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	got, err := svc.Momskompensasjon(ctx, club.ID, club.PeriodID, "simplified")
	if err != nil {
		t.Fatalf("momskomp: %v", err)
	}
	if v := breakdownTotal(got, "6200"); math.Abs(v-55) > 0.001 {
		t.Errorf("inactive 6200 = %.2f, want 55", v)
	}
}

// A void's reversal copies mva_amount onto swapped sides, so the MVA of a
// voided cost must net to zero, not count twice.
func TestMomskompensasjonVoidedCostMVANetsToZero(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	ctx := context.Background()

	book := func(amount, mva float64) string {
		t.Helper()
		e, err := svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: club.PeriodID, EntryDate: dateIn(club.Year, 5, 2), Description: "Synthetic bilag",
			CreatedBy: club.Treasurer.ID, ClubID: club.ID,
			Lines: []CreateJournalLineInput{
				{AccountCode: "6200", Debit: amount, MVAAmount: mva},
				{AccountCode: "1920", Credit: amount},
			},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := svc.PostJournalEntry(ctx, club.ID, e.ID, club.Treasurer.ID); err != nil {
			t.Fatalf("post: %v", err)
		}
		return e.ID
	}
	voided := book(1000, 200)
	book(500, 100)
	if _, err := svc.VoidJournalEntry(ctx, club.ID, voided, club.Treasurer.ID); err != nil {
		t.Fatalf("void: %v", err)
	}

	for _, model := range []string{"simplified", "documented"} {
		got, err := svc.Momskompensasjon(ctx, club.ID, club.PeriodID, model)
		if err != nil {
			t.Fatalf("momskomp %s: %v", model, err)
		}
		if len(got.BreakdownByAccount) != 1 || math.Abs(got.BreakdownByAccount[0].MVAAmount-100) > 0.001 {
			t.Errorf("%s: breakdown %+v, want the MVA of the surviving cost only (100)", model, got.BreakdownByAccount)
		}
		if math.Abs(got.TotalOperatingCosts-500) > 0.001 {
			t.Errorf("%s: costs %.2f, want 500", model, got.TotalOperatingCosts)
		}
		if model == "documented" && math.Abs(got.CompensationAmount-100) > 0.001 {
			t.Errorf("documented compensation %.2f, want 100", got.CompensationAmount)
		}
	}
}
