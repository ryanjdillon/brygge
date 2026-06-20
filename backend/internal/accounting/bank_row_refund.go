package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// refundReasons are the dismiss reasons that create a pending refund obligation.
var refundReasons = map[string]bool{
	"double_payment":   true,
	"overpayment":      true,
	"refund_or_credit": true,
}

// RefundPendingRow is a dismissed bank row that needs a refund sent.
type RefundPendingRow struct {
	ID              string     `json:"id"`
	RowDate         time.Time  `json:"row_date"`
	Amount          float64    `json:"amount"`
	Counterpart     string     `json:"counterpart"`
	Description     string     `json:"description"`
	KIDNumber       string     `json:"kid_number"`
	DismissedReason string     `json:"dismissed_reason"`
	DismissedAt     time.Time  `json:"dismissed_at"`
	BankAccountCode string     `json:"bank_account_code"`
	ImportID        string     `json:"import_id"`
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
		   FROM bank_import_rows
		  WHERE club_id = $1
		    AND dismissed_reason IN ('double_payment', 'overpayment', 'refund_or_credit')
		    AND refund_paired_with IS NULL`,
		clubID,
	).Scan(&n)
	return n, err
}

// SuggestRefundOutbound finds unmatched outgoing bank rows that look like
// they could be the refund transfer for the given inbound row (same club,
// negative amount within 1 % of the inbound amount, undismissed, unmatched).
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
	targetAbs := inboundAmount
	// Outgoing rows have negative amounts; match within 1 % tolerance.
	rows, err := s.db.Query(ctx,
		`SELECT id, row_date, amount,
		        COALESCE(counterpart, ''), COALESCE(description, '')
		   FROM bank_import_rows
		  WHERE club_id = $1
		    AND amount < 0
		    AND ABS(amount) BETWEEN $2 * 0.99 AND $2 * 1.01
		    AND journal_entry_id IS NULL
		    AND dismissed_at IS NULL
		    AND refund_paired_with IS NULL
		  ORDER BY row_date DESC
		  LIMIT 20`,
		clubID, targetAbs,
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

// PairRefund links a dismissed inbound row with the matching outbound refund
// transfer. It creates two balanced journal entries:
//   - Inbound: DR bank / CR receivables  (the money that came in)
//   - Outbound: DR receivables / CR bank (the refund that went out)
//
// Both rows are marked with refund_paired_with pointing at each other.
// The inbound row keeps its dismissed_* columns for audit — it is not
// un-dismissed.
func (s *Service) PairRefund(
	ctx context.Context, clubID, actorID, inboundRowID, outboundRowID string,
) (inboundJournalID, outboundJournalID string, err error) {
	if inboundRowID == outboundRowID {
		return "", "", errors.New("inbound and outbound row must differ")
	}

	type rowInfo struct {
		date            time.Time
		amount          float64
		desc            string
		bankAcct        string
		dismissed       bool
		dismissedReason string
		alreadyJournal  bool
		alreadyPaired   bool
	}

	loadRow := func(id string) (rowInfo, error) {
		var ri rowInfo
		var reason *string
		err := s.db.QueryRow(ctx,
			`SELECT bir.row_date, bir.amount, COALESCE(bir.description,''),
			        bi.bank_account_code,
			        bir.dismissed_at IS NOT NULL,
			        bir.dismissed_reason,
			        bir.journal_entry_id IS NOT NULL,
			        bir.refund_paired_with IS NOT NULL
			   FROM bank_import_rows bir
			   JOIN bank_imports bi ON bi.id = bir.bank_import_id
			  WHERE bir.id = $1 AND bir.club_id = $2`,
			id, clubID,
		).Scan(&ri.date, &ri.amount, &ri.desc, &ri.bankAcct,
			&ri.dismissed, &reason, &ri.alreadyJournal, &ri.alreadyPaired)
		if reason != nil {
			ri.dismissedReason = *reason
		}
		return ri, err
	}

	inbound, err := loadRow(inboundRowID)
	if err != nil {
		return "", "", fmt.Errorf("load inbound row: %w", err)
	}
	outbound, err := loadRow(outboundRowID)
	if err != nil {
		return "", "", fmt.Errorf("load outbound row: %w", err)
	}

	if !inbound.dismissed || !refundReasons[inbound.dismissedReason] {
		return "", "", errors.New("inbound row must be dismissed with a refund reason (double_payment / overpayment / refund_or_credit)")
	}
	if inbound.alreadyPaired {
		return "", "", errors.New("inbound row already has a paired refund")
	}
	if inbound.amount <= 0 {
		return "", "", errors.New("inbound row must have a positive amount")
	}
	if outbound.dismissed {
		return "", "", errors.New("outbound row is dismissed — cannot use as refund transfer")
	}
	if outbound.alreadyJournal {
		return "", "", errors.New("outbound row is already reconciled")
	}
	if outbound.alreadyPaired {
		return "", "", errors.New("outbound row already has a paired refund")
	}
	if outbound.amount >= 0 {
		return "", "", errors.New("outbound row must have a negative (outgoing) amount")
	}

	// Both entries use the outbound (later) date for the fiscal period,
	// ensuring they land in the same period for a clean reversal.
	periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, outbound.date, "")
	if perr != nil {
		return "", "", fmt.Errorf("resolve period: %w", perr)
	}
	if periodStatus == "closed" {
		return "", "", fmt.Errorf("fiscal period for %s is closed; reopen first", outbound.date.Format("2006-01-02"))
	}

	absAmount := inbound.amount

	srcIn := inboundRowID
	srcTable := "bank_import_rows"
	inboundEntry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      outbound.date.Format("2006-01-02"),
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
	if err := s.PostJournalEntry(ctx, inboundEntry.ID, actorID); err != nil {
		return "", "", fmt.Errorf("post inbound journal: %w", err)
	}

	srcOut := outboundRowID
	outboundEntry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      outbound.date.Format("2006-01-02"),
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
	if err := s.PostJournalEntry(ctx, outboundEntry.ID, actorID); err != nil {
		return "", "", fmt.Errorf("post outbound journal: %w", err)
	}

	// Mark both rows with their journal entries and cross-link them.
	if _, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows
		    SET journal_entry_id = $1, refund_paired_with = $2
		  WHERE id = $3 AND club_id = $4`,
		inboundEntry.ID, outboundRowID, inboundRowID, clubID,
	); err != nil {
		return "", "", fmt.Errorf("update inbound row: %w", err)
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows
		    SET journal_entry_id = $1, refund_paired_with = $2
		  WHERE id = $3 AND club_id = $4`,
		outboundEntry.ID, inboundRowID, outboundRowID, clubID,
	); err != nil {
		return "", "", fmt.Errorf("update outbound row: %w", err)
	}

	return inboundEntry.ID, outboundEntry.ID, nil
}
