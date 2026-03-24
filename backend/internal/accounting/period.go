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

// ClosePeriod sets a period's status to closed, preventing new journal entries.
func (s *Service) ClosePeriod(ctx context.Context, periodID, closedBy string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'closed', closed_by = $2, closed_at = now(), updated_at = now()
		 WHERE id = $1 AND status = 'open'`,
		periodID, closedBy,
	)
	if err != nil {
		return fmt.Errorf("closing period: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("period not found or not open")
	}
	return nil
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
