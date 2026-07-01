package accounting

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// RetentionYears is the bokføringsloven §13 retention window for
// financially-relevant records, counted from the end of the accounting year
// the record belongs to.
const RetentionYears = 5

// MemberRetention describes whether a member's financial records still fall
// within the mandatory retention window, and when they clear it.
type MemberRetention struct {
	HasRecords   bool       `json:"has_records"`
	LatestRecord *time.Time `json:"latest_record,omitempty"`
	ClearsAt     *time.Time `json:"clears_at,omitempty"`
	Eligible     bool       `json:"eligible"`
}

// rowQuerier is satisfied by both *pgxpool.Pool and pgx.Tx, so retention can
// be evaluated inside or outside a transaction.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// MemberFinancialRetention reports whether the member (userID) still has
// financial records inside the retention window, and the date it clears.
//
// The anchor is the member's most recent invoice (BRY-206 decision):
// bokføringsloven counts 5 years from the end of the accounting year a record
// belongs to, so a record from year Y clears on 1 January Y+6. A member with
// no invoices has no retention obligation and is immediately eligible for PII
// erasure.
func MemberFinancialRetention(ctx context.Context, db rowQuerier, clubID, userID string) (MemberRetention, error) {
	var latest *time.Time
	if err := db.QueryRow(ctx,
		`SELECT MAX(issue_date) FROM invoices WHERE club_id = $1 AND user_id = $2`,
		clubID, userID,
	).Scan(&latest); err != nil {
		return MemberRetention{}, err
	}
	if latest == nil {
		return MemberRetention{HasRecords: false, Eligible: true}, nil
	}
	clears := retentionClearsAt(*latest)
	return MemberRetention{
		HasRecords:   true,
		LatestRecord: latest,
		ClearsAt:     &clears,
		Eligible:     !time.Now().Before(clears),
	}, nil
}

// retentionClearsAt returns the first instant a record from the given date is
// out of retention: 1 January of the year after the 5-year window past the
// record's accounting year (a record in year Y clears on 1 January Y+6).
func retentionClearsAt(latest time.Time) time.Time {
	return time.Date(latest.Year()+RetentionYears+1, time.January, 1, 0, 0, 0, 0, time.UTC)
}
