package accounting

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/stripe"
)

// Undoing a Stripe booking, and handing the row back. A booked charge, fee
// or manually booked row is unassigned from the Stripe row only, never in
// the ledger (VoidLedgerEntry refuses its entry): the unassign voids the
// entry, reopens the fakturas it settled and holds the row for the person
// (review reason unassigned), whom nothing overrides. The ingest skips a
// held row and the booking pass never retries it; the person books it again
// by hand, hands it back to the pass (rebook) or dismisses it.

// StripeTxnUnassignResult is what UnassignStripeTxn undid.
type StripeTxnUnassignResult struct {
	ID                   string `json:"id"`
	BalanceTransactionID string `json:"balance_transaction_id"`
	// VoidedJournalEntryID is the row's entry, now voided.
	VoidedJournalEntryID string `json:"voided_journal_entry_id"`
	// ReversalEntryID is the reversal the void posted; empty when the entry
	// was voided already.
	ReversalEntryID string `json:"reversal_entry_id,omitempty"`
	AlreadyVoided   bool   `json:"already_voided,omitempty"`
	// InvoiceIDs are the fakturas the entry settled, reopened.
	InvoiceIDs   []string `json:"invoice_ids"`
	Status       string   `json:"status"`
	ReviewReason string   `json:"review_reason"`
}

// UnassignStripeTxn undoes a Stripe row's booking, as actorID, in the
// club's Stripe slot and one transaction: lock the row, then the fakturas
// its entry settled (in id order, as the booking pass and the manual assign
// take them), void the entry (a reversal in the entry's own period), reopen
// those fakturas (payment_id cleared, the payment linkInvoicePayment made
// deleted) and hold the row in review as unassigned with no entry and no
// faktura. online_paid_at stays: the card money did arrive, and the marker
// keeps the faktura off bank matching while the row is held.
//
// Refused (a *StripeTxnRefusal, nothing changed): a row not booked or whose
// entry is not its own (not_allowed), a payout row (use_bank_unassign: the
// payout clearing is undone from its bank deposit), a row whose payout is
// cleared in the books (payout_cleared: that clearing counted the row), and
// an entry in a closed or locked period (period_closed). An entry voided
// already (in the ledger before the refusal there) is tolerated: the row and
// fakturas are released without a second reversal.
func (s *Service) UnassignStripeTxn(ctx context.Context, clubID, actorID, txnID string) (*StripeTxnUnassignResult, error) {
	var res *StripeTxnUnassignResult
	err := s.stripeManualTx(ctx, clubID, actorID, func(tx pgx.Tx) error {
		var err error
		res, err = unassignStripeTxnTx(ctx, tx, clubID, actorID, txnID)
		if err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func unassignStripeTxnTx(ctx context.Context, tx pgx.Tx, clubID, actorID, txnID string) (*StripeTxnUnassignResult, error) {
	row, err := lockStripeTxnTx(ctx, tx, clubID, txnID)
	if err != nil {
		return nil, err
	}
	if err := requireStripeUnassign(row); err != nil {
		return nil, err
	}
	switch {
	case row.category == stripe.CategoryCharge && row.refundedOre > 0:
		return nil, stripeRefusal(StripeTxnRefundBooked,
			"kortbetalingen har en bokført refusjon – angre refusjonen først")
	case stripeRefundCategory(row.category):
		return unassignStripeRefundTx(ctx, tx, clubID, actorID, row)
	}

	// The row is locked, so the fields the faktura set derives from
	// (invoice_id, review_reason, order_id) cannot change under us.
	rows, err := tx.Query(ctx,
		`SELECT i.id::text FROM stripe_balance_transactions t
		   JOIN invoices i ON `+stripeSettledInvoiceSQL+`
		  WHERE t.id = $1 AND t.club_id = $2
		  ORDER BY i.id`,
		row.id, clubID)
	if err != nil {
		return nil, fmt.Errorf("listing settled fakturas: %w", err)
	}
	invoiceIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("listing settled fakturas: %w", err)
	}
	if err := lockInvoicesTx(ctx, tx, clubID, invoiceIDs); err != nil {
		return nil, err
	}

	res := &StripeTxnUnassignResult{
		ID: row.id, BalanceTransactionID: row.btID, VoidedJournalEntryID: row.entryID,
		InvoiceIDs: invoiceIDs, Status: StripeTxnNeedsReview, ReviewReason: StripeReviewUnassigned,
	}
	if err := voidStripeEntryTx(ctx, tx, clubID, actorID, row, res); err != nil {
		return nil, err
	}
	for _, id := range invoiceIDs {
		if _, err := reopenInvoicePaymentTx(ctx, tx, clubID, id); err != nil {
			return nil, err
		}
	}
	if err := holdUnassignedTx(ctx, tx, clubID, row); err != nil {
		return nil, err
	}
	return res, nil
}

// voidStripeEntryTx voids the row's entry (a reversal in its own period),
// tolerating one voided already.
func voidStripeEntryTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeManualRow, res *StripeTxnUnassignResult) error {
	reversal, err := voidJournalEntryTx(ctx, tx, clubID, row.entryID, actorID)
	switch {
	case err == nil:
		res.ReversalEntryID = reversal.ID
	case errors.Is(err, ErrEntryNotPosted) && row.entryStatus != "draft":
		// Voided already; a Stripe entry is never left a draft, so a draft
		// here is a bug and falls through to the error below.
		res.AlreadyVoided = true
	case errors.Is(err, ErrPeriodClosed):
		return stripeRefusal(StripeTxnPeriodClosed,
			"regnskapsperioden bilaget er ført i er stengt – åpne perioden eller korriger med et nytt bilag")
	default:
		return fmt.Errorf("voiding stripe entry: %w", err)
	}
	return nil
}

// holdUnassignedTx holds the row in review as unassigned, with no entry
// and no faktura.
func holdUnassignedTx(ctx context.Context, tx pgx.Tx, clubID string, row *stripeManualRow) error {
	tag, err := tx.Exec(ctx,
		`UPDATE stripe_balance_transactions
		    SET status = 'needs_review', review_reason = $4, journal_entry_id = NULL, invoice_id = NULL, updated_at = now()
		  WHERE id = $1 AND club_id = $2 AND status = 'booked' AND journal_entry_id = $3`,
		row.id, clubID, row.entryID, StripeReviewUnassigned)
	if err != nil {
		return fmt.Errorf("holding stripe transaction: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("stripe transaction changed under its lock")
	}
	return nil
}

// requireStripeUnassign refuses an unassign the row does not allow, with
// the reason a person can act on.
func requireStripeUnassign(r *stripeManualRow) error {
	switch {
	case stripePayoutCategory(r.category):
		return stripeRefusal(StripeTxnUseBankUnassign,
			"utbetalingen bokføres av avstemmingen – angre bankinnbetalingen i Tildel i stedet")
	case !r.booked || r.status != StripeTxnBooked:
		return stripeRefusal(StripeTxnNotAllowed, "transaksjonen er ikke bokført")
	case r.payoutCleared:
		return stripeRefusal(StripeTxnPayoutCleared,
			"utbetalingen transaksjonen var med i er avstemt – angre bankinnbetalingen for utbetalingen først")
	case !r.ownEntry:
		return stripeRefusal(StripeTxnNotAllowed, "bilaget hører ikke til denne transaksjonen")
	}
	return nil
}

// StripeTxnRebookResult is a rebooked row after the booking pass ran on it.
type StripeTxnRebookResult struct {
	ID                   string `json:"id"`
	BalanceTransactionID string `json:"balance_transaction_id"`
	// Outcome is booked, needs_review (the pass held it again: Reason is the
	// review reason), deferred (no open period: Reason is the deferral
	// reason; the next pass retries it) or failed (Error; the row waits as
	// unbooked for the next pass).
	Outcome        string `json:"outcome"`
	Reason         string `json:"reason,omitempty"`
	Error          string `json:"error,omitempty"`
	Status         string `json:"status"`
	JournalEntryID string `json:"journal_entry_id,omitempty"`
}

// Outcomes of a rebook (StripeTxnRebookResult.Outcome).
const (
	StripeRebookBooked      = "booked"
	StripeRebookNeedsReview = "needs_review"
	StripeRebookDeferred    = "deferred"
	StripeRebookFailed      = "failed"
)

// RebookStripeTxn hands a row a person holds (unassigned, or a refund or
// dispute stored before they were booked) back to the booking pass, as
// actorID: a charge with an order, a fee, a refund or a dispute goes back to
// unbooked with no
// review reason (a guarded update, so only a held row moves), and the pass
// books that row at once, in the same hold of the
// club's Stripe slot. The pass re-checks everything as for any ready row,
// so a charge whose order no longer adds up is held for review again with
// the pass's reason. Refused with not_allowed for any other row.
func (s *Service) RebookStripeTxn(ctx context.Context, clubID, actorID, txnID string) (*StripeTxnRebookResult, error) {
	var res *StripeTxnRebookResult
	err := s.stripeManualTx(ctx, clubID, actorID, func(tx pgx.Tx) error {
		row, err := lockStripeTxnTx(ctx, tx, clubID, txnID)
		if err != nil {
			return err
		}
		if err := requireStripeAction(row, StripeActionRebook); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE stripe_balance_transactions SET status = 'unbooked', review_reason = NULL, updated_at = now()
			  WHERE id = $1 AND club_id = $2 AND journal_entry_id IS NULL
			    AND status = 'needs_review' AND review_reason IN ($3, $4)`,
			row.id, clubID, StripeReviewUnassigned, StripeReviewRefundLegacy)
		if err != nil {
			return fmt.Errorf("releasing stripe transaction: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return errors.New("stripe transaction changed under its lock")
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		res = &StripeTxnRebookResult{ID: row.id, BalanceTransactionID: row.btID}
		// The commit released the row; the pass locks it again in its own
		// transaction, still in this hold of the slot.
		out, err := s.bookStripeTxn(ctx, clubID, actorID, row.id)
		res.Outcome, res.Reason = stripeRebookOutcome(out, err)
		if err != nil {
			s.log.Error().Err(err).Str("balance_transaction_id", row.btID).Msg("stripe rebook: booking failed")
			res.Error = stripeBookFailedText
		}
		// The row is released and committed by now, so a failed read is
		// reported in the result rather than as an error: the rebook (and
		// its audit row) happened, only its outcome is unknown.
		if err := s.db.QueryRow(ctx,
			`SELECT status, COALESCE(journal_entry_id::text, '') FROM stripe_balance_transactions WHERE id = $1 AND club_id = $2`,
			row.id, clubID,
		).Scan(&res.Status, &res.JournalEntryID); err != nil {
			s.log.Error().Err(err).Str("balance_transaction_id", row.btID).Msg("stripe rebook: reading the row back failed")
			res.Outcome, res.Status, res.JournalEntryID = StripeRebookFailed, "", ""
			res.Error = stripeBookFailedText
			return nil
		}
		if res.Outcome == "" {
			// The pass skipped the row: it was booked by a racing pass of
			// another process, or it is not ready (and stays unbooked).
			res.Outcome = StripeRebookFailed
			if res.Status == StripeTxnBooked {
				res.Outcome = StripeRebookBooked
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// stripeRebookOutcome names what the booking pass did with a rebooked row.
func stripeRebookOutcome(out stripeBookOutcome, err error) (outcome, reason string) {
	switch {
	case err != nil:
		return StripeRebookFailed, ""
	case out.kind == stripeOutcomeCharge, out.kind == stripeOutcomeFee, out.kind == stripeOutcomeRefund, out.kind == stripeOutcomeDispute:
		return StripeRebookBooked, ""
	case out.kind == stripeOutcomeReview:
		return StripeRebookNeedsReview, out.reason
	case out.kind == stripeOutcomeDeferred:
		return StripeRebookDeferred, out.reason
	}
	return "", ""
}
