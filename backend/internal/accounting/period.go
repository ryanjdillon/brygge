package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/brygge-klubb/brygge/internal/finance"
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
	// ReferenceOnly marks a year whose books of record live elsewhere (see
	// reference_period.go).
	ReferenceOnly bool `json:"reference_only"`
	// ReferenceBlocked is why the reference toggle is unavailable: marking is
	// refused for the year covering today ("current_year") and for a year
	// holding an opening balance ("opening_balance"); unmarking needs the
	// period open ("period_closed"). The server decides so the SPA never
	// re-derives it.
	ReferenceBlocked *string `json:"reference_blocked"`
	// OpenInvoiceBookings counts posted faktura bookings of the period whose
	// faktura is still open. In a reference year they can never be reversed
	// (a void or credit needs a 1500 line), so the mark dialog warns about
	// them.
	OpenInvoiceBookings int `json:"open_invoice_bookings"`
}

// periodSelectSQL reads a FiscalPeriod; callers append a WHERE on fp.
var periodSelectSQL = `SELECT fp.id, fp.club_id, fp.year, fp.start_date::text, fp.end_date::text, fp.status,
	       fp.closed_by, fp.closed_at, fp.created_at, fp.reference_only,
	       CASE WHEN fp.reference_only THEN
	                CASE WHEN fp.status <> 'open' THEN 'period_closed' END
	            WHEN CURRENT_DATE BETWEEN fp.start_date AND fp.end_date THEN 'current_year'
	            WHEN EXISTS (SELECT 1 FROM journal_entries je
	                          WHERE je.club_id = fp.club_id AND je.fiscal_period_id = fp.id
	                            AND je.source = 'opening_balance' AND je.status <> 'voided')
	                THEN 'opening_balance'
	       END,
	       (SELECT count(*) FROM journal_entries ib
	          JOIN invoices i ON i.id::text = ib.source_id AND i.club_id = ib.club_id
	         WHERE ib.club_id = fp.club_id AND ib.fiscal_period_id = fp.id
	           AND ib.source = 'invoice_sync' AND ib.status = 'posted'
	           AND ` + finance.OpenReceivableSQL("i") + `)
	  FROM fiscal_periods fp`

func scanPeriod(row pgx.Row) (*FiscalPeriod, error) {
	var p FiscalPeriod
	if err := row.Scan(&p.ID, &p.ClubID, &p.Year, &p.StartDate, &p.EndDate, &p.Status, &p.ClosedBy, &p.ClosedAt,
		&p.CreatedAt, &p.ReferenceOnly, &p.ReferenceBlocked, &p.OpenInvoiceBookings); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreatePeriod opens a new fiscal year (Jan 1 – Dec 31).
func (s *Service) CreatePeriod(ctx context.Context, clubID string, year int) (*FiscalPeriod, error) {
	startDate := fmt.Sprintf("%d-01-01", year)
	endDate := fmt.Sprintf("%d-12-31", year)

	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		clubID, year, startDate, endDate,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("creating fiscal period: %w", err)
	}
	p, err := scanPeriod(s.db.QueryRow(ctx, periodSelectSQL+` WHERE fp.id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("reading fiscal period: %w", err)
	}
	return p, nil
}

// ResolvePeriodForDate returns the fiscal period whose date range contains
// the given date. If none exists, it auto-creates a calendar-year period.
// Returns (id, status, error). Status is "open" or "closed"; callers should
// refuse to book new entries into a closed period.
func (s *Service) ResolvePeriodForDate(ctx context.Context, clubID string, date time.Time) (id, status string, err error) {
	return resolvePeriodForDate(ctx, s.db, clubID, date)
}

// dbtx is satisfied by both the pool and a transaction, for helpers that a
// transaction holding a row lock must run on its own connection.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// resolvePeriodForDate is ResolvePeriodForDate on q. The auto-create is ON
// CONFLICT DO NOTHING followed by a re-read, so a concurrent create of the
// same year neither fails this call nor aborts the caller's transaction.
func resolvePeriodForDate(ctx context.Context, q dbtx, clubID string, date time.Time) (id, status string, err error) {
	lookup := func() error {
		return q.QueryRow(ctx,
			`SELECT id, status FROM fiscal_periods
			 WHERE club_id = $1 AND $2::date BETWEEN start_date AND end_date
			 LIMIT 1`,
			clubID, date.Format("2006-01-02"),
		).Scan(&id, &status)
	}
	err = lookup()
	if err == nil {
		return id, status, nil
	}
	if err != pgx.ErrNoRows {
		return "", "", fmt.Errorf("looking up period: %w", err)
	}

	// Auto-create as a calendar-year period.
	year := date.Year()
	err = q.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (club_id, year) DO NOTHING
		 RETURNING id, status`,
		clubID, year, fmt.Sprintf("%d-01-01", year), fmt.Sprintf("%d-12-31", year),
	).Scan(&id, &status)
	if err == nil {
		return id, status, nil
	}
	if err != pgx.ErrNoRows {
		return "", "", fmt.Errorf("creating fiscal period: %w", err)
	}
	// The insert found a period for the year already there: either created
	// concurrently, or one whose dates don't follow the calendar year.
	if err := lookup(); errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("no fiscal period covers %s; the %d period exists with other dates", date.Format("2006-01-02"), year)
	} else if err != nil {
		return "", "", fmt.Errorf("looking up fiscal period: %w", err)
	}
	return id, status, nil
}

// ListPeriods returns all fiscal periods for a club, newest first.
func (s *Service) ListPeriods(ctx context.Context, clubID string) ([]FiscalPeriod, error) {
	rows, err := s.db.Query(ctx, periodSelectSQL+` WHERE fp.club_id = $1 ORDER BY fp.year DESC`, clubID)
	if err != nil {
		return nil, fmt.Errorf("listing periods: %w", err)
	}
	defer rows.Close()

	var periods []FiscalPeriod
	for rows.Next() {
		p, err := scanPeriod(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning period: %w", err)
		}
		periods = append(periods, *p)
	}
	return periods, rows.Err()
}

// GetPeriod returns a single fiscal period by ID.
func (s *Service) GetPeriod(ctx context.Context, periodID string) (*FiscalPeriod, error) {
	p, err := scanPeriod(s.db.QueryRow(ctx, periodSelectSQL+` WHERE fp.id = $1`, periodID))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}
	return p, nil
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
// entries. It refuses while the period has drafts (PeriodHasDraftsError),
// except for a reference period: its drafts can never be posted (only
// expense-shaped entries are accepted there), so stranding them is the
// intent, and closing is how the treasurer stops cost bookings once the
// year's momskompensasjon is filed.
//
// The period row is locked FOR UPDATE before drafts are counted. Every
// writer that can add to the period (CreateJournalEntry, PostJournalEntry,
// VoidJournalEntry) holds it FOR SHARE until it commits, so the lock waits
// for in-flight writers, and the draft count that follows (a new statement,
// so a fresh READ COMMITTED snapshot) sees what they committed.
func (s *Service) ClosePeriod(ctx context.Context, clubID, periodID, closedBy string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var status string
	var reference bool
	err = tx.QueryRow(ctx,
		`SELECT status, reference_only FROM fiscal_periods WHERE id = $1 AND club_id = $2 FOR UPDATE`,
		periodID, clubID,
	).Scan(&status, &reference)
	if err == pgx.ErrNoRows || (err == nil && status != "open") {
		return fmt.Errorf("period not found or not open")
	}
	if err != nil {
		return fmt.Errorf("locking period: %w", err)
	}

	if !reference {
		drafts, err := periodDrafts(ctx, tx, clubID, periodID)
		if err != nil {
			return err
		}
		if len(drafts) > 0 {
			return &PeriodHasDraftsError{Drafts: drafts}
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'closed', closed_by = $3, closed_at = now(), updated_at = now()
		 WHERE id = $1 AND club_id = $2`,
		periodID, clubID, closedBy,
	); err != nil {
		return fmt.Errorf("closing period: %w", err)
	}
	return tx.Commit(ctx)
}

// periodDrafts lists the period's draft entries per source with their total
// (the debit side of each entry).
func periodDrafts(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, clubID, periodID string) ([]PeriodDraftSummary, error) {
	rows, err := q.Query(ctx,
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

// ReopenPeriod sets a closed period back to open. A locked period stays
// locked, and another club's period is reported as not found.
func (s *Service) ReopenPeriod(ctx context.Context, clubID, periodID string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'open', closed_by = NULL, closed_at = NULL, updated_at = now()
		 WHERE id = $1 AND club_id = $2 AND status = 'closed'`,
		periodID, clubID,
	)
	if err != nil {
		return fmt.Errorf("reopening period: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("period not found or not closed")
	}
	return nil
}
