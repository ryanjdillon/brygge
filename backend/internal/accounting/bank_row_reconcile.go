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
	"bounced":           true,
	"internal_transfer": true,
	"duplicate":         true,
	"double_payment":    true,
	"bank_fee":          true,
	"refund_or_credit":  true,
	"overpayment":       true,
	"unidentifiable":    true,
	"test_transaction":  true,
	// superseded: a provisional "Bokført på vei" placeholder row that
	// the bank later replaced with a fully-attributed settled row
	// (DIL-325). Lets the treasurer clear the stale placeholder without
	// touching journal entries.
	"superseded": true,
}

// IsValidDismissReason returns true when r is one of the values allowed
// by the bank_row_dismissal CHECK. Handlers use this to reject invalid
// input before the DB does.
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
		`UPDATE bank_import_rows SET journal_entry_id = $1, invoice_id = $2, auto_matched = false WHERE id = $3`,
		entry.ID, invoiceID, bankRowID,
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
	ctx context.Context, clubID, actorID, bankRowID, accountCode, kind, description string,
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
	if description != "" {
		desc = truncate(description, 220)
	}
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

	var invoiceIDPtr *string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(journal_entry_id::text, ''), invoice_id
		   FROM bank_import_rows
		  WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&priorJournalID, &invoiceIDPtr); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errors.New("bank row not found")
		}
		return "", "", fmt.Errorf("load row: %w", err)
	}
	if priorJournalID == "" {
		return "", "", errors.New("bank row not assigned")
	}
	if invoiceIDPtr != nil {
		priorInvoiceID = *invoiceIDPtr
	}

	// Collect all journal entry IDs to void. For multi-row invoice
	// assignments every sibling row shares the same invoice_id and
	// must be unassigned atomically with this one.
	journalIDsToVoid := []string{priorJournalID}
	if priorInvoiceID != "" {
		rows, rerr := tx.Query(ctx,
			`SELECT COALESCE(journal_entry_id::text, '')
			   FROM bank_import_rows
			  WHERE invoice_id = $1 AND club_id = $2 AND id != $3
			    AND journal_entry_id IS NOT NULL`,
			priorInvoiceID, clubID, bankRowID,
		)
		if rerr != nil {
			return "", "", fmt.Errorf("load siblings: %w", rerr)
		}
		for rows.Next() {
			var jid string
			if err := rows.Scan(&jid); err != nil {
				rows.Close()
				return "", "", fmt.Errorf("scan sibling: %w", err)
			}
			journalIDsToVoid = append(journalIDsToVoid, jid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", "", fmt.Errorf("siblings: %w", err)
		}

		// Clear all sibling rows.
		if _, err := tx.Exec(ctx,
			`UPDATE bank_import_rows
			    SET journal_entry_id = NULL, invoice_id = NULL, auto_matched = false
			  WHERE invoice_id = $1 AND club_id = $2`,
			priorInvoiceID, clubID,
		); err != nil {
			return "", "", fmt.Errorf("clear sibling rows: %w", err)
		}
	} else {
		// Single-row case: no invoice back-link. Use legacy lookup via
		// payments table for rows assigned before migration 000057.
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

		if _, err := tx.Exec(ctx,
			`UPDATE bank_import_rows
			    SET journal_entry_id = NULL, invoice_id = NULL, auto_matched = false
			  WHERE id = $1`,
			bankRowID,
		); err != nil {
			return "", "", fmt.Errorf("clear row: %w", err)
		}
	}

	if priorInvoiceID != "" {
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

	for _, jid := range journalIDsToVoid {
		if _, verr := s.VoidJournalEntry(ctx, jid, actorID); verr != nil {
			s.log.Warn().Err(verr).
				Str("journal_entry_id", jid).
				Str("bank_row_id", bankRowID).
				Msg("UnassignBankRow: void journal failed; row cleared but GL still posted to original")
		}
	}
	return priorJournalID, priorInvoiceID, nil
}

// AssignMultipleBankRowsToInvoice assigns N bank rows to a single
// invoice in one atomic transaction. The sum of the row amounts must
// equal invoice.total_amount exactly (compared in integer cents to
// avoid float rounding). All rows get their own journal entry; the
// invoice is marked paid via linkInvoicePayment using the latest row
// date. Unassigning any one of the resulting rows reverses all of them.
func (s *Service) AssignMultipleBankRowsToInvoice(
	ctx context.Context, clubID, actorID string, rowIDs []string, invoiceID string,
) ([]string, error) {
	if len(rowIDs) < 2 {
		return nil, errors.New("use assign-invoice for single rows")
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	type rowData struct {
		id       string
		date     time.Time
		amount   float64
		desc     string
		bankAcct string
	}
	loaded := make([]rowData, 0, len(rowIDs))
	for _, rid := range rowIDs {
		var rd rowData
		rd.id = rid
		var alreadyJrnl, dismissed bool
		if err := tx.QueryRow(ctx,
			`SELECT bir.row_date, bir.amount, bir.description,
			        bi.bank_account_code,
			        bir.journal_entry_id IS NOT NULL,
			        bir.dismissed_at IS NOT NULL
			   FROM bank_import_rows bir
			   JOIN bank_imports bi ON bi.id = bir.bank_import_id
			  WHERE bir.id = $1 AND bir.club_id = $2`,
			rid, clubID,
		).Scan(&rd.date, &rd.amount, &rd.desc, &rd.bankAcct, &alreadyJrnl, &dismissed); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("bank row %s not found", rid)
			}
			return nil, fmt.Errorf("load bank row %s: %w", rid, err)
		}
		if alreadyJrnl {
			return nil, fmt.Errorf("bank row %s is already reconciled", rid)
		}
		if dismissed {
			return nil, fmt.Errorf("bank row %s is dismissed", rid)
		}
		if rd.amount <= 0 {
			return nil, fmt.Errorf("bank row %s has non-positive amount", rid)
		}
		loaded = append(loaded, rd)
	}

	var invoiceTotal float64
	var paymentID *string
	var invoiceStatus string
	if err := tx.QueryRow(ctx,
		`SELECT total_amount, payment_id, status FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID,
	).Scan(&invoiceTotal, &paymentID, &invoiceStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("invoice not found")
		}
		return nil, fmt.Errorf("load invoice: %w", err)
	}
	if invoiceStatus == "voided" {
		return nil, errors.New("invoice is voided")
	}
	if paymentID != nil {
		return nil, errors.New("invoice is already paid")
	}

	var sumAmount float64
	var latestDate time.Time
	for _, rd := range loaded {
		sumAmount += rd.amount
		if rd.date.After(latestDate) {
			latestDate = rd.date
		}
	}
	if sumCents, invCents := int64(sumAmount*100+0.5), int64(invoiceTotal*100+0.5); sumCents != invCents {
		return nil, fmt.Errorf("row sum %.2f does not match invoice total %.2f", sumAmount, invoiceTotal)
	}

	periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, latestDate, "")
	if perr != nil {
		return nil, fmt.Errorf("resolve period: %w", perr)
	}
	if periodStatus == "closed" {
		return nil, fmt.Errorf("fiscal period for %s is closed; reopen first", latestDate.Format("2006-01-02"))
	}

	journalIDs := make([]string, 0, len(loaded))
	for _, rd := range loaded {
		srcID := rd.id
		srcTable := "bank_import_rows"
		desc := fmt.Sprintf("Manuell tildeling: %s", truncate(rd.desc, 200))
		entry, cerr := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID,
			EntryDate:      rd.date.Format("2006-01-02"),
			Description:    desc,
			Source:         "bank_import",
			SourceID:       &srcID,
			SourceTable:    &srcTable,
			CreatedBy:      actorID,
			ClubID:         clubID,
			Lines: []CreateJournalLineInput{
				{AccountCode: rd.bankAcct, Debit: rd.amount, Credit: 0},
				{AccountCode: receivablesAccountCode, Debit: 0, Credit: rd.amount},
			},
		})
		if cerr != nil {
			return nil, fmt.Errorf("create journal for row %s: %w", rd.id, cerr)
		}
		if err := s.PostJournalEntry(ctx, entry.ID, actorID); err != nil {
			return nil, fmt.Errorf("post journal for row %s: %w", rd.id, err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE bank_import_rows
			    SET journal_entry_id = $1, invoice_id = $2, auto_matched = false
			  WHERE id = $3`,
			entry.ID, invoiceID, rd.id,
		); err != nil {
			return nil, fmt.Errorf("mark row %s matched: %w", rd.id, err)
		}
		journalIDs = append(journalIDs, entry.ID)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	if err := s.linkInvoicePayment(ctx, clubID, invoiceID, latestDate); err != nil {
		s.log.Warn().Err(err).
			Str("invoice_id", invoiceID).
			Msg("AssignMultipleBankRowsToInvoice: linkInvoicePayment failed; GL posted but dashboard back-link missing")
	}
	return journalIDs, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}
