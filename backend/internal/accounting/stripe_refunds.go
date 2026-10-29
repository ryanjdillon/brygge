package accounting

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/stripe"
)

// Booking Stripe refunds and refund failures (ACC-2, ACC-5, ACC-8, ACC-9).
// The booking pass books a refund only once Stripe's balance shows it; the
// webhook never books. A refund books against its charge (origin_txn_id),
// and what it books depends on how that charge was booked:
//
//   - the charge settled an order or a faktura (booked from its order, or
//     assigned to a faktura by hand): a credit note per faktura (or one for
//     an order with no faktura) for its share of the refund, and
//     DR 1500 / CR stripe clearing; or, for a full refund the treasurer
//     asked to collect again, no credit note: the faktura is reopened and
//     detached from the refunded order, and the charge stops settling it
//     (recollected_at);
//   - the charge was booked to an account by hand: DR that account /
//     CR clearing, no document;
//   - the charge is held as a duplicate capture (no faktura open for it)
//     and this refund returns all of it: the pair is booked through the
//     club's suspense account, the charge DR clearing / CR suspense and the
//     refund DR suspense / CR clearing.
//
// Anything else holds the refund for review: retried while what it waits
// for may still come (refund_unlinked, refund_charge_unbooked), held for a
// person otherwise. A refund failure (the money came back) mirrors its
// refund's entry and, where the refund issued credit notes, issues a new
// debit faktura settled by the returned money; where it reopened a faktura
// that is still open, it settles it again.
//
// Locks, all on the pass's transaction and in this order: the refund (or
// failure) row, the row it books against, the charge, then the fakturas in
// id order. Nothing here touches the pool.

// stripeDuplicateReasons are the review reasons of a charge Brygge could
// not book against an open faktura: paid another way already, not what the
// attempt asked, or no attempt at all. Refunded in full, such a charge is a
// duplicate capture returned.
var stripeDuplicateReasons = []string{StripeReviewReceivableNotOpen, StripeReviewAmountMismatch, StripeReviewNoAttempt}

// stripeSuspenseAccountCode is the default suspense account for a
// duplicate capture (clubs.stripe_suspense_account): NS 4102 Annen
// kortsiktig gjeld, money the club holds for someone else.
const stripeSuspenseAccountCode = "2900"

// Outcomes of a refund booking, recorded in its audit row.
const (
	refundOutcomeCredit    = "credit"
	refundOutcomeRecollect = "recollect"
	refundOutcomeAccount   = "account"
	refundOutcomeDuplicate = "duplicate"
	refundOutcomeMirror    = "mirror"
)

// errRefundSplit holds a refund as refund_split: the work done for it so
// far is rolled back to the savepoint it ran under.
var errRefundSplit = errors.New("refund cannot be divided exactly")

// stripeRefundMarkSQL links a refund or refund failure to its entry; it
// matches nothing once the row is booked or dismissed. review_reason is
// cleared: the pass books it, not a person.
const stripeRefundMarkSQL = `UPDATE stripe_balance_transactions
    SET journal_entry_id = $1, status = 'booked', review_reason = NULL, updated_at = now()
  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL AND status IN ('unbooked', 'needs_review')`

// refundEffects is what a refund booking did, for its audit rows.
type refundEffects struct {
	rowID, btID, category, originID, outcome string
	amountOre                                int64
	documents                                []issuedDocument
	duplicateChargeID                        string
}

// stripeOrigin is a locked row a refund or refund failure books against.
type stripeOrigin struct {
	id, btID, category, status, reviewReason string
	amountOre, feeOre, refundedOre           int64
	createdAt                                time.Time
	orderID, attemptID, invoiceID, entryID   *string
	originID                                 *string
	recollected                              bool
}

func (o *stripeOrigin) booked() bool { return o.status == StripeTxnBooked && o.entryID != nil }

// lockStripeOriginTx locks a row FOR UPDATE and reads it in a second
// statement, so what it reads reflects any booking it waited for.
func lockStripeOriginTx(ctx context.Context, tx pgx.Tx, clubID, id string) (*stripeOrigin, error) {
	if err := tx.QueryRow(ctx,
		`SELECT id FROM stripe_balance_transactions WHERE id = $1 AND club_id = $2 FOR UPDATE`, id, clubID,
	).Scan(new(string)); err != nil {
		return nil, fmt.Errorf("locking stripe transaction: %w", err)
	}
	o := &stripeOrigin{id: id}
	if err := tx.QueryRow(ctx,
		`SELECT balance_transaction_id, reporting_category, status, COALESCE(review_reason, ''),
		        amount_ore, fee_ore, refunded_ore, created_at, order_id::text, attempt_id::text,
		        invoice_id::text, journal_entry_id::text, origin_txn_id::text, recollected_at IS NOT NULL
		   FROM stripe_balance_transactions WHERE id = $1 AND club_id = $2`, id, clubID,
	).Scan(&o.btID, &o.category, &o.status, &o.reviewReason, &o.amountOre, &o.feeOre, &o.refundedOre,
		&o.createdAt, &o.orderID, &o.attemptID, &o.invoiceID, &o.entryID, &o.originID, &o.recollected); err != nil {
		return nil, fmt.Errorf("reading stripe transaction: %w", err)
	}
	return o, nil
}

// findStripeOriginTx finds the row a refund (its charge, by charge id) or a
// refund failure (its refund, by the refund's id) books against.
func findStripeOriginTx(ctx context.Context, tx pgx.Tx, clubID string, row *stripeTxnRow) (string, bool, error) {
	if row.originID != nil {
		return *row.originID, true, nil
	}
	category, key := stripe.CategoryCharge, row.chargeID
	sql := `SELECT id FROM stripe_balance_transactions
	         WHERE club_id = $1 AND reporting_category = $2 AND charge_id = $3 ORDER BY created_at, id LIMIT 1`
	if row.category == stripe.CategoryRefundFailure {
		category, key = stripe.CategoryRefund, row.sourceID
		sql = `SELECT id FROM stripe_balance_transactions
		        WHERE club_id = $1 AND reporting_category = $2 AND source_id = $3 ORDER BY created_at, id LIMIT 1`
	}
	if key == "" {
		return "", false, nil
	}
	var id string
	err := tx.QueryRow(ctx, sql, clubID, category, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("finding what the refund books against: %w", err)
	}
	return id, true, nil
}

// bookStripeRefundRowTx books a refund or refund failure, or holds it for
// review. Whatever a case did is rolled back to a savepoint when it turns
// out the refund cannot be divided (errRefundSplit), and the row is held.
func bookStripeRefundRowTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, date time.Time, periodID string) (int, string, *refundEffects, error) {
	originID, found, err := findStripeOriginTx(ctx, tx, clubID, row)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if !found {
		return holdRefundTx(ctx, tx, clubID, row.id, StripeReviewRefundUnlinked)
	}
	origin, err := lockStripeOriginTx(ctx, tx, clubID, originID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE stripe_balance_transactions SET origin_txn_id = $3, updated_at = now()
		  WHERE id = $1 AND club_id = $2 AND origin_txn_id IS DISTINCT FROM $3`,
		row.id, clubID, origin.id); err != nil {
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("linking refund: %w", err)
	}

	sp, err := tx.Begin(ctx)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("savepoint: %w", err)
	}
	var kind int
	var reason string
	var eff *refundEffects
	if row.category == stripe.CategoryRefundFailure {
		kind, reason, eff, err = bookRefundFailureTx(ctx, sp, clubID, actorID, row, origin, date, periodID)
	} else {
		kind, reason, eff, err = bookRefundTx(ctx, sp, clubID, actorID, row, origin, date, periodID)
	}
	if errors.Is(err, errRefundSplit) {
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			return stripeOutcomeSkipped, "", nil, fmt.Errorf("rolling back refund: %w", rbErr)
		}
		return holdRefundTx(ctx, tx, clubID, row.id, StripeReviewRefundSplit)
	}
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if err := sp.Commit(ctx); err != nil {
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("releasing savepoint: %w", err)
	}
	if eff != nil {
		eff.originID = origin.id
	}
	return kind, reason, eff, nil
}

// afterRefundBooked audits a committed refund booking and nudges the
// delivery worker for the documents it queued. It runs after the commit,
// so it may use the pool.
func (s *Service) afterRefundBooked(ctx context.Context, clubID, actorID string, eff *refundEffects) {
	if len(eff.documents) > 0 && s.kickDeliveries != nil {
		s.kickDeliveries()
	}
	if s.audit == nil {
		return
	}
	numbers := make([]int, 0, len(eff.documents))
	for _, d := range eff.documents {
		numbers = append(numbers, d.Number)
	}
	s.audit.LogAction(ctx, clubID, actorID, "", audit.ActionStripeTxnRefundBooked, "stripe_balance_transaction", eff.rowID,
		map[string]any{
			"balance_transaction_id": eff.btID, "category": eff.category, "origin_txn_id": eff.originID,
			"outcome": eff.outcome, "amount_ore": eff.amountOre, "document_numbers": numbers,
		})
	if eff.duplicateChargeID != "" {
		s.audit.LogAction(ctx, clubID, actorID, "", audit.ActionStripeTxnDuplicateRefunded, "stripe_balance_transaction",
			eff.duplicateChargeID, map[string]any{"refund_txn_id": eff.rowID, "amount_ore": eff.amountOre})
	}
	if eff.category != stripe.CategoryRefund {
		return
	}
	for _, d := range eff.documents {
		s.audit.LogAction(ctx, clubID, actorID, "", audit.ActionInvoiceRefundCreditIssued, "invoice", d.ID,
			map[string]any{"credit_note_number": d.Number, "invoice_number": d.InvoiceNumber,
				"amount_ore": d.AmountOre, "refund_txn_id": eff.rowID})
	}
}

// holdRefundTx holds a refund or refund failure for review.
func holdRefundTx(ctx context.Context, tx pgx.Tx, clubID, rowID, reason string) (int, string, *refundEffects, error) {
	if err := holdStripeTxnForReviewTx(ctx, tx, clubID, rowID, reason); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	return stripeOutcomeReview, reason, nil, nil
}

// bookRefundTx books a refund against its locked charge.
func bookRefundTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, charge *stripeOrigin, date time.Time, periodID string) (int, string, *refundEffects, error) {
	refundOre := -row.amountOre
	if refundOre <= 0 {
		return stripeOutcomeSkipped, "", nil, errors.New("refund has no amount")
	}
	switch {
	case charge.category != stripe.CategoryCharge:
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("refund points at a %s row", charge.category)
	case charge.status == StripeTxnDismissed:
		return holdRefundTx(ctx, tx, clubID, row.id, StripeReviewRefundOriginDismissed)
	case charge.booked() && (charge.reviewReason == "" || charge.invoiceID != nil):
		return bookRefundOfSaleTx(ctx, tx, clubID, actorID, row, charge, date, periodID)
	case charge.booked():
		return bookRefundToAccountTx(ctx, tx, clubID, actorID, row, charge, date, periodID)
	case charge.status == StripeTxnNeedsReview && slices.Contains(stripeDuplicateReasons, charge.reviewReason) &&
		charge.refundedOre+refundOre == charge.amountOre:
		return bookDuplicateCaptureTx(ctx, tx, clubID, actorID, row, charge, date, periodID)
	}
	return holdRefundTx(ctx, tx, clubID, row.id, StripeReviewRefundChargeUnbooked)
}

// refundEntry is a refund or refund failure's own entry.
type refundEntry struct {
	desc  string
	lines []CreateJournalLineInput
}

// postStripeRowEntryTx creates the row's entry, marks the row booked with a
// guarded update and posts the entry, all on tx. A lost mark (the row is
// locked, so it is no longer waiting) is errChargeRaced.
func postStripeRowEntryTx(ctx context.Context, tx pgx.Tx, clubID, actorID, rowID string, date time.Time, periodID string, e refundEntry) (string, error) {
	sourceID, sourceTable := rowID, "stripe_balance_transactions"
	entry, err := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      date.Format("2006-01-02"),
		Description:    e.desc,
		Source:         "stripe",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      actorID,
		ClubID:         clubID,
		Lines:          e.lines,
	})
	if err != nil {
		return "", fmt.Errorf("creating journal entry: %w", err)
	}
	tag, err := tx.Exec(ctx, stripeRefundMarkSQL, entry.ID, rowID, clubID)
	if err != nil {
		return "", fmt.Errorf("marking refund booked: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", errChargeRaced
	}
	if err := postJournalEntryTx(ctx, tx, clubID, entry.ID, actorID); err != nil {
		return "", fmt.Errorf("posting journal entry: %w", err)
	}
	return entry.ID, nil
}

// moneyOutEntry is DR account / CR clearing amountOre, with the row's fee.
func moneyOutEntry(desc, account, clearing, feeAccount string, amountOre, feeOre int64) refundEntry {
	amount := oreToKr(amountOre)
	return refundEntry{desc: desc, lines: append([]CreateJournalLineInput{
		{AccountCode: account, Debit: amount, Description: desc},
		{AccountCode: clearing, Credit: amount, Description: desc},
	}, stripeFeeLines(feeAccount, clearing, feeOre, "Stripe-gebyr "+desc)...)}
}

// moneyInEntry is DR clearing / CR account amountOre, with the row's fee.
func moneyInEntry(desc, account, clearing, feeAccount string, amountOre, feeOre int64) refundEntry {
	amount := oreToKr(amountOre)
	return refundEntry{desc: desc, lines: append([]CreateJournalLineInput{
		{AccountCode: clearing, Debit: amount, Description: desc},
		{AccountCode: account, Credit: amount, Description: desc},
	}, stripeFeeLines(feeAccount, clearing, feeOre, "Stripe-gebyr "+desc)...)}
}

// addRefundedTx moves a charge's refunded_ore by deltaOre.
func addRefundedTx(ctx context.Context, tx pgx.Tx, clubID, chargeID string, deltaOre int64) error {
	if _, err := tx.Exec(ctx,
		`UPDATE stripe_balance_transactions SET refunded_ore = refunded_ore + $3, updated_at = now()
		  WHERE id = $1 AND club_id = $2`, chargeID, clubID, deltaOre); err != nil {
		return fmt.Errorf("recording refunded amount: %w", err)
	}
	return nil
}

// bookRefundToAccountTx books a refund of a charge a person booked to an
// account: the money goes back out of that same account. No document: the
// charge was no sale through Brygge.
func bookRefundToAccountTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, charge *stripeOrigin, date time.Time, periodID string) (int, string, *refundEffects, error) {
	clearing, feeAccount, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	account, err := entryCreditAccountTx(ctx, tx, *charge.entryID, clearing)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	refundOre := -row.amountOre
	desc := "Kortrefusjon " + row.btID
	if _, err := postStripeRowEntryTx(ctx, tx, clubID, actorID, row.id, date, periodID,
		moneyOutEntry(desc, account, clearing, feeAccount, refundOre, row.feeOre)); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if err := addRefundedTx(ctx, tx, clubID, charge.id, refundOre); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	return stripeOutcomeRefund, "", &refundEffects{outcome: refundOutcomeAccount, amountOre: refundOre}, nil
}

// entryCreditAccountTx is the account a charge's entry credited, other
// than the clearing account (which a fee line credits): where the person
// booked the money.
func entryCreditAccountTx(ctx context.Context, tx pgx.Tx, entryID, clearing string) (string, error) {
	var code string
	err := tx.QueryRow(ctx,
		`SELECT a.code FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1 AND jl.credit > 0 AND a.code <> $2
		  ORDER BY jl.credit DESC, a.code LIMIT 1`, entryID, clearing,
	).Scan(&code)
	if err != nil {
		return "", fmt.Errorf("reading the charge's account: %w", err)
	}
	return code, nil
}

// entryDebitAccountTx is the account a refund's entry debited, other than
// the clearing and fee accounts: where its money was taken from.
func entryDebitAccountTx(ctx context.Context, tx pgx.Tx, entryID, clearing, fee string) (string, error) {
	var code string
	err := tx.QueryRow(ctx,
		`SELECT a.code FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1 AND jl.debit > 0 AND a.code NOT IN ($2, $3)
		  ORDER BY jl.debit DESC, a.code LIMIT 1`, entryID, clearing, fee,
	).Scan(&code)
	if err != nil {
		return "", fmt.Errorf("reading the refund's account: %w", err)
	}
	return code, nil
}

// stripeSuspenseAccountIn is the club's suspense account for duplicate
// captures (stripeSuspenseAccountCode when unset).
func stripeSuspenseAccountIn(ctx context.Context, q dbtx, clubID string) (string, error) {
	var code string
	if err := q.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(stripe_suspense_account, ''), $2) FROM clubs WHERE id = $1`,
		clubID, stripeSuspenseAccountCode,
	).Scan(&code); err != nil {
		return "", fmt.Errorf("loading stripe suspense account: %w", err)
	}
	return code, nil
}

// bookDuplicateCaptureTx books a charge held as a duplicate capture and
// the refund that returns all of it, through the suspense account, with no
// order or faktura effect. The charge books on its own date; its period
// must be open too, or both wait. The charge keeps its review reason, so
// nothing reads it as settling its order's fakturas (invoiceBookedRowSQL).
func bookDuplicateCaptureTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, charge *stripeOrigin, date time.Time, periodID string) (int, string, *refundEffects, error) {
	chargeDate := stripeBookingDate(charge.createdAt, time.Local)
	chargePeriod, status, err := existingPeriodIn(ctx, tx, clubID, chargeDate, "")
	if errors.Is(err, errNoFiscalPeriod) {
		return stripeOutcomeDeferred, StripeDeferNoPeriod, nil, nil
	}
	if err != nil {
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("resolving fiscal period: %w", err)
	}
	if status != "open" {
		return stripeOutcomeDeferred, StripeDeferPeriodClosed, nil, nil
	}
	clearing, feeAccount, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	suspense, err := stripeSuspenseAccountIn(ctx, tx, clubID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}

	chargeEntry := moneyInEntry("Dobbel kortbetaling "+charge.btID, suspense, clearing, feeAccount, charge.amountOre, charge.feeOre)
	sourceID, sourceTable := charge.id, "stripe_balance_transactions"
	entry, err := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
		FiscalPeriodID: chargePeriod, EntryDate: chargeDate.Format("2006-01-02"), Description: chargeEntry.desc,
		Source: "stripe", SourceID: &sourceID, SourceTable: &sourceTable, CreatedBy: actorID, ClubID: clubID,
		Lines: chargeEntry.lines,
	})
	if err != nil {
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("creating journal entry: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE stripe_balance_transactions SET journal_entry_id = $1, status = 'booked', updated_at = now()
		  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL AND status = 'needs_review'`,
		entry.ID, charge.id, clubID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("marking charge booked: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return stripeOutcomeSkipped, "", nil, errors.New("duplicate charge changed under its lock")
	}
	if err := postJournalEntryTx(ctx, tx, clubID, entry.ID, actorID); err != nil {
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("posting journal entry: %w", err)
	}

	refundOre := -row.amountOre
	if _, err := postStripeRowEntryTx(ctx, tx, clubID, actorID, row.id, date, periodID,
		moneyOutEntry("Refusjon dobbel kortbetaling "+charge.btID, suspense, clearing, feeAccount, refundOre, row.feeOre)); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if err := addRefundedTx(ctx, tx, clubID, charge.id, refundOre); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	return stripeOutcomeRefund, "", &refundEffects{outcome: refundOutcomeDuplicate, amountOre: refundOre, duplicateChargeID: charge.id}, nil
}

// paymentRefundMatch is the payment_refunds row a refund booking found or
// made, and the outcome it carries ("" for credit).
type paymentRefundMatch struct {
	id      *string
	outcome string
}

// matchPaymentRefundTx finds the refund's payment_refunds row by Stripe's
// refund id, else by the request key Brygge sent with it. A refund made in
// the Stripe Dashboard before its webhook arrived has none: one is made
// from the row (no person, reason "Stripe Dashboard"), when the charge
// names its attempt. A row matched by key learns the Stripe refund id. The
// webhook may insert the same Dashboard refund at the same moment: the
// insert then yields to the other row (a unique-index conflict must not
// abort the booking), which is read again.
func matchPaymentRefundTx(ctx context.Context, tx pgx.Tx, clubID string, row *stripeTxnRow, charge *stripeOrigin) (paymentRefundMatch, error) {
	m, found, err := findPaymentRefundTx(ctx, tx, clubID, row)
	if err != nil || found || charge.attemptID == nil {
		return m, err
	}
	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, currency, provider_refund_id,
		                              status, reason, charge_ref, request_key, provider_payment_ref)
		 VALUES ($1, $2, $3, $4::bigint / 100.0, 'NOK', NULLIF($5, ''), 'succeeded', 'Stripe Dashboard',
		         NULLIF($6, ''), $7::uuid, NULLIF($8, ''))
		 ON CONFLICT DO NOTHING
		 RETURNING id::text`,
		clubID, *charge.attemptID, stripe.ProviderName, -row.amountOre, row.sourceID, row.chargeID,
		row.requestKey, row.paymentIntentID,
	).Scan(&id)
	switch {
	case err == nil:
		m.id = &id
		return m, nil
	case errors.Is(err, pgx.ErrNoRows):
		// Another transaction made the row first.
		m, found, err = findPaymentRefundTx(ctx, tx, clubID, row)
		if err != nil {
			return m, err
		}
		if !found {
			return m, errors.New("the dashboard refund's row conflicted but cannot be found")
		}
		return m, nil
	}
	return m, fmt.Errorf("recording the dashboard refund: %w", err)
}

// findPaymentRefundTx reads the refund's request row by Stripe's refund id,
// else by the request key, and records the Stripe refund id on a row matched
// by key.
func findPaymentRefundTx(ctx context.Context, tx pgx.Tx, clubID string, row *stripeTxnRow) (m paymentRefundMatch, found bool, err error) {
	var id, outcome string
	err = tx.QueryRow(ctx,
		`SELECT id, outcome FROM (
		     SELECT 1 AS pref, id::text AS id, COALESCE(outcome, '') AS outcome FROM payment_refunds
		      WHERE club_id = $1 AND provider = $2 AND $3 <> '' AND provider_refund_id = $3
		     UNION ALL
		     SELECT 2, id::text, COALESCE(outcome, '') FROM payment_refunds
		      WHERE club_id = $1 AND request_key = $4::uuid) m
		  ORDER BY pref LIMIT 1`,
		clubID, stripe.ProviderName, row.sourceID, row.requestKey,
	).Scan(&id, &outcome)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return m, false, nil
	case err != nil:
		return m, false, fmt.Errorf("finding the refund request: %w", err)
	}
	m.id, m.outcome = &id, outcome
	if row.sourceID != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE payment_refunds SET provider_refund_id = $3, updated_at = now()
			  WHERE id = $1 AND club_id = $2 AND provider_refund_id IS NULL`, id, clubID, row.sourceID); err != nil {
			return m, true, fmt.Errorf("recording the stripe refund id: %w", err)
		}
	}
	return m, true, nil
}

// loadRefundTargetsTx reads and locks what a charge settled (before any
// detach): each faktura stripeSettledInvoiceSQL names, in id order, and,
// for a charge booked from its order, the order's catalogue lines as one
// order target.
func loadRefundTargetsTx(ctx context.Context, tx pgx.Tx, clubID string, charge *stripeOrigin) ([]*refundTarget, error) {
	rows, err := tx.Query(ctx,
		`SELECT i.id::text FROM stripe_balance_transactions t
		   JOIN invoices i ON `+stripeSettledInvoiceSQL+`
		  WHERE t.id = $1 AND t.club_id = $2
		  ORDER BY i.id`, charge.id, clubID)
	if err != nil {
		return nil, fmt.Errorf("listing settled fakturas: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("listing settled fakturas: %w", err)
	}
	if err := lockInvoicesTx(ctx, tx, clubID, ids); err != nil {
		return nil, err
	}
	targets := make([]*refundTarget, 0, len(ids)+1)
	for _, id := range ids {
		t := &refundTarget{invoiceID: &id, orderID: charge.orderID}
		if err := tx.QueryRow(ctx,
			`SELECT i.invoice_number, round(i.total_amount * 100)::bigint, i.status,
			        EXISTS (SELECT 1 FROM invoices cn WHERE cn.credits_invoice_id = i.id AND cn.status <> 'voided'),
			        (SELECT COALESCE(SUM(rd.amount_ore), 0) FROM refund_documents rd
			          WHERE rd.invoice_id = i.id AND rd.kind = 'credit')
			   FROM invoices i WHERE i.id = $1 AND i.club_id = $2`, id, clubID,
		).Scan(&t.number, &t.shareOre, &t.status, &t.manuallyCredited, &t.creditedOre); err != nil {
			return nil, fmt.Errorf("reading faktura: %w", err)
		}
		if t.recipient, err = loadInvoiceRecipientTx(ctx, tx, clubID, id); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	if charge.reviewReason != "" || charge.orderID == nil {
		return targets, nil
	}
	ot, err := loadOrderTargetTx(ctx, tx, clubID, *charge.orderID)
	if err != nil || ot == nil {
		return targets, err
	}
	return append(targets, ot), nil
}

// loadOrderTargetTx is the order's catalogue lines as one target, or nil
// when the order has none.
func loadOrderTargetTx(ctx context.Context, tx pgx.Tx, clubID, orderID string) (*refundTarget, error) {
	t := &refundTarget{orderID: &orderID, status: "open"}
	var lines int
	if err := tx.QueryRow(ctx,
		`SELECT count(*), COALESCE(round(SUM(total_price) * 100), 0)::bigint,
		        (SELECT COALESCE(SUM(rd.amount_ore), 0) FROM refund_documents rd
		          WHERE rd.order_id = $1 AND rd.invoice_id IS NULL AND rd.kind = 'credit')
		   FROM order_lines WHERE order_id = $1`, orderID,
	).Scan(&lines, &t.shareOre, &t.creditedOre); err != nil {
		return nil, fmt.Errorf("reading order lines: %w", err)
	}
	if lines == 0 {
		return nil, nil
	}
	var err error
	if t.recipient, err = loadOrderRecipientTx(ctx, tx, clubID, orderID); err != nil {
		return nil, err
	}
	return t, nil
}

// bookRefundOfSaleTx books a refund of a charge that settled an order or
// a faktura: credit notes, or for a full refund the treasurer asked to
// collect again, the fakturas reopened.
func bookRefundOfSaleTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, charge *stripeOrigin, date time.Time, periodID string) (int, string, *refundEffects, error) {
	refundOre := -row.amountOre
	refunded := charge.refundedOre + refundOre
	if refunded > charge.amountOre {
		return stripeOutcomeSkipped, "", nil, errRefundSplit
	}
	full := refunded == charge.amountOre
	targets, err := loadRefundTargetsTx(ctx, tx, clubID, charge)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if !refundTargetsSplittable(targets, charge.amountOre) {
		return stripeOutcomeSkipped, "", nil, errRefundSplit
	}
	pr, err := matchPaymentRefundTx(ctx, tx, clubID, row, charge)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	eff := &refundEffects{outcome: refundOutcomeCredit, amountOre: refundOre}
	clearing, feeAccount, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	entry := moneyOutEntry("Kortrefusjon "+targetsLabel(targets), receivablesAccountCode, clearing, feeAccount,
		refundOre, row.feeOre)
	if pr.outcome == refundOutcomeRecollect {
		if !full {
			return stripeOutcomeSkipped, "", nil, errRefundSplit
		}
		eff.outcome = refundOutcomeRecollect
		err = recollectRefundTx(ctx, tx, clubID, row.id, pr.id, charge, targets)
	} else {
		eff.documents, err = creditRefundTx(ctx, tx, clubID, actorID, row.id, pr.id, targets, refundOre, full, date, periodID)
	}
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if _, err := postStripeRowEntryTx(ctx, tx, clubID, actorID, row.id, date, periodID, entry); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	// Only a charge the pass booked from its order refunds that order; one
	// assigned by hand settled a faktura or an account, not the order.
	if full && charge.orderID != nil && charge.reviewReason == "" {
		if err := markOrderRefundedTx(ctx, tx, clubID, *charge.orderID); err != nil {
			return stripeOutcomeSkipped, "", nil, err
		}
	}
	if err := addRefundedTx(ctx, tx, clubID, charge.id, refundOre); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	return stripeOutcomeRefund, "", eff, nil
}

// targetsLabel names a refund's targets in its entry description.
func targetsLabel(targets []*refundTarget) string {
	if len(targets) == 1 {
		return targets[0].label()
	}
	if targets[0].orderID != nil {
		return "ordre " + shortOrderID(*targets[0].orderID)
	}
	return targets[0].label()
}

// refundTargetsSplittable reports whether a refund can be divided over the
// targets as the charge paid them: there is at least one, they add up to
// the charge, and none was voided or credited by hand since.
func refundTargetsSplittable(targets []*refundTarget, chargeOre int64) bool {
	if len(targets) == 0 {
		return false
	}
	var sum int64
	for _, t := range targets {
		if t.status == "voided" || t.manuallyCredited {
			return false
		}
		sum += t.shareOre
	}
	return sum == chargeOre
}

// insertRefundDocumentTx records what a refund (or refund failure) did to
// one target.
func insertRefundDocumentTx(ctx context.Context, tx pgx.Tx, clubID, refundRowID string, paymentRefundID *string,
	t *refundTarget, creditInvoiceID *string, amountOre int64, kind string, detachedOrderID *string) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO refund_documents (club_id, refund_txn_id, payment_refund_id, invoice_id, order_id,
		                               credit_invoice_id, amount_ore, kind, detached_order_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		clubID, refundRowID, paymentRefundID, t.invoiceID, t.orderID, creditInvoiceID, amountOre, kind, detachedOrderID,
	); err != nil {
		return fmt.Errorf("recording refund document: %w", err)
	}
	return nil
}

// creditRefundTx issues a credit note per target for its part of the
// refund. The fakturas keep their payment: the refund paid the member
// back, and the credit note takes the sale out of revenue.
func creditRefundTx(ctx context.Context, tx pgx.Tx, clubID, actorID, refundRowID string, paymentRefundID *string,
	targets []*refundTarget, refundOre int64, full bool, date time.Time, periodID string) ([]issuedDocument, error) {
	alloc, ok := allocateRefund(targets, refundOre, full)
	if !ok {
		return nil, errRefundSplit
	}
	docs := make([]issuedDocument, 0, len(targets))
	for i, t := range targets {
		if alloc[i] == 0 {
			continue
		}
		note, err := issueRefundCreditNoteTx(ctx, tx, clubID, actorID, t, alloc[i], date, periodID)
		if errors.Is(err, errRefundNotScalable) {
			return nil, errRefundSplit
		}
		if err != nil {
			return nil, err
		}
		note.InvoiceNumber = t.number
		if err := insertRefundDocumentTx(ctx, tx, clubID, refundRowID, paymentRefundID, t, &note.ID, alloc[i],
			refundOutcomeCredit, nil); err != nil {
			return nil, err
		}
		docs = append(docs, *note)
	}
	return docs, nil
}

// recollectRefundTx reopens the fakturas of a fully refunded charge the
// treasurer chose to collect again: no credit note (only allowed before any
// partial refund was credited), the payment is unlinked, the online marker
// cleared and the faktura detached from the refunded order, so it is an
// ordinary open faktura again (reminders, a new pay link, bank KID
// matching). The charge stops settling it (recollected_at).
func recollectRefundTx(ctx context.Context, tx pgx.Tx, clubID, refundRowID string, paymentRefundID *string,
	charge *stripeOrigin, targets []*refundTarget) error {
	for _, t := range targets {
		if t.invoiceID == nil || t.creditedOre > 0 {
			return errRefundSplit
		}
	}
	for _, t := range targets {
		id := *t.invoiceID
		if _, err := reopenInvoicePaymentTx(ctx, tx, clubID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE invoices SET online_paid_at = NULL, online_paid_provider = NULL
			  WHERE id = $1 AND club_id = $2`, id, clubID); err != nil {
			return fmt.Errorf("clearing the online payment: %w", err)
		}
		// Only the link to the refunded order goes: a faktura assigned by
		// hand to a charge may sit on another order (a new pay link's), which
		// this refund has nothing to do with.
		var detached *string
		if charge.reviewReason == "" && charge.orderID != nil {
			err := tx.QueryRow(ctx,
				`DELETE FROM order_sources WHERE invoice_id = $1 AND order_id = $2 RETURNING order_id::text`,
				id, *charge.orderID).Scan(&detached)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("detaching the faktura: %w", err)
			}
		}
		if err := insertRefundDocumentTx(ctx, tx, clubID, refundRowID, paymentRefundID, t, nil, t.shareOre,
			refundOutcomeRecollect, detached); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE stripe_balance_transactions SET recollected_at = now(), updated_at = now()
		  WHERE id = $1 AND club_id = $2`, charge.id, clubID); err != nil {
		return fmt.Errorf("recording the recollect: %w", err)
	}
	return nil
}

// markOrderRefundedTx moves an order refunded in full to refunded, from
// whatever state it is in: Stripe's balance shows the charge was taken, so
// a capture webhook that has not arrived yet (pending) changes nothing,
// and once refunded a late capture leaves it alone (payOrder). A booking
// the order paid for would be cancelled here; no checkout creates one yet
// (BRY-499), so there is nothing to cancel.
func markOrderRefundedTx(ctx context.Context, tx pgx.Tx, clubID, orderID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET status = 'refunded', updated_at = now()
		  WHERE id = $1 AND club_id = $2 AND status <> 'refunded'`, orderID, clubID); err != nil {
		return fmt.Errorf("marking order refunded: %w", err)
	}
	return nil
}

// refundDocument is a refund_documents row.
type refundDocument struct {
	id, kind                                           string
	invoiceID, orderID, creditInvoiceID, detachedOrder *string
	amountOre                                          int64
}

// loadRefundDocumentsTx reads what a refund booking did, in id order.
func loadRefundDocumentsTx(ctx context.Context, tx pgx.Tx, clubID, refundRowID string) ([]refundDocument, error) {
	rows, err := tx.Query(ctx,
		`SELECT id::text, kind, invoice_id::text, order_id::text, credit_invoice_id::text, detached_order_id::text, amount_ore
		   FROM refund_documents WHERE club_id = $1 AND refund_txn_id = $2
		  ORDER BY invoice_id NULLS LAST, id`, clubID, refundRowID)
	if err != nil {
		return nil, fmt.Errorf("loading refund documents: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (refundDocument, error) {
		var d refundDocument
		err := r.Scan(&d.id, &d.kind, &d.invoiceID, &d.orderID, &d.creditInvoiceID, &d.detachedOrder, &d.amountOre)
		return d, err
	})
}

// documentInvoiceIDs are the fakturas the documents touched.
func documentInvoiceIDs(docs []refundDocument) []string {
	var ids []string
	for _, d := range docs {
		if d.invoiceID != nil {
			ids = append(ids, *d.invoiceID)
		}
	}
	return ids
}

// recollectStillOpenTx reports whether a faktura a refund reopened is still
// open, with nothing paid or booked against it since, so the refund's money
// coming back may settle it again.
func recollectStillOpenTx(ctx context.Context, tx pgx.Tx, clubID, invoiceID string) (bool, error) {
	var open bool
	if err := tx.QueryRow(ctx,
		`SELECT `+finance.OpenReceivableSQL("i")+` AND i.online_paid_at IS NULL AND NOT `+invoiceAlreadyBookedSQL("i")+`
		   FROM invoices i WHERE i.id = $1 AND i.club_id = $2`, invoiceID, clubID,
	).Scan(&open); err != nil {
		return false, fmt.Errorf("checking the faktura: %w", err)
	}
	return open, nil
}

// restoreRecollectTx undoes a recollect on a faktura that is still open:
// the payment is linked again and the online marker restored (the charge's
// time), the unpaid replacement order a new pay link made is failed and its
// link replaced by the original one to the refunded order, which is paid
// again, and the charge settles the faktura again.
func restoreRecollectTx(ctx context.Context, tx pgx.Tx, clubID string, d refundDocument, charge *stripeOrigin) error {
	id := derefOr(d.invoiceID, "")
	if err := linkInvoicePaymentIn(ctx, tx, clubID, id, stripeBookingDate(charge.createdAt, time.Local)); err != nil {
		return fmt.Errorf("linking the payment again: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE invoices SET online_paid_at = $3, online_paid_provider = $4
		  WHERE id = $1 AND club_id = $2 AND online_paid_at IS NULL`,
		id, clubID, charge.createdAt, stripe.ProviderName); err != nil {
		return fmt.Errorf("restoring the online payment: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders o SET status = 'failed', updated_at = now()
		   FROM order_sources os
		  WHERE os.invoice_id = $1 AND o.id = os.order_id AND o.club_id = $2 AND o.status = 'pending'`,
		id, clubID); err != nil {
		return fmt.Errorf("cancelling the replacement order: %w", err)
	}
	if d.detachedOrder != nil {
		if _, err := tx.Exec(ctx,
			`DELETE FROM order_sources WHERE invoice_id = $1 AND order_id <> $2`, id, *d.detachedOrder); err != nil {
			return fmt.Errorf("removing the replacement link: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_sources (order_id, invoice_id)
			 SELECT $2, $1 WHERE NOT EXISTS (SELECT 1 FROM order_sources WHERE invoice_id = $1)`,
			id, *d.detachedOrder); err != nil {
			return fmt.Errorf("restoring the order link: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET status = 'paid', updated_at = now()
			  WHERE id = $1 AND club_id = $2 AND status = 'refunded'`, *d.detachedOrder, clubID); err != nil {
			return fmt.Errorf("restoring the order: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE stripe_balance_transactions SET recollected_at = NULL, updated_at = now()
		  WHERE id = $1 AND club_id = $2`, charge.id, clubID); err != nil {
		return fmt.Errorf("restoring the charge: %w", err)
	}
	return nil
}

// bookRefundFailureTx books a refund failure against its locked refund:
// the refund's entry mirrored (DR clearing / CR the account the refund
// debited), and, by what the refund did: a debit faktura per credit note,
// settled by the returned money (ACC-9); a reopened faktura that is still
// open settled again; nothing more for a duplicate pair or an account.
func bookRefundFailureTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, row *stripeTxnRow, refund *stripeOrigin, date time.Time, periodID string) (int, string, *refundEffects, error) {
	returnedOre := row.amountOre
	switch {
	case refund.category != stripe.CategoryRefund:
		return stripeOutcomeSkipped, "", nil, fmt.Errorf("refund failure points at a %s row", refund.category)
	case refund.status == StripeTxnDismissed:
		return holdRefundTx(ctx, tx, clubID, row.id, StripeReviewRefundOriginDismissed)
	case !refund.booked():
		return holdRefundTx(ctx, tx, clubID, row.id, StripeReviewRefundChargeUnbooked)
	case returnedOre <= 0 || returnedOre != -refund.amountOre:
		return stripeOutcomeSkipped, "", nil, errRefundSplit
	}
	var charge *stripeOrigin
	if refund.originID != nil {
		var err error
		if charge, err = lockStripeOriginTx(ctx, tx, clubID, *refund.originID); err != nil {
			return stripeOutcomeSkipped, "", nil, err
		}
	}
	docs, err := loadRefundDocumentsTx(ctx, tx, clubID, refund.id)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if err := lockInvoicesTx(ctx, tx, clubID, documentInvoiceIDs(docs)); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	eff := &refundEffects{outcome: refundOutcomeMirror, amountOre: returnedOre}
	if held, err := applyRefundFailureDocsTx(ctx, tx, clubID, actorID, row.id, docs, charge, date, periodID, eff); err != nil || held {
		if held {
			return holdRefundTx(ctx, tx, clubID, row.id, StripeReviewRefundFailureSettled)
		}
		return stripeOutcomeSkipped, "", nil, err
	}

	clearing, feeAccount, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	account, err := entryDebitAccountTx(ctx, tx, *refund.entryID, clearing, feeAccount)
	if err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	if _, err := postStripeRowEntryTx(ctx, tx, clubID, actorID, row.id, date, periodID,
		moneyInEntry("Mislykket kortrefusjon "+refund.btID, account, clearing, feeAccount, returnedOre, row.feeOre)); err != nil {
		return stripeOutcomeSkipped, "", nil, err
	}
	// Only a refund the pass booked counted in refunded_ore; one booked by
	// hand went to an account a person chose.
	if charge != nil && refund.reviewReason == "" {
		if err := addRefundedTx(ctx, tx, clubID, charge.id, -returnedOre); err != nil {
			return stripeOutcomeSkipped, "", nil, err
		}
	}
	return stripeOutcomeRefund, "", eff, nil
}

// applyRefundFailureDocsTx does what a refund failure does to the
// documents of its refund. held is true when a faktura the refund reopened
// was settled another way since: the failure then waits for a person and
// nothing is written.
func applyRefundFailureDocsTx(ctx context.Context, tx pgx.Tx, clubID, actorID, failureRowID string, docs []refundDocument,
	charge *stripeOrigin, date time.Time, periodID string, eff *refundEffects) (held bool, err error) {
	for _, d := range docs {
		if d.kind != refundOutcomeRecollect {
			continue
		}
		open, err := recollectStillOpenTx(ctx, tx, clubID, derefOr(d.invoiceID, ""))
		if err != nil || !open || charge == nil {
			return err == nil, err
		}
	}
	credited := false
	for _, d := range docs {
		switch d.kind {
		case refundOutcomeRecollect:
			eff.outcome = refundOutcomeRecollect
			if err := restoreRecollectTx(ctx, tx, clubID, d, charge); err != nil {
				return false, err
			}
		case refundOutcomeCredit:
			credited = true
			eff.outcome = refundOutcomeCredit
			doc, err := refailCreditNoteTx(ctx, tx, clubID, actorID, failureRowID, d, date, periodID)
			if err != nil {
				return false, err
			}
			eff.documents = append(eff.documents, *doc)
		}
	}
	if credited && charge != nil && charge.orderID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET status = 'paid', updated_at = now()
			  WHERE id = $1 AND club_id = $2 AND status = 'refunded'`, *charge.orderID, clubID); err != nil {
			return false, fmt.Errorf("restoring the order: %w", err)
		}
	}
	return false, nil
}

// refailCreditNoteTx issues the debit faktura for one credit note of a
// failed refund and settles it with the returned money: the failure's
// entry credits 1500 for it, and its payment is linked in the same
// transaction, so it is never open for a bank or card payment to take.
func refailCreditNoteTx(ctx context.Context, tx pgx.Tx, clubID, actorID, failureRowID string, d refundDocument,
	date time.Time, periodID string) (*issuedDocument, error) {
	if d.creditInvoiceID == nil {
		return nil, errors.New("credit document without its credit note")
	}
	doc, err := issueRefailInvoiceTx(ctx, tx, clubID, actorID, *d.creditInvoiceID, date, periodID)
	if err != nil {
		return nil, err
	}
	if err := linkInvoicePaymentIn(ctx, tx, clubID, doc.ID, date); err != nil {
		return nil, fmt.Errorf("settling the new faktura: %w", err)
	}
	t := &refundTarget{invoiceID: d.invoiceID, orderID: d.orderID}
	if err := insertRefundDocumentTx(ctx, tx, clubID, failureRowID, nil, t, &doc.ID, doc.AmountOre,
		"refail_invoice", nil); err != nil {
		return nil, err
	}
	return doc, nil
}
