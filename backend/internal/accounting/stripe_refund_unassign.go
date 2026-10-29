package accounting

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/stripe"
)

// Undoing a refund booking. A refund that issued credit notes is never
// undone (ACC-9: the notes stand; the correction is a new faktura). A
// refund booked to an account or as a duplicate capture's pair is undone
// by voiding its entry. A refund that reopened fakturas to collect again is
// undone only while each is still open, and undoing it settles them again,
// as a failed refund would. A refund failure is undone only when its refund
// touched no faktura: the documents it issued stand too. Locks: the row
// (already held), then the charge, then the fakturas.

// unassignStripeRefundTx undoes a booked refund or refund failure and
// holds the row as unassigned.
func unassignStripeRefundTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeManualRow) (*StripeTxnUnassignResult, error) {
	res := &StripeTxnUnassignResult{
		ID: row.id, BalanceTransactionID: row.btID, VoidedJournalEntryID: row.entryID,
		InvoiceIDs: []string{}, Status: StripeTxnNeedsReview, ReviewReason: StripeReviewUnassigned,
	}
	var err error
	if row.category == stripe.CategoryRefundFailure {
		err = unassignRefundFailureTx(ctx, tx, clubID, actorID, row, res)
	} else {
		err = unassignRefundTx(ctx, tx, clubID, actorID, row, res)
	}
	if err != nil {
		return nil, err
	}
	if err := holdUnassignedTx(ctx, tx, clubID, row); err != nil {
		return nil, err
	}
	return res, nil
}

// refusalForDocuments refuses an unassign of a refund that issued credit
// notes.
func refusalForDocuments(docs []refundDocument) error {
	for _, d := range docs {
		if d.kind == refundOutcomeCredit || d.kind == "refail_invoice" {
			return stripeRefusal(StripeTxnCreditNoteIssued,
				"refusjonen har utstedt kreditnota, som ikke kan annulleres – lag en ny faktura i stedet")
		}
	}
	return nil
}

func unassignRefundTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeManualRow, res *StripeTxnUnassignResult) error {
	var failed bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM stripe_balance_transactions
		                 WHERE club_id = $1 AND origin_txn_id = $2 AND reporting_category = 'refund_failure'
		                   AND journal_entry_id IS NOT NULL)`, clubID, row.id,
	).Scan(&failed); err != nil {
		return fmt.Errorf("checking refund failures: %w", err)
	}
	if failed {
		return stripeRefusal(StripeTxnNotAllowed, "en mislykket refusjon er bokført mot denne refusjonen")
	}
	docs, err := loadRefundDocumentsTx(ctx, tx, clubID, row.id)
	if err != nil {
		return err
	}
	if err := refusalForDocuments(docs); err != nil {
		return err
	}
	var charge *stripeOrigin
	if row.originID != nil {
		if charge, err = lockStripeOriginTx(ctx, tx, clubID, *row.originID); err != nil {
			return err
		}
	}
	invoiceIDs := documentInvoiceIDs(docs)
	if err := lockInvoicesTx(ctx, tx, clubID, invoiceIDs); err != nil {
		return err
	}
	for _, id := range invoiceIDs {
		open, err := recollectStillOpenTx(ctx, tx, clubID, id)
		if err != nil {
			return err
		}
		if !open || charge == nil {
			return stripeRefusal(StripeTxnFakturaSettledSince,
				"fakturaen refusjonen åpnet igjen er betalt, kreditert eller bokført siden")
		}
	}
	if err := voidStripeEntryTx(ctx, tx, clubID, actorID, row, res); err != nil {
		return err
	}
	for _, d := range docs {
		if err := restoreRecollectTx(ctx, tx, clubID, d, charge); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM refund_documents WHERE club_id = $1 AND refund_txn_id = $2`, clubID, row.id); err != nil {
		return fmt.Errorf("removing refund documents: %w", err)
	}
	res.InvoiceIDs = append(res.InvoiceIDs, invoiceIDs...)
	if charge != nil && row.reviewReason == "" {
		return addRefundedTx(ctx, tx, clubID, charge.id, row.amountOre)
	}
	return nil
}

func unassignRefundFailureTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeManualRow, res *StripeTxnUnassignResult) error {
	own, err := loadRefundDocumentsTx(ctx, tx, clubID, row.id)
	if err != nil {
		return err
	}
	if err := refusalForDocuments(own); err != nil {
		return err
	}
	var chargeID *string
	// The failure moved refunded_ore only if the refund it books against
	// counted in it, i.e. the pass booked that refund (bookRefundFailureTx).
	counted := false
	if row.originID != nil {
		docs, err := loadRefundDocumentsTx(ctx, tx, clubID, *row.originID)
		if err != nil {
			return err
		}
		if len(docs) > 0 {
			return stripeRefusal(StripeTxnNotAllowed,
				"refusjonen gjaldt en faktura, som den mislykkede refusjonen gjorde opp igjen – bokfør en korrigering i stedet")
		}
		if err := tx.QueryRow(ctx,
			`SELECT origin_txn_id::text, review_reason IS NULL FROM stripe_balance_transactions WHERE id = $1 AND club_id = $2`,
			*row.originID, clubID,
		).Scan(&chargeID, &counted); err != nil {
			return fmt.Errorf("reading the refund: %w", err)
		}
	}
	if chargeID != nil {
		if _, err := lockStripeOriginTx(ctx, tx, clubID, *chargeID); err != nil {
			return err
		}
	}
	if err := voidStripeEntryTx(ctx, tx, clubID, actorID, row, res); err != nil {
		return err
	}
	if chargeID != nil && counted {
		return addRefundedTx(ctx, tx, clubID, *chargeID, row.amountOre)
	}
	return nil
}
