package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// refundReasons are the dismiss reasons that create a pending refund obligation.
var refundReasons = map[string]bool{
	"double_payment":   true,
	"overpayment":      true,
	"refund_or_credit": true,
}

// RefundPendingRow is a dismissed bank row that needs a refund sent.
type RefundPendingRow struct {
	ID              string    `json:"id"`
	RowDate         time.Time `json:"row_date"`
	Amount          float64   `json:"amount"`
	Counterpart     string    `json:"counterpart"`
	Description     string    `json:"description"`
	KIDNumber       string    `json:"kid_number"`
	DismissedReason string    `json:"dismissed_reason"`
	DismissedAt     time.Time `json:"dismissed_at"`
	BankAccountCode string    `json:"bank_account_code"`
	ImportID        string    `json:"import_id"`
}

// RefundOutboundCandidate is an unmatched outgoing bank row that could be
// paired as the refund transfer for a pending row.
type RefundOutboundCandidate struct {
	ID          string    `json:"id"`
	RowDate     time.Time `json:"row_date"`
	Amount      float64   `json:"amount"`
	Counterpart string    `json:"counterpart"`
	Description string    `json:"description"`
}

// ListPendingRefunds returns dismissed rows with a refund-type reason that
// have not yet been paired with an outbound refund transfer.
func (s *Service) ListPendingRefunds(ctx context.Context, clubID string) ([]RefundPendingRow, error) {
	rows, err := s.db.Query(ctx,
		`SELECT bir.id, bir.row_date, bir.amount,
		        COALESCE(bir.counterpart, ''), COALESCE(bir.description, ''),
		        COALESCE(bir.kid_number, ''), bir.dismissed_reason, bir.dismissed_at,
		        bi.bank_account_code, bir.bank_import_id
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.club_id = $1
		    AND bir.dismissed_reason IN ('double_payment', 'overpayment', 'refund_or_credit')
		    AND bir.refund_paired_with IS NULL
		    AND NOT `+referenceDateSQL("bir.club_id", "bir.row_date")+`
		  ORDER BY bir.row_date DESC`,
		clubID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefundPendingRow
	for rows.Next() {
		var r RefundPendingRow
		if err := rows.Scan(
			&r.ID, &r.RowDate, &r.Amount, &r.Counterpart, &r.Description,
			&r.KIDNumber, &r.DismissedReason, &r.DismissedAt,
			&r.BankAccountCode, &r.ImportID,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountPendingRefunds returns the number of unpaired refund-required rows.
// Used for the sidebar badge.
func (s *Service) CountPendingRefunds(ctx context.Context, clubID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*)
		   FROM bank_import_rows bir
		  WHERE bir.club_id = $1
		    AND bir.dismissed_reason IN ('double_payment', 'overpayment', 'refund_or_credit')
		    AND bir.refund_paired_with IS NULL
		    AND NOT `+referenceDateSQL("bir.club_id", "bir.row_date"),
		clubID,
	).Scan(&n)
	return n, err
}

// SuggestRefundOutbound finds unmatched outgoing bank rows that look like
// they could be the refund transfer for the given inbound row (same club,
// negative amount equal to the inbound amount to the øre, undismissed,
// unmatched, not provisional). Only an exact amount is offered, because
// PairRefund refuses any other (BRY-669).
func (s *Service) SuggestRefundOutbound(ctx context.Context, clubID, inboundRowID string) ([]RefundOutboundCandidate, error) {
	var inboundAmount float64
	if err := s.db.QueryRow(ctx,
		`SELECT amount FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
		inboundRowID, clubID,
	).Scan(&inboundAmount); err != nil {
		return nil, fmt.Errorf("load inbound row: %w", err)
	}
	if inboundAmount <= 0 {
		return nil, errors.New("inbound row has non-positive amount")
	}
	// Outgoing rows have negative amounts; only the exact amount, in øre.
	rows, err := s.db.Query(ctx,
		`SELECT id, row_date, amount,
		        COALESCE(counterpart, ''), COALESCE(description, '')
		   FROM bank_import_rows
		  WHERE club_id = $1
		    AND amount < 0
		    AND round(-amount * 100)::bigint = $2
		    AND journal_entry_id IS NULL
		    AND dismissed_at IS NULL
		    AND refund_paired_with IS NULL
		    AND NOT COALESCE(`+provisionalRowSQL("bank_import_rows")+`, false)
		    AND NOT `+referenceDateSQL("bank_import_rows.club_id", "bank_import_rows.row_date")+`
		  ORDER BY row_date DESC
		  LIMIT 20`,
		clubID, toCents(inboundAmount),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefundOutboundCandidate
	for rows.Next() {
		var c RefundOutboundCandidate
		if err := rows.Scan(&c.ID, &c.RowDate, &c.Amount, &c.Counterpart, &c.Description); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// refundPairRow is one side of a refund pair as PairRefund reads it, under
// its row lock.
type refundPairRow struct {
	date            time.Time
	amount          float64
	desc            string
	bankAcct        string
	dismissed       bool
	dismissedReason string
	alreadyJournal  bool
	alreadyPaired   bool
	// provisional: an in-flight row a later statement replaces
	// (provisionalRowSQL); never a refund's outbound side.
	provisional bool
}

// lockRefundPairRows locks both rows of a refund pair (FOR UPDATE OF bir, in
// id order, so two pairings or a pairing and a group booking sharing a row
// queue instead of deadlocking) and reads them on tx. A row that does not
// exist for the club is pgx.ErrNoRows, wrapped with which side it is.
func lockRefundPairRows(ctx context.Context, tx pgx.Tx, clubID, inboundRowID, outboundRowID string) (inbound, outbound refundPairRow, err error) {
	rows, err := tx.Query(ctx,
		`SELECT bir.id::text, bir.row_date, bir.amount, COALESCE(bir.description, ''),
		        bi.bank_account_code,
		        bir.dismissed_at IS NOT NULL,
		        COALESCE(bir.dismissed_reason, ''),
		        bir.journal_entry_id IS NOT NULL,
		        bir.refund_paired_with IS NOT NULL,
		        COALESCE(`+provisionalRowSQL("bir")+`, false)
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.id = ANY($1::uuid[]) AND bir.club_id = $2
		  ORDER BY bir.id
		    FOR UPDATE OF bir FOR SHARE OF bi`,
		[]string{inboundRowID, outboundRowID}, clubID,
	)
	if err != nil {
		return inbound, outbound, fmt.Errorf("lock refund rows: %w", err)
	}
	defer rows.Close()
	var foundIn, foundOut bool
	for rows.Next() {
		var id string
		var r refundPairRow
		if err := rows.Scan(&id, &r.date, &r.amount, &r.desc, &r.bankAcct,
			&r.dismissed, &r.dismissedReason, &r.alreadyJournal, &r.alreadyPaired, &r.provisional); err != nil {
			return inbound, outbound, fmt.Errorf("scan refund row: %w", err)
		}
		switch id {
		case inboundRowID:
			inbound, foundIn = r, true
		case outboundRowID:
			outbound, foundOut = r, true
		}
	}
	if err := rows.Err(); err != nil {
		return inbound, outbound, fmt.Errorf("lock refund rows: %w", err)
	}
	if !foundIn {
		return inbound, outbound, fmt.Errorf("load inbound row: %w", pgx.ErrNoRows)
	}
	if !foundOut {
		return inbound, outbound, fmt.Errorf("load outbound row: %w", pgx.ErrNoRows)
	}
	return inbound, outbound, nil
}

// checkRefundPair refuses a pair PairRefund must not book.
func checkRefundPair(inbound, outbound refundPairRow) error {
	switch {
	case !inbound.dismissed || !refundReasons[inbound.dismissedReason]:
		return errors.New("inbound row must be dismissed with a refund reason (double_payment / overpayment / refund_or_credit)")
	case inbound.alreadyPaired:
		return errors.New("inbound row already has a paired refund")
	case inbound.alreadyJournal:
		return errors.New("inbound row is already reconciled")
	case inbound.amount <= 0:
		return errors.New("inbound row must have a positive amount")
	case outbound.dismissed:
		return errors.New("outbound row is dismissed — cannot use as refund transfer")
	case outbound.alreadyJournal:
		return errors.New("outbound row is already reconciled")
	case outbound.alreadyPaired:
		return errors.New("outbound row already has a paired refund")
	case outbound.amount >= 0:
		return errors.New("outbound row must have a negative (outgoing) amount")
	case outbound.provisional:
		return errors.New("outbound row is provisional — import the statement for its day again, then pair the settled row")
	case toCents(-outbound.amount) != toCents(inbound.amount):
		// Both entries book one amount; any difference would be left on
		// the bank account with nothing behind it.
		return fmt.Errorf("outbound amount %.2f does not match the inbound amount %.2f — a refund pair must return the exact amount",
			-outbound.amount, inbound.amount)
	}
	return nil
}

// PairRefund links a dismissed inbound row with the matching outbound refund
// transfer. It creates two balanced journal entries:
//   - Inbound: DR bank / CR receivables  (the money that came in)
//   - Outbound: DR receivables / CR bank (the refund that went out)
//
// Both rows are marked with refund_paired_with pointing at each other.
// The inbound row keeps its dismissed_* columns for audit — it is not
// un-dismissed.
//
// Everything runs in one transaction (BRY-669): both rows are locked in id
// order and every read, the period (which a person's pairing may create),
// both entries, the guarded link and both posts run on it (BRY-433). A
// concurrent booking of either row therefore either waits and is refused
// once the pair commits, or wins and the pair is refused; a refusal leaves
// no entry, link or new period behind. A closed or locked period is refused
// before any entry with its own reason (periodNotOpenConfirmError). The link
// is a person's, so it lifts a decline (auto_declined_at) on both rows.
func (s *Service) PairRefund(
	ctx context.Context, clubID, actorID, inboundRowID, outboundRowID string,
) (inboundJournalID, outboundJournalID string, err error) {
	if inboundRowID == outboundRowID {
		return "", "", errors.New("inbound and outbound row must differ")
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	inbound, outbound, err := lockRefundPairRows(ctx, tx, clubID, inboundRowID, outboundRowID)
	if err != nil {
		return "", "", err
	}
	if err := checkRefundPair(inbound, outbound); err != nil {
		return "", "", err
	}

	// Both entries use the outbound (later) date for the fiscal period,
	// ensuring they land in the same period for a clean reversal.
	periodID, periodStatus, err := resolvePeriodIn(ctx, tx, clubID, outbound.date, "")
	if err != nil {
		return "", "", fmt.Errorf("resolve period: %w", err)
	}
	if periodStatus != "open" {
		return "", "", periodNotOpenConfirmError(outbound.date.Year(), periodStatus)
	}

	absAmount := inbound.amount
	srcIn, srcOut := inboundRowID, outboundRowID
	srcTable := "bank_import_rows"
	entryDate := outbound.date.Format("2006-01-02")
	inboundEntry, err := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      entryDate,
		Description:    fmt.Sprintf("Refusjon (innbetaling): %s", truncate(inbound.desc, 180)),
		Source:         "bank_import",
		SourceID:       &srcIn,
		SourceTable:    &srcTable,
		CreatedBy:      actorID,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: inbound.bankAcct, Debit: absAmount, Credit: 0},
			{AccountCode: receivablesAccountCode, Debit: 0, Credit: absAmount},
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("create inbound journal: %w", err)
	}
	outboundEntry, err := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      entryDate,
		Description:    fmt.Sprintf("Refusjon (utbetaling): %s", truncate(outbound.desc, 180)),
		Source:         "bank_import",
		SourceID:       &srcOut,
		SourceTable:    &srcTable,
		CreatedBy:      actorID,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: receivablesAccountCode, Debit: absAmount, Credit: 0},
			{AccountCode: outbound.bankAcct, Debit: 0, Credit: absAmount},
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("create outbound journal: %w", err)
	}

	// Mark both rows with their journal entries and cross-link them. The
	// rows are locked, so the guard is defence in depth: it must hit both
	// rows, still unbooked and unpaired, with the outbound one undismissed.
	tag, err := tx.Exec(ctx,
		`UPDATE bank_import_rows
		    SET journal_entry_id   = CASE WHEN id = $1::uuid THEN $3::uuid ELSE $4::uuid END,
		        refund_paired_with = CASE WHEN id = $1::uuid THEN $2::uuid ELSE $1::uuid END,
		        auto_declined_at   = NULL
		  WHERE club_id = $5 AND id IN ($1::uuid, $2::uuid)
		    AND journal_entry_id IS NULL AND refund_paired_with IS NULL
		    AND (id = $1::uuid OR dismissed_at IS NULL)`,
		inboundRowID, outboundRowID, inboundEntry.ID, outboundEntry.ID, clubID,
	)
	if err != nil {
		return "", "", fmt.Errorf("link refund rows: %w", err)
	}
	if tag.RowsAffected() != 2 {
		return "", "", errBankRowRaced
	}

	if err := postJournalEntryTx(ctx, tx, clubID, inboundEntry.ID, actorID); err != nil {
		return "", "", fmt.Errorf("post inbound journal: %w", err)
	}
	if err := postJournalEntryTx(ctx, tx, clubID, outboundEntry.ID, actorID); err != nil {
		return "", "", fmt.Errorf("post outbound journal: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("commit refund pair: %w", err)
	}
	return inboundEntry.ID, outboundEntry.ID, nil
}
