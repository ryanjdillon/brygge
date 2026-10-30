package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrRebookClearingDebit is returned by rebookVippsChargeTx when the
// replacement would not debit the clearing account by exactly what the entry
// it replaces did. The settlement's fee and payout credits are booked against
// that debit, so a different one would leave the clearing account off.
var ErrRebookClearingDebit = errors.New("the replacement would change the charge's clearing debit")

// vippsRebook is what rebookVippsChargeTx did, for the caller to finish after
// its commit.
type vippsRebook struct {
	// EntryID is the posted replacement entry.
	EntryID string
	// PriorEntryID and PriorInvoiceID are what the charge was booked on.
	PriorEntryID   string
	PriorInvoiceID string
	// InvoiceID is the faktura the replacement settles ("" for none). Its
	// payment back-link updates the faktura, so the caller runs
	// Service.linkInvoicePayment(ctx, clubID, InvoiceID, Date) once the
	// transaction has committed (AssignVippsChargeToInvoice does the same).
	InvoiceID string
	// Date is the entry date, the old entry's.
	Date time.Time
}

// rebookVippsChargeTx replaces the booking of one Vipps charge by another
// that credits different accounts but debits the clearing account by the same
// amount, inside the caller's transaction. It is the one sanctioned way to
// change what a posted (or draft) charge books while its settlement is
// cleared: the settlement's fee and payout entries are never touched and the
// clearing account's balance is the same before and after, so no unbook of
// the charge on its own is needed (UnassignVippsCharge refuses that, see
// vipps_cleared_settlement.go).
//
// credits are the new credit lines; they must sum to the charge amount. Their
// first line's description is the entry's. invoiceID is the faktura the new
// booking settles, "" for none (a revenue account); a faktura is checked as a
// manual assign checks it (lockInvoiceForAssign, lockManual) and its payment
// back-link is left to the caller (vippsRebook.InvoiceID).
//
// Lock order, as everywhere on this path: the fakturas (the old and the new
// one, id order), the charge row, the settlement's payout rows
// (lockSettlementPayoutsTx), then the old entry. The old entry is voided with
// voidJournalEntryTx when posted (its reversal and original both stay) or
// deleted when a draft; the replacement is posted through postChargeTx on the
// old entry's date. Any failure, including a clearing debit that differs from
// the old entry's (ErrRebookClearingDebit), leaves the caller's transaction
// to roll everything back: the charge stays booked as it was.
func rebookVippsChargeTx(ctx context.Context, tx pgx.Tx, clubID, actorID, rowID string, credits []CreateJournalLineInput, invoiceID string) (*vippsRebook, error) { //nolint:gocognit,cyclop // one guard per rule, each with its own error; split tracked in BRY-424
	if len(credits) == 0 {
		return nil, errors.New("a replacement booking needs credit lines")
	}
	b, err := loadVippsBelastningIn(ctx, tx, clubID, rowID)
	if err != nil {
		return nil, err
	}
	if b.entryID == nil {
		return nil, errors.New("charge is not booked")
	}
	if b.dismissedAt != nil {
		return nil, errors.New("charge is dismissed")
	}
	priorEntryID := *b.entryID
	var priorInvoiceID string
	var orderRef string
	var invPtr *string
	if err := tx.QueryRow(ctx,
		`SELECT invoice_id::text, COALESCE(order_id, '') FROM vipps_import_rows WHERE id = $1 AND club_id = $2`,
		rowID, clubID,
	).Scan(&invPtr, &orderRef); err != nil {
		return nil, fmt.Errorf("loading charge: %w", err)
	}
	if invPtr != nil {
		priorInvoiceID = *invPtr
	}
	if priorInvoiceID == "" && orderRef != "" {
		var isOrder bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM orders WHERE club_id = $1 AND vipps_reference = $2)`,
			clubID, orderRef,
		).Scan(&isOrder); err != nil {
			return nil, fmt.Errorf("checking order: %w", err)
		}
		if isOrder {
			return nil, fmt.Errorf("charge was booked from checkout order %s — it cannot be rebooked", orderRef)
		}
	}

	// Fakturas first, in id order, so two rebooks (or a rebook and an assign)
	// of charges on the same fakturas queue instead of crossing.
	ids := []string{}
	for _, id := range []string{priorInvoiceID, invoiceID} {
		if id != "" && (len(ids) == 0 || ids[0] != id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 2 && ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1 AND club_id = $2 FOR NO KEY UPDATE`, id, clubID); err != nil {
			return nil, fmt.Errorf("locking invoice: %w", err)
		}
	}

	var lockedEntry, lockedInvoice *string
	if err := tx.QueryRow(ctx,
		`SELECT journal_entry_id::text, invoice_id::text FROM vipps_import_rows
		  WHERE id = $1 AND club_id = $2 AND row_type = 'belastning' FOR UPDATE`,
		rowID, clubID,
	).Scan(&lockedEntry, &lockedInvoice); err != nil {
		return nil, fmt.Errorf("locking vipps charge: %w", err)
	}
	if lockedEntry == nil || *lockedEntry != priorEntryID || (lockedInvoice == nil) != (priorInvoiceID == "") ||
		(lockedInvoice != nil && *lockedInvoice != priorInvoiceID) {
		return nil, errors.New("charge was changed while it was being rebooked — try again")
	}

	// The settlement's payout rows are locked (and the answer ignored): the
	// replacement is allowed in a cleared settlement, and the lock keeps a
	// clearing from reading the charge between the void and the new posting.
	if _, err := clearedSettlementOfChargeTx(ctx, tx, clubID, rowID); err != nil {
		return nil, err
	}

	var status string
	var entryDate time.Time
	if err := tx.QueryRow(ctx,
		`SELECT status, entry_date FROM journal_entries WHERE id = $1 AND club_id = $2 FOR UPDATE`,
		priorEntryID, clubID,
	).Scan(&status, &entryDate); err != nil {
		return nil, fmt.Errorf("loading journal entry: %w", err)
	}
	if status != "posted" && status != "draft" {
		return nil, fmt.Errorf("the charge's bilag is %s — release the charge with Angre first, then book it from Tildel", status)
	}
	var shared bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM bank_import_rows WHERE club_id = $1 AND journal_entry_id = $2::uuid)`,
		clubID, priorEntryID,
	).Scan(&shared); err != nil {
		return nil, fmt.Errorf("checking settlement booking: %w", err)
	}
	if shared {
		return nil, ErrVippsChargeInSettlement
	}

	clearing, _, err := vippsAccountsIn(ctx, tx, clubID)
	if err != nil {
		return nil, err
	}
	var oldDebit float64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(jl.debit - jl.credit), 0)
		   FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1 AND a.club_id = $2 AND a.code = $3`,
		priorEntryID, clubID, clearing,
	).Scan(&oldDebit); err != nil {
		return nil, fmt.Errorf("reading the clearing debit: %w", err)
	}
	if toCents(oldDebit) != toCents(b.amount) {
		return nil, fmt.Errorf("%w: the bilag debits %s %.2f, the charge is %.2f", ErrRebookClearingDebit, clearing, oldDebit, b.amount)
	}
	var creditCents int64
	for _, c := range credits {
		if c.AccountCode == clearing {
			return nil, fmt.Errorf("%w: a replacement line may not touch the clearing account %s", ErrRebookClearingDebit, clearing)
		}
		creditCents += toCents(c.Credit) - toCents(c.Debit)
	}
	if creditCents != toCents(b.amount) {
		return nil, fmt.Errorf("%w: the new lines credit %.2f, the charge is %.2f", ErrRebookClearingDebit, float64(creditCents)/100, b.amount)
	}

	// Unlink, then take the old booking out of the ledger.
	if _, err := tx.Exec(ctx,
		`UPDATE vipps_import_rows SET journal_entry_id = NULL, invoice_id = NULL, auto_assign_rule = NULL
		  WHERE id = $1 AND club_id = $2`,
		rowID, clubID,
	); err != nil {
		return nil, fmt.Errorf("clearing charge: %w", err)
	}
	if priorInvoiceID != "" {
		if _, err := reopenInvoicePaymentTx(ctx, tx, clubID, priorInvoiceID); err != nil {
			return nil, err
		}
	}
	switch status {
	case "posted":
		if _, err := voidJournalEntryTx(ctx, tx, clubID, priorEntryID, actorID); err != nil {
			if errors.Is(err, ErrPeriodClosed) {
				return nil, fmt.Errorf("charge is booked in a closed fiscal period — correct it with a new entry in an open period: %w", ErrPeriodClosed)
			}
			return nil, fmt.Errorf("voiding bilag: %w", err)
		}
	default:
		if _, err := tx.Exec(ctx,
			`DELETE FROM journal_entries WHERE id = $1 AND club_id = $2 AND status = 'draft'`,
			priorEntryID, clubID,
		); err != nil {
			return nil, fmt.Errorf("deleting draft: %w", err)
		}
	}

	if invoiceID != "" {
		invCents, err := lockInvoiceForAssign(ctx, tx, clubID, invoiceID, invoiceLockOpts{mode: lockManual, bookingDate: b.date})
		if err != nil {
			return nil, err
		}
		if invAmount := float64(invCents) / 100; !floatNear(invAmount, b.amount, 1.00) {
			return nil, fmt.Errorf("charge %.2f does not match invoice total %.2f", b.amount, invAmount)
		}
	}

	description := credits[0].Description
	if description == "" {
		description = fmt.Sprintf("Vipps innbetaling: %s", b.customerName)
	}
	p := vippsChargePosting(clubID, actorID, clearing, description, b, credits)
	p.date = entryDate
	entryID, err := postChargeTx(ctx, tx, p)
	if err != nil {
		if errors.Is(err, errChargeRaced) {
			return nil, errors.New("charge was booked concurrently while it was being rebooked — try again")
		}
		return nil, err
	}
	if invoiceID != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE vipps_import_rows SET invoice_id = $3 WHERE id = $1 AND club_id = $2`,
			rowID, clubID, invoiceID,
		); err != nil {
			return nil, fmt.Errorf("recording the settled invoice: %w", err)
		}
	}
	return &vippsRebook{
		EntryID: entryID, PriorEntryID: priorEntryID, PriorInvoiceID: priorInvoiceID,
		InvoiceID: invoiceID, Date: entryDate,
	}, nil
}
