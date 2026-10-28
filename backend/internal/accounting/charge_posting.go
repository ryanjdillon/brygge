package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/finance"
)

// What every payment provider shares when it books a charge from its
// checkout order: the guard that decides whether the order's split may be
// booked at all (checkOrderChargeSplit), and the posting of one charge
// through the provider's clearing account (chargePosting). Vipps and Stripe
// differ only in the source row, its guarded mark and the accounts.

// errOrderReceivableNotOpen: a faktura on the order is already paid,
// voided or credited, so crediting 1500 for it again would leave a credit
// nothing offsets.
var errOrderReceivableNotOpen = errors.New("a faktura on the order is not open")

// orderSplitMismatchError: the order's split does not add up to the charge.
type orderSplitMismatchError struct {
	Sum, Amount float64
}

func (e *orderSplitMismatchError) Error() string {
	return fmt.Sprintf("order total %.2f does not match charge %.2f", e.Sum, e.Amount)
}

// checkOrderChargeSplit decides whether a charge of amount may be booked
// with split on bookingDate: the credits must sum to the charge, and every
// faktura the split settles must still have an open receivable
// (finance.OpenReceivableSQL) that is in the GL by the booking date
// (finance.ImportedReceivableOpenSQL). It returns the credit lines and the
// fakturas to back-link once the entry is committed. Refusals are
// *orderSplitMismatchError, errOrderReceivableNotOpen or
// ErrImportedNoOpeningBalance; anything else is a database error. A caller
// that holds the fakturas locked passes its transaction as q, so the check
// sees the lock holder it waited for.
func checkOrderChargeSplit(ctx context.Context, q dbtx, clubID string, split []VippsReconcileLine, amount float64, bookingDate time.Time) (credits []CreateJournalLineInput, invoiceIDs []string, err error) {
	var sum float64
	credits = make([]CreateJournalLineInput, 0, len(split))
	for _, l := range split {
		sum += l.Credit
		credits = append(credits, CreateJournalLineInput{AccountCode: l.AccountCode, Credit: l.Credit, Description: l.Description})
		if l.LinkedInvoiceID != "" {
			invoiceIDs = append(invoiceIDs, l.LinkedInvoiceID)
		}
	}
	if !floatNear(sum, amount, 0.01) {
		return nil, nil, &orderSplitMismatchError{Sum: sum, Amount: amount}
	}
	for _, id := range invoiceIDs {
		var open, receivableOK bool
		if err := q.QueryRow(ctx,
			`SELECT `+finance.OpenReceivableSQL("i")+`, `+finance.ImportedReceivableOpenSQL("i", "$3::date")+`
			   FROM invoices i WHERE i.id = $1 AND i.club_id = $2`,
			id, clubID, bookingDate,
		).Scan(&open, &receivableOK); err != nil {
			return nil, nil, fmt.Errorf("loading invoice: %w", err)
		}
		if !open {
			return nil, nil, errOrderReceivableNotOpen
		}
		// Checkout refuses such a faktura, but an order minted before the
		// guard could still carry one (BRY-386).
		if !receivableOK {
			return nil, nil, ErrImportedNoOpeningBalance
		}
	}
	return credits, invoiceIDs, nil
}

// chargePosting is one provider charge booked as DR clearing (the gross)
// / CR the split, plus any extra balanced lines (a fee booked with the
// charge). markSQL links the source row to the entry ($1 entry, $2 row,
// $3 club, then markArgs) and must match no row once the charge is booked; winnerSQL
// ($1 row, $2 club) reads the entry that booked it.
type chargePosting struct {
	clubID, createdBy string
	source            string
	sourceTable       string
	sourceID          string
	clearing          string
	date              time.Time
	amount            float64
	description       string
	credits           []CreateJournalLineInput
	extra             []CreateJournalLineInput
	markSQL           string
	// markArgs are markSQL's parameters after the first three ($4 on).
	markArgs  []any
	winnerSQL string
	// savepoint names the savepoint the Tx form takes before its entry.
	savepoint string
	// periodID, when set, is the open period the caller already resolved;
	// otherwise postChargeTx resolves (and may create) it.
	periodID string
}

// entryInput is the journal entry for the posting in periodID.
func (p *chargePosting) entryInput(periodID string) CreateJournalEntryInput {
	sourceID, sourceTable := p.sourceID, p.sourceTable
	lines := make([]CreateJournalLineInput, 0, 1+len(p.credits)+len(p.extra))
	lines = append(lines, CreateJournalLineInput{AccountCode: p.clearing, Debit: p.amount, Credit: 0, Description: p.description})
	lines = append(lines, p.credits...)
	lines = append(lines, p.extra...)
	return CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      p.date.Format("2006-01-02"),
		Description:    p.description,
		Source:         p.source,
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      p.createdBy,
		ClubID:         p.clubID,
		Lines:          lines,
	}
}

// postChargeTx books p inside the caller's transaction and returns the
// entry. A failed post leaves the transaction to roll back. Losing the
// guarded mark (errChargeRaced, with the winner's entry) leaves it usable:
// the entry is created under a savepoint that the loss rolls back to, so a
// caller that goes on to commit never commits the losing draft.
func postChargeTx(ctx context.Context, tx pgx.Tx, p *chargePosting) (string, error) {
	periodID := p.periodID
	if periodID == "" {
		id, periodStatus, err := resolvePeriodIn(ctx, tx, p.clubID, p.date, "")
		if err != nil {
			return "", fmt.Errorf("resolving fiscal period: %w", err)
		}
		if periodStatus == "closed" {
			return "", fmt.Errorf("fiscal period %d is closed — reopen it or pick a different period", p.date.Year())
		}
		periodID = id
	}

	if _, err := tx.Exec(ctx, `SAVEPOINT `+p.savepoint); err != nil {
		return "", fmt.Errorf("savepoint: %w", err)
	}
	entry, err := createJournalEntryTx(ctx, tx, p.entryInput(periodID))
	if err != nil {
		return "", fmt.Errorf("creating journal entry: %w", err)
	}

	tag, err := tx.Exec(ctx, p.markSQL, append([]any{entry.ID, p.sourceID, p.clubID}, p.markArgs...)...)
	if err != nil {
		return "", fmt.Errorf("marking charge reconciled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT `+p.savepoint); err != nil {
			return "", fmt.Errorf("rollback to savepoint: %w", err)
		}
		var existing *string
		if err := tx.QueryRow(ctx, p.winnerSQL, p.sourceID, p.clubID).Scan(&existing); err != nil {
			return "", fmt.Errorf("charge already reconciled but reading winner failed: %w", err)
		}
		if existing == nil {
			// Not booked by anyone: the row left the bookable state (dismissed,
			// sent to review) after it was read.
			return "", errors.New("charge changed while it was being booked")
		}
		return *existing, errChargeRaced
	}
	if err := postJournalEntryTx(ctx, tx, p.clubID, entry.ID, p.createdBy); err != nil {
		return "", fmt.Errorf("posting journal entry: %w", err)
	}
	if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT `+p.savepoint); err != nil {
		return "", fmt.Errorf("release savepoint: %w", err)
	}
	return entry.ID, nil
}
