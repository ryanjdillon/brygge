package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrBankImportNotFound is a reassign of an import that is not the
	// club's, or of an id that is not a UUID.
	ErrBankImportNotFound = errors.New("bank import not found")
	// ErrReassignOldAccountMissing means the import's current bank account
	// is not in the club's chart of accounts.
	ErrReassignOldAccountMissing = errors.New("old account not found in chart of accounts")
	// ErrReassignTargetAccountMissing means the target code is not in the
	// club's chart of accounts.
	ErrReassignTargetAccountMissing = errors.New("target account not found in chart of accounts")
	// ErrReassignBlocked is what *ReassignBlockedError matches.
	ErrReassignBlocked = errors.New("reassign touches an immutable journal")
	// ErrReassignCollidesTransfer means an entry linked from the import
	// already has a line on the target account (a transfer between the
	// club's own accounts, BRY-672): the swap would book DR target / CR
	// target.
	ErrReassignCollidesTransfer = errors.New("a linked bilag already has a line on the target account")
)

// ReassignBlockedError refuses a reassign that would rewrite an entry in a
// closed or locked period, in (or dated in) a reference year, or voided.
// Years lists the fiscal years of those entries.
type ReassignBlockedError struct {
	Years []string
}

func (e *ReassignBlockedError) Error() string {
	return "cannot reassign: some matched rows live in closed/locked, reference-year or voided journals (" +
		strings.Join(e.Years, ", ") + ") — reopen them first or void the entries manually"
}

// Is makes errors.Is(err, ErrReassignBlocked) true.
func (e *ReassignBlockedError) Is(target error) bool { return target == ErrReassignBlocked }

// BankImportReassign is what a reassign did. Unchanged is true (and nothing
// was written) when the import was already on the target account.
type BankImportReassign struct {
	From, To         string
	Unchanged        bool
	CascadedJournals int64
}

// ReassignBankImport moves a bank import to another GL bank account and
// swaps the bank-side line of every entry linked from its rows from the old
// account to the new one; counter-legs are untouched.
//
// The import is locked FOR NO KEY UPDATE first (it changes no key, and that
// mode still conflicts with a booking's FOR SHARE); every check and the swap
// run on that one transaction. Every booking that links one of the import's rows
// reads the bank account under the import's FOR SHARE lock (BRY-672,
// BRY-674), so it either commits before the swap (and is swapped with the
// rest) or reads the new account after it, never in between. The reassign
// locks nothing else a booking holds: no bank row, faktura, period or bilag
// number, and only lines of committed entries.
//
// It refuses (*ReassignBlockedError) when an affected entry is in a closed or
// locked period, in or dated in a reference year (BRY-418), or voided: the
// swap rewrites posted lines directly and never reaches the journal guard.
// It refuses ErrReassignCollidesTransfer when an affected entry already has
// a line on the target account.
func (s *Service) ReassignBankImport(ctx context.Context, clubID, importID, newCode string) (*BankImportReassign, error) { //nolint:cyclop // lock, check and swap in one transaction
	if _, err := uuid.Parse(importID); err != nil {
		return nil, ErrBankImportNotFound
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res := &BankImportReassign{To: newCode}
	if err := tx.QueryRow(ctx,
		`SELECT bank_account_code FROM bank_imports WHERE id = $1 AND club_id = $2 FOR NO KEY UPDATE`,
		importID, clubID,
	).Scan(&res.From); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBankImportNotFound
		}
		return nil, fmt.Errorf("locking bank import: %w", err)
	}
	if res.From == newCode {
		res.Unchanged = true
		return res, nil
	}

	var oldAccountID, newAccountID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM accounts WHERE club_id = $1 AND code = $2`, clubID, res.From,
	).Scan(&oldAccountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReassignOldAccountMissing
		}
		return nil, fmt.Errorf("loading old account: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT id FROM accounts WHERE club_id = $1 AND code = $2`, clubID, newCode,
	).Scan(&newAccountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReassignTargetAccountMissing
		}
		return nil, fmt.Errorf("loading target account: %w", err)
	}

	rows, err := tx.Query(ctx,
		`SELECT DISTINCT fp.year::text
		   FROM bank_import_rows bir
		   JOIN journal_entries je ON je.id = bir.journal_entry_id
		   JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		  WHERE bir.bank_import_id = $1
		    AND (fp.status IN ('closed','locked') OR fp.reference_only
		         OR je.status = 'voided' OR `+referenceDateSQL("je.club_id", "je.entry_date")+`)
		  ORDER BY 1`,
		importID,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning blocked periods: %w", err)
	}
	years, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scanning blocked periods: %w", err)
	}
	if len(years) > 0 {
		return nil, &ReassignBlockedError{Years: years}
	}

	var collides bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM journal_lines jl_new
		     JOIN journal_lines jl_old ON jl_old.journal_entry_id = jl_new.journal_entry_id AND jl_old.account_id = $2
		    WHERE jl_new.account_id = $1
		      AND jl_new.journal_entry_id IN (
		        SELECT bir.journal_entry_id FROM bank_import_rows bir
		         WHERE bir.bank_import_id = $3 AND bir.journal_entry_id IS NOT NULL))`,
		newAccountID, oldAccountID, importID,
	).Scan(&collides); err != nil {
		return nil, fmt.Errorf("scanning reassign collisions: %w", err)
	}
	if collides {
		return nil, ErrReassignCollidesTransfer
	}

	// A single-row booking has one bank line; a transfer's entry has one per
	// bank account, and only this import's is swapped (the collision check
	// keeps the two apart), so this is a straight swap, not a re-balance.
	tag, err := tx.Exec(ctx,
		`UPDATE journal_lines
		    SET account_id = $1
		  WHERE account_id = $2
		    AND journal_entry_id IN (
		      SELECT bir.journal_entry_id
		        FROM bank_import_rows bir
		       WHERE bir.bank_import_id = $3
		         AND bir.journal_entry_id IS NOT NULL)`,
		newAccountID, oldAccountID, importID,
	)
	if err != nil {
		return nil, fmt.Errorf("cascading journal lines: %w", err)
	}
	res.CascadedJournals = tag.RowsAffected()

	if _, err := tx.Exec(ctx,
		`UPDATE bank_imports SET bank_account_code = $1 WHERE id = $2 AND club_id = $3`,
		newCode, importID, clubID,
	); err != nil {
		return nil, fmt.Errorf("reassigning bank import: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing reassign: %w", err)
	}
	return res, nil
}
