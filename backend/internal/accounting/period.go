package accounting

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// FiscalPeriod represents a fiscal year for a club.
type FiscalPeriod struct {
	ID        string     `json:"id"`
	ClubID    string     `json:"club_id"`
	Year      int        `json:"year"`
	StartDate string     `json:"start_date"`
	EndDate   string     `json:"end_date"`
	Status    string     `json:"status"`
	ClosedBy  *string    `json:"closed_by"`
	ClosedAt  *time.Time `json:"closed_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// CreatePeriod opens a new fiscal year (Jan 1 – Dec 31).
func (s *Service) CreatePeriod(ctx context.Context, clubID string, year int) (*FiscalPeriod, error) {
	startDate := fmt.Sprintf("%d-01-01", year)
	endDate := fmt.Sprintf("%d-12-31", year)

	var p FiscalPeriod
	err := s.db.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, club_id, year, start_date::text, end_date::text, status, closed_by, closed_at, created_at`,
		clubID, year, startDate, endDate,
	).Scan(&p.ID, &p.ClubID, &p.Year, &p.StartDate, &p.EndDate, &p.Status, &p.ClosedBy, &p.ClosedAt, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("creating fiscal period: %w", err)
	}
	return &p, nil
}

// ResolvePeriodForDate returns the fiscal period whose date range contains
// the given date. If none exists, it auto-creates a calendar-year period.
// Returns (id, status, error). Status is "open" or "closed"; callers should
// refuse to book new entries into a closed period.
func (s *Service) ResolvePeriodForDate(ctx context.Context, clubID string, date time.Time) (id, status string, err error) {
	err = s.db.QueryRow(ctx,
		`SELECT id, status FROM fiscal_periods
		 WHERE club_id = $1 AND $2::date BETWEEN start_date AND end_date
		 LIMIT 1`,
		clubID, date.Format("2006-01-02"),
	).Scan(&id, &status)
	if err == nil {
		return id, status, nil
	}
	if err != pgx.ErrNoRows {
		return "", "", fmt.Errorf("looking up period: %w", err)
	}

	// Auto-create as a calendar-year period.
	p, cerr := s.CreatePeriod(ctx, clubID, date.Year())
	if cerr != nil {
		return "", "", cerr
	}
	return p.ID, p.Status, nil
}

// ListPeriods returns all fiscal periods for a club, newest first.
func (s *Service) ListPeriods(ctx context.Context, clubID string) ([]FiscalPeriod, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, club_id, year, start_date::text, end_date::text, status, closed_by, closed_at, created_at
		 FROM fiscal_periods WHERE club_id = $1 ORDER BY year DESC`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing periods: %w", err)
	}
	defer rows.Close()

	var periods []FiscalPeriod
	for rows.Next() {
		var p FiscalPeriod
		if err := rows.Scan(&p.ID, &p.ClubID, &p.Year, &p.StartDate, &p.EndDate, &p.Status, &p.ClosedBy, &p.ClosedAt, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning period: %w", err)
		}
		periods = append(periods, p)
	}
	return periods, rows.Err()
}

// GetPeriod returns a single fiscal period by ID.
func (s *Service) GetPeriod(ctx context.Context, periodID string) (*FiscalPeriod, error) {
	var p FiscalPeriod
	err := s.db.QueryRow(ctx,
		`SELECT id, club_id, year, start_date::text, end_date::text, status, closed_by, closed_at, created_at
		 FROM fiscal_periods WHERE id = $1`,
		periodID,
	).Scan(&p.ID, &p.ClubID, &p.Year, &p.StartDate, &p.EndDate, &p.Status, &p.ClosedBy, &p.ClosedAt, &p.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}
	return &p, nil
}

// PeriodDraftSummary counts the draft entries of one source left in a period.
type PeriodDraftSummary struct {
	Source string  `json:"source"`
	Count  int     `json:"count"`
	Total  float64 `json:"total"`
}

// PeriodHasDraftsError is returned by ClosePeriod while the period still
// holds draft entries. Drafts are left out of every report, and a closed
// period can no longer take a post, so closing would strand them for good.
type PeriodHasDraftsError struct {
	Drafts []PeriodDraftSummary
}

func (e *PeriodHasDraftsError) Error() string {
	total := 0
	for _, d := range e.Drafts {
		total += d.Count
	}
	return fmt.Sprintf("period has %d draft entries; post or delete them before closing", total)
}

// ClosePeriod sets a period's status to closed, preventing new journal
// entries. It refuses while the period has drafts (PeriodHasDraftsError).
func (s *Service) ClosePeriod(ctx context.Context, clubID, periodID, closedBy string) error {
	drafts, err := s.periodDrafts(ctx, clubID, periodID)
	if err != nil {
		return err
	}
	if len(drafts) > 0 {
		return &PeriodHasDraftsError{Drafts: drafts}
	}
	// The NOT EXISTS re-checks inside the update, so a draft created after
	// the read above still blocks the close.
	tag, err := s.db.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'closed', closed_by = $3, closed_at = now(), updated_at = now()
		 WHERE id = $1 AND club_id = $2 AND status = 'open'
		   AND NOT EXISTS (
		     SELECT 1 FROM journal_entries
		      WHERE fiscal_period_id = $1 AND club_id = $2 AND status = 'draft'
		   )`,
		periodID, clubID, closedBy,
	)
	if err != nil {
		return fmt.Errorf("closing period: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if drafts, derr := s.periodDrafts(ctx, clubID, periodID); derr == nil && len(drafts) > 0 {
			return &PeriodHasDraftsError{Drafts: drafts}
		}
		return fmt.Errorf("period not found or not open")
	}
	return nil
}

// periodDrafts lists the period's draft entries per source with their total
// (the debit side of each entry).
func (s *Service) periodDrafts(ctx context.Context, clubID, periodID string) ([]PeriodDraftSummary, error) {
	rows, err := s.db.Query(ctx,
		`SELECT je.source::text, count(*),
		        COALESCE(sum((SELECT sum(jl.debit) FROM journal_lines jl WHERE jl.journal_entry_id = je.id)), 0)
		   FROM journal_entries je
		  WHERE je.club_id = $1 AND je.fiscal_period_id = $2 AND je.status = 'draft'
		  GROUP BY je.source
		  ORDER BY je.source`,
		clubID, periodID,
	)
	if err != nil {
		return nil, fmt.Errorf("counting draft entries: %w", err)
	}
	defer rows.Close()
	var out []PeriodDraftSummary
	for rows.Next() {
		var d PeriodDraftSummary
		if err := rows.Scan(&d.Source, &d.Count, &d.Total); err != nil {
			return nil, fmt.Errorf("scanning draft summary: %w", err)
		}
		d.Total = roundCents(d.Total)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ReopenPeriod sets a closed period back to open.
func (s *Service) ReopenPeriod(ctx context.Context, periodID string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'open', closed_by = NULL, closed_at = NULL, updated_at = now()
		 WHERE id = $1 AND status = 'closed'`,
		periodID,
	)
	if err != nil {
		return fmt.Errorf("reopening period: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("period not found or not closed")
	}
	return nil
}
