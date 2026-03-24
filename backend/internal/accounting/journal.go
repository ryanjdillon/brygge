package accounting

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrUnbalancedEntry = errors.New("journal entry is not balanced: total debits must equal total credits")
	ErrPeriodClosed    = errors.New("cannot post to a closed or locked period")
	ErrEntryNotDraft   = errors.New("only draft entries can be modified")
	ErrEntryNotPosted  = errors.New("only posted entries can be voided")
)

// JournalEntry represents a bilag (voucher) with its lines.
type JournalEntry struct {
	ID             string         `json:"id"`
	ClubID         string         `json:"club_id"`
	FiscalPeriodID string         `json:"fiscal_period_id"`
	EntryNumber    int            `json:"entry_number"`
	EntryDate      string         `json:"entry_date"`
	Description    string         `json:"description"`
	Status         string         `json:"status"`
	Source         string         `json:"source"`
	SourceID       *string        `json:"source_id"`
	SourceTable    *string        `json:"source_table"`
	AttachmentURL  *string        `json:"attachment_url"`
	CreatedBy      string         `json:"created_by"`
	PostedBy       *string        `json:"posted_by"`
	PostedAt       *time.Time     `json:"posted_at"`
	VoidedBy       *string        `json:"voided_by"`
	VoidedAt       *time.Time     `json:"voided_at"`
	CreatedAt      time.Time      `json:"created_at"`
	Lines          []JournalLine  `json:"lines,omitempty"`
}

// JournalLine represents a single debit or credit posting.
type JournalLine struct {
	ID             string         `json:"id"`
	JournalEntryID string         `json:"journal_entry_id"`
	AccountID      string         `json:"account_id"`
	AccountCode    string         `json:"account_code,omitempty"`
	AccountName    string         `json:"account_name,omitempty"`
	Debit          float64        `json:"debit"`
	Credit         float64        `json:"credit"`
	Description    string         `json:"description"`
	MVAAmount      float64        `json:"mva_amount"`
	MVAEligible    MVAEligibility `json:"mva_eligible"`
}

// CreateJournalEntryInput is the input for creating a new journal entry.
type CreateJournalEntryInput struct {
	FiscalPeriodID string
	EntryDate      string
	Description    string
	Source         string
	SourceID       *string
	SourceTable    *string
	AttachmentURL  *string
	CreatedBy      string
	ClubID         string
	Lines          []CreateJournalLineInput
}

type CreateJournalLineInput struct {
	AccountCode string
	Debit       float64
	Credit      float64
	Description string
	MVAAmount   float64
}

// CreateJournalEntry creates a draft journal entry with lines.
func (s *Service) CreateJournalEntry(ctx context.Context, input CreateJournalEntryInput) (*JournalEntry, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Verify period is open
	var periodStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		input.FiscalPeriodID, input.ClubID,
	).Scan(&periodStatus)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("fiscal period not found")
	}
	if err != nil {
		return nil, fmt.Errorf("checking period: %w", err)
	}
	if periodStatus != "open" {
		return nil, ErrPeriodClosed
	}

	// Get next entry number
	var entryNumber int
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(entry_number), 0) + 1 FROM journal_entries
		 WHERE club_id = $1 AND fiscal_period_id = $2`,
		input.ClubID, input.FiscalPeriodID,
	).Scan(&entryNumber)
	if err != nil {
		return nil, fmt.Errorf("getting next entry number: %w", err)
	}

	source := input.Source
	if source == "" {
		source = "manual"
	}

	// Insert journal entry
	var entry JournalEntry
	err = tx.QueryRow(ctx,
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, source, source_id, source_table, attachment_url, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id, club_id, fiscal_period_id, entry_number, entry_date::text, description, status, source, source_id, source_table, attachment_url, created_by, posted_by, posted_at, voided_by, voided_at, created_at`,
		input.ClubID, input.FiscalPeriodID, entryNumber, input.EntryDate, input.Description,
		source, input.SourceID, input.SourceTable, input.AttachmentURL, input.CreatedBy,
	).Scan(
		&entry.ID, &entry.ClubID, &entry.FiscalPeriodID, &entry.EntryNumber, &entry.EntryDate,
		&entry.Description, &entry.Status, &entry.Source, &entry.SourceID, &entry.SourceTable,
		&entry.AttachmentURL, &entry.CreatedBy, &entry.PostedBy, &entry.PostedAt,
		&entry.VoidedBy, &entry.VoidedAt, &entry.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting journal entry: %w", err)
	}

	// Insert lines
	for _, line := range input.Lines {
		// Resolve account ID from code
		var accountID string
		var mvaEligible MVAEligibility
		err = tx.QueryRow(ctx,
			`SELECT id, mva_eligible FROM accounts WHERE club_id = $1 AND code = $2 AND is_active = true`,
			input.ClubID, line.AccountCode,
		).Scan(&accountID, &mvaEligible)
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("account %s not found", line.AccountCode)
		}
		if err != nil {
			return nil, fmt.Errorf("resolving account %s: %w", line.AccountCode, err)
		}

		// Use account's default MVA eligibility for the line
		lineEligible := mvaEligible

		var jl JournalLine
		err = tx.QueryRow(ctx,
			`INSERT INTO journal_lines (journal_entry_id, account_id, debit, credit, description, mva_amount, mva_eligible)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id, journal_entry_id, account_id, debit, credit, description, mva_amount, mva_eligible`,
			entry.ID, accountID, line.Debit, line.Credit, line.Description, line.MVAAmount, lineEligible,
		).Scan(&jl.ID, &jl.JournalEntryID, &jl.AccountID, &jl.Debit, &jl.Credit, &jl.Description, &jl.MVAAmount, &jl.MVAEligible)
		if err != nil {
			return nil, fmt.Errorf("inserting journal line: %w", err)
		}
		jl.AccountCode = line.AccountCode
		entry.Lines = append(entry.Lines, jl)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &entry, nil
}

// PostJournalEntry validates balance and finalizes the entry.
func (s *Service) PostJournalEntry(ctx context.Context, entryID, postedBy string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Get entry and verify it's draft
	var status, periodID string
	err = tx.QueryRow(ctx,
		`SELECT status, fiscal_period_id FROM journal_entries WHERE id = $1 FOR UPDATE`,
		entryID,
	).Scan(&status, &periodID)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("journal entry not found")
	}
	if err != nil {
		return fmt.Errorf("getting entry: %w", err)
	}
	if status != "draft" {
		return ErrEntryNotDraft
	}

	// Verify period is still open
	var periodStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE id = $1`, periodID).Scan(&periodStatus)
	if err != nil {
		return fmt.Errorf("checking period: %w", err)
	}
	if periodStatus != "open" {
		return ErrPeriodClosed
	}

	// Balance check: sum debits must equal sum credits
	var totalDebit, totalCredit float64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(debit), 0), COALESCE(SUM(credit), 0) FROM journal_lines WHERE journal_entry_id = $1`,
		entryID,
	).Scan(&totalDebit, &totalCredit)
	if err != nil {
		return fmt.Errorf("checking balance: %w", err)
	}

	if math.Abs(totalDebit-totalCredit) > 0.001 {
		return ErrUnbalancedEntry
	}

	// Post
	_, err = tx.Exec(ctx,
		`UPDATE journal_entries SET status = 'posted', posted_by = $2, posted_at = now(), updated_at = now()
		 WHERE id = $1`,
		entryID, postedBy,
	)
	if err != nil {
		return fmt.Errorf("posting entry: %w", err)
	}

	return tx.Commit(ctx)
}

// VoidJournalEntry creates a reversal entry and marks the original as voided.
func (s *Service) VoidJournalEntry(ctx context.Context, entryID, voidedBy string) (*JournalEntry, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Get entry and verify it's posted
	var entry JournalEntry
	err = tx.QueryRow(ctx,
		`SELECT id, club_id, fiscal_period_id, entry_number, entry_date::text, description, status, created_by
		 FROM journal_entries WHERE id = $1 FOR UPDATE`,
		entryID,
	).Scan(&entry.ID, &entry.ClubID, &entry.FiscalPeriodID, &entry.EntryNumber, &entry.EntryDate,
		&entry.Description, &entry.Status, &entry.CreatedBy)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("journal entry not found")
	}
	if err != nil {
		return nil, fmt.Errorf("getting entry: %w", err)
	}
	if entry.Status != "posted" {
		return nil, ErrEntryNotPosted
	}

	// Get original lines
	rows, err := tx.Query(ctx,
		`SELECT account_id, debit, credit, description, mva_amount, mva_eligible FROM journal_lines WHERE journal_entry_id = $1`,
		entryID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting lines: %w", err)
	}
	defer rows.Close()

	type origLine struct {
		accountID   string
		debit       float64
		credit      float64
		description string
		mvaAmount   float64
		mvaEligible MVAEligibility
	}
	var origLines []origLine
	for rows.Next() {
		var l origLine
		if err := rows.Scan(&l.accountID, &l.debit, &l.credit, &l.description, &l.mvaAmount, &l.mvaEligible); err != nil {
			return nil, fmt.Errorf("scanning line: %w", err)
		}
		origLines = append(origLines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating lines: %w", err)
	}

	// Mark original as voided
	_, err = tx.Exec(ctx,
		`UPDATE journal_entries SET status = 'voided', voided_by = $2, voided_at = now(), updated_at = now()
		 WHERE id = $1`,
		entryID, voidedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("voiding entry: %w", err)
	}

	// Create reversal entry (same period, next entry number)
	var nextNumber int
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(entry_number), 0) + 1 FROM journal_entries
		 WHERE club_id = $1 AND fiscal_period_id = $2`,
		entry.ClubID, entry.FiscalPeriodID,
	).Scan(&nextNumber)
	if err != nil {
		return nil, fmt.Errorf("getting next entry number: %w", err)
	}

	var reversal JournalEntry
	err = tx.QueryRow(ctx,
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, status, source, created_by, posted_by, posted_at)
		 VALUES ($1, $2, $3, $4, $5, 'posted', 'manual', $6, $6, now())
		 RETURNING id, club_id, fiscal_period_id, entry_number, entry_date::text, description, status, source, created_by, posted_by, posted_at, created_at`,
		entry.ClubID, entry.FiscalPeriodID, nextNumber, entry.EntryDate,
		fmt.Sprintf("Reversering av bilag #%d: %s", entry.EntryNumber, entry.Description),
		voidedBy,
	).Scan(
		&reversal.ID, &reversal.ClubID, &reversal.FiscalPeriodID, &reversal.EntryNumber,
		&reversal.EntryDate, &reversal.Description, &reversal.Status, &reversal.Source,
		&reversal.CreatedBy, &reversal.PostedBy, &reversal.PostedAt, &reversal.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating reversal entry: %w", err)
	}

	// Insert reversed lines (swap debit/credit)
	for _, l := range origLines {
		_, err = tx.Exec(ctx,
			`INSERT INTO journal_lines (journal_entry_id, account_id, debit, credit, description, mva_amount, mva_eligible)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			reversal.ID, l.accountID, l.credit, l.debit, l.description, l.mvaAmount, l.mvaEligible,
		)
		if err != nil {
			return nil, fmt.Errorf("inserting reversal line: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &reversal, nil
}

// GetJournalEntry returns a journal entry with its lines.
func (s *Service) GetJournalEntry(ctx context.Context, entryID string) (*JournalEntry, error) {
	var entry JournalEntry
	err := s.db.QueryRow(ctx,
		`SELECT id, club_id, fiscal_period_id, entry_number, entry_date::text, description, status, source,
		        source_id, source_table, attachment_url, created_by, posted_by, posted_at, voided_by, voided_at, created_at
		 FROM journal_entries WHERE id = $1`,
		entryID,
	).Scan(
		&entry.ID, &entry.ClubID, &entry.FiscalPeriodID, &entry.EntryNumber, &entry.EntryDate,
		&entry.Description, &entry.Status, &entry.Source, &entry.SourceID, &entry.SourceTable,
		&entry.AttachmentURL, &entry.CreatedBy, &entry.PostedBy, &entry.PostedAt,
		&entry.VoidedBy, &entry.VoidedAt, &entry.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting entry: %w", err)
	}

	rows, err := s.db.Query(ctx,
		`SELECT jl.id, jl.journal_entry_id, jl.account_id, a.code, a.name, jl.debit, jl.credit, jl.description, jl.mva_amount, jl.mva_eligible
		 FROM journal_lines jl
		 JOIN accounts a ON a.id = jl.account_id
		 WHERE jl.journal_entry_id = $1
		 ORDER BY jl.debit DESC, jl.credit DESC`,
		entryID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting lines: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var l JournalLine
		if err := rows.Scan(&l.ID, &l.JournalEntryID, &l.AccountID, &l.AccountCode, &l.AccountName,
			&l.Debit, &l.Credit, &l.Description, &l.MVAAmount, &l.MVAEligible); err != nil {
			return nil, fmt.Errorf("scanning line: %w", err)
		}
		entry.Lines = append(entry.Lines, l)
	}

	return &entry, rows.Err()
}

// JournalFilters for listing journal entries.
type JournalFilters struct {
	PeriodID  string
	Status    string
	StartDate string
	EndDate   string
}

// ListJournalEntries returns journal entries for a club with optional filters.
func (s *Service) ListJournalEntries(ctx context.Context, clubID string, filters JournalFilters) ([]JournalEntry, error) {
	query := `SELECT id, club_id, fiscal_period_id, entry_number, entry_date::text, description, status, source,
	                  source_id, source_table, attachment_url, created_by, posted_by, posted_at, voided_by, voided_at, created_at
	           FROM journal_entries WHERE club_id = $1`
	args := []any{clubID}
	argIdx := 2

	if filters.PeriodID != "" {
		query += fmt.Sprintf(` AND fiscal_period_id = $%d`, argIdx)
		args = append(args, filters.PeriodID)
		argIdx++
	}
	if filters.Status != "" {
		query += fmt.Sprintf(` AND status = $%d`, argIdx)
		args = append(args, filters.Status)
		argIdx++
	}
	if filters.StartDate != "" {
		query += fmt.Sprintf(` AND entry_date >= $%d`, argIdx)
		args = append(args, filters.StartDate)
		argIdx++
	}
	if filters.EndDate != "" {
		query += fmt.Sprintf(` AND entry_date <= $%d`, argIdx)
		args = append(args, filters.EndDate)
	}
	query += ` ORDER BY entry_number DESC LIMIT 100`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing journal entries: %w", err)
	}
	defer rows.Close()

	var entries []JournalEntry
	for rows.Next() {
		var e JournalEntry
		if err := rows.Scan(
			&e.ID, &e.ClubID, &e.FiscalPeriodID, &e.EntryNumber, &e.EntryDate,
			&e.Description, &e.Status, &e.Source, &e.SourceID, &e.SourceTable,
			&e.AttachmentURL, &e.CreatedBy, &e.PostedBy, &e.PostedAt,
			&e.VoidedBy, &e.VoidedAt, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
