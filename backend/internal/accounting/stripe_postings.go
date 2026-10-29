package accounting

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// Stripe charge and fee postings (refunds are in stripe_refunds.go, disputes
// in stripe_disputes.go). A charge is booked from its checkout
// order, the way an order-linked Vipps charge is (checkOrderChargeSplit,
// chargePosting), with Stripe's fee in the same entry:
//
//	DR stripe clearing (gross) / CR 1500 per faktura, CR revenue per line
//	DR payment fee / CR stripe clearing (the fee Stripe kept)
//
// A fee transaction (Stripe billing the club outside a charge) books
// DR payment fee / CR stripe clearing. What stays on the clearing account
// is what Stripe owes the club; the payout clears it
// (stripe_payout_clearing.go).
//
// Each row is booked in a transaction of its own that locks the row, then
// the fakturas its order settles, and holds both until the entry is posted
// and the row marked booked, so two runs (or two processes) book a row
// once and a bank or Vipps assign of the same faktura waits for it. The
// invoice payment back-link runs after the commit, as on the Vipps paths.

// stripeSplitLabel prefixes the line descriptions of a card payment split.
const stripeSplitLabel = "Kortbetaling"

// Why a booking pass left a row unbooked for now (StripeBookDeferral.Reason).
const (
	// StripeDeferPeriodClosed: the fiscal period of the booking date is
	// closed or locked.
	StripeDeferPeriodClosed = "period_closed"
	// StripeDeferNoPeriod: no fiscal period covers the booking date. Stripe
	// booking never creates one; the treasurer opens the year.
	StripeDeferNoPeriod = "no_period"
)

const stripeBookFailedText = "Bokføring av Stripe-transaksjoner feilet – prøv igjen, eller se loggen"

// stripeMarkSQL links a ready row to its entry; it matches nothing once the
// row is booked, dismissed or held for review.
const stripeMarkSQL = `UPDATE stripe_balance_transactions
    SET journal_entry_id = $1, status = 'booked', updated_at = now()
  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL AND status = 'unbooked'`

const stripeWinnerSQL = `SELECT journal_entry_id FROM stripe_balance_transactions WHERE id = $1 AND club_id = $2`

// StripeBookResult summarises one booking pass over the Stripe balance.
// Per-row failures are collected in Errors and do not stop the pass.
type StripeBookResult struct {
	ChargesBooked int `json:"charges_booked"`
	FeesBooked    int `json:"fees_booked"`
	// RefundsBooked counts refunds and refund failures booked.
	RefundsBooked int `json:"refunds_booked"`
	// DisputesBooked counts disputes and dispute reversals booked.
	DisputesBooked int `json:"disputes_booked"`
	// NeedsReview counts, by review_reason, the charges this pass held for
	// review (newly, or again on a retry).
	NeedsReview map[string]int `json:"needs_review"`
	// Deferred lists rows left unbooked for now; the next pass retries them.
	Deferred []StripeBookDeferral `json:"deferred"`
	Errors   []string             `json:"errors,omitempty"`
	// Busy is set when another Stripe sync or booking of the club was
	// running, so this pass booked nothing.
	Busy bool `json:"busy,omitempty"`
	// Error is set when the pass itself failed (BookStripeBalanceReported).
	Error string `json:"error,omitempty"`

	candidates int
}

// StripeBookDeferral is one row a booking pass left unbooked for now.
type StripeBookDeferral struct {
	BalanceTransactionID string `json:"balance_transaction_id"`
	Date                 string `json:"date"`
	Reason               string `json:"reason"`
}

func newStripeBookResult() *StripeBookResult {
	return &StripeBookResult{NeedsReview: map[string]int{}, Deferred: []StripeBookDeferral{}}
}

// BookStripeBalance books every Stripe charge and fee that is ready, oldest
// first, as actorID. It is the blocking primitive: it waits while another
// Stripe sync or booking of the club runs. The request paths use
// TrySyncAndBookStripeBalance and BookStripeBalanceReported, which do not
// wait; tests and one-off tools use this.
func (s *Service) BookStripeBalance(ctx context.Context, clubID, actorID string) (*StripeBookResult, error) {
	if actorID == "" {
		return newStripeBookResult(), errors.New("booking stripe transactions needs an acting user")
	}
	release, err := s.stripeSync.acquire(ctx, clubID)
	if err != nil {
		return newStripeBookResult(), err
	}
	defer release()
	return s.bookStripeBalance(ctx, clubID, actorID)
}

// BookStripeBalanceReported is the booking pass other work runs at its end
// (bank sync, bank import), followed by the payout sweep
// (ClearStripePayouts) in the same hold of the club's slot. It never fails
// that work: a failure is logged and set as the result's Error. It does not
// wait for a running Stripe sync (Busy, and no sweep) and returns nil
// results when there is nothing to book or clear, or no acting user.
func (s *Service) BookStripeBalanceReported(ctx context.Context, clubID, actorID string) (*StripeBookResult, *StripePayoutSweepResult) {
	if actorID == "" {
		return nil, nil
	}
	release, ok := s.stripeSync.tryAcquire(clubID)
	if !ok {
		res := newStripeBookResult()
		res.Busy = true
		return res, nil
	}
	defer release()
	res, err := s.bookStripeBalance(ctx, clubID, actorID)
	if err != nil {
		s.log.Error().Err(err).Str("club_id", clubID).Msg("stripe booking failed")
		res.Error = stripeBookFailedText
	}
	// The sweep runs even after a failed booking pass: it re-checks every
	// member itself, so a payout whose members did get booked still clears.
	payouts := s.clearStripePayoutsReported(ctx, clubID, actorID)
	if err == nil && res.candidates == 0 {
		return nil, payouts
	}
	return res, payouts
}

// TrySyncAndBookStripeBalance ingests the Stripe balance, books what is
// ready and clears the payouts that are ready, in one hold of the club's
// Stripe slot. Like TrySyncStripeBalance it returns ErrStripeSyncBusy at
// once while another Stripe sync or booking runs. A failed ingest books
// nothing and is the error; a booking pass or sweep that fails after a
// good ingest is logged and reported in its result's Error, so the ingest
// is not lost.
func (s *Service) TrySyncAndBookStripeBalance(ctx context.Context, src StripeBalanceSource, clubID, actorID string) (StripeSyncResult, *StripeBookResult, *StripePayoutSweepResult, error) {
	if src == nil {
		return newStripeSyncResult(), nil, nil, errors.New("stripe client not configured")
	}
	if actorID == "" {
		return newStripeSyncResult(), nil, nil, errors.New("booking stripe transactions needs an acting user")
	}
	release, ok := s.stripeSync.tryAcquire(clubID)
	if !ok {
		return newStripeSyncResult(), nil, nil, ErrStripeSyncBusy
	}
	defer release()
	synced, err := s.syncStripeBalance(ctx, src, clubID)
	if err != nil {
		return synced, nil, nil, err
	}
	booked, err := s.bookStripeBalance(ctx, clubID, actorID)
	if err != nil {
		s.log.Error().Err(err).Str("club_id", clubID).Msg("stripe booking failed")
		booked.Error = stripeBookFailedText
	}
	payouts, err := s.clearStripePayouts(ctx, clubID, actorID)
	if err != nil {
		s.log.Error().Err(err).Str("club_id", clubID).Msg("stripe payout sweep failed")
		payouts.Error = stripePayoutSweepFailedTxt
	}
	return synced, booked, payouts, nil
}

func (s *Service) bookStripeBalance(ctx context.Context, clubID, actorID string) (*StripeBookResult, error) {
	res := newStripeBookResult()
	rows, err := s.db.Query(ctx,
		`SELECT id FROM stripe_balance_transactions
		  WHERE club_id = $1 AND journal_entry_id IS NULL AND currency = 'nok'
		    AND ((reporting_category = 'fee' AND status = 'unbooked')
		      OR (reporting_category = 'charge' AND order_id IS NOT NULL
		          AND (status = 'unbooked' OR `+stripeBookingReviewSQL+`))
		      OR (reporting_category IN ('refund', 'refund_failure', 'dispute', 'dispute_reversal')
		          AND (status = 'unbooked' OR `+stripeBookingReviewSQL+`)))
		  ORDER BY created_at, id`,
		clubID)
	if err != nil {
		return res, fmt.Errorf("listing stripe transactions to book: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return res, fmt.Errorf("listing stripe transactions to book: %w", err)
	}
	res.candidates = len(ids)
	for _, id := range ids {
		out, err := s.bookStripeTxn(ctx, clubID, actorID, id)
		res.record(out, err)
		if out.kind == stripeOutcomeDeferred {
			s.log.Info().Str("balance_transaction_id", out.btID).Str("date", out.date.Format("2006-01-02")).
				Str("reason", out.reason).Msg("stripe booking: no open fiscal period; left unbooked")
		}
	}
	return res, nil
}

// Outcomes of booking one row.
const (
	stripeOutcomeSkipped = iota
	stripeOutcomeCharge
	stripeOutcomeFee
	stripeOutcomeReview
	stripeOutcomeDeferred
	stripeOutcomeRefund
	stripeOutcomeDispute
)

type stripeBookOutcome struct {
	kind   int
	btID   string
	date   time.Time
	reason string // review reason, or deferral reason
}

func (r *StripeBookResult) record(out stripeBookOutcome, err error) {
	if err != nil {
		r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", out.btID, err))
		return
	}
	switch out.kind {
	case stripeOutcomeCharge:
		r.ChargesBooked++
	case stripeOutcomeFee:
		r.FeesBooked++
	case stripeOutcomeRefund:
		r.RefundsBooked++
	case stripeOutcomeDispute:
		r.DisputesBooked++
	case stripeOutcomeReview:
		r.NeedsReview[out.reason]++
	case stripeOutcomeDeferred:
		r.Deferred = append(r.Deferred, StripeBookDeferral{
			BalanceTransactionID: out.btID, Date: out.date.Format("2006-01-02"), Reason: out.reason,
		})
	}
}

// stripeTxnRow is a stripe_balance_transactions row as the booking reads it.
type stripeTxnRow struct {
	id, btID, category, stripeType, status, reviewReason string
	sourceID, chargeID, paymentIntentID                  string
	amountOre, feeOre                                    int64
	createdAt                                            time.Time
	orderID, originID, requestKey                        *string
	booked                                               bool
}

// bookable reports whether the row is still waiting to be booked: an
// unbooked charge, fee, refund or dispute, or one the pass held for review on
// a reason it retries.
func (r *stripeTxnRow) bookable() bool {
	if r.booked {
		return false
	}
	switch r.status {
	case StripeTxnUnbooked:
		return r.category == "charge" || r.category == "fee" || stripeRefundCategory(r.category) || stripeDisputeCategory(r.category)
	case StripeTxnNeedsReview:
		return (r.category == "charge" && slices.Contains(stripeChargeReviewReasons, r.reviewReason)) ||
			(stripeRefundCategory(r.category) && slices.Contains(stripeRefundRetryReasons, r.reviewReason)) ||
			(stripeDisputeCategory(r.category) && slices.Contains(stripeDisputeRetryReasons, r.reviewReason))
	}
	return false
}

// bookStripeTxn books one row in its own transaction. Rows found already
// booked, or no longer waiting, are skipped.
func (s *Service) bookStripeTxn(ctx context.Context, clubID, actorID, rowID string) (stripeBookOutcome, error) {
	out := stripeBookOutcome{btID: rowID}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var row stripeTxnRow
	err = tx.QueryRow(ctx,
		`SELECT id, balance_transaction_id, reporting_category, stripe_type, status, COALESCE(review_reason, ''),
		        source_id, charge_id, payment_intent_id,
		        amount_ore, fee_ore, created_at, order_id, origin_txn_id::text, refund_request_key::text,
		        journal_entry_id IS NOT NULL
		   FROM stripe_balance_transactions
		  WHERE id = $1 AND club_id = $2
		  FOR UPDATE`,
		rowID, clubID,
	).Scan(&row.id, &row.btID, &row.category, &row.stripeType, &row.status, &row.reviewReason,
		&row.sourceID, &row.chargeID, &row.paymentIntentID,
		&row.amountOre, &row.feeOre, &row.createdAt, &row.orderID, &row.originID, &row.requestKey, &row.booked)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("locking transaction: %w", err)
	}
	out.btID = row.btID
	if !row.bookable() {
		return out, nil
	}
	out.date = stripeBookingDate(row.createdAt, time.Local)

	// Never creates a fiscal year: a booking without a human decision must
	// not open one.
	periodID, periodStatus, err := existingPeriodIn(ctx, tx, clubID, out.date, "")
	if errors.Is(err, errNoFiscalPeriod) {
		out.kind, out.reason = stripeOutcomeDeferred, StripeDeferNoPeriod
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("resolving fiscal period: %w", err)
	}
	if periodStatus != "open" {
		out.kind, out.reason = stripeOutcomeDeferred, StripeDeferPeriodClosed
		return out, nil
	}

	booked, err := bookStripeRowTx(ctx, tx, clubID, actorID, &row, &out, periodID)
	switch {
	case errors.Is(err, ErrPeriodClosed):
		out.kind, out.reason = stripeOutcomeDeferred, StripeDeferPeriodClosed
		return out, nil
	case errors.Is(err, errChargeRaced):
		return stripeBookOutcome{btID: row.btID}, nil
	case err != nil:
		return out, err
	case out.kind == stripeOutcomeSkipped:
		return out, nil
	case out.kind == stripeOutcomeDeferred:
		// A deferral found inside the booking (a duplicate capture's own
		// period) wrote nothing worth keeping.
		return out, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit: %w", err)
	}
	s.afterStripeBooked(ctx, clubID, actorID, &row, out.date, booked)
	return out, nil
}

// stripeBooked is what booking a row leaves for after the commit.
type stripeBooked struct {
	// invoiceIDs are the fakturas a charge settled, to back-link.
	invoiceIDs []string
	refund     *refundEffects
	dispute    *disputeEffects
}

// bookStripeRowTx books the row by its category on tx, recording the
// outcome in out.
func bookStripeRowTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, out *stripeBookOutcome, periodID string) (b stripeBooked, err error) {
	switch row.category {
	case "charge":
		out.kind, out.reason, b.invoiceIDs, err = bookStripeChargeTx(ctx, tx, clubID, actorID, row, out.date, periodID)
	case "fee":
		out.kind, err = bookStripeFeeTx(ctx, tx, clubID, actorID, row, out.date, periodID)
	case "refund", "refund_failure":
		out.kind, out.reason, b.refund, err = bookStripeRefundRowTx(ctx, tx, clubID, actorID, row, out.date, periodID)
	case "dispute", "dispute_reversal":
		out.kind, out.reason, b.dispute, err = bookStripeDisputeTx(ctx, tx, clubID, actorID, row, out.date, periodID)
	}
	return b, err
}

// afterStripeBooked audits a committed booking and runs what must not hold
// the booking's locks: the refund's delivery nudge and the charge's faktura
// payment back-link.
func (s *Service) afterStripeBooked(ctx context.Context, clubID, actorID string, row *stripeTxnRow, date time.Time, b stripeBooked) {
	if b.refund != nil {
		b.refund.rowID, b.refund.btID, b.refund.category = row.id, row.btID, row.category
		s.afterRefundBooked(ctx, clubID, actorID, b.refund)
	}
	if b.dispute != nil {
		b.dispute.rowID, b.dispute.btID, b.dispute.category = row.id, row.btID, row.category
		s.afterDisputeBooked(ctx, clubID, actorID, b.dispute)
	}

	// The back-link updates the fakturas, so it runs once the commit has
	// released their locks. Best-effort, as on the Vipps paths: the GL is
	// already right.
	for _, id := range b.invoiceIDs {
		if err := s.linkInvoicePayment(ctx, clubID, id, date); err != nil {
			s.log.Warn().Err(err).Str("invoice_id", id).Str("balance_transaction_id", row.btID).
				Msg("stripe charge: invoice payment back-link failed")
		}
	}
}

// bookStripeChargeTx books a charge from its order, or holds it for review
// when the order's split may not be booked. It returns the fakturas to
// back-link after the commit.
func bookStripeChargeTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, date time.Time, periodID string) (kind int, reason string, invoiceIDs []string, err error) {
	if row.orderID == nil {
		return stripeOutcomeSkipped, "", nil, errors.New("charge has no order")
	}
	split, found, err := resolveOrderChargeSplitByOrderID(ctx, tx, clubID, *row.orderID, stripeSplitLabel)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if !found {
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("order %s not found", *row.orderID)
	}
	if err := lockSplitInvoicesTx(ctx, tx, clubID, split); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}

	amount := oreToKr(row.amountOre)
	credits, invoiceIDs, err := checkOrderChargeSplit(ctx, tx, clubID, split, amount, date)
	if reason := stripeSplitReviewReason(err); reason != "" {
		if err := holdStripeTxnForReviewTx(ctx, tx, clubID, row.id, reason); err != nil {
			return stripeOutcomeSkipped, "", nil, err
		}
		return stripeOutcomeReview, reason, nil, nil
	}
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}

	// A retried review row is ready again.
	if row.status == StripeTxnNeedsReview {
		if _, err := tx.Exec(ctx,
			`UPDATE stripe_balance_transactions SET status = 'unbooked', review_reason = NULL, updated_at = now()
			  WHERE id = $1 AND club_id = $2 AND journal_entry_id IS NULL AND status = 'needs_review'`,
			row.id, clubID); err != nil {
			return stripeOutcomeSkipped, "", nil, fmt.Errorf("releasing review: %w", err)
		}
	}

	clearing, feeAccount, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	short := shortOrderID(*row.orderID)
	p := &chargePosting{
		clubID:      clubID,
		createdBy:   actorID,
		source:      "stripe",
		sourceTable: "stripe_balance_transactions",
		sourceID:    row.id,
		clearing:    clearing,
		date:        date,
		amount:      amount,
		description: "Kortbetaling ordre " + short,
		credits:     credits,
		markSQL:     stripeMarkSQL,
		winnerSQL:   stripeWinnerSQL,
		savepoint:   "stripe_charge",
		periodID:    periodID,
	}
	if row.feeOre != 0 {
		fee := oreToKr(row.feeOre)
		feeDesc := "Stripe-gebyr ordre " + short
		p.extra = []CreateJournalLineInput{
			{AccountCode: feeAccount, Debit: fee, Description: feeDesc},
			{AccountCode: clearing, Credit: fee, Description: feeDesc},
		}
	}
	if _, err := postChargeTx(ctx, tx, p); errors.Is(err, errChargeRaced) {
		return stripeOutcomeSkipped, "", nil, nil
	} else if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	return stripeOutcomeCharge, "", invoiceIDs, nil
}

// stripeSplitReviewReason maps a checkOrderChargeSplit refusal to its
// review reason; "" for success or a database error.
func stripeSplitReviewReason(err error) string {
	var mismatch *orderSplitMismatchError
	switch {
	case errors.As(err, &mismatch):
		return StripeReviewSplitMismatch
	case errors.Is(err, errOrderReceivableNotOpen):
		return StripeReviewReceivableNotOpen
	case errors.Is(err, ErrImportedNoOpeningBalance):
		return StripeReviewImportedNoOpeningBalance
	}
	return ""
}

// bookStripeFeeTx books a fee transaction: a negative amount is a fee
// Stripe charged (DR fee / CR clearing), a positive one a fee it returned.
func bookStripeFeeTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, date time.Time, periodID string) (int, error) {
	if row.amountOre == 0 {
		return stripeOutcomeSkipped, errors.New("fee transaction has no amount")
	}
	clearing, feeAccount, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return stripeOutcomeSkipped, err
	}
	desc := "Stripe-gebyr"
	if row.stripeType != "" {
		desc += " " + row.stripeType
	}
	amount := oreToKr(row.amountOre)
	dr, cr := feeAccount, clearing
	if amount > 0 {
		dr, cr = clearing, feeAccount
	} else {
		amount = -amount
	}
	sourceID, sourceTable := row.id, "stripe_balance_transactions"
	entry, err := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      date.Format("2006-01-02"),
		Description:    desc,
		Source:         "stripe",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      actorID,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: dr, Debit: amount, Description: desc},
			{AccountCode: cr, Credit: amount, Description: desc},
		},
	})
	if err != nil {
		return stripeOutcomeSkipped, fmt.Errorf("creating journal entry: %w", err)
	}
	tag, err := tx.Exec(ctx, stripeMarkSQL, entry.ID, row.id, clubID)
	if err != nil {
		return stripeOutcomeSkipped, fmt.Errorf("marking fee booked: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// The row is locked, so this means it is no longer ready; the
		// rollback discards the entry.
		return stripeOutcomeSkipped, nil
	}
	if err := postJournalEntryTx(ctx, tx, clubID, entry.ID, actorID); err != nil {
		return stripeOutcomeSkipped, fmt.Errorf("posting journal entry: %w", err)
	}
	return stripeOutcomeFee, nil
}

// holdStripeTxnForReviewTx sends a charge to review with a booking reason.
func holdStripeTxnForReviewTx(ctx context.Context, tx pgx.Tx, clubID, rowID, reason string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE stripe_balance_transactions SET status = 'needs_review', review_reason = $3, updated_at = now()
		  WHERE id = $1 AND club_id = $2 AND journal_entry_id IS NULL AND status IN ('unbooked', 'needs_review')`,
		rowID, clubID, reason); err != nil {
		return fmt.Errorf("holding charge for review: %w", err)
	}
	return nil
}

// lockSplitInvoicesTx locks the fakturas a split settles, in id order, the
// way lockInvoiceForAssign locks one (FOR NO KEY UPDATE), so a bank or
// Vipps assign of one of them waits for this booking.
func lockSplitInvoicesTx(ctx context.Context, tx pgx.Tx, clubID string, split []VippsReconcileLine) error {
	var ids []string
	for _, l := range split {
		if l.LinkedInvoiceID != "" {
			ids = append(ids, l.LinkedInvoiceID)
		}
	}
	return lockInvoicesTx(ctx, tx, clubID, ids)
}

// lockInvoicesTx locks fakturas FOR NO KEY UPDATE in id order, as
// lockInvoiceForAssign locks one, so every path holding several takes them
// in the same order.
func lockInvoicesTx(ctx context.Context, tx pgx.Tx, clubID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	rows, err := tx.Query(ctx,
		`SELECT id FROM invoices WHERE club_id = $1 AND id = ANY($2::uuid[]) ORDER BY id FOR NO KEY UPDATE`,
		clubID, ids)
	if err != nil {
		return fmt.Errorf("locking fakturas: %w", err)
	}
	if _, err := pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return fmt.Errorf("locking fakturas: %w", err)
	}
	return nil
}

// stripeAccountsIn returns the club's Stripe clearing account and its
// payment fee account (shared by all providers), falling back to the
// defaults (1580 / 7700) when unset.
func stripeAccountsIn(ctx context.Context, q dbtx, clubID string) (clearing, fee string, err error) {
	err = q.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(stripe_clearing_account, ''), $2),
		        COALESCE(NULLIF(payment_fee_account, ''), $3)
		   FROM clubs WHERE id = $1`,
		clubID, stripeClearingAccountCode, vippsFeeAccountCode,
	).Scan(&clearing, &fee)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", errors.New("club not found")
	}
	if err != nil {
		return "", "", fmt.Errorf("loading stripe accounts: %w", err)
	}
	return clearing, fee, nil
}

// stripeClearingAccountCode is the default Stripe clearing account
// (clubs.stripe_clearing_account).
const stripeClearingAccountCode = "1580"

// stripeBookingDate is the calendar day in loc of a Stripe timestamp. The
// API runs with the host's zone (time.Local), which the NixOS host sets to
// the club's timezone, so a card payment made just after midnight is
// booked on the day the payer made it.
func stripeBookingDate(ts time.Time, loc *time.Location) time.Time {
	t := ts.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// oreToKr converts integer øre to the kroner amounts journal lines carry.
func oreToKr(ore int64) float64 {
	return float64(ore) / 100
}

// shortOrderID is the order id as shown in entry descriptions.
func shortOrderID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// StripeReviewItem is one Stripe balance transaction waiting for a person.
type StripeReviewItem struct {
	ID                   string `json:"id"`
	BalanceTransactionID string `json:"balance_transaction_id"`
	ReportingCategory    string `json:"reporting_category"`
	ReviewReason         string `json:"review_reason"`
	AmountOre            int64  `json:"amount_ore"`
	Currency             string `json:"currency"`
	// Date is the booking date the row would get (created, local time).
	Date string `json:"date"`
}

// ListStripeReview returns the club's Stripe balance transactions held for
// review, newest first.
func (s *Service) ListStripeReview(ctx context.Context, clubID string) ([]StripeReviewItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, balance_transaction_id, reporting_category, review_reason, amount_ore, currency, created_at
		   FROM stripe_balance_transactions
		  WHERE club_id = $1 AND status = 'needs_review'
		  ORDER BY created_at DESC, id`,
		clubID)
	if err != nil {
		return nil, fmt.Errorf("listing stripe review: %w", err)
	}
	defer rows.Close()
	items := []StripeReviewItem{}
	for rows.Next() {
		var it StripeReviewItem
		var created time.Time
		if err := rows.Scan(&it.ID, &it.BalanceTransactionID, &it.ReportingCategory, &it.ReviewReason,
			&it.AmountOre, &it.Currency, &created); err != nil {
			return nil, fmt.Errorf("scanning stripe review: %w", err)
		}
		it.Date = stripeBookingDate(created, time.Local).Format("2006-01-02")
		items = append(items, it)
	}
	return items, rows.Err()
}
