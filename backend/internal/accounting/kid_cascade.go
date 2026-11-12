package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/finance"
)

// kidCascadeEligibleSQL is true for a bank row the payment-match cascade
// (KID → invoice number → counterpart name, at bank import and bank sync)
// may book: not booked, not dismissed, not paired as a refund, not declined
// by a person's undo (auto_declined_at, ACC-13(b)) and not dated in a
// reference year (BRY-418: that year's deposits are left alone, whatever
// period an import override names). alias is the bank_import_rows alias, ""
// for an unaliased table. The cascade is automatic, and this predicate serves
// only it, so a declined row is filtered here rather than counted.
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
	// The reference check runs in a subquery over fiscal_periods, where a bare
	// club_id would resolve to the period's own column and always be true, so
	// it names the table even when the rest is unqualified.
	t := alias
	if t == "" {
		t = "bank_import_rows"
	}
	return "(" + p + "journal_entry_id IS NULL AND " + p + "dismissed_at IS NULL AND " + p + "refund_paired_with IS NULL AND " +
		p + "auto_declined_at IS NULL AND NOT " +
		referenceDateSQL(t+".club_id", t+".row_date") + ")"
}

// findCascadeInvoice is the cascade's choice of faktura for an incoming row,
// "" when none: each KID-like candidate (kidMatchCandidates: the parsed KID,
// the arkivref, a KID in the description, since some giro-med-melding
// exports carry the real KID outside the KID column), then "Fakturanummer
// NN" with the amount equal to the total (a part payment of a multi-line
// faktura must not land on it by number), then, with nameMatch, a unique
// counterpart name + amount (findInvoiceByCounterpartAndAmountErr). Only
// fakturas still owed and without a KID-booked row qualify; the booking
// re-checks that under the faktura lock.
func (s *Service) findCascadeInvoice(ctx context.Context, clubID string, row BankRow, nameMatch bool) string {
	id, _ := s.findCascadeInvoiceErr(ctx, clubID, row, nameMatch)
	return id
}

// findCascadeInvoiceErr is findCascadeInvoice that also reports the first
// database error it met (BRY-575: WaitingFiscalYears must not read a failed
// lookup as "nothing to book"). The lookups and their order are unchanged:
// an error on one step still goes on to the next, and a faktura found later
// is returned with it.
func (s *Service) findCascadeInvoiceErr(ctx context.Context, clubID string, row BankRow, nameMatch bool) (string, error) {
	var invoiceID string
	var firstErr error
	note := func(err error) {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) && firstErr == nil {
			firstErr = err
		}
	}
	for _, cand := range kidMatchCandidates(row) {
		note(s.db.QueryRow(ctx,
			`SELECT i.id FROM invoices i
			 WHERE i.club_id = $1 AND i.kid_number = $2
			 AND `+finance.OwedByMemberSQL("i")+`
			 AND NOT EXISTS (
			   SELECT 1 FROM bank_import_rows bir
			   WHERE bir.club_id = i.club_id AND bir.kid_number = $2 AND bir.journal_entry_id IS NOT NULL
			 )
			 LIMIT 1`,
			clubID, cand,
		).Scan(&invoiceID))
		if invoiceID != "" {
			return invoiceID, firstErr
		}
	}
	if num := ExtractInvoiceNumberFromDescription(row.Description); num != "" {
		note(s.db.QueryRow(ctx,
			`SELECT i.id FROM invoices i
			 WHERE i.club_id = $1 AND i.invoice_number::text = $2
			   AND ABS(i.total_amount - $3) < 0.005
			   AND `+finance.OwedByMemberSQL("i")+`
			   AND NOT EXISTS (
			     SELECT 1 FROM bank_import_rows bir
			     WHERE bir.club_id = i.club_id AND bir.journal_entry_id IS NOT NULL
			       AND bir.kid_number = i.kid_number
			   )
			 LIMIT 1`,
			clubID, num, row.Amount,
		).Scan(&invoiceID))
		if invoiceID != "" {
			return invoiceID, firstErr
		}
	}
	if nameMatch && row.Counterpart != "" {
		id, err := s.findInvoiceByCounterpartAndAmountErr(ctx, clubID, row.Counterpart, row.Amount)
		note(err)
		return id, firstErr
	}
	return "", firstErr
}

// kidCascadeMatch is one bank row the cascade matched to a faktura.
type kidCascadeMatch struct {
	rowID, invoiceID string
	// actorID posts the entry. bankAccount is the GL code the candidate was
	// read with; the booking debits the code it reads under the import lock
	// (BRY-674), and bankAccount is only the fallback for an empty one.
	actorID, bankAccount string
	// kid and desc make the entry's description (kidMatchDescription).
	kid, desc      string
	date           time.Time
	amount         float64
	periodOverride string
}

var (
	// errKIDPeriodClosed means the row's fiscal period is closed or locked,
	// including one closed while the match ran; the row stays in Tildel and
	// the year is reported.
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
// is never held across a whole import. The period, the entry, the row link
// and the post all run in that transaction, which holds the period FOR
// SHARE from the entry's creation to the commit. A faktura already
// settled is errInvoiceSettled, and the caller leaves the row in Tildel and
// counts it, unless the booking it waited for was this row's own
// (errKIDRowTaken). A faktura voided or credited meanwhile is
// errInvoiceNotOpen; an imported one whose receivable isn't in the GL by
// the row date is ErrImportedNoOpeningBalance (BRY-386).
//
// It never creates a fiscal period (BRY-578): a row dated in a year with no
// period is *noFiscalYearError (errors.Is errNoFiscalPeriod), which the
// callers report as a missing year rather than a row error, and a closed or
// locked period is errKIDPeriodClosed.
//
// The entry, the row link and the post commit together, so a posted entry
// never exists without its row (a later sync would book the money again),
// and the link records the faktura so the other settle paths see this
// booking. Any failure rolls all of it back.
func (s *Service) bookKIDCascadeMatch(ctx context.Context, clubID string, m kidCascadeMatch) error { //nolint:cyclop // one transaction from lock to posting; each outcome is a cascade result; split tracked in BRY-424
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, lockErr := lockInvoiceForAssign(ctx, tx, clubID, m.invoiceID, invoiceLockOpts{mode: lockKIDCascade, bookingDate: m.date})
	if lockErr != nil && !errors.Is(lockErr, errInvoiceSettled) && !errors.Is(lockErr, errInvoiceNotOpen) &&
		!errors.Is(lockErr, ErrImportedNoOpeningBalance) {
		return lockErr
	}
	// The booking we waited for may have been this very row (a second sync,
	// or an import during a sync): then the faktura reads settled, but the
	// row is booked, not a double payment. Checked in a statement after the
	// lock, so it sees what the lock holder committed. The row is locked
	// here, before the entry is created: a booking that locks the row first
	// and then numbers its entry (transfer pairing, Par som overføring)
	// would otherwise deadlock with this one on the bilag number. The
	// import is share-locked with it and the bank account debited is read
	// there, not taken from the candidate: a bank-account reassign (which
	// locks the import FOR NO KEY UPDATE) then either swaps this committed entry or
	// commits first and is read here (BRY-674).
	var bankAccount string
	err = tx.QueryRow(ctx,
		`SELECT bi.bank_account_code
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.id = $1 AND bir.club_id = $2 AND `+kidCascadeEligibleSQL("bir")+`
		    FOR UPDATE OF bir FOR SHARE OF bi`,
		m.rowID, clubID,
	).Scan(&bankAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return errKIDRowTaken
	}
	if err != nil {
		return fmt.Errorf("re-checking row: %w", err)
	}
	if lockErr != nil {
		return lockErr
	}
	if bankAccount == "" {
		bankAccount = m.bankAccount
	}

	// The cascade is automatic whoever started the import or sync, so it
	// never opens a fiscal year: a date with no year is *noFiscalYearError
	// and the row waits in Tildel until the treasurer creates the year. An
	// import's period override names an existing period and is only loaded.
	// Looked up on tx, which holds the faktura lock (BRY-433).
	periodID, err := openPeriodIn(ctx, tx, clubID, m.date, m.periodOverride)
	if errors.Is(err, ErrVippsPeriodClosed) {
		return errKIDPeriodClosed
	}
	if err != nil {
		return err
	}

	sourceID := m.rowID
	sourceTable := "bank_import_rows"
	entry, err := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      m.date.Format("2006-01-02"),
		Description:    kidMatchDescription(m.kid, m.desc),
		Source:         "bank_import",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      m.actorID,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: bankAccount, Debit: m.amount, Credit: 0},
			{AccountCode: receivablesAccountCode, Debit: 0, Credit: m.amount},
		},
	})
	if errors.Is(err, ErrPeriodClosed) {
		// Closed between openPeriodIn and here, by a concurrent ClosePeriod.
		return errKIDPeriodClosed
	}
	if err != nil {
		return fmt.Errorf("creating journal entry: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1, invoice_id = $4, auto_matched = true
		  WHERE id = $2 AND club_id = $3 AND `+kidCascadeEligibleSQL(""),
		entry.ID, m.rowID, clubID, m.invoiceID,
	)
	if err != nil || tag.RowsAffected() != 1 {
		if err == nil {
			err = errKIDRowTaken
		}
		return fmt.Errorf("linking row: %w", err)
	}
	if err := postJournalEntryTx(ctx, tx, clubID, entry.ID, m.actorID); err != nil {
		if errors.Is(err, ErrPeriodClosed) {
			return errKIDPeriodClosed
		}
		return fmt.Errorf("posting journal entry: %w", err)
	}
	// The back-link updates the faktura, so it runs once the commit has
	// released the lock.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing KID match: %w", err)
	}

	// Best-effort: the GL is already correct, and bank sync's back-link
	// passes heal a missing full-amount payment row. It follows the commit,
	// so it runs on postCommitContext: a request or run cancelled right
	// after the booking still marks the faktura paid.
	linkCtx, cancel := postCommitContext(ctx)
	defer cancel()
	if err := s.linkInvoicePayment(linkCtx, clubID, m.invoiceID, m.date); err != nil {
		s.log.Warn().Err(err).
			Str("invoice_id", m.invoiceID).
			Str("bank_row_id", m.rowID).
			Msg("KID match journal posted but invoice payment back-link failed")
	}
	return nil
}
