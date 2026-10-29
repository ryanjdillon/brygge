package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/stripe"
)

// Booking Stripe disputes (ACC-4, ACC-5). A dispute is the card network
// taking a payment back from the club's Stripe balance, so it is booked as a
// loss when Stripe's balance shows the withdrawal, and reversed when Stripe
// gives the money back (the club won):
//
//	dispute:          DR dispute account |amount| / CR stripe clearing
//	                  DR payment fee / CR stripe clearing (the dispute fee)
//	dispute_reversal: DR stripe clearing / CR dispute account |amount|
//	                  the fee lines mirrored, only when Stripe returned a fee
//
// The faktura and its order are not touched: the member did pay, and whether
// to invoice again after a lost dispute is a board decision. Nothing here
// needs the charge to be booked, so the charge row is only linked for
// information (origin_txn_id). The webhook keeps payment_disputes up to
// date; it books nothing.

// stripeDisputeAccountCode is the default account a lost dispute is booked
// to (clubs.stripe_dispute_account): NS 4102 Tap på fordringer.
const stripeDisputeAccountCode = "7830"

// disputeEffects is what a dispute booking did, for its audit row.
type disputeEffects struct {
	rowID, btID, category, originID, account string
	amountOre, feeOre                        int64
}

// stripeDisputeAccountIn is the club's dispute account (the default when
// unset) and whether it is an active account of the club's chart.
func stripeDisputeAccountIn(ctx context.Context, q dbtx, clubID string) (code string, usable bool, err error) {
	var active *bool
	err = q.QueryRow(ctx,
		`SELECT c.code, a.is_active
		   FROM (SELECT COALESCE(NULLIF(stripe_dispute_account, ''), $2) AS code FROM clubs WHERE id = $1) c
		   LEFT JOIN accounts a ON a.club_id = $1 AND a.code = c.code`,
		clubID, stripeDisputeAccountCode,
	).Scan(&code, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, errors.New("club not found")
	}
	if err != nil {
		return "", false, fmt.Errorf("loading stripe dispute account: %w", err)
	}
	return code, active != nil && *active, nil
}

// bookStripeDisputeTx books a dispute or dispute reversal, or holds it for
// review while the club has no usable dispute account.
func bookStripeDisputeTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, date time.Time, periodID string) (int, string, *disputeEffects, error) {
	if row.amountOre == 0 && row.feeOre == 0 {
		// Nothing moved, so there is nothing to post; done is done, and the
		// pass must not fail on it every time.
		tag, err := tx.Exec(ctx,
			`UPDATE stripe_balance_transactions
			    SET status = 'booked', review_reason = NULL, updated_at = now()
			  WHERE id = $1 AND club_id = $2 AND journal_entry_id IS NULL AND status IN ('unbooked', 'needs_review')`,
			row.id, clubID)
		if err != nil {
			return stripeOutcomeSkipped, "", nil, fmt.Errorf("marking dispute without money booked: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return stripeOutcomeSkipped, "", nil, nil
		}
		return stripeOutcomeDispute, "", nil, nil
	}
	// A dispute takes money out of the balance and a reversal gives it
	// back; a row that says otherwise is for a person to look at.
	if (row.category == stripe.CategoryDispute && row.amountOre > 0) ||
		(row.category == stripe.CategoryDisputeReversal && row.amountOre < 0) {
		kind, reason, _, err := holdRefundTx(ctx, tx, clubID, row.id, StripeReviewDisputeDirection)
		return kind, reason, nil, err
	}
	account, usable, err := stripeDisputeAccountIn(ctx, tx, clubID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if !usable {
		kind, reason, _, err := holdRefundTx(ctx, tx, clubID, row.id, StripeReviewDisputeAccountMissing)
		return kind, reason, nil, err
	}
	clearing, feeAccount, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}

	// The charge is only looked up, not locked: nothing about it decides the
	// booking.
	originID, found, err := findStripeOriginTx(ctx, tx, clubID, row)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if found {
		if _, err := tx.Exec(ctx,
			`UPDATE stripe_balance_transactions SET origin_txn_id = $3, updated_at = now()
			  WHERE id = $1 AND club_id = $2 AND origin_txn_id IS DISTINCT FROM $3`,
			row.id, clubID, originID); err != nil {
			return stripeOutcomeSkipped, "", nil, fmt.Errorf("linking dispute: %w", err)
		}
	}

	if _, err := postStripeRowEntryTx(ctx, tx, clubID, actorID, row.id, date, periodID,
		disputeEntry(row, account, clearing, feeAccount)); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	return stripeOutcomeDispute, "", &disputeEffects{
		originID: originID, account: account, amountOre: abs64(row.amountOre), feeOre: row.feeOre,
	}, nil
}

// disputeEntry is the entry of a dispute (money out of the Stripe balance
// into the loss account) or of its reversal (the mirror), with the fee Stripe
// kept or returned. The side follows the sign of the amount, as it does for
// every other balance row. A row that moved no principal books its fee alone.
func disputeEntry(row *stripeTxnRow, account, clearing, feeAccount string) refundEntry {
	desc := "Kortbetalingstvist " + row.btID
	if row.category == stripe.CategoryDisputeReversal {
		desc = "Kortbetalingstvist vunnet " + row.btID
	}
	var lines []CreateJournalLineInput
	if amount := oreToKr(abs64(row.amountOre)); amount != 0 {
		if row.amountOre > 0 {
			lines = append(lines,
				CreateJournalLineInput{AccountCode: clearing, Debit: amount, Description: desc},
				CreateJournalLineInput{AccountCode: account, Credit: amount, Description: desc})
		} else {
			lines = append(lines,
				CreateJournalLineInput{AccountCode: account, Debit: amount, Description: desc},
				CreateJournalLineInput{AccountCode: clearing, Credit: amount, Description: desc})
		}
	}
	lines = append(lines, stripeFeeLines(feeAccount, clearing, row.feeOre, "Stripe-gebyr "+desc)...)
	return refundEntry{desc: desc, lines: lines}
}

// afterDisputeBooked audits a committed dispute booking. It runs after the
// commit, so it may use the pool.
func (s *Service) afterDisputeBooked(ctx context.Context, clubID, actorID string, eff *disputeEffects) {
	if s.audit == nil {
		return
	}
	s.audit.LogAction(ctx, clubID, actorID, "", audit.ActionStripeTxnDisputeBooked, "stripe_balance_transaction", eff.rowID,
		map[string]any{
			"balance_transaction_id": eff.btID, "category": eff.category, "origin_txn_id": eff.originID,
			"account": eff.account, "amount_ore": eff.amountOre, "fee_ore": eff.feeOre,
		})
}
