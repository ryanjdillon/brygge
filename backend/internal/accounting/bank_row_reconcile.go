package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/finance"
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
	entryID, _, err := s.assignBankRowToInvoice(ctx, clubID, actorID, bankRowID, invoiceID, assignOpts{})
	return entryID, err
}

// AssignBankRowToInvoiceRemembering is AssignBankRowToInvoice that can also
// remember the row's payer for the faktura's member (BRY-383). It returns the
// alias it saved, or nil. A conflicting payer is ErrAliasConflict with
// nothing booked unless alias.ConfirmOverride is set.
func (s *Service) AssignBankRowToInvoiceRemembering(
	ctx context.Context, clubID, actorID, bankRowID, invoiceID string, alias PayerAliasRequest,
) (string, *TaughtAlias, error) {
	return s.assignBankRowToInvoice(ctx, clubID, actorID, bankRowID, invoiceID, assignOpts{alias: alias})
}

// assignOpts tunes assignBankRowToInvoice.
type assignOpts struct {
	// alias asks a manual assign to remember the payer.
	alias PayerAliasRequest
	// auto marks a booking by the bank auto-assign rule (BRY-366): it is
	// described as automatic, re-checks inside the faktura lock that the
	// faktura is still the clean single match the rule decided on, and
	// never creates a fiscal period.
	auto bool
	// periodOverride forces the entry into that period, as an import with
	// a chosen period does for its own rows.
	periodOverride string
}

var (
	errInvoiceHasBookedRow = errors.New("invoice already has a booked bank, Vipps or card payment — unassign it first")
	errInvoiceNoLongerOpen = errors.New("invoice is no longer the open faktura for this deposit")
)

func (s *Service) assignBankRowToInvoice(
	ctx context.Context, clubID, actorID, bankRowID, invoiceID string, opts assignOpts,
) (string, *TaughtAlias, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		rowDate     time.Time
		rowAmount   float64
		rowDesc     string
		counterpart string
		bankAcct    string
		alreadyJrnl bool
		dismissed   bool
	)
	if err := tx.QueryRow(ctx,
		`SELECT bir.row_date, bir.amount, bir.description, COALESCE(bir.counterpart, ''),
		        bi.bank_account_code,
		        bir.journal_entry_id IS NOT NULL,
		        bir.dismissed_at IS NOT NULL
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.id = $1 AND bir.club_id = $2`,
		bankRowID, clubID,
	).Scan(&rowDate, &rowAmount, &rowDesc, &counterpart, &bankAcct, &alreadyJrnl, &dismissed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, errors.New("bank row not found")
		}
		return "", nil, fmt.Errorf("load bank row: %w", err)
	}
	if alreadyJrnl {
		return "", nil, errBankRowRaced
	}
	if dismissed {
		return "", nil, errors.New("bank row is dismissed")
	}
	if rowAmount <= 0 {
		return "", nil, errors.New("assign-invoice requires a positive (incoming) amount; use assign-account for outgoing rows")
	}
	// Before the faktura lock, so the guard's reads never hold it.
	if err := refuseProviderPayouts(ctx, tx, clubID,
		assignRowText{id: bankRowID, description: rowDesc, counterpart: counterpart, cents: toCents(rowAmount)}); err != nil {
		return "", nil, err
	}

	lockOpts := invoiceLockOpts{mode: lockManual, bookingDate: rowDate}
	if opts.auto {
		lockOpts = invoiceLockOpts{mode: lockAutoAssign, rowCents: toCents(rowAmount)}
	}
	if _, err := lockInvoiceForAssign(ctx, tx, clubID, invoiceID, lockOpts); err != nil {
		return "", nil, err
	}

	var periodID, periodStatus string
	if opts.auto {
		periodID, periodStatus, err = existingPeriodIn(ctx, tx, clubID, rowDate, opts.periodOverride)
	} else {
		periodID, periodStatus, err = resolvePeriodIn(ctx, tx, clubID, rowDate, opts.periodOverride)
	}
	if err != nil {
		return "", nil, fmt.Errorf("resolve period: %w", err)
	}
	if periodStatus != "open" {
		return "", nil, fmt.Errorf("fiscal period for %s is closed; reopen first", rowDate.Format("2006-01-02"))
	}

	// In the lock tx, so a failed link or post rolls the alias back with
	// the entry.
	taught, err := s.teachPayerAlias(ctx, tx, clubID, invoiceID, []string{bankRowID}, opts.alias)
	if err != nil {
		return "", nil, err
	}

	sourceID := bankRowID
	sourceTable := "bank_import_rows"
	desc := fmt.Sprintf("Manuell tildeling: %s", truncate(rowDesc, 200))
	if opts.auto {
		desc = fmt.Sprintf("Automatisk tildeling: %s", truncate(rowDesc, 200))
	}
	entry, cerr := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
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
		return "", nil, fmt.Errorf("create journal: %w", cerr)
	}
	if err := linkAndPostBankRowsTx(ctx, tx, clubID, actorID, entry.ID, []string{bankRowID}, &invoiceID); err != nil {
		return "", nil, err
	}
	// The entry, the row link and the alias commit together; the back-link
	// below updates the faktura, so it runs once the lock is released.
	if err := tx.Commit(ctx); err != nil {
		return "", nil, fmt.Errorf("commit invoice assign: %w", err)
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
	return entry.ID, taught, nil
}

// invoiceLockMode picks which re-checks lockInvoiceForAssign runs once it
// holds the faktura.
type invoiceLockMode int

const (
	// lockManual is a treasurer's assign (and the Vipps charge assign): it
	// refuses a voided or paid faktura, one a booked row is recorded
	// against, or an imported one whose receivable isn't in the GL by the
	// booking date (ErrImportedNoOpeningBalance).
	lockManual invoiceLockMode = iota
	// lockAutoAssign adds the auto-assign rule's conditions: still owed,
	// sent, not imported, nothing booked against it (incl. a KID-matched
	// row) and a total equal to the row.
	lockAutoAssign
	// lockKIDCascade is the KID / invoice-number / counterpart cascade of
	// bank import and sync: still owed and nothing booked against it. It
	// has no amount, sent or import condition, because the cascade books
	// any amount and imported fakturas too. A voided, credited or non-debt
	// faktura is errInvoiceNotOpen; one paid or booked is errInvoiceSettled;
	// an imported one without its receivable in the GL is
	// ErrImportedNoOpeningBalance. The cascade's SELECTs never offer an
	// imported faktura today (empty KID), so that check is defence in depth.
	lockKIDCascade
)

// invoiceLockOpts tunes lockInvoiceForAssign.
type invoiceLockOpts struct {
	mode invoiceLockMode
	// rowCents is the payment in øre; lockAutoAssign compares it to the
	// faktura total.
	rowCents int64
	// bookingDate is the date the payment is booked on. lockManual and
	// lockKIDCascade need it to decide whether an imported faktura's
	// receivable is in the GL by then; a zero date is refused as a caller
	// bug rather than silently treated as "no opening balance".
	bookingDate time.Time
}

// ErrImportedNoOpeningBalance means the faktura was imported from the
// previous system and its receivable isn't on 1500 in Brygge's GL by the
// payment's booking date, so crediting 1500 would leave a credit nothing
// offsets (BRY-386). Nothing is booked and the payment stays in Tildel until
// the opening balance is posted. See finance.ImportedReceivableOpenSQL. The
// text is shown to the treasurer as is (Tildel, import row errors), so it is
// a full sentence.
var ErrImportedNoOpeningBalance = errors.New( //nolint:staticcheck // ST1005: a user-facing sentence, see above
	"This faktura was imported from the previous system and its receivable isn't in Brygge's opening balance yet. Book the payment after the opening balance is posted.")

// errInvoiceSettled means the KID cascade found its faktura already settled
// once it held the lock. The row is left in Tildel and counted, not failed:
// it is most likely a double payment.
var errInvoiceSettled = errors.New("invoice is already settled")

// errInvoiceNotOpen means the KID cascade found its faktura voided, credited
// or no longer a debt once it held the lock: the payment is not a double
// payment, but it has nowhere to go and stays in Tildel.
var errInvoiceNotOpen = errors.New("faktura no longer open")

// errInvoicePaid means a manual or Vipps assignment found its faktura already
// paid once it held the lock: a concurrent assignment got there first.
var errInvoicePaid = errors.New("invoice is already paid")

// errInvoiceVoided means a manual or Vipps assignment found its faktura
// voided once it held the lock.
var errInvoiceVoided = errors.New("invoice is voided")

// lockInvoiceForAssign locks the faktura for the rest of tx and refuses it
// when it can't take this payment. It returns the faktura total in øre.
// Every path that settles a faktura with a bank row or a Vipps charge
// (single and multi-row bank assigns, the bank and Vipps auto-assign rules,
// the KID cascade of bank import and sync) takes it.
//
// The faktura stays locked until the payment is linked and posted, so two
// bookings racing for one faktura (a sync's auto-assign and a manual
// assign of another row, or a Vipps and a bank auto-assign) run one after
// the other, and the second sees the first's booked row. NO KEY UPDATE
// rather than UPDATE: a write that only references the faktura (a foreign
// key on bank_import_rows.invoice_id or vipps_import_rows.invoice_id takes
// KEY SHARE) need not queue behind the lock. The checks run in a second
// statement: one that waited for the lock still reads the rows as they
// were when it started, so it would miss the booking it waited for.
//
// Everything the booking does while the lock is held goes through tx,
// never s.db: period resolution, the entry, the row link and the post. A
// holder that waits for a second pool connection deadlocks once every
// connection belongs to a booking queued on this faktura, and Postgres
// cannot see that wait to break it.
func lockInvoiceForAssign(ctx context.Context, tx pgx.Tx, clubID, invoiceID string, opts invoiceLockOpts) (int64, error) { //nolint:cyclop // one guard per settlement rule, each with its own error; split tracked in BRY-424
	if opts.mode != lockAutoAssign && opts.bookingDate.IsZero() {
		return 0, errors.New("lock invoice: booking date is required")
	}
	if err := tx.QueryRow(ctx,
		`SELECT id FROM invoices WHERE id = $1 AND club_id = $2 FOR NO KEY UPDATE`,
		invoiceID, clubID,
	).Scan(new(string)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errors.New("invoice not found")
		}
		return 0, fmt.Errorf("lock invoice: %w", err)
	}
	var (
		invoiceCents  int64
		paid          bool
		invoiceStatus string
		owed          bool
		credited      bool
		sentAndLive   bool
		bookedByRow   bool
		bookedAny     bool
		receivableOK  bool
	)
	if err := tx.QueryRow(ctx,
		`SELECT round(i.total_amount * 100)::bigint, i.payment_id IS NOT NULL, i.status,
		        `+finance.OwedByMemberSQL("i")+`, `+finance.CreditedSQL("i")+`,
		        i.sent_at IS NOT NULL AND i.import_source IS NULL,
		        `+invoiceBookedRowSQL("i")+`,
		        `+invoiceAlreadyBookedSQL("i")+`,
		        `+finance.ImportedReceivableOpenSQL("i", "$3::date")+`
		   FROM invoices i
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID, opts.bookingDate,
	).Scan(&invoiceCents, &paid, &invoiceStatus, &owed, &credited, &sentAndLive, &bookedByRow, &bookedAny, &receivableOK); err != nil {
		return 0, fmt.Errorf("load invoice: %w", err)
	}
	if opts.mode == lockKIDCascade {
		switch {
		case invoiceStatus == "voided" || credited || invoiceCents <= 0:
			return 0, errInvoiceNotOpen
		case !owed || bookedAny:
			return 0, errInvoiceSettled
		case !receivableOK:
			return 0, ErrImportedNoOpeningBalance
		}
		return invoiceCents, nil
	}
	switch {
	case invoiceStatus == "voided":
		return 0, errInvoiceVoided
	case paid:
		return 0, errInvoicePaid
	case bookedByRow:
		return 0, errInvoiceHasBookedRow
	case opts.mode == lockAutoAssign && (!owed || !sentAndLive || bookedAny || invoiceCents != opts.rowCents):
		return 0, errInvoiceNoLongerOpen
	case opts.mode == lockManual && !receivableOK:
		return 0, ErrImportedNoOpeningBalance
	}
	return invoiceCents, nil
}

// errNoFiscalPeriod: no fiscal period covers the date, and the caller must
// not create one.
var errNoFiscalPeriod = errors.New("no fiscal period")

// existingPeriodIn is resolvePeriodIn without the auto-create: an automatic
// booking never opens a fiscal year on its own.
func existingPeriodIn(ctx context.Context, q dbtx, clubID string, date time.Time, override string) (id, status string, err error) {
	if override != "" {
		return resolvePeriodIn(ctx, q, clubID, date, override)
	}
	err = q.QueryRow(ctx,
		`SELECT id, status FROM fiscal_periods
		  WHERE club_id = $1 AND $2::date BETWEEN start_date AND end_date
		  ORDER BY start_date DESC
		  LIMIT 1`,
		clubID, date.Format("2006-01-02"),
	).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("%w for %s", errNoFiscalPeriod, date.Format("2006-01-02"))
	}
	return id, status, err
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
func (s *Service) AssignBankRowToAccount( //nolint:cyclop // validate, lock and post one bank row in a single transaction; split tracked in BRY-424
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
		counterpart string
		bankAcct    string
		alreadyJrnl bool
		dismissed   bool
	)
	if err := tx.QueryRow(ctx,
		`SELECT bir.row_date, bir.amount, bir.description, COALESCE(bir.counterpart, ''),
		        bi.bank_account_code,
		        bir.journal_entry_id IS NOT NULL,
		        bir.dismissed_at IS NOT NULL
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.id = $1 AND bir.club_id = $2`,
		bankRowID, clubID,
	).Scan(&rowDate, &rowAmount, &rowDesc, &counterpart, &bankAcct, &alreadyJrnl, &dismissed); err != nil {
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
	if err := refuseProviderPayouts(ctx, tx, clubID,
		assignRowText{id: bankRowID, description: rowDesc, counterpart: counterpart, cents: toCents(rowAmount)}); err != nil {
		return "", err
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
	return s.linkAndPostBankRows(ctx, clubID, actorID, entryID, []string{bankRowID}, invoiceID)
}

// linkAndPostBankRows is linkAndPostBankRow for rows booked together on one
// entry: all of them link or none do.
func (s *Service) linkAndPostBankRows(ctx context.Context, clubID, actorID, entryID string, bankRowIDs []string, invoiceID *string) error {
	if err := s.linkBankRows(ctx, clubID, entryID, bankRowIDs, invoiceID); err != nil {
		s.discardDraft(ctx, clubID, entryID)
		return err
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

// linkAndPostBankRowsTx is linkAndPostBankRows inside the caller's
// transaction, for a booking that holds the faktura lock: a failed link or
// post rolls back with the entry, so there is nothing to unlink or discard.
func linkAndPostBankRowsTx(ctx context.Context, tx pgx.Tx, clubID, actorID, entryID string, bankRowIDs []string, invoiceID *string) error {
	if err := linkBankRowsTx(ctx, tx, clubID, entryID, bankRowIDs, invoiceID); err != nil {
		return err
	}
	if err := postJournalEntryTx(ctx, tx, clubID, entryID, actorID); err != nil {
		return fmt.Errorf("post journal: %w", err)
	}
	return nil
}

// linkBankRows points every row at the entry in one transaction, guarded on
// each row still being unbooked and not dismissed.
func (s *Service) linkBankRows(ctx context.Context, clubID, entryID string, bankRowIDs []string, invoiceID *string) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := linkBankRowsTx(ctx, tx, clubID, entryID, bankRowIDs, invoiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit row link: %w", err)
	}
	return nil
}

// linkBankRowsTx is linkBankRows inside the caller's transaction, which then
// holds the rows until it ends.
func linkBankRowsTx(ctx context.Context, tx pgx.Tx, clubID, entryID string, bankRowIDs []string, invoiceID *string) error {
	// Lock in id order first, so two overlapping group bookings queue
	// instead of deadlocking on each other's rows.
	if _, err := tx.Exec(ctx,
		`SELECT id FROM bank_import_rows WHERE id = ANY($1::uuid[]) AND club_id = $2 ORDER BY id FOR UPDATE`,
		bankRowIDs, clubID,
	); err != nil {
		return fmt.Errorf("lock rows: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1, invoice_id = COALESCE($2, invoice_id), auto_matched = false
		  WHERE id = ANY($3::uuid[]) AND club_id = $4 AND journal_entry_id IS NULL AND dismissed_at IS NULL`,
		entryID, invoiceID, bankRowIDs, clubID,
	)
	if err != nil {
		return fmt.Errorf("mark row matched: %w", err)
	}
	if tag.RowsAffected() != int64(len(bankRowIDs)) {
		return errBankRowRaced
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
	res, err := s.UnassignBankRowReported(ctx, clubID, actorID, bankRowID)
	return res.PriorJournalID, res.PriorInvoiceID, err
}

// UnassignResult is what UnassignBankRowReported undid: the row's prior
// booking and the other rows released with it (a group booking's or a
// transfer's other rows, a multi-row faktura's siblings).
type UnassignResult struct {
	PriorJournalID string
	PriorInvoiceID string
	SiblingRowIDs  []string
}

// UnassignBankRowReported is UnassignBankRow, also reporting the sibling
// rows it released, for the audit row.
func (s *Service) UnassignBankRowReported(
	ctx context.Context, clubID, actorID, bankRowID string,
) (UnassignResult, error) {
	priorJournalID, priorInvoiceID, siblings, err := s.unassignBankRow(ctx, clubID, actorID, bankRowID)
	return UnassignResult{PriorJournalID: priorJournalID, PriorInvoiceID: priorInvoiceID, SiblingRowIDs: siblings}, err
}

func (s *Service) unassignBankRow( //nolint:gocognit,cyclop // reverses each posting kind inside one transaction; split tracked in BRY-424
	ctx context.Context, clubID, actorID, bankRowID string,
) (priorJournalID, priorInvoiceID string, siblings []string, err error) {
	var invoiceIDPtr *string
	if err := s.db.QueryRow(ctx,
		`SELECT COALESCE(journal_entry_id::text, ''), invoice_id
		   FROM bank_import_rows
		  WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&priorJournalID, &invoiceIDPtr); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil, ErrBankRowNotFound
		}
		return "", "", nil, fmt.Errorf("load row: %w", err)
	}
	if priorJournalID == "" {
		return "", "", nil, ErrBankRowNotAssigned
	}
	if invoiceIDPtr != nil {
		priorInvoiceID = *invoiceIDPtr
	} else {
		// Rows assigned before migration 000057 have no invoice back-link;
		// recover the faktura through the payment the assignment created.
		// Only a faktura booking credits receivables: an account booking
		// (Lag bilag) has no 1500 line, and without that guard a faktura
		// paid the same day for the same amount would lose its payment.
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
			    AND EXISTS (
			        SELECT 1 FROM journal_lines jl
			          JOIN accounts a ON a.id = jl.account_id
			         WHERE jl.journal_entry_id = je.id AND a.code = $3)
			  LIMIT 1`,
			priorJournalID, clubID, receivablesAccountCode,
		).Scan(&priorInvoiceID)
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Every entry booked for this assignment: this row's, its invoice
	// siblings', and any other row pointing at the same entry.
	type involved struct {
		id, status string
	}
	rows, err := tx.Query(ctx,
		// FOR UPDATE (in id order) waits out a concurrent post of the same
		// entry, so a draft read here can't be posted behind our back and
		// leave a posted entry with its row already unlinked.
		`SELECT je.id, je.status
		   FROM journal_entries je
		  WHERE je.club_id = $1
		    AND je.id IN (
		        SELECT b.journal_entry_id
		          FROM bank_import_rows b
		         WHERE b.club_id = $1
		           AND b.journal_entry_id IS NOT NULL
		           AND (b.id = $2
		                OR ($3 <> '' AND b.invoice_id::text = $3)
		                OR b.journal_entry_id::text = $4))
		  ORDER BY je.id
		  FOR UPDATE`,
		clubID, bankRowID, priorInvoiceID, priorJournalID,
	)
	if err != nil {
		return "", "", nil, fmt.Errorf("load bookings: %w", err)
	}
	var entries []involved
	for rows.Next() {
		var e involved
		if err := rows.Scan(&e.id, &e.status); err != nil {
			rows.Close()
			return "", "", nil, fmt.Errorf("scan booking: %w", err)
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", "", nil, fmt.Errorf("bookings: %w", err)
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
					return "", "", nil, fmt.Errorf("bank row is booked in a closed fiscal period; reopen it or correct with a new entry: %w", ErrPeriodClosed)
				}
				return "", "", nil, fmt.Errorf("voiding bilag: %w", err)
			}
		case "draft":
			drafts = append(drafts, e.id)
		}
	}

	entryIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		entryIDs = append(entryIDs, e.id)
	}
	cleared, err := tx.Query(ctx,
		`UPDATE bank_import_rows
		    SET journal_entry_id = NULL, invoice_id = NULL, auto_matched = false
		  WHERE club_id = $1
		    AND (id = $2
		         OR ($3 <> '' AND invoice_id::text = $3)
		         OR journal_entry_id = ANY($4::uuid[]))
		RETURNING id::text`,
		clubID, bankRowID, priorInvoiceID, entryIDs,
	)
	if err != nil {
		return "", "", nil, fmt.Errorf("clear rows: %w", err)
	}
	if siblings, err = otherRowIDs(cleared, bankRowID); err != nil {
		return "", "", nil, fmt.Errorf("clear rows: %w", err)
	}
	if len(drafts) > 0 {
		if _, err := tx.Exec(ctx,
			`DELETE FROM journal_entries
			  WHERE club_id = $1 AND status = 'draft' AND id = ANY($2::uuid[])`,
			clubID, drafts,
		); err != nil {
			return "", "", nil, fmt.Errorf("delete draft bilag: %w", err)
		}
	}
	if err := releaseStripePayoutTx(ctx, tx, clubID, entryIDs); err != nil {
		return "", "", nil, err
	}
	if priorInvoiceID != "" {
		var paymentID *string
		if err := tx.QueryRow(ctx,
			`SELECT payment_id FROM invoices WHERE id = $1 AND club_id = $2`,
			priorInvoiceID, clubID,
		).Scan(&paymentID); err != nil {
			return "", "", nil, fmt.Errorf("load invoice payment: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE invoices SET payment_id = NULL WHERE id = $1 AND club_id = $2`,
			priorInvoiceID, clubID,
		); err != nil {
			return "", "", nil, fmt.Errorf("clear invoice payment_id: %w", err)
		}
		if paymentID != nil {
			if _, err := tx.Exec(ctx,
				`DELETE FROM payments WHERE id = $1 AND club_id = $2`,
				*paymentID, clubID,
			); err != nil {
				return "", "", nil, fmt.Errorf("delete payment row: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", nil, fmt.Errorf("commit: %w", err)
	}
	return priorJournalID, priorInvoiceID, siblings, nil
}

// releaseStripePayoutTx undoes a Stripe payout clearing whose entry the
// unassign voided: the payout loses its deposit and entry, and its own
// payout balance row is unbooked again, so the next sweep clears it afresh
// (or reports why not). Without this the payout would look cleared for ever
// and its deposit would come back to Tildel as a Stripe deposit no payout
// explains.
func releaseStripePayoutTx(ctx context.Context, tx pgx.Tx, clubID string, entryIDs []string) error {
	if len(entryIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE stripe_payouts SET bank_row_id = NULL, journal_entry_id = NULL, updated_at = now()
		  WHERE club_id = $1 AND journal_entry_id = ANY($2::uuid[])`,
		clubID, entryIDs); err != nil {
		return fmt.Errorf("release stripe payout: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE stripe_balance_transactions SET status = 'unbooked', journal_entry_id = NULL, updated_at = now()
		  WHERE club_id = $1 AND reporting_category = 'payout' AND journal_entry_id = ANY($2::uuid[])`,
		clubID, entryIDs); err != nil {
		return fmt.Errorf("release stripe payout transaction: %w", err)
	}
	return nil
}

// otherRowIDs collects the row ids a query returned, except self.
func otherRowIDs(rows pgx.Rows, self string) ([]string, error) {
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, id := range ids {
		if id != self {
			out = append(out, id)
		}
	}
	return out, nil
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
	ids, _, err := s.AssignMultipleBankRowsToInvoiceRemembering(ctx, clubID, actorID, rowIDs, invoiceID, PayerAliasRequest{})
	return ids, err
}

// AssignMultipleBankRowsToInvoiceRemembering is AssignMultipleBankRowsToInvoice
// that can also remember the rows' payer for the faktura's member, when every
// row has the same payer (BRY-383). It returns the alias it saved, or nil.
func (s *Service) AssignMultipleBankRowsToInvoiceRemembering(
	ctx context.Context, clubID, actorID string, rowIDs []string, invoiceID string, alias PayerAliasRequest,
) ([]string, *TaughtAlias, error) {
	if len(rowIDs) < 2 {
		return nil, nil, errors.New("use assign-invoice for single rows")
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	loaded, err := loadMultiAssignRows(ctx, tx, clubID, rowIDs)
	if err != nil {
		return nil, nil, err
	}

	// Each row's entry is booked on its own date, so the imported-faktura
	// receivable check takes the earliest: every row must find the
	// receivable already in the GL.
	var earliestDate time.Time
	for _, rd := range loaded {
		if earliestDate.IsZero() || rd.date.Before(earliestDate) {
			earliestDate = rd.date
		}
	}
	// Held until the rows are linked and their entries posted (the commit
	// below), like the single-row path; every entry is created and posted
	// in this tx, so they all commit or none do.
	invCents, err := lockInvoiceForAssign(ctx, tx, clubID, invoiceID, invoiceLockOpts{mode: lockManual, bookingDate: earliestDate})
	if err != nil {
		return nil, nil, err
	}
	invoiceTotal := float64(invCents) / 100

	var sumAmount float64
	var latestDate time.Time
	for _, rd := range loaded {
		sumAmount += rd.amount
		if rd.date.After(latestDate) {
			latestDate = rd.date
		}
	}
	if sumCents, invCents := int64(sumAmount*100+0.5), int64(invoiceTotal*100+0.5); sumCents != invCents {
		return nil, nil, fmt.Errorf("row sum %.2f does not match invoice total %.2f", sumAmount, invoiceTotal)
	}

	periodID, periodStatus, perr := resolvePeriodIn(ctx, tx, clubID, latestDate, "")
	if perr != nil {
		return nil, nil, fmt.Errorf("resolve period: %w", perr)
	}
	if periodStatus != "open" {
		return nil, nil, fmt.Errorf("fiscal period for %s is closed; reopen first", latestDate.Format("2006-01-02"))
	}

	// Before any entry exists and in the lock tx, as on the single-row path.
	taught, err := s.teachPayerAlias(ctx, tx, clubID, invoiceID, rowIDs, alias)
	if err != nil {
		return nil, nil, err
	}

	journalIDs := make([]string, 0, len(loaded))
	for _, rd := range loaded {
		srcID := rd.id
		srcTable := "bank_import_rows"
		desc := fmt.Sprintf("Manuell tildeling: %s", truncate(rd.desc, 200))
		entry, cerr := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
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
			return nil, nil, fmt.Errorf("create journal for row %s: %w", rd.id, cerr)
		}
		if err := postJournalEntryTx(ctx, tx, clubID, entry.ID, actorID); err != nil {
			return nil, nil, fmt.Errorf("post journal for row %s: %w", rd.id, err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE bank_import_rows
			    SET journal_entry_id = $1, invoice_id = $2, auto_matched = false
			  WHERE id = $3`,
			entry.ID, invoiceID, rd.id,
		); err != nil {
			return nil, nil, fmt.Errorf("mark row %s matched: %w", rd.id, err)
		}
		journalIDs = append(journalIDs, entry.ID)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit: %w", err)
	}

	if err := s.linkInvoicePayment(ctx, clubID, invoiceID, latestDate); err != nil {
		s.log.Warn().Err(err).
			Str("invoice_id", invoiceID).
			Msg("AssignMultipleBankRowsToInvoice: linkInvoicePayment failed; GL posted but dashboard back-link missing")
	}
	return journalIDs, taught, nil
}

// multiAssignRow is a row of a multi-row faktura assign.
type multiAssignRow struct {
	id          string
	date        time.Time
	amount      float64
	desc        string
	counterpart string
	bankAcct    string
}

// loadMultiAssignRows loads the rows of a multi-row assign, refusing any that
// is missing, booked, dismissed, not incoming or a provider payout deposit.
// It runs before the faktura lock, as the single-row path's checks do.
func loadMultiAssignRows(ctx context.Context, tx pgx.Tx, clubID string, rowIDs []string) ([]multiAssignRow, error) {
	loaded := make([]multiAssignRow, 0, len(rowIDs))
	for _, rid := range rowIDs {
		rd := multiAssignRow{id: rid}
		var alreadyJrnl, dismissed bool
		if err := tx.QueryRow(ctx,
			`SELECT bir.row_date, bir.amount, bir.description, COALESCE(bir.counterpart, ''),
			        bi.bank_account_code,
			        bir.journal_entry_id IS NOT NULL,
			        bir.dismissed_at IS NOT NULL
			   FROM bank_import_rows bir
			   JOIN bank_imports bi ON bi.id = bir.bank_import_id
			  WHERE bir.id = $1 AND bir.club_id = $2`,
			rid, clubID,
		).Scan(&rd.date, &rd.amount, &rd.desc, &rd.counterpart, &rd.bankAcct, &alreadyJrnl, &dismissed); err != nil {
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
	texts := make([]assignRowText, 0, len(loaded))
	for _, rd := range loaded {
		texts = append(texts, assignRowText{id: rd.id, description: rd.desc, counterpart: rd.counterpart, cents: toCents(rd.amount)})
	}
	if err := refuseProviderPayouts(ctx, tx, clubID, texts...); err != nil {
		return nil, err
	}
	return loaded, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}
