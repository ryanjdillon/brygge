package accounting

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// A person settles a Stripe balance transaction the booking pass leaves in
// review: book it against one faktura, book it to an account, or set it
// aside (dismiss), and take a dismissal back. Which of these a row allows
// follows from its review reason (stripeAllowedActions); the list endpoint
// hands the same answer to the UI, and every action re-checks it under the
// row's lock.
//
// Each action runs in the club's Stripe slot, so no booking pass or sync of
// the club runs meanwhile, and in one transaction that locks the row first
// (then the faktura, as the booking pass does). Everything the transaction
// does goes through it; the faktura's payment back-link runs after the
// commit.
//
// A row a person booked keeps its review_reason: the booking pass clears
// it on everything it books from an order (except a duplicate capture it
// books with its refund, which keeps the reason it was held under), so a
// booked charge with a review_reason settles only the faktura in its
// invoice_id, never its order's (invoiceBookedRowSQL).

// Manual actions on a Stripe balance transaction (StripeTxnItem.AllowedActions).
const (
	StripeActionAssignInvoice = "assign_invoice"
	StripeActionAssignAccount = "assign_account"
	StripeActionDismiss       = "dismiss"
	StripeActionUndismiss     = "undismiss"
	// StripeActionUnassign undoes a booking (UnassignStripeTxn).
	StripeActionUnassign = "unassign"
	// StripeActionRebook hands a row a person holds back to the booking
	// pass (RebookStripeTxn).
	StripeActionRebook = "rebook"
)

// Codes of a refused manual action (StripeTxnRefusal.Code).
const (
	// StripeTxnNotAllowed: the action is not one the row's category, status
	// and review reason allow.
	StripeTxnNotAllowed = "not_allowed"
	// StripeTxnAmountMismatch: the charge is not the faktura total to the
	// øre.
	StripeTxnAmountMismatch = "amount_mismatch"
	// StripeTxnInvoiceSettled: the faktura cannot take the payment (voided,
	// paid, a booked payment recorded against it, or imported without its
	// opening balance by the booking date).
	StripeTxnInvoiceSettled = "invoice_settled"
	// StripeTxnInvoiceCredited: the faktura is credited in full (a live
	// credit note links to it, or its refunds' credit notes add up to its
	// total), so a payment assigned to it would credit 1500 twice (BRY-508).
	StripeTxnInvoiceCredited = "invoice_credited"
	// StripeTxnInvoiceNotDebt: the target is a credit note, which no payment
	// can clear.
	StripeTxnInvoiceNotDebt = "invoice_not_debt"
	// StripeTxnPeriodClosed: the booking date's fiscal period is closed or
	// locked.
	StripeTxnPeriodClosed = "period_closed"
	// StripeTxnAlreadyBooked: the row has its journal entry.
	StripeTxnAlreadyBooked = "already_booked"
	// StripeTxnPayoutCleared: the row's payout is cleared in the books, so
	// the row may not leave the dismissed state.
	StripeTxnPayoutCleared = "payout_cleared"
	// StripeTxnInvalidReason: not a Stripe dismissal reason.
	StripeTxnInvalidReason = "invalid_reason"
	// StripeTxnInvalidAccount: the account does not exist, is inactive, or
	// is one a Stripe row may not be booked against by hand.
	StripeTxnInvalidAccount = "invalid_account"
	// StripeTxnUseBankUnassign: the row is Stripe moving a payout, booked by
	// the payout's clearing, which is undone from the bank deposit.
	StripeTxnUseBankUnassign = "use_bank_unassign"
	// StripeTxnRefundBooked: a refund of the charge is booked, so the
	// charge's booking is what the refund booked against.
	StripeTxnRefundBooked = "refund_booked"
	// StripeTxnCreditNoteIssued: the refund issued credit notes, which are
	// never voided (ACC-9); the correction is a new faktura.
	StripeTxnCreditNoteIssued = "credit_note_issued"
	// StripeTxnFakturaSettledSince: a faktura the refund reopened has been
	// paid, credited or booked another way since.
	StripeTxnFakturaSettledSince = "faktura_settled_since"
)

// StripeTxnRefusal is a manual action refused, nothing changed. Reason is
// Norwegian and shown as is.
type StripeTxnRefusal struct {
	Code   string
	Reason string
}

func (e *StripeTxnRefusal) Error() string {
	if e.Reason == "" {
		return "stripe transaction action refused: " + e.Code
	}
	return "stripe transaction action refused: " + e.Code + ": " + e.Reason
}

func stripeRefusal(code, reason string) error {
	return &StripeTxnRefusal{Code: code, Reason: reason}
}

// ErrStripeTxnNotFound is a row id that is not one of the club's Stripe
// balance transactions.
var ErrStripeTxnNotFound = errors.New("stripe transaction not found")

// ErrStripeInvoiceNotFound is a faktura id that is not one of the club's.
var ErrStripeInvoiceNotFound = errors.New("invoice not found")

// Why a person set a Stripe row aside (dismissed_reason). Bank-only reasons
// such as internal_transfer do not apply to a balance transaction.
var stripeDismissReasons = map[string]bool{
	"booked_manually":  true,
	"test_transaction": true,
	"unidentifiable":   true,
	"refund_or_credit": true,
	"duplicate":        true,
}

// IsValidStripeDismissReason reports whether r is a Stripe dismissal reason.
func IsValidStripeDismissReason(r string) bool {
	return stripeDismissReasons[r]
}

// stripeDismissNoteSep joins the reason code and the note in
// dismissed_reason, which has no separate note column.
const stripeDismissNoteSep = ": "

// splitStripeDismissal is the reason code and the note of a stored
// dismissed_reason.
func splitStripeDismissal(stored string) (reason, note string) {
	reason, note, _ = strings.Cut(stored, stripeDismissNoteSep)
	return reason, note
}

// stripeTxnState is what decides a row's manual actions. ownEntry is set
// when the row's journal entry is sourced to the row itself (a charge, fee
// or manual booking), unlike a payout row, which carries its clearing's.
type stripeTxnState struct {
	category, status, reviewReason, currency  string
	booked, payoutCleared, hasOrder, ownEntry bool
}

// stripeAllowedActions lists the manual actions the row allows. Only rows in
// review are assigned or dismissed: an unbooked row waits for the booking
// pass, and fees and payouts book themselves. A booked row is unassigned
// when its entry is its own; a payout row's is the payout clearing's, undone
// from the bank deposit. A row a person unassigned is held for that person
// (stripeHeldActions). A row in another currency can only be dismissed; a
// refund or dispute follows its review reason (stripeRefundReasonActions).
// A charge whose order's faktura is no longer open, or whose imported
// receivable is not in the GL, never goes to a faktura. A row whose
// payout is cleared in the books was counted as done by that clearing, so
// it is never booked (that would put its net on clearing a second time) nor
// taken out of the dismissed state.
func stripeAllowedActions(st stripeTxnState) []string {
	actions := stripeReasonActions(st)
	if st.payoutCleared {
		return slices.DeleteFunc(actions, func(a string) bool { return a != StripeActionDismiss })
	}
	return actions
}

// stripeReasonActions is stripeAllowedActions before the payout rule.
func stripeReasonActions(st stripeTxnState) []string {
	switch {
	case st.booked:
		if st.status == StripeTxnBooked && st.ownEntry && !stripePayoutCategory(st.category) {
			return []string{StripeActionUnassign}
		}
		return []string{}
	case st.status == StripeTxnDismissed:
		return []string{StripeActionUndismiss}
	case st.status != StripeTxnNeedsReview:
		return []string{}
	case st.currency != "nok", st.reviewReason == StripeReviewCurrency:
		return []string{StripeActionDismiss}
	case st.reviewReason == StripeReviewUnassigned:
		return stripeHeldActions(st)
	case st.category == "other_adjustment" && st.reviewReason == StripeReviewUnsupportedCategory:
		return []string{StripeActionAssignAccount, StripeActionDismiss}
	case st.category != "charge":
		return stripeRefundReasonActions(st)
	}
	switch st.reviewReason {
	case StripeReviewNoAttempt, StripeReviewAmountMismatch, StripeReviewSplitMismatch:
		return []string{StripeActionAssignInvoice, StripeActionAssignAccount, StripeActionDismiss}
	case StripeReviewReceivableNotOpen, StripeReviewImportedNoOpeningBalance:
		return []string{StripeActionAssignAccount, StripeActionDismiss}
	}
	return []string{}
}

// stripeRefundReasonActions are the actions of a refund, refund failure or
// dispute the booking pass could not book. Those it retries (the charge or
// refund it books against may still arrive or be booked, the dispute account
// may be added) are only dismissed; those it leaves for a person can go to
// an account, or back to the pass once rebooking them makes sense (a row
// stored before refunds and disputes were booked).
func stripeRefundReasonActions(st stripeTxnState) []string {
	if stripeDisputeCategory(st.category) {
		switch st.reviewReason {
		case StripeReviewRefundLegacy:
			return []string{StripeActionRebook, StripeActionDismiss}
		case StripeReviewDisputeAccountMissing:
			return []string{StripeActionDismiss}
		case StripeReviewDisputeDirection:
			return []string{StripeActionAssignAccount, StripeActionDismiss}
		}
		return []string{}
	}
	if !stripeRefundCategory(st.category) {
		return []string{}
	}
	switch st.reviewReason {
	case StripeReviewRefundUnlinked, StripeReviewRefundOriginDismissed, StripeReviewRefundSplit,
		StripeReviewRefundFailureSettled, StripeReviewRefundFailureReissued:
		return []string{StripeActionAssignAccount, StripeActionDismiss}
	case StripeReviewRefundLegacy:
		return []string{StripeActionRebook, StripeActionDismiss}
	case StripeReviewRefundChargeUnbooked:
		return []string{StripeActionDismiss}
	}
	return []string{}
}

// stripeHeldActions are the actions of a row a person unassigned: booked
// again from its order or as a fee, refund or dispute by the booking pass (rebook),
// or by hand as any row in review of its kind is. Only charges go to a
// faktura.
func stripeHeldActions(st stripeTxnState) []string {
	switch st.category {
	case "charge":
		if st.hasOrder {
			return []string{StripeActionRebook, StripeActionAssignInvoice, StripeActionAssignAccount, StripeActionDismiss}
		}
		return []string{StripeActionAssignInvoice, StripeActionAssignAccount, StripeActionDismiss}
	case "fee", "refund", "refund_failure", "dispute", "dispute_reversal":
		return []string{StripeActionRebook, StripeActionAssignAccount, StripeActionDismiss}
	case "other_adjustment":
		return []string{StripeActionAssignAccount, StripeActionDismiss}
	}
	return []string{StripeActionDismiss}
}

// stripePayoutCategory reports whether a reporting category is Stripe
// moving a payout itself, which only the payout's clearing books.
func stripePayoutCategory(category string) bool {
	return category == "payout" || category == "payout_reversal"
}

// stripeManualRow is a stripe_balance_transactions row as the manual
// actions read it, with its journal entry's id and status (empty when
// unbooked).
type stripeManualRow struct {
	stripeTxnState
	id, btID                       string
	amountOre, feeOre, refundedOre int64
	createdAt                      time.Time
	entryID, entryStatus           string
	originID                       *string
}

// lockStripeTxnTx locks the row for the rest of tx and reads it with its
// payout's state. The read is a second statement: one that waited for the
// lock would otherwise still see the payout as it was when it started.
func lockStripeTxnTx(ctx context.Context, tx pgx.Tx, clubID, rowID string) (*stripeManualRow, error) {
	if err := tx.QueryRow(ctx,
		`SELECT id FROM stripe_balance_transactions WHERE id = $1 AND club_id = $2 FOR UPDATE`,
		rowID, clubID,
	).Scan(new(string)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStripeTxnNotFound
		}
		return nil, fmt.Errorf("locking stripe transaction: %w", err)
	}
	r := &stripeManualRow{id: rowID}
	if err := tx.QueryRow(ctx,
		`SELECT t.balance_transaction_id, t.reporting_category, t.status, COALESCE(t.review_reason, ''), t.currency,
		        t.amount_ore, t.fee_ore, t.created_at, t.journal_entry_id IS NOT NULL,
		        COALESCE(p.journal_entry_id IS NOT NULL, false), t.order_id IS NOT NULL,
		        `+stripeOwnEntrySQL+`, COALESCE(t.journal_entry_id::text, ''), COALESCE(e.status::text, ''),
		        t.refunded_ore, t.origin_txn_id::text
		   FROM stripe_balance_transactions t
		   LEFT JOIN stripe_payouts p ON p.id = t.payout_id
		   LEFT JOIN journal_entries e ON e.id = t.journal_entry_id AND e.club_id = t.club_id
		  WHERE t.id = $1 AND t.club_id = $2`,
		rowID, clubID,
	).Scan(&r.btID, &r.category, &r.status, &r.reviewReason, &r.currency,
		&r.amountOre, &r.feeOre, &r.createdAt, &r.booked, &r.payoutCleared, &r.hasOrder,
		&r.ownEntry, &r.entryID, &r.entryStatus, &r.refundedOre, &r.originID); err != nil {
		return nil, fmt.Errorf("reading stripe transaction: %w", err)
	}
	return r, nil
}

// stripeOwnEntrySQL is true when the journal entry e of row t is sourced to
// t: the booking pass's and the manual actions' entries are; a payout row
// carries its clearing's, sourced to the bank deposit.
const stripeOwnEntrySQL = `COALESCE(e.source_table = 'stripe_balance_transactions' AND e.source_id::text = t.id::text, false)`

// requireStripeAction refuses an action the row does not allow.
func requireStripeAction(r *stripeManualRow, action string) error {
	if r.booked {
		return stripeRefusal(StripeTxnAlreadyBooked, "transaksjonen er alt bokført – angre bokføringen først")
	}
	if r.payoutCleared && action != StripeActionDismiss {
		return stripeRefusal(StripeTxnPayoutCleared, stripePayoutClearedText)
	}
	if slices.Contains(stripeAllowedActions(r.stripeTxnState), action) {
		return nil
	}
	if r.status == StripeTxnDismissed {
		return stripeRefusal(StripeTxnNotAllowed, "transaksjonen er avvist – angre avvisningen først")
	}
	reason := r.reviewReason
	if reason == "" {
		reason = r.status
	}
	return stripeRefusal(StripeTxnNotAllowed, fmt.Sprintf("ikke tillatt for %s (%s)", r.category, reason))
}

// stripePayoutClearedText is the payout_cleared refusal's reason.
const stripePayoutClearedText = "utbetalingen transaksjonen var med i er alt avstemt – den kan ikke bokføres eller tas tilbake fra avvist"

// stripeManualMarkSQL links a row a person booked to its entry and, for a
// faktura assign, records the faktura ($4, NULL otherwise) in the same
// guarded statement; it matches nothing once the row is booked or
// dismissed. review_reason stays (see the top of this file).
const stripeManualMarkSQL = `UPDATE stripe_balance_transactions
    SET journal_entry_id = $1, status = 'booked', invoice_id = $4::uuid, updated_at = now()
  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL AND status IN ('unbooked', 'needs_review')`

// StripeTxnBookResult is what a manual booking of a Stripe row posted.
type StripeTxnBookResult struct {
	ID                   string `json:"id"`
	BalanceTransactionID string `json:"balance_transaction_id"`
	JournalEntryID       string `json:"journal_entry_id"`
	InvoiceID            string `json:"invoice_id,omitempty"`
	AccountCode          string `json:"account_code,omitempty"`
	// Date is the booking date of the entry.
	Date string `json:"date"`
}

// stripeManualTx runs fn on a transaction in the club's Stripe slot, which
// it waits for at most manualClearSlotWait (then ErrStripeSyncBusy). fn
// commits; the deferred rollback discards anything it did not.
func (s *Service) stripeManualTx(ctx context.Context, clubID, actorID string, fn func(pgx.Tx) error) error {
	if actorID == "" {
		return errors.New("a manual stripe action needs an acting user")
	}
	release, err := s.acquireStripeSlotBriefly(ctx, clubID)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return fn(tx)
}

// stripeManualPeriodTx resolves the open period of the booking date. Unlike
// the booking pass it may create the fiscal year: a person decided to book
// on that date, as with a manual bank assign.
func stripeManualPeriodTx(ctx context.Context, tx pgx.Tx, clubID string, date time.Time) (string, error) {
	periodID, status, err := resolvePeriodIn(ctx, tx, clubID, date, "")
	if err != nil {
		return "", fmt.Errorf("resolving fiscal period: %w", err)
	}
	if status != "open" {
		return "", stripeRefusal(StripeTxnPeriodClosed,
			fmt.Sprintf("regnskapsperioden for %s er stengt", date.Format("02.01.2006")))
	}
	return periodID, nil
}

// stripeFeeLines books the fee Stripe kept on a row (DR fee / CR clearing),
// or returned (a negative fee: DR clearing / CR fee); none for no fee.
func stripeFeeLines(feeAccount, clearing string, feeOre int64, desc string) []CreateJournalLineInput {
	if feeOre == 0 {
		return nil
	}
	fee := oreToKr(feeOre)
	dr, cr := feeAccount, clearing
	if fee < 0 {
		fee, dr, cr = -fee, clearing, feeAccount
	}
	return []CreateJournalLineInput{
		{AccountCode: dr, Debit: fee, Description: desc},
		{AccountCode: cr, Credit: fee, Description: desc},
	}
}

// AssignStripeTxnToInvoice books a Stripe charge in review against one
// faktura, as actorID: DR the Stripe clearing account / CR 1500 the charge,
// and DR the payment fee account / CR clearing the fee Stripe kept, on the
// charge's booking date. The faktura is locked as every settle path locks
// it (lockInvoiceForAssign, lockManual) and recorded on the row
// (invoice_id); its payment back-link runs after the commit, still in the
// club's Stripe slot, so an unassign of the row cannot run between the two
// and leave the faktura marked paid by a voided entry. The charge must
// equal the faktura total to the øre. A refusal is a *StripeTxnRefusal; an
// unknown row or faktura is ErrStripeTxnNotFound or
// ErrStripeInvoiceNotFound; a Stripe slot still busy after a short wait is
// ErrStripeSyncBusy.
func (s *Service) AssignStripeTxnToInvoice(ctx context.Context, clubID, actorID, txnID, invoiceID string) (*StripeTxnBookResult, error) {
	var res *StripeTxnBookResult
	err := s.stripeManualTx(ctx, clubID, actorID, func(tx pgx.Tx) error {
		var err error
		var date time.Time
		res, date, err = assignStripeTxnToInvoiceTx(ctx, tx, clubID, actorID, txnID, invoiceID)
		if err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		// Best-effort, as on the bank and Vipps paths: the GL is already
		// right. The commit released every lock, so the pool is safe here.
		if err := s.linkInvoicePayment(ctx, clubID, invoiceID, date); err != nil {
			s.log.Warn().Err(err).Str("invoice_id", invoiceID).Str("balance_transaction_id", res.BalanceTransactionID).
				Msg("stripe manual assign: invoice payment back-link failed")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func assignStripeTxnToInvoiceTx(ctx context.Context, tx pgx.Tx, clubID, actorID, txnID, invoiceID string) (*StripeTxnBookResult, time.Time, error) {
	row, err := lockStripeTxnTx(ctx, tx, clubID, txnID)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err := requireStripeAction(row, StripeActionAssignInvoice); err != nil {
		return nil, time.Time{}, err
	}
	date := stripeBookingDate(row.createdAt, time.Local)

	var invoiceNumber int
	if err := tx.QueryRow(ctx,
		`SELECT invoice_number FROM invoices WHERE id = $1 AND club_id = $2`, invoiceID, clubID,
	).Scan(&invoiceNumber); errors.Is(err, pgx.ErrNoRows) {
		return nil, date, ErrStripeInvoiceNotFound
	} else if err != nil {
		return nil, date, fmt.Errorf("loading invoice: %w", err)
	}
	invCents, err := lockInvoiceForAssign(ctx, tx, clubID, invoiceID, invoiceLockOpts{mode: lockManual, bookingDate: date})
	if err != nil {
		return nil, date, stripeInvoiceLockRefusal(err)
	}
	// A card charge takes exactly what was asked, so any difference means
	// another faktura (or an account); the back-link never marks a faktura
	// paid by a different amount, and nothing is left on 1500.
	if invCents != row.amountOre {
		return nil, date, stripeRefusal(StripeTxnAmountMismatch,
			fmt.Sprintf("kortbetalingen %.2f kr er ikke fakturabeløpet %.2f kr – bokfør mot konto i stedet",
				oreToKr(row.amountOre), oreToKr(invCents)))
	}
	periodID, err := stripeManualPeriodTx(ctx, tx, clubID, date)
	if err != nil {
		return nil, date, err
	}
	clearing, feeAccount, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return nil, date, err
	}

	desc := fmt.Sprintf("Kortbetaling: faktura %d", invoiceNumber)
	amount := oreToKr(row.amountOre)
	p := &chargePosting{
		clubID:      clubID,
		createdBy:   actorID,
		source:      "stripe",
		sourceTable: "stripe_balance_transactions",
		sourceID:    row.id,
		clearing:    clearing,
		date:        date,
		amount:      amount,
		description: desc,
		credits:     []CreateJournalLineInput{{AccountCode: receivablesAccountCode, Credit: amount, Description: desc}},
		extra:       stripeFeeLines(feeAccount, clearing, row.feeOre, fmt.Sprintf("Stripe-gebyr faktura %d", invoiceNumber)),
		markSQL:     stripeManualMarkSQL,
		markArgs:    []any{invoiceID},
		winnerSQL:   stripeWinnerSQL,
		savepoint:   "stripe_manual_invoice",
		periodID:    periodID,
	}
	entryID, err := postChargeTx(ctx, tx, p)
	if err != nil {
		return nil, date, stripePostRefusal(err)
	}
	return &StripeTxnBookResult{
		ID: row.id, BalanceTransactionID: row.btID, JournalEntryID: entryID,
		InvoiceID: invoiceID, Date: date.Format("2006-01-02"),
	}, date, nil
}

// stripeInvoiceLockRefusal turns lockInvoiceForAssign's refusals into
// invoice_settled (invoice_credited for a credited one, invoice_not_debt for
// a credit note); anything else is a database error and passes through.
func stripeInvoiceLockRefusal(err error) error {
	switch {
	case errors.Is(err, ErrInvoiceCredited):
		return stripeRefusal(StripeTxnInvoiceCredited, ErrInvoiceCredited.Error())
	case errors.Is(err, ErrInvoiceNotDebt):
		return stripeRefusal(StripeTxnInvoiceNotDebt, ErrInvoiceNotDebt.Error())
	case errors.Is(err, ErrInvoiceVoided):
		return stripeRefusal(StripeTxnInvoiceSettled, "fakturaen er annullert")
	case errors.Is(err, errInvoicePaid), errors.Is(err, errInvoiceHasBookedRow):
		return stripeRefusal(StripeTxnInvoiceSettled, "fakturaen er alt betalt eller har en bokført innbetaling")
	case errors.Is(err, ErrImportedNoOpeningBalance):
		return stripeRefusal(StripeTxnInvoiceSettled,
			"fakturaen er importert, og kundefordringen er ikke i inngående balanse på betalingsdatoen")
	}
	return err
}

// stripePostRefusal turns a failed post into its refusal: a period closed
// or locked meanwhile, or the row booked by someone else. The row is
// locked, so the second cannot happen short of a bug; it is still refused
// rather than reported as a server error.
func stripePostRefusal(err error) error {
	switch {
	case errors.Is(err, ErrPeriodClosed):
		return stripeRefusal(StripeTxnPeriodClosed, "regnskapsperioden er stengt")
	case errors.Is(err, errChargeRaced):
		return stripeRefusal(StripeTxnAlreadyBooked, "transaksjonen ble bokført samtidig")
	}
	return err
}

// AssignStripeTxnToAccount books a Stripe row in review to an account, as
// actorID, on its booking date: a positive amount DR the Stripe clearing
// account / CR the account, a negative one DR the account / CR clearing,
// with the fee Stripe kept (DR payment fee / CR clearing; reversed for a
// negative fee). What stays on clearing is the row's net, as when the
// booking pass books it. The account must be active and not one a Stripe
// row may not go to (stripeExcludedAccountsIn). note, when set, is the entry
// description's tail. Refusals and errors are AssignStripeTxnToInvoice's.
func (s *Service) AssignStripeTxnToAccount(ctx context.Context, clubID, actorID, txnID, accountCode, note string) (*StripeTxnBookResult, error) {
	var res *StripeTxnBookResult
	err := s.stripeManualTx(ctx, clubID, actorID, func(tx pgx.Tx) error {
		var err error
		res, err = assignStripeTxnToAccountTx(ctx, tx, clubID, actorID, txnID, strings.TrimSpace(accountCode), strings.TrimSpace(note))
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

func assignStripeTxnToAccountTx(ctx context.Context, tx pgx.Tx, clubID, actorID, txnID, accountCode, note string) (*StripeTxnBookResult, error) {
	row, err := lockStripeTxnTx(ctx, tx, clubID, txnID)
	if err != nil {
		return nil, err
	}
	if err := requireStripeAction(row, StripeActionAssignAccount); err != nil {
		return nil, err
	}
	if row.amountOre == 0 {
		return nil, stripeRefusal(StripeTxnNotAllowed, "transaksjonen har ikke noe beløp")
	}
	clearing, feeAccount, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return nil, err
	}
	if err := checkStripeTargetAccountTx(ctx, tx, clubID, clearing, accountCode); err != nil {
		return nil, err
	}
	date := stripeBookingDate(row.createdAt, time.Local)
	periodID, err := stripeManualPeriodTx(ctx, tx, clubID, date)
	if err != nil {
		return nil, err
	}

	desc := "Kortbetaling"
	if row.category != "charge" {
		desc = "Stripe-justering"
	}
	if note != "" {
		desc += " — " + truncate(note, 200)
	} else {
		desc += " " + row.btID
	}
	amount := oreToKr(row.amountOre)
	dr, cr := clearing, accountCode
	if amount < 0 {
		amount, dr, cr = -amount, accountCode, clearing
	}
	lines := append([]CreateJournalLineInput{
		{AccountCode: dr, Debit: amount, Description: desc},
		{AccountCode: cr, Credit: amount, Description: desc},
	}, stripeFeeLines(feeAccount, clearing, row.feeOre, "Stripe-gebyr "+row.btID)...)

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
		Lines:          lines,
	})
	if err != nil {
		return nil, stripePostRefusal(fmt.Errorf("creating journal entry: %w", err))
	}
	tag, err := tx.Exec(ctx, stripeManualMarkSQL, entry.ID, row.id, clubID, nil)
	if err != nil {
		return nil, fmt.Errorf("marking stripe transaction booked: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, stripePostRefusal(errChargeRaced)
	}
	if err := postJournalEntryTx(ctx, tx, clubID, entry.ID, actorID); err != nil {
		return nil, stripePostRefusal(fmt.Errorf("posting journal entry: %w", err))
	}
	return &StripeTxnBookResult{
		ID: row.id, BalanceTransactionID: row.btID, JournalEntryID: entry.ID,
		AccountCode: accountCode, Date: date.Format("2006-01-02"),
	}, nil
}

// stripeExcludedAccountsIn is the accounts a Stripe row is never booked
// against by hand: 1500 (a faktura goes through the faktura assign, which
// records and locks it), the bank accounts (19xx, the club's bank accounts
// and any account a bank import books to: card money reaches the bank only
// through a payout), the
// payment provider clearing accounts (the Stripe one would book the money
// against itself; the Vipps one, 2900 by default, is another provider's)
// and 2900 itself.
// stripeClearing is the club's Stripe clearing account (stripeAccountsIn).
func stripeExcludedAccountsIn(ctx context.Context, q dbtx, clubID, stripeClearing string) (map[string]bool, error) {
	vippsClearing, _, err := vippsAccountsIn(ctx, q, clubID)
	if err != nil {
		return nil, err
	}
	excluded := map[string]bool{
		receivablesAccountCode: true, vippsClearingAccountCode: true,
		stripeClearing: true, vippsClearing: true,
	}
	rows, err := q.Query(ctx,
		`SELECT bank_account_code FROM bank_imports WHERE club_id = $1 AND bank_account_code <> ''
		 UNION
		 SELECT gl_code FROM club_bank_accounts WHERE club_id = $1 AND gl_code <> ''`, clubID)
	if err != nil {
		return nil, fmt.Errorf("listing bank accounts: %w", err)
	}
	codes, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("listing bank accounts: %w", err)
	}
	for _, c := range codes {
		excluded[c] = true
	}
	return excluded, nil
}

// stripeAccountExcluded applies stripeExcludedAccountsIn and the 19xx rule.
func stripeAccountExcluded(excluded map[string]bool, code string) bool {
	return excluded[code] || strings.HasPrefix(code, "19")
}

// checkStripeTargetAccountTx refuses an account a Stripe row may not be
// booked against by hand.
func checkStripeTargetAccountTx(ctx context.Context, tx pgx.Tx, clubID, stripeClearing, code string) error {
	if code == "" {
		return stripeRefusal(StripeTxnInvalidAccount, "velg en konto")
	}
	var active bool
	err := tx.QueryRow(ctx,
		`SELECT is_active FROM accounts WHERE club_id = $1 AND code = $2`, clubID, code,
	).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return stripeRefusal(StripeTxnInvalidAccount, fmt.Sprintf("konto %s finnes ikke eller er inaktiv", code))
	}
	if err != nil {
		return fmt.Errorf("loading account: %w", err)
	}
	excluded, err := stripeExcludedAccountsIn(ctx, tx, clubID, stripeClearing)
	if err != nil {
		return err
	}
	if stripeAccountExcluded(excluded, code) {
		return stripeRefusal(StripeTxnInvalidAccount, fmt.Sprintf("konto %s kan ikke brukes for en Stripe-transaksjon", code))
	}
	return nil
}

// StripeAssignableAccount is an account a Stripe row may be booked against.
type StripeAssignableAccount struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	AccountType string `json:"account_type"`
}

// ListStripeAssignableAccounts lists the club's active accounts a Stripe
// row may be booked against by hand, by code.
func (s *Service) ListStripeAssignableAccounts(ctx context.Context, clubID string) ([]StripeAssignableAccount, error) {
	stripeClearing, _, err := stripeAccountsIn(ctx, s.db, clubID)
	if err != nil {
		return nil, err
	}
	excluded, err := stripeExcludedAccountsIn(ctx, s.db, clubID, stripeClearing)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT code, name, account_type::text FROM accounts WHERE club_id = $1 AND is_active ORDER BY code`, clubID)
	if err != nil {
		return nil, fmt.Errorf("listing accounts: %w", err)
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (StripeAssignableAccount, error) {
		var a StripeAssignableAccount
		err := r.Scan(&a.Code, &a.Name, &a.AccountType)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("listing accounts: %w", err)
	}
	out := make([]StripeAssignableAccount, 0, len(all))
	for _, a := range all {
		if !stripeAccountExcluded(excluded, a.Code) {
			out = append(out, a)
		}
	}
	return out, nil
}

// StripeTxnDismissResult is a row's state after a dismissal or its undoing.
type StripeTxnDismissResult struct {
	ID                   string `json:"id"`
	BalanceTransactionID string `json:"balance_transaction_id"`
	Status               string `json:"status"`
	ReviewReason         string `json:"review_reason,omitempty"`
	// Unchanged is set when the row already was in that state.
	Unchanged bool `json:"unchanged,omitempty"`
}

// DismissStripeTxn sets a Stripe row in review aside, as actorID, with a
// Stripe dismissal reason and an optional note (kept in dismissed_reason
// as "reason: note"). Nothing is posted: the row's net stays out of the
// Stripe clearing account while its payout's clearing still takes it out,
// so the account is off by that net until it is booked another way. The
// review reason is kept for the undoing. Dismissing a dismissed row changes
// nothing. A refusal is a *StripeTxnRefusal (invalid_reason,
// already_booked, not_allowed).
func (s *Service) DismissStripeTxn(ctx context.Context, clubID, actorID, txnID, reason, note string) (*StripeTxnDismissResult, error) {
	if !IsValidStripeDismissReason(reason) {
		return nil, stripeRefusal(StripeTxnInvalidReason, "ugyldig årsak")
	}
	stored := reason
	if note = strings.TrimSpace(note); note != "" {
		stored += stripeDismissNoteSep + truncate(note, 500)
	}
	var res *StripeTxnDismissResult
	err := s.stripeManualTx(ctx, clubID, actorID, func(tx pgx.Tx) error {
		row, err := lockStripeTxnTx(ctx, tx, clubID, txnID)
		if err != nil {
			return err
		}
		res = &StripeTxnDismissResult{ID: row.id, BalanceTransactionID: row.btID, Status: StripeTxnDismissed, ReviewReason: row.reviewReason}
		if row.status == StripeTxnDismissed && !row.booked {
			res.Unchanged = true
			return nil
		}
		if err := requireStripeAction(row, StripeActionDismiss); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE stripe_balance_transactions
			    SET status = 'dismissed', dismissed_at = now(), dismissed_by = $3, dismissed_reason = $4, updated_at = now()
			  WHERE id = $1 AND club_id = $2 AND journal_entry_id IS NULL AND status IN ('unbooked', 'needs_review')`,
			row.id, clubID, actorID, stored); err != nil {
			return fmt.Errorf("dismissing stripe transaction: %w", err)
		}
		return tx.Commit(ctx)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// UndismissStripeTxn takes a dismissal back, as actorID: the row returns to
// review with the reason it had, or to unbooked (for the next booking pass)
// when it had none. Refused with payout_cleared once the row's payout is
// cleared in the books, since that clearing counted the row as done. A row
// that is not dismissed is left as it is (already_booked when booked).
func (s *Service) UndismissStripeTxn(ctx context.Context, clubID, actorID, txnID string) (*StripeTxnDismissResult, error) {
	var res *StripeTxnDismissResult
	err := s.stripeManualTx(ctx, clubID, actorID, func(tx pgx.Tx) error {
		row, err := lockStripeTxnTx(ctx, tx, clubID, txnID)
		if err != nil {
			return err
		}
		if row.booked {
			return stripeRefusal(StripeTxnAlreadyBooked, "transaksjonen er bokført")
		}
		res = &StripeTxnDismissResult{ID: row.id, BalanceTransactionID: row.btID, Status: row.status, ReviewReason: row.reviewReason}
		if row.status != StripeTxnDismissed {
			res.Unchanged = true
			return nil
		}
		if row.payoutCleared {
			return stripeRefusal(StripeTxnPayoutCleared, stripePayoutClearedText)
		}
		res.Status = StripeTxnUnbooked
		if row.reviewReason != "" {
			res.Status = StripeTxnNeedsReview
		}
		if _, err := tx.Exec(ctx,
			`UPDATE stripe_balance_transactions
			    SET status = $3, dismissed_at = NULL, dismissed_by = NULL, dismissed_reason = NULL, updated_at = now()
			  WHERE id = $1 AND club_id = $2 AND status = 'dismissed' AND journal_entry_id IS NULL`,
			row.id, clubID, res.Status); err != nil {
			return fmt.Errorf("undismissing stripe transaction: %w", err)
		}
		return tx.Commit(ctx)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// StripeTxnItem is one Stripe balance transaction as the review list shows
// it, with the manual actions it allows.
type StripeTxnItem struct {
	ID                   string  `json:"id"`
	BalanceTransactionID string  `json:"balance_transaction_id"`
	ReportingCategory    string  `json:"reporting_category"`
	StripeType           string  `json:"stripe_type"`
	Amount               float64 `json:"amount"`
	Fee                  float64 `json:"fee"`
	Net                  float64 `json:"net"`
	Currency             string  `json:"currency"`
	// Date is the booking date the row gets (created, local time).
	Date         string  `json:"date"`
	CreatedAt    string  `json:"created_at"`
	Status       string  `json:"status"`
	ReviewReason string  `json:"review_reason,omitempty"`
	OrderID      *string `json:"order_id"`
	AttemptID    *string `json:"attempt_id"`
	// PayoutID is Stripe's id of the payout that paid the row out.
	PayoutID        *string `json:"payout_id"`
	PayoutCleared   bool    `json:"payout_cleared"`
	DismissedAt     *string `json:"dismissed_at,omitempty"`
	DismissedReason string  `json:"dismissed_reason,omitempty"`
	DismissedNote   string  `json:"dismissed_note,omitempty"`
	// JournalEntryID, JournalEntryNumber, JournalEntryDescription and
	// JournalEntryStatus describe a booked row's entry.
	JournalEntryID          *string `json:"journal_entry_id,omitempty"`
	JournalEntryNumber      *int    `json:"journal_entry_number,omitempty"`
	JournalEntryDescription string  `json:"journal_entry_description,omitempty"`
	JournalEntryStatus      string  `json:"journal_entry_status,omitempty"`
	// InvoiceNumbers are the fakturas a booked row settles
	// (stripeSettledInvoiceSQL), by number.
	InvoiceNumbers []int `json:"invoice_numbers,omitempty"`
	// AllowedActions are the StripeAction* values the row allows now.
	AllowedActions []string `json:"allowed_actions"`
}

// IsListableStripeTxnStatus reports whether ListStripeTransactions lists
// rows in status.
func IsListableStripeTxnStatus(status string) bool {
	return status == StripeTxnNeedsReview || status == StripeTxnDismissed || status == StripeTxnBooked
}

// stripeChargeSettlesSQL is true when the Stripe row alias is a charge
// whose booking still settles what it paid: booked, and not refunded in
// full with the choice to collect again (recollected_at). It is the one
// definition every "does this card payment settle the faktura" check uses
// (invoiceBookedRowSQL, stripeSettledInvoiceSQL); a refund never settles
// anything, and a recollected charge has given its fakturas back.
func stripeChargeSettlesSQL(alias string) string {
	return `(` + alias + `.reporting_category = 'charge' AND ` + alias + `.journal_entry_id IS NOT NULL AND ` +
		alias + `.recollected_at IS NULL)`
}

// stripeSettledInvoiceSQL is true for a faktura i that the booked Stripe
// charge t settles: the one a person assigned it to (invoice_id) or, for a
// charge the booking pass booked from its order (no review_reason), every
// faktura of the order. These are the Stripe clauses of invoiceBookedRowSQL.
var stripeSettledInvoiceSQL = `(i.club_id = t.club_id AND ` + stripeChargeSettlesSQL("t") + `
	      AND (i.id = t.invoice_id
	           OR (t.review_reason IS NULL
	               AND i.id IN (SELECT os.invoice_id FROM order_sources os WHERE os.order_id = t.order_id))))`

// ListStripeTransactions lists the club's Stripe balance transactions in
// status (needs_review, dismissed or booked), newest first, from offset.
// hasMore is set when more follow.
func (s *Service) ListStripeTransactions(ctx context.Context, clubID, status string, limit, offset int) (items []StripeTxnItem, hasMore bool, err error) {
	if !IsListableStripeTxnStatus(status) {
		return nil, false, fmt.Errorf("unsupported status %q", status)
	}
	rows, err := s.db.Query(ctx,
		`SELECT t.id, t.balance_transaction_id, t.reporting_category, t.stripe_type,
		        t.amount_ore, t.fee_ore, t.net_ore, t.currency, t.created_at, t.status, COALESCE(t.review_reason, ''),
		        t.order_id::text, t.attempt_id::text, p.payout_id, COALESCE(p.journal_entry_id IS NOT NULL, false),
		        t.journal_entry_id IS NOT NULL, t.dismissed_at, COALESCE(t.dismissed_reason, ''),
		        t.journal_entry_id::text, e.entry_number, COALESCE(e.description, ''), COALESCE(e.status::text, ''),
		        `+stripeOwnEntrySQL+`,
		        ARRAY(SELECT i.invoice_number FROM invoices i WHERE `+stripeSettledInvoiceSQL+` ORDER BY i.invoice_number)
		   FROM stripe_balance_transactions t
		   LEFT JOIN stripe_payouts p ON p.id = t.payout_id
		   LEFT JOIN journal_entries e ON e.id = t.journal_entry_id AND e.club_id = t.club_id
		  WHERE t.club_id = $1 AND t.status = $2
		  ORDER BY t.created_at DESC, t.id
		  LIMIT $3 OFFSET $4`,
		clubID, status, limit+1, offset)
	if err != nil {
		return nil, false, fmt.Errorf("listing stripe transactions: %w", err)
	}
	defer rows.Close()
	items = []StripeTxnItem{}
	for rows.Next() {
		it, err := scanStripeTxnItem(rows)
		if err != nil {
			return nil, false, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("listing stripe transactions: %w", err)
	}
	if len(items) > limit {
		return items[:limit], true, nil
	}
	return items, false, nil
}

func scanStripeTxnItem(rows pgx.Rows) (StripeTxnItem, error) {
	var (
		it                     StripeTxnItem
		amountOre, feeOre, net int64
		created                time.Time
		dismissedAt            *time.Time
		stored                 string
		st                     stripeTxnState
		invoiceNumbers         []int
	)
	if err := rows.Scan(&it.ID, &it.BalanceTransactionID, &it.ReportingCategory, &it.StripeType,
		&amountOre, &feeOre, &net, &it.Currency, &created, &it.Status, &it.ReviewReason,
		&it.OrderID, &it.AttemptID, &it.PayoutID, &it.PayoutCleared,
		&st.booked, &dismissedAt, &stored,
		&it.JournalEntryID, &it.JournalEntryNumber, &it.JournalEntryDescription, &it.JournalEntryStatus,
		&st.ownEntry, &invoiceNumbers); err != nil {
		return it, fmt.Errorf("scanning stripe transaction: %w", err)
	}
	if len(invoiceNumbers) > 0 {
		it.InvoiceNumbers = invoiceNumbers
	}
	it.Amount, it.Fee, it.Net = oreToKr(amountOre), oreToKr(feeOre), oreToKr(net)
	it.Date = stripeBookingDate(created, time.Local).Format("2006-01-02")
	it.CreatedAt = created.UTC().Format(time.RFC3339)
	if dismissedAt != nil {
		d := dismissedAt.UTC().Format(time.RFC3339)
		it.DismissedAt = &d
		it.DismissedReason, it.DismissedNote = splitStripeDismissal(stored)
	}
	st.category, st.status, st.reviewReason, st.currency = it.ReportingCategory, it.Status, it.ReviewReason, it.Currency
	st.payoutCleared, st.hasOrder = it.PayoutCleared, it.OrderID != nil
	it.AllowedActions = stripeAllowedActions(st)
	return it, nil
}

// PotentialInvoicesForStripeTxn lists the open, unpaid fakturas whose total
// equals a Stripe charge, the candidates for assigning it (the bank row
// query on the charge's amount; there is no bank payer to resolve). q
// narrows by number or member.
func (s *Service) PotentialInvoicesForStripeTxn(ctx context.Context, clubID, txnID, q string, limit int) ([]InvoiceSuggestion, error) {
	var amountOre int64
	err := s.db.QueryRow(ctx,
		`SELECT amount_ore FROM stripe_balance_transactions WHERE id = $1 AND club_id = $2 AND reporting_category = 'charge'`,
		txnID, clubID,
	).Scan(&amountOre)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrStripeTxnNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading stripe transaction: %w", err)
	}
	if amountOre <= 0 {
		return []InvoiceSuggestion{}, nil
	}
	return s.potentialInvoicesByAmount(ctx, clubID, q, limit, oreToKr(amountOre))
}
