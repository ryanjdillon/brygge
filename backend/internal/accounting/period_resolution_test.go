package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

func TestResolvePeriodForDateAutoCreates(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	svc := NewService(pool, nil, zerolog.Nop())

	// No periods seeded → calling resolve for a 2024 date should create one.
	id, status, err := svc.ResolvePeriodForDate(ctx, clubID, time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("resolve 2024: %v", err)
	}
	if id == "" {
		t.Errorf("expected auto-created period id")
	}
	if status != "open" {
		t.Errorf("expected status=open for auto-created period, got %q", status)
	}

	// Second call same year hits the existing period.
	id2, _, err := svc.ResolvePeriodForDate(ctx, clubID, time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("resolve same year: %v", err)
	}
	if id2 != id {
		t.Errorf("expected same period id for different dates in same year")
	}

	// Different year creates a separate one.
	id3, _, err := svc.ResolvePeriodForDate(ctx, clubID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("resolve 2026: %v", err)
	}
	if id3 == id {
		t.Errorf("expected distinct period id for distinct year")
	}
}

func TestResolvePeriodForDateRespectsClosed(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())

	var periodID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status, closed_at, closed_by)
		 VALUES ($1, 2025, '2025-01-01', '2025-12-31', 'closed', now(), $2) RETURNING id`,
		clubID, userID,
	).Scan(&periodID); err != nil {
		t.Fatalf("seed closed period: %v", err)
	}

	id, status, err := svc.ResolvePeriodForDate(ctx, clubID, time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != periodID {
		t.Errorf("expected existing period id, got %q", id)
	}
	if status != "closed" {
		t.Errorf("expected status=closed, got %q", status)
	}
}
