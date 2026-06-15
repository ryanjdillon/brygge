package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// DIL-392 Phase 1: per-row manual reconciliation.
//
// This file owns the mutating service methods that back the new
// /admin/accounting/bank-rows/* endpoints. Reads (list / count /
// suggestions / potential-invoices) live in bank_row_queue.go.
//
// Invariants the methods preserve, mirroring banksync.go:
//   - journal_entry_id IS NOT NULL ⇒ row is reconciled. Both queues
//     and auto-matchers honour this; no parallel "manually_assigned"
//     flag.
//   - dismissed_at IS NOT NULL ⇒ row is permanently excluded from the
//     queue but kept for audit. (CHECK constraint requires all three
//     dismissed_* columns travel together.)
//   - linkInvoicePayment is the only path that touches
//     invoices.payment_id; same reuse here as in the auto-match path.

// Allowed dismissal reasons. Mirrors the CHECK constraint on
// bank_import_rows.dismissed_reason. Validated in handler input
// parsing too — defense in depth.
var allowedDismissReasons = map[string]bool{
	"bounced":          true,
	"internal_transfer": true,
	"duplicate":        true,
	"double_payment":   true,
	"bank_fee":         true,
	"refund_or_credit": true,
	"overpayment":      true,
	"unidentifiable":   true,
	"test_transaction": true,
}

// IsValidDismissReason returns true when r is one of the eight values
// allowed by the bank_row_dismissal CHECK. Handlers use this to reject
// invalid input before the DB does.
func IsValidDismissReason(r string) bool {
	return allowedDismissReasons[r]
}

// AssignBankRowToInvoice creates a balanced bank-DR / receivables-CR
// journal entry for the row, marks the row reconciled, and calls
// linkInvoicePayment so the dashboard reflects the payment. Returns
// the new journal entry ID for the audit log.
//
// Errors when the bank row is already reconciled or dismissed, the
// invoice is already paid or voided, or the fiscal period for the
// row's date is closed.
func (s *Service) AssignBankRowToInvoice(
	ctx context.Context, clubID, actorID, bankRowID, invoiceID string,
) (string, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		rowDate     time.Time
		rowAmount   float64
		rowDesc     string
		bankAcct    string
		alreadyJrnl bool
		dismissed   bool
	)
	if err := tx.QueryRow(ctx,
		`SELECT bir.row_date, bir.amount, bir.description,
		        bi.bank_account_code,
		        bir.journal_entry_id IS NOT NULL,
		        bir.dismissed_at IS NOT NULL
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.id = $1 AND bir.club_id = $2`,
		bankRowID, clubID,
	).Scan(&rowDate, &rowAmount, &rowDesc, &bankAcct, &alreadyJrnl, &dismissed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("bank row not found")
		}
		return "", fmt.Errorf("load bank row: %w", err)
	}
	if alreadyJrnl {
		return "", errors.New("bank row already reconciled — unassign first")
	}
	if dismissed {
		return "", errors.New("bank row is dismissed")
	}
	if rowAmount <= 0 {
		return "", errors.New("assign-invoice requires a positive (incoming) amount; use assign-account for outgoing rows")
	}

	var invoiceTotal float64
	var paymentID *string
	var invoiceStatus string
	if err := tx.QueryRow(ctx,
		`SELECT total_amount, payment_id, status
		   FROM invoices
		  WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID,
	).Scan(&invoiceTotal, &paymentID, &invoiceStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("invoice not found")
		}
		return "", fmt.Errorf("load invoice: %w", err)
	}
	if invoiceStatus == "voided" {
		return "", errors.New("invoice is voided")
	}
	if paymentID != nil {
		return "", errors.New("invoice is already paid")
	}

	periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, rowDate, "")
	if perr != nil {
		return "", fmt.Errorf("resolve period: %w", perr)
	}
	if periodStatus == "closed" {
		return "", fmt.Errorf("fiscal period for %s is closed; reopen first", rowDate.Format("2006-01-02"))
	}

	sourceID := bankRowID
	sourceTable := "bank_import_rows"
	desc := fmt.Sprintf("Manuell tildeling: %s", truncate(rowDesc, 200))
	entry, cerr := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      rowDate.Format("2006-01-02"),
		Description:    desc,
		Source:         "bank_import",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      actorID,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: bankAcct, Debit: rowAmount, Credit: 0},
			{AccountCode: receivablesAccountCode, Debit: 0, Credit: rowAmount},
		},
	})
	if cerr != nil {
		return "", fmt.Errorf("create journal: %w", cerr)
	}
	if err := s.PostJournalEntry(ctx, entry.ID, actorID); err != nil {
		return "", fmt.Errorf("post journal: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = false WHERE id = $2`,
		entry.ID, bankRowID,
	); err != nil {
		return "", fmt.Errorf("mark row matched: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	// linkInvoicePayment outside the tx — it's idempotent and best-effort
	// per the existing DIL-363 contract. A failure here doesn't roll
	// back the GL journal (which is already correct), it just means
	// the dashboard will reflect the GL but not the payments row until
	// a subsequent sync or the operator retries.
	if err := s.linkInvoicePayment(ctx, clubID, invoiceID, rowDate); err != nil {
		s.log.Warn().Err(err).
			Str("invoice_id", invoiceID).
			Str("bank_row_id", bankRowID).
			Msg("AssignBankRowToInvoice: linkInvoicePayment failed; GL is posted but dashboard back-link missing")
	}
	return entry.ID, nil
}

// AssignBankRowToAccount creates a journal entry against an operator-
// picked GL account instead of an invoice. kind selects which side the
// chosen account sits on:
//
//   - kind="expense" — outgoing row (amount < 0). DR account, CR bank.
//     The bank-row amount is treated as positive in the journal.
//   - kind="revenue" — incoming row (amount > 0) that isn't a faktura
//     (e.g. donation, misc income). DR bank, CR account.
//
// Returns the new journal entry ID.
func (s *Service) AssignBankRowToAccount(
	ctx context.Context, clubID, actorID, bankRowID, accountCode, kind string,
) (string, error) {
	if kind != "expense" && kind != "revenue" {
		return "", errors.New("kind must be 'expense' or 'revenue'")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		rowDate     time.Time
		rowAmount   float64
		rowDesc     string
		bankAcct    string
		alreadyJrnl bool
		dismissed   bool
	)
	if err := tx.QueryRow(ctx,
		`SELECT bir.row_date, bir.amount, bir.description,
		        bi.bank_account_code,
		        bir.journal_entry_id IS NOT NULL,
		        bir.dismissed_at IS NOT NULL
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.id = $1 AND bir.club_id = $2`,
		bankRowID, clubID,
	).Scan(&rowDate, &rowAmount, &rowDesc, &bankAcct, &alreadyJrnl, &dismissed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("bank row not found")
		}
		return "", fmt.Errorf("load bank row: %w", err)
	}
	if alreadyJrnl {
		return "", errors.New("bank row already reconciled — unassign first")
	}
	if dismissed {
		return "", errors.New("bank row is dismissed")
	}
	if kind == "expense" && rowAmount >= 0 {
		return "", errors.New("expense kind requires a negative (outgoing) amount")
	}
	if kind == "revenue" && rowAmount <= 0 {
		return "", errors.New("revenue kind requires a positive (incoming) amount")
	}

	periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, rowDate, "")
	if perr != nil {
		return "", fmt.Errorf("resolve period: %w", perr)
	}
	if periodStatus == "closed" {
		return "", fmt.Errorf("fiscal period for %s is closed", rowDate.Format("2006-01-02"))
	}

	// Validate the picked account exists for this club.
	var accountExists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM accounts WHERE club_id = $1 AND code = $2 AND is_active)`,
		clubID, accountCode,
	).Scan(&accountExists); err != nil {
		return "", fmt.Errorf("validate account: %w", err)
	}
	if !accountExists {
		return "", fmt.Errorf("account %s not found in chart of accounts", accountCode)
	}

	absAmount := rowAmount
	if absAmount < 0 {
		absAmount = -absAmount
	}
	var lines []CreateJournalLineInput
	if kind == "expense" {
		lines = []CreateJournalLineInput{
			{AccountCode: accountCode, Debit: absAmount, Credit: 0},
			{AccountCode: bankAcct, Debit: 0, Credit: absAmount},
		}
	} else {
		lines = []CreateJournalLineInput{
			{AccountCode: bankAcct, Debit: absAmount, Credit: 0},
			{AccountCode: accountCode, Debit: 0, Credit: absAmount},
		}
	}

	sourceID := bankRowID
	sourceTable := "bank_import_rows"
	desc := fmt.Sprintf("Manuell tildeling: %s", truncate(rowDesc, 200))
	entry, cerr := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      rowDate.Format("2006-01-02"),
		Description:    desc,
		Source:         "bank_import",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      actorID,
		ClubID:         clubID,
		Lines:          lines,
	})
	if cerr != nil {
		return "", fmt.Errorf("create journal: %w", cerr)
	}
	if err := s.PostJournalEntry(ctx, entry.ID, actorID); err != nil {
		return "", fmt.Errorf("post journal: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = false WHERE id = $2`,
		entry.ID, bankRowID,
	); err != nil {
		return "", fmt.Errorf("mark row matched: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return entry.ID, nil
}

// DismissBankRow flags the row as not-actionable with a structured
// reason. Sets all three dismissal columns atomically per the
// bank_row_dismissal_consistency check constraint.
func (s *Service) DismissBankRow(ctx context.Context, clubID, actorID, bankRowID, reason string) error {
	if !IsValidDismissReason(reason) {
		return errors.New("invalid dismiss reason")
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows
		    SET dismissed_at = now(), dismissed_by = $1, dismissed_reason = $2
		  WHERE id = $3 AND club_id = $4
		    AND journal_entry_id IS NULL
		    AND dismissed_at IS NULL`,
		actorID, reason, bankRowID, clubID,
	)
	if err != nil {
		return fmt.Errorf("dismiss: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("bank row not found, already reconciled, or already dismissed")
	}
	return nil
}

// UnassignBankRow reverses a prior assignment: voids the journal
// entry (creates a balanced reversal), nulls the row's
// journal_entry_id so it returns to the queue, removes the payments
// row if linkInvoicePayment had set one, and clears
// invoices.payment_id. The original journal entry plus the reversal
// stay in the GL — bokføringsloven §13 requires audit retention.
//
// Returns the prior journal entry ID and invoice ID (if any) for the
// audit row.
func (s *Service) UnassignBankRow(
	ctx context.Context, clubID, actorID, bankRowID string,
) (priorJournalID, priorInvoiceID string, err error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(journal_entry_id::text, '')
		   FROM bank_import_rows
		  WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&priorJournalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errors.New("bank row not found")
		}
		return "", "", fmt.Errorf("load row: %w", err)
	}
	if priorJournalID == "" {
		return "", "", errors.New("bank row not assigned")
	}

	// Identify any invoice that was linked via this bank row's
	// journal (the payments row carries the source via paid_at +
	// user_id + amount — see linkInvoicePayment — so we look it up
	// via the journal's source instead, which is more reliable).
	_ = tx.QueryRow(ctx,
		`SELECT i.id
		   FROM invoices i
		   JOIN payments p ON p.id = i.payment_id
		   JOIN journal_entries je
		     ON je.club_id = i.club_id
		    AND je.id::text = $1
		    AND je.entry_date = p.paid_at::date
		  WHERE i.club_id = $2
		    AND i.user_id = p.user_id
		    AND i.total_amount = p.amount
		  LIMIT 1`,
		priorJournalID, clubID,
	).Scan(&priorInvoiceID)

	// Void the journal entry (creates a balanced reversal). Must
	// commit our outer tx first since VoidJournalEntry opens its own.
	if _, err := tx.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = NULL, auto_matched = false WHERE id = $1`,
		bankRowID,
	); err != nil {
		return "", "", fmt.Errorf("clear row: %w", err)
	}

	if priorInvoiceID != "" {
		// Clear the payment back-link + delete the payments row so
		// the dashboard returns to "unpaid" state and the row can be
		// re-assigned cleanly.
		var paymentID *string
		if err := tx.QueryRow(ctx,
			`SELECT payment_id FROM invoices WHERE id = $1`,
			priorInvoiceID,
		).Scan(&paymentID); err != nil {
			return "", "", fmt.Errorf("load invoice payment: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE invoices SET payment_id = NULL WHERE id = $1 AND club_id = $2`,
			priorInvoiceID, clubID,
		); err != nil {
			return "", "", fmt.Errorf("clear invoice payment_id: %w", err)
		}
		if paymentID != nil {
			if _, err := tx.Exec(ctx,
				`DELETE FROM payments WHERE id = $1 AND club_id = $2`,
				*paymentID, clubID,
			); err != nil {
				return "", "", fmt.Errorf("delete payment row: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("commit: %w", err)
	}

	if _, verr := s.VoidJournalEntry(ctx, priorJournalID, actorID); verr != nil {
		s.log.Warn().Err(verr).
			Str("journal_entry_id", priorJournalID).
			Str("bank_row_id", bankRowID).
			Msg("UnassignBankRow: void journal failed; row cleared but GL still posted to original")
	}
	return priorJournalID, priorInvoiceID, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}
