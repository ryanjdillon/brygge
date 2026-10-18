package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// kidCascadeEligibleSQL is true for a bank row the payment-match cascade
// (KID → invoice number → counterpart name, at bank import and bank sync)
// may book: not booked, not dismissed and not paired as a refund. alias is
// the bank_import_rows alias, "" for an unaliased table.
//
// Bank sync lists its candidates with it, and both cascades guard the row
// link with it, so a row the treasurer dismissed while a sync was waiting
// on the faktura lock is not booked either. Rejected-KID notice rows
// (IsRejectedKIDDescription) are not part of it because that check is on
// the description in Go; both cascades skip them before matching.
func kidCascadeEligibleSQL(alias string) string {
	p := ""
	if alias != "" {
		p = alias + "."
	}
	return "(" + p + "journal_entry_id IS NULL AND " + p + "dismissed_at IS NULL AND " + p + "refund_paired_with IS NULL)"
}

// kidCascadeMatch is one bank row the cascade matched to a faktura.
type kidCascadeMatch struct {
	rowID, invoiceID string
	// actorID posts the entry; bankAccount is the GL code debited.
	actorID, bankAccount string
	// kid and desc make the entry's description (kidMatchDescription).
	kid, desc      string
	date           time.Time
	amount         float64
	periodOverride string
}

var (
	// errKIDPeriodClosed means the row's fiscal period is closed; the row
	// stays in Tildel and the year is reported.
	errKIDPeriodClosed = errors.New("fiscal period is closed")
	// errKIDRowTaken means the row was booked, dismissed or refund-paired
	// after the cascade picked it.
	errKIDRowTaken = errors.New("row was booked or dismissed concurrently")
)

// bookKIDCascadeMatch books one cascade match: DR the bank account, CR
// receivables, and the faktura's payment back-link.
//
// It holds the faktura lock (lockInvoiceForAssign, cascade mode) from
// before the entry is created until the row is linked and the entry
// posted, in a transaction of its own, so a manual, auto or Vipps assign of
// the same faktura runs before or after it, never alongside, and the lock
// is never held across a whole import. The lock order is faktura, then
// bank row, then period, as on every other settle path. A faktura already
// settled is errInvoiceSettled; the caller leaves the row in Tildel and
// counts it.
//
// The row is linked before the entry is posted, so a posted entry never
// exists without its row (a later sync would book the money again), and the
// link records the faktura so the other settle paths see this booking. A
// failed post unlinks the row and drops the draft.
func (s *Service) bookKIDCascadeMatch(ctx context.Context, clubID string, m kidCascadeMatch) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockInvoiceForAssign(ctx, tx, clubID, m.invoiceID, invoiceLockOpts{mode: lockKIDCascade}); err != nil {
		return err
	}

	periodID, periodStatus, err := s.resolvePeriod(ctx, clubID, m.date, m.periodOverride)
	if err != nil {
		return fmt.Errorf("resolving fiscal period: %w", err)
	}
	if periodStatus == "closed" {
		return errKIDPeriodClosed
	}

	sourceID := m.rowID
	sourceTable := "bank_import_rows"
	entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      m.date.Format("2006-01-02"),
		Description:    kidMatchDescription(m.kid, m.desc),
		Source:         "bank_import",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      m.actorID,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: m.bankAccount, Debit: m.amount, Credit: 0},
			{AccountCode: receivablesAccountCode, Debit: 0, Credit: m.amount},
		},
	})
	if err != nil {
		return fmt.Errorf("creating journal entry: %w", err)
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1, invoice_id = $4, auto_matched = true
		  WHERE id = $2 AND club_id = $3 AND `+kidCascadeEligibleSQL(""),
		entry.ID, m.rowID, clubID, m.invoiceID,
	)
	if err != nil || tag.RowsAffected() != 1 {
		s.discardDraft(ctx, clubID, entry.ID)
		if err == nil {
			err = errKIDRowTaken
		}
		return fmt.Errorf("linking row: %w", err)
	}
	if err := s.PostJournalEntry(ctx, clubID, entry.ID, m.actorID); err != nil {
		if _, uerr := s.db.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id = NULL, invoice_id = NULL, auto_matched = false
			  WHERE id = $1 AND club_id = $2 AND journal_entry_id = $3`,
			m.rowID, clubID, entry.ID,
		); uerr != nil {
			s.log.Error().Err(uerr).Str("entry_id", entry.ID).Msg("KID match: unlinking the row after a failed post failed")
		}
		s.discardDraft(ctx, clubID, entry.ID)
		return fmt.Errorf("posting journal entry: %w", err)
	}
	// Release the faktura lock before the back-link, which updates the
	// faktura from another connection and would otherwise wait on it. The
	// booking is complete either way; a failed commit only ends the lock.
	if err := tx.Commit(ctx); err != nil {
		s.log.Warn().Err(err).Str("bank_row_id", m.rowID).Msg("KID match: releasing the faktura lock failed")
	}

	// Best-effort: the GL is already correct, and bank sync's back-link
	// passes heal a missing full-amount payment row.
	if err := s.linkInvoicePayment(ctx, clubID, m.invoiceID, m.date); err != nil {
		s.log.Warn().Err(err).
			Str("invoice_id", m.invoiceID).
			Str("bank_row_id", m.rowID).
			Msg("KID match journal posted but invoice payment back-link failed")
	}
	return nil
}
