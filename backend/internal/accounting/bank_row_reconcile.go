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
		return "", errBankRowRaced
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
	if periodStatus != "open" {
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
	if err := s.linkAndPostBankRow(ctx, clubID, actorID, entry.ID, bankRowID, &invoiceID); err != nil {
		return "", err
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
		return "", errBankRowRaced
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
	if periodStatus != "open" {
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
	if err := s.linkAndPostBankRow(ctx, clubID, actorID, entry.ID, bankRowID, nil); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// errBankRowRaced means another booking linked the row first.
var errBankRowRaced = errors.New("bank row already reconciled — unassign first")

// linkAndPostBankRow links a manual assignment's draft to its bank row and
// then posts it. The link is guarded on the row still being unbooked and
// not dismissed, so of two concurrent assigns exactly one links; the loser's
// draft is discarded. Posting comes after the link so a posted entry never
// exists without its row (a later bank sync would book the money again);
// a failed post unlinks the row and deletes the draft. invoiceID, when set,
// is recorded on the row as the faktura it settles.
func (s *Service) linkAndPostBankRow(ctx context.Context, clubID, actorID, entryID, bankRowID string, invoiceID *string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1, invoice_id = COALESCE($2, invoice_id), auto_matched = false
		  WHERE id = $3 AND club_id = $4 AND journal_entry_id IS NULL AND dismissed_at IS NULL`,
		entryID, invoiceID, bankRowID, clubID,
	)
	if err != nil || tag.RowsAffected() != 1 {
		s.discardDraft(ctx, clubID, entryID)
		if err != nil {
			return fmt.Errorf("mark row matched: %w", err)
		}
		return errBankRowRaced
	}
	if err := s.PostJournalEntry(ctx, clubID, entryID, actorID); err != nil {
		if _, uerr := s.db.Exec(ctx,
			`UPDATE bank_import_rows
			    SET journal_entry_id = NULL, auto_matched = false,
			        invoice_id = CASE WHEN $3::uuid IS NULL THEN invoice_id ELSE NULL END
			  WHERE club_id = $1 AND journal_entry_id = $2`,
			clubID, entryID, invoiceID,
		); uerr != nil {
			s.log.Error().Err(uerr).Str("entry_id", entryID).Msg("unlinking bank row after a failed post failed")
		}
		s.discardDraft(ctx, clubID, entryID)
		return fmt.Errorf("post journal: %w", err)
	}
	return nil
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

// ErrBankRowNotFound is returned for a row id that doesn't exist or belongs
// to another club.
var ErrBankRowNotFound = errors.New("bank row not found")

// ErrBankRowNotAssigned is returned when unassigning a row with no booking.
var ErrBankRowNotAssigned = errors.New("bank row not assigned")

// UnassignBankRow reverses a prior assignment and returns the row to the
// queue. Every row that shares the booking (multi-row faktura assignments
// share an invoice_id; an intra-bank transfer's two rows share one entry)
// is unassigned with it.
//
// It all runs in one transaction: the posted entries are voided (each a
// balanced reversal; the original and the reversal stay in the GL for
// bokføringsloven §13 retention), drafts are deleted (they were never
// reported), the rows are cleared, and invoices.payment_id and the payments
// row linkInvoicePayment created are removed. Each void holds its period FOR
// SHARE until commit, so a posted entry in a closed or locked period fails
// with ErrPeriodClosed and rolls everything back, and ClosePeriod cannot
// close a period between the check and the reversal. Entries already voided
// (an earlier run, or a manual void) are skipped.
//
// Returns the prior journal entry ID and invoice ID (if any) for the audit
// row.
func (s *Service) UnassignBankRow(
	ctx context.Context, clubID, actorID, bankRowID string,
) (priorJournalID, priorInvoiceID string, err error) {
	var invoiceIDPtr *string
	if err := s.db.QueryRow(ctx,
		`SELECT COALESCE(journal_entry_id::text, ''), invoice_id
		   FROM bank_import_rows
		  WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&priorJournalID, &invoiceIDPtr); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrBankRowNotFound
		}
		return "", "", fmt.Errorf("load row: %w", err)
	}
	if priorJournalID == "" {
		return "", "", ErrBankRowNotAssigned
	}
	if invoiceIDPtr != nil {
		priorInvoiceID = *invoiceIDPtr
	} else {
		// Rows assigned before migration 000057 have no invoice back-link;
		// recover the faktura through the payment the assignment created.
		_ = s.db.QueryRow(ctx,
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
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Every entry booked for this assignment: this row's, its invoice
	// siblings', and any other row pointing at the same entry.
	type involved struct {
		id, status string
	}
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT je.id, je.status
		   FROM bank_import_rows b
		   JOIN journal_entries je ON je.id = b.journal_entry_id AND je.club_id = b.club_id
		  WHERE b.club_id = $1
		    AND (b.id = $2
		         OR ($3 <> '' AND b.invoice_id::text = $3)
		         OR b.journal_entry_id::text = $4)
		  ORDER BY je.id`,
		clubID, bankRowID, priorInvoiceID, priorJournalID,
	)
	if err != nil {
		return "", "", fmt.Errorf("load bookings: %w", err)
	}
	var entries []involved
	for rows.Next() {
		var e involved
		if err := rows.Scan(&e.id, &e.status); err != nil {
			rows.Close()
			return "", "", fmt.Errorf("scan booking: %w", err)
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", "", fmt.Errorf("bookings: %w", err)
	}

	var drafts []string
	for _, e := range entries {
		switch e.status {
		case "posted":
			if _, err := voidJournalEntryTx(ctx, tx, clubID, e.id, actorID); err != nil {
				// A concurrent unassign of a sibling row voided it first
				// (the status above was read before the row lock).
				if errors.Is(err, ErrEntryNotPosted) {
					continue
				}
				if errors.Is(err, ErrPeriodClosed) {
					return "", "", fmt.Errorf("bank row is booked in a closed fiscal period; reopen it or correct with a new entry: %w", ErrPeriodClosed)
				}
				return "", "", fmt.Errorf("voiding bilag: %w", err)
			}
		case "draft":
			drafts = append(drafts, e.id)
		}
	}

	entryIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		entryIDs = append(entryIDs, e.id)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bank_import_rows
		    SET journal_entry_id = NULL, invoice_id = NULL, auto_matched = false
		  WHERE club_id = $1
		    AND (id = $2
		         OR ($3 <> '' AND invoice_id::text = $3)
		         OR journal_entry_id = ANY($4::uuid[]))`,
		clubID, bankRowID, priorInvoiceID, entryIDs,
	); err != nil {
		return "", "", fmt.Errorf("clear rows: %w", err)
	}
	if len(drafts) > 0 {
		if _, err := tx.Exec(ctx,
			`DELETE FROM journal_entries
			  WHERE club_id = $1 AND status = 'draft' AND id = ANY($2::uuid[])`,
			clubID, drafts,
		); err != nil {
			return "", "", fmt.Errorf("delete draft bilag: %w", err)
		}
	}
	if priorInvoiceID != "" {
		var paymentID *string
		if err := tx.QueryRow(ctx,
			`SELECT payment_id FROM invoices WHERE id = $1 AND club_id = $2`,
			priorInvoiceID, clubID,
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
	if periodStatus != "open" {
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
		if err := s.PostJournalEntry(ctx, clubID, entry.ID, actorID); err != nil {
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
