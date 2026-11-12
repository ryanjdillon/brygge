package accounting

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// Per-charge Vipps postings (BRY-226). Where the legacy single-bilag model
// (vippsreconcile.go) books one all-or-nothing journal entry per bank payout,
// this model books each Vipps charge on its own through a per-club clearing
// account so one unresolved payer no longer blocks a whole settlement:
//
//	Stage 1 (per charge, driven from the Tildel queue)
//	  assign→faktura : DR clearing / CR 1500  + invoices.payment_id
//	  lag bilag      : DR clearing / CR <chosen account>
//	Stage 2 (per settlement, auto-cleared once every charge is booked)
//	  fee            : DR gebyr    / CR clearing
//	  bank payout    : DR bank     / CR clearing   (keyed on settlement number)
//
// Per settlement the clearing account nets to ~0: sum of the charge debits
// equals the fee credit plus the net-payout credit. Every entry is posted on
// creation (BRY-339): reports and momskomp only count booked entries, so a
// draft would leave Vipps income and fees out of the accounts. Each posting
// first marks the source row's journal_entry_id with a guarded UPDATE ...
// WHERE journal_entry_id IS NULL (the loser of a concurrent double booking
// deletes its still-draft entry and returns the winner's), and only then
// posts; a failed post unlinks the row and discards the draft. A charge
// assigned to a faktura is booked in the faktura-lock transaction instead
// (postVippsChargeTx), where a rollback does both.

// VippsChargePostResult is the outcome of booking a single Vipps belastning.
type VippsChargePostResult struct {
	EntryID string `json:"entry_id"`
	// AlreadyReconciled is true when the charge was already booked; EntryID
	// then points at the existing entry and no new posting was made.
	AlreadyReconciled bool `json:"already_reconciled"`
}

// VippsPayoutClearResult is the outcome of a Stage-2 payout clear.
type VippsPayoutClearResult struct {
	SettlementNumber string `json:"settlement_number"`
	MSN              string `json:"msn"`
	// Ready is false when the settlement still has charges that are not
	// posted (unbooked, on a draft or on a voided entry) and not dismissed; no
	// postings are made and UnreconciledCharges says how many remain.
	Ready               bool `json:"ready"`
	UnreconciledCharges int  `json:"unreconciled_charges"`
	// AlreadyCleared is true when the bank row was already linked; the
	// entries below then reference the existing postings.
	AlreadyCleared bool   `json:"already_cleared"`
	FeeEntryID     string `json:"fee_entry_id,omitempty"`
	BankEntryID    string `json:"bank_entry_id,omitempty"`
	// ClearingBalance is the net balance on the clearing account across every
	// entry belonging to this settlement after the clear. It should be ~0.
	ClearingBalance float64 `json:"clearing_balance"`
}

// vippsAccounts returns the club's configured Vipps clearing account and its
// payment fee account (shared by all providers), falling back to the model
// defaults (2900 / 7700) when unset.
func (s *Service) vippsAccounts(ctx context.Context, clubID string) (clearing, fee string, err error) {
	return vippsAccountsIn(ctx, s.db, clubID)
}

// vippsAccountsIn is vippsAccounts on q.
func vippsAccountsIn(ctx context.Context, q dbtx, clubID string) (clearing, fee string, err error) {
	err = q.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(vipps_clearing_account, ''), $2),
		        COALESCE(NULLIF(payment_fee_account, ''), $3)
		   FROM clubs WHERE id = $1`,
		clubID, vippsClearingAccountCode, vippsFeeAccountCode,
	).Scan(&clearing, &fee)
	if err == pgx.ErrNoRows {
		return "", "", fmt.Errorf("club not found")
	}
	if err != nil {
		return "", "", fmt.Errorf("loading vipps accounts: %w", err)
	}
	return clearing, fee, nil
}

// vippsBelastning is a single unreconciled customer-payment row.
type vippsBelastning struct {
	id           string
	amount       float64
	date         time.Time
	customerName string
	message      string
	orderRef     string // merchant reference (chk-…) on ePayment charges
	entryID      *string
	dismissedAt  *time.Time
	// declined is true when a person's undo took the charge out of the
	// automatic passes (auto_declined_at, ACC-13(b)).
	declined bool
}

// loadVippsBelastning reads one belastning row and validates it is an
// unbooked, positive customer payment. entryID is non-nil when the row has
// already been reconciled; dismissedAt is non-nil when it was avvist.
func (s *Service) loadVippsBelastning(ctx context.Context, clubID, rowID string) (*vippsBelastning, error) {
	return loadVippsBelastningIn(ctx, s.db, clubID, rowID)
}

// loadVippsBelastningIn is loadVippsBelastning on q.
func loadVippsBelastningIn(ctx context.Context, q dbtx, clubID, rowID string) (*vippsBelastning, error) {
	var b vippsBelastning
	var rowType string
	err := q.QueryRow(ctx,
		`SELECT id, row_type, amount,
		        COALESCE(booking_date, tx_at::date),
		        COALESCE(customer_name, ''), COALESCE(message, ''),
		        COALESCE(order_id, ''), journal_entry_id, dismissed_at,
		        auto_declined_at IS NOT NULL
		   FROM vipps_import_rows
		  WHERE id = $1 AND club_id = $2`,
		rowID, clubID,
	).Scan(&b.id, &rowType, &b.amount, &b.date, &b.customerName, &b.message, &b.orderRef, &b.entryID, &b.dismissedAt, &b.declined)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("vipps charge not found")
	}
	if err != nil {
		return nil, fmt.Errorf("loading vipps charge: %w", err)
	}
	if VippsRowType(rowType) != VippsRowBelastning {
		return nil, fmt.Errorf("vipps row %s is a %q row, not a customer charge", rowID, rowType)
	}
	if b.amount <= 0 {
		return nil, fmt.Errorf("vipps charge %s has non-positive amount %.2f", rowID, b.amount)
	}
	return &b, nil
}

// DismissVippsCharge marks an individual Vipps charge as avvist so it drops out
// of the Tildel queue and no longer blocks its settlement's payout auto-clear.
// Only unbooked, non-dismissed charges can be dismissed; re-dismissing is a
// no-op. Reasons reuse the bank-row dismissal vocabulary (IsValidDismissReason).
//
// Caveat: dismiss only a charge Vipps left out of the payout (a reversed or
// duplicate charge). A charge dismissed although Vipps paid it out leaves its
// settlement's clearing account off zero, so ClearVippsPayout refuses the
// settlement (ErrVippsSettlementNotNetting) and it stays skipped. No app path
// undoes a Vipps dismissal yet (an operator clears dismissed_at in the
// database; BRY-573 tracks an in-app undo), and CreateVippsChargeBilag
// refuses a dismissed charge, so there is no in-app remedy.
func (s *Service) DismissVippsCharge(ctx context.Context, clubID, userID, rowID, reason string) error {
	if !IsValidDismissReason(reason) {
		return fmt.Errorf("invalid dismiss reason %q", reason)
	}
	b, err := s.loadVippsBelastning(ctx, clubID, rowID)
	if err != nil {
		return err
	}
	if b.entryID != nil {
		return fmt.Errorf("charge is already reconciled — unassign it before dismissing")
	}
	if b.dismissedAt != nil {
		return nil // already dismissed — idempotent
	}
	_, err = s.db.Exec(ctx,
		`UPDATE vipps_import_rows
		    SET dismissed_at = now(), dismissed_reason = $3, dismissed_by = $4
		  WHERE id = $1 AND club_id = $2 AND journal_entry_id IS NULL AND dismissed_at IS NULL`,
		rowID, clubID, reason, userID,
	)
	if err != nil {
		return fmt.Errorf("dismissing vipps charge: %w", err)
	}
	return nil
}

// postVippsCharge books one belastning as DR clearing / CR <creditAccount>,
// marks the row reconciled, and returns the entry. The guarded mark makes the
// operation safe against a concurrent second booking of the same charge.
// mode is automaticBooking for an automatic pass, which never opens a fiscal
// year and never books a charge a person declined (errAutoDeclined), and
// personBooking for a person's booking.
func (s *Service) postVippsCharge(ctx context.Context, clubID, createdBy, creditAccount, description string, b *vippsBelastning, mode bookingMode) (string, error) {
	return s.postVippsChargeSplit(ctx, clubID, createdBy, description, b, []CreateJournalLineInput{
		{AccountCode: creditAccount, Debit: 0, Credit: b.amount, Description: description},
	}, mode)
}

// postVippsChargeSplit is postVippsCharge with the credit side given as lines,
// for a charge that pays several things at once (a checkout order settling
// fakturas plus catalog lines). The credits must sum to the charge amount.
func (s *Service) postVippsChargeSplit(ctx context.Context, clubID, createdBy, description string, b *vippsBelastning, credits []CreateJournalLineInput, mode bookingMode) (string, error) {
	clearing, _, err := s.vippsAccounts(ctx, clubID)
	if err != nil {
		return "", err
	}
	p := vippsChargePosting(clubID, createdBy, clearing, description, b, credits, mode)

	var periodID string
	if mode.policy == periodMayCreate {
		var periodStatus string
		periodID, periodStatus, err = s.resolvePeriod(ctx, clubID, b.date, "")
		if err != nil {
			return "", fmt.Errorf("resolving fiscal period: %w", err)
		}
		// A person's booking (Lag bilag): a closed or locked period is
		// refused here with its own reason, before the entry insert would
		// refuse both with ErrPeriodClosed.
		if periodStatus != "open" {
			return "", periodNotOpenConfirmError(b.date.Year(), periodStatus)
		}
	} else {
		periodID, err = existingOpenPeriodIn(ctx, s.db, clubID, b.date)
		if err != nil {
			return "", err
		}
	}

	entry, err := s.CreateJournalEntry(ctx, p.entryInput(periodID))
	if err != nil {
		return "", fmt.Errorf("creating journal entry: %w", err)
	}

	tag, err := s.db.Exec(ctx, p.markSQL, entry.ID, b.id, clubID)
	if err != nil {
		return "", fmt.Errorf("marking charge reconciled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Lost a race: the row was booked concurrently, or (an automatic
		// pass) a person declined it. Drop the draft we just created and
		// report the winning entry, or the decline, instead.
		_, _ = s.db.Exec(ctx,
			`DELETE FROM journal_entries WHERE id = $1 AND club_id = $2 AND status = 'draft'`,
			entry.ID, clubID,
		)
		return lostChargeMark(ctx, s.db, p)
	}
	if err := s.PostJournalEntry(ctx, clubID, entry.ID, createdBy); err != nil {
		// The mark did not touch auto_declined_at, so the unwind leaves a
		// person's decline as it was.
		s.unwindVippsPosting(ctx, clubID, entry.ID, []string{b.id})
		return "", fmt.Errorf("posting journal entry: %w", err)
	}
	// Booked and posted: the row is no longer declined. Logged on failure,
	// the booking stands; an automatic pass never reaches a booked row.
	if _, err := s.db.Exec(ctx, p.liftSQL, b.id, clubID, entry.ID); err != nil {
		s.log.Warn().Err(err).Str("vipps_row_id", b.id).Msg("vipps charge booked; lifting its decline failed")
	}
	return entry.ID, nil
}

// vippsChargePosting is the chargePosting of one belastning. The mode has no
// default here: each caller states whether the booking may open a fiscal year
// and whether it is an automatic pass, whose guarded mark refuses a charge a
// person declined (auto_declined_at). The mark never touches the marker; the
// lift after a successful post clears it (a person's booking of a declined
// charge), so a pool-backed post that fails and unwinds keeps it.
func vippsChargePosting(clubID, createdBy, clearing, description string, b *vippsBelastning, credits []CreateJournalLineInput, mode bookingMode) *chargePosting {
	markSQL := `UPDATE vipps_import_rows SET journal_entry_id = $1
		  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL`
	if mode.auto {
		markSQL += ` AND auto_declined_at IS NULL`
	}
	return &chargePosting{
		periodPolicy: mode.policy,
		auto:         mode.auto,
		clubID:       clubID,
		createdBy:    createdBy,
		source:       "vipps",
		sourceTable:  "vipps_import_rows",
		sourceID:     b.id,
		clearing:     clearing,
		date:         b.date,
		amount:       b.amount,
		description:  description,
		credits:      credits,
		markSQL:      markSQL,
		winnerSQL:    `SELECT journal_entry_id FROM vipps_import_rows WHERE id = $1 AND club_id = $2`,
		declinedSQL:  `SELECT auto_declined_at IS NOT NULL FROM vipps_import_rows WHERE id = $1 AND club_id = $2`,
		liftSQL: `UPDATE vipps_import_rows SET auto_declined_at = NULL
		  WHERE id = $1 AND club_id = $2 AND journal_entry_id = $3 AND auto_declined_at IS NOT NULL`,
		savepoint: "vipps_charge",
	}
}

// postVippsChargeTx is postVippsCharge inside the caller's transaction, for
// a booking that holds the faktura lock.
func postVippsChargeTx(ctx context.Context, tx pgx.Tx, clubID, createdBy, creditAccount, description string, b *vippsBelastning, mode bookingMode) (string, error) {
	return postVippsChargeSplitTx(ctx, tx, clubID, createdBy, description, b, []CreateJournalLineInput{
		{AccountCode: creditAccount, Debit: 0, Credit: b.amount, Description: description},
	}, mode)
}

// postVippsChargeSplitTx is postVippsChargeSplit inside the caller's
// transaction (postChargeTx).
func postVippsChargeSplitTx(ctx context.Context, tx pgx.Tx, clubID, createdBy, description string, b *vippsBelastning, credits []CreateJournalLineInput, mode bookingMode) (string, error) {
	clearing, _, err := vippsAccountsIn(ctx, tx, clubID)
	if err != nil {
		return "", err
	}
	return postChargeTx(ctx, tx, vippsChargePosting(clubID, createdBy, clearing, description, b, credits, mode))
}

// unwindVippsPosting undoes a charge posting whose PostJournalEntry failed: it
// unlinks the rows the draft was marked on and deletes the draft, so the
// charge goes back to the Tildel queue instead of sitting on an unposted
// orphan.
func (s *Service) unwindVippsPosting(ctx context.Context, clubID, entryID string, rowIDs []string) {
	if _, err := s.db.Exec(ctx,
		`UPDATE vipps_import_rows SET journal_entry_id = NULL
		  WHERE club_id = $1 AND journal_entry_id = $2 AND id = ANY($3::uuid[])`,
		clubID, entryID, rowIDs,
	); err != nil {
		s.log.Error().Err(err).Str("entry_id", entryID).Msg("vipps posting: unlinking rows after a failed post failed")
	}
	s.discardDraft(ctx, clubID, entryID)
}

// ErrVippsSettlementNotImported is returned by ClearVippsPayout when the bank
// deposit names a settlement whose Vipps rows have not been imported yet.
var ErrVippsSettlementNotImported = errors.New("no imported settlement")

// ErrVippsSettlementAlreadyCleared is returned by ClearVippsPayout when the
// settlement was already cleared through another bank deposit: booking this
// one too would credit the net payout to clearing twice.
var ErrVippsSettlementAlreadyCleared = errors.New("settlement already cleared by another deposit")

// ErrVippsPayoutAmountMismatch is returned by ClearVippsPayout when the bank
// deposit does not equal the imported net payout to the øre. Clearing it
// would leave the settlement's clearing balance off by the difference.
var ErrVippsPayoutAmountMismatch = errors.New("deposit does not match the payout amount")

// ErrVippsSettlementNotNetting is returned by ClearVippsPayout when posting
// the settlement's fee and payout entries would leave a balance on the Vipps
// clearing account: a charge that never reached the ledger (dismissed, or in a
// foreign period) or a fee the report does not explain. Nothing is posted or
// linked, so an unattended sweep cannot book a settlement that does not close.
var ErrVippsSettlementNotNetting = errors.New("settlement would not net to zero on the clearing account")

// ErrVippsSettlementAmbiguous is returned by ClearVippsPayout when more than
// one payout shares the settlement's MSN and booking date. Charges and fees
// carry no settlement number, only MSN + booking date, so they cannot be
// attributed to one settlement.
var ErrVippsSettlementAmbiguous = errors.New("several settlements share the booking date")

// ErrVippsPeriodClosed is returned by ClearVippsPayout when the fee or bank
// entry would land in a fiscal period that is not open (closed or locked). Nothing is posted.
var ErrVippsPeriodClosed = errors.New("fiscal period is closed")

// ErrVippsPayoutRaced is returned by ClearVippsPayout when a concurrent clear
// booked part of the settlement first; the next sync sees the result.
var ErrVippsPayoutRaced = errors.New("settlement booked concurrently")

// errChargeRaced signals that postVippsCharge lost the guarded-mark race and
// the returned entry ID belongs to the concurrent winner.
var errChargeRaced = fmt.Errorf("vipps charge booked concurrently")

// AssignVippsChargeToInvoice books a single Vipps belastning against an open
// invoice: DR clearing / CR 1500, marks the charge reconciled, and back-links
// invoices.payment_id so the faktura reads as paid. Idempotent per charge.
// This is a person's booking in Tildel: a missing fiscal year for the charge's
// date is created, as it always was, a charge a person's undo declined is
// booked (and so no longer declined), and the decline is lifted from the
// settlement's payout deposit (ACC-11). The automatic passes use
// assignVippsChargeToInvoice with automaticBooking.
func (s *Service) AssignVippsChargeToInvoice(ctx context.Context, clubID, vippsRowID, invoiceID, createdBy string) (*VippsChargePostResult, error) {
	res, err := s.assignVippsChargeToInvoice(ctx, clubID, vippsRowID, invoiceID, createdBy, personBooking)
	if err == nil && !res.AlreadyReconciled {
		s.liftSettlementDepositDecline(ctx, clubID, vippsRowID)
	}
	return res, err
}

// assignVippsChargeToInvoice is AssignVippsChargeToInvoice with the booking
// mode chosen by the caller; automaticBooking refuses to open a fiscal year,
// to book into a period that is not open, and to book a charge a person
// declined (errAutoDeclined).
func (s *Service) assignVippsChargeToInvoice(ctx context.Context, clubID, vippsRowID, invoiceID, createdBy string, mode bookingMode) (*VippsChargePostResult, error) {
	b, err := s.loadVippsBelastning(ctx, clubID, vippsRowID)
	if err != nil {
		return nil, err
	}
	if b.entryID != nil {
		return &VippsChargePostResult{EntryID: *b.entryID, AlreadyReconciled: true}, nil
	}
	if b.dismissedAt != nil {
		return nil, fmt.Errorf("charge is dismissed — un-dismiss it before assigning")
	}
	if mode.auto && b.declined {
		return nil, errAutoDeclined
	}

	// Lock the faktura across the posting, as the bank assigns do
	// (lockInvoiceForAssign), so a Vipps charge and a bank row can't both
	// settle it: the loser sees the winner's booked row.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	invCents, err := lockInvoiceForAssign(ctx, tx, clubID, invoiceID, invoiceLockOpts{mode: lockManual, bookingDate: b.date})
	if err != nil {
		return nil, err
	}
	// Requiring the amounts to agree keeps the payment_id back-link honest —
	// a differently priced invoice should go through Lag bilag, not be
	// silently marked paid.
	if invAmount := float64(invCents) / 100; !floatNear(invAmount, b.amount, 1.00) {
		return nil, fmt.Errorf("charge %.2f does not match invoice total %.2f — use Lag bilag instead", b.amount, invAmount)
	}

	desc := fmt.Sprintf("Vipps innbetaling: %s", b.customerName)
	entryID, err := postVippsChargeTx(ctx, tx, clubID, createdBy, receivablesAccountCode, desc, b, mode)
	if err == errChargeRaced {
		return &VippsChargePostResult{EntryID: entryID, AlreadyReconciled: true}, nil
	}
	if err != nil {
		return nil, err
	}
	// Record the settled faktura in the same commit as the booking, so a
	// booking that waited for the lock sees this charge.
	if _, err := tx.Exec(ctx,
		`UPDATE vipps_import_rows SET invoice_id = $3 WHERE id = $1 AND club_id = $2`,
		vippsRowID, clubID, invoiceID,
	); err != nil {
		return nil, fmt.Errorf("recording the settled invoice: %w", err)
	}
	// The back-link updates the faktura, so it runs once the commit has
	// released the lock.
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit vipps charge assign: %w", err)
	}

	// Back-link the invoice payment. Best-effort: the GL is already correct
	// if this fails, mirroring the legacy reconcile path. It follows the
	// commit, so it runs on postCommitContext: a request or run cancelled
	// right after the booking still marks the faktura paid.
	linkCtx, cancel := postCommitContext(ctx)
	defer cancel()
	if err := s.linkInvoicePayment(linkCtx, clubID, invoiceID, b.date); err != nil {
		s.log.Warn().Err(err).
			Str("invoice_id", invoiceID).
			Str("vipps_row_id", vippsRowID).
			Str("journal_entry_id", entryID).
			Msg("vipps per-charge assign: invoice payment back-link failed")
	}

	return &VippsChargePostResult{EntryID: entryID}, nil
}

// CreateVippsChargeBilag books a single Vipps belastning to a chosen revenue
// (or other) account: DR clearing / CR <accountCode>. Use this for walk-up
// payments with no faktura. Idempotent per charge. This is a person's booking
// in Tildel (Lag bilag): a missing fiscal year for the charge's date is
// created, as it always was, a declined charge is booked, and the decline is
// lifted from the settlement's payout deposit (ACC-11). The automatic passes
// use createVippsChargeBilag with automaticBooking.
func (s *Service) CreateVippsChargeBilag(ctx context.Context, clubID, vippsRowID, accountCode, note, createdBy string) (*VippsChargePostResult, error) {
	res, err := s.createVippsChargeBilag(ctx, clubID, vippsRowID, accountCode, note, createdBy, personBooking)
	if err == nil && !res.AlreadyReconciled {
		s.liftSettlementDepositDecline(ctx, clubID, vippsRowID)
	}
	return res, err
}

// createVippsChargeBilag is CreateVippsChargeBilag with the booking mode
// chosen by the caller; automaticBooking refuses to open a fiscal year, to
// book into a period that is not open, and to book a charge a person declined
// (errAutoDeclined).
func (s *Service) createVippsChargeBilag(ctx context.Context, clubID, vippsRowID, accountCode, note, createdBy string, mode bookingMode) (*VippsChargePostResult, error) {
	if accountCode == "" {
		return nil, fmt.Errorf("account code is required")
	}
	b, err := s.loadVippsBelastning(ctx, clubID, vippsRowID)
	if err != nil {
		return nil, err
	}
	if b.entryID != nil {
		return &VippsChargePostResult{EntryID: *b.entryID, AlreadyReconciled: true}, nil
	}
	if b.dismissedAt != nil {
		return nil, fmt.Errorf("charge is dismissed — un-dismiss it before booking")
	}
	if mode.auto && b.declined {
		return nil, errAutoDeclined
	}

	desc := fmt.Sprintf("Vipps innbetaling: %s", b.customerName)
	if note != "" {
		desc = fmt.Sprintf("%s — %s", desc, note)
	}
	entryID, err := s.postVippsCharge(ctx, clubID, createdBy, accountCode, desc, b, mode)
	if err == errChargeRaced {
		return &VippsChargePostResult{EntryID: entryID, AlreadyReconciled: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return &VippsChargePostResult{EntryID: entryID}, nil
}

// ClearVippsPayout performs Stage 2 for the settlement named by a bank payout
// row: it books the per-settlement fee (DR gebyr / CR clearing, or the reverse
// for a net fee refund) and the bank payout (DR bank / CR clearing), keyed on
// the settlement number parsed from the bank row description. It waits until
// every belastning charge in the settlement has been booked, refuses with
// ErrVippsSettlementNotNetting (nothing posted) when the clearing account would
// not net to zero, and is idempotent: an already-cleared bank row returns its
// existing entries. This is a person's clearing in Tildel (Utlign
// utbetaling): a missing fiscal year for the fee or bank date is
// created, as it always was, and a deposit a person declined ("Angre
// innbetalingen") is cleared, which lifts its decline. The payout sweep uses
// clearVippsPayout with automaticBooking.
func (s *Service) ClearVippsPayout(ctx context.Context, clubID, bankRowID, createdBy string) (*VippsPayoutClearResult, error) {
	return s.clearVippsPayout(ctx, clubID, bankRowID, createdBy, personBooking)
}

// clearVippsPayout is ClearVippsPayout with the booking mode chosen by the
// caller; automaticBooking refuses to open a fiscal year (a missing one is
// *noFiscalYearError, nothing is posted) and to clear a deposit a person
// declined (errAutoDeclined, also when the decline lands while it waits for
// the settlement's lock).
func (s *Service) clearVippsPayout(ctx context.Context, clubID, bankRowID, createdBy string, mode bookingMode) (*VippsPayoutClearResult, error) { //nolint:gocognit,cyclop // validate, lock and post one payout clearing in a single transaction; split tracked in BRY-424
	var (
		bankDate    time.Time
		description string
		bankAmount  float64
		bankAccount string
		bankEntry   *string
		declined    bool
	)
	err := s.db.QueryRow(ctx,
		`SELECT bir.row_date, bir.description, bir.amount, bi.bank_account_code, bir.journal_entry_id,
		        bir.auto_declined_at IS NOT NULL
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.id = $1 AND bir.club_id = $2`,
		bankRowID, clubID,
	).Scan(&bankDate, &description, &bankAmount, &bankAccount, &bankEntry, &declined)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("bank row not found")
	}
	if err != nil {
		return nil, fmt.Errorf("loading bank row: %w", err)
	}
	if bankAccount == "" {
		bankAccount = bankAccountCode
	}

	m := VippsSettlementPattern.FindStringSubmatch(description)
	if len(m) != 3 {
		return nil, fmt.Errorf("bank row description does not look like a Vipps payout: %q", description)
	}
	res := &VippsPayoutClearResult{SettlementNumber: m[1], MSN: m[2]}

	// Locate the payout row so the fee/belastning rows can be grouped by its
	// booking_date + MSN (only the payout row carries the settlement number).
	var payoutBooking time.Time
	var payoutAmount float64
	var payoutEntry *string
	var payoutRowID string
	err = s.db.QueryRow(ctx,
		`SELECT id, booking_date, amount, journal_entry_id
		   FROM vipps_import_rows
		  WHERE club_id = $1 AND msn = $2 AND row_type = 'payout' AND settlement_number = $3
		  ORDER BY booking_date, id
		  LIMIT 1`,
		clubID, res.MSN, res.SettlementNumber,
	).Scan(&payoutRowID, &payoutBooking, &payoutAmount, &payoutEntry)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("%w for Utb. %s Vippsnr %s — import the Vipps export first", ErrVippsSettlementNotImported, res.SettlementNumber, res.MSN)
	}
	if err != nil {
		return nil, fmt.Errorf("loading payout row: %w", err)
	}

	// If the bank row is already linked, the settlement is cleared. Report the
	// existing entries and the clearing balance so callers stay idempotent.
	if bankEntry != nil {
		return s.alreadyClearedResult(ctx, clubID, res, bankRowID, *bankEntry, payoutBooking), nil
	}
	if mode.auto && declined {
		return nil, fmt.Errorf("%w: Utb. %s Vippsnr %s", errAutoDeclined, res.SettlementNumber, res.MSN)
	}

	// Everything below runs in one transaction that holds the settlement's
	// payout rows locked, so the charges it counts as booked stay booked until
	// the clearing commits (the unbooks of a charge take the same lock before
	// they check whether the settlement is cleared), and a failure anywhere
	// leaves neither entry behind.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockSettlementPayoutsTx(ctx, tx, clubID, res.MSN, payoutBooking); err != nil {
		return nil, err
	}
	// A clear that held the lock first may have booked the deposit and the
	// payout row since they were read above.
	var winner *string
	if err := tx.QueryRow(ctx,
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&winner); err != nil {
		return nil, fmt.Errorf("re-reading bank row: %w", err)
	}
	if winner != nil {
		_ = tx.Rollback(ctx)
		return s.alreadyClearedResult(ctx, clubID, res, bankRowID, *winner, payoutBooking), nil
	}
	if err := tx.QueryRow(ctx,
		`SELECT journal_entry_id::text FROM vipps_import_rows WHERE id = $1 AND club_id = $2`,
		payoutRowID, clubID,
	).Scan(&payoutEntry); err != nil {
		return nil, fmt.Errorf("re-reading payout row: %w", err)
	}

	if err := checkSettlementClearable(ctx, tx, clubID, res, bankRowID, payoutBooking, payoutEntry); err != nil {
		return nil, err
	}

	// Gate: every belastning in the settlement must be booked first, and
	// booked means posted. A charge linked to a draft is not in the ledger,
	// and one linked to a voided entry is not either, so clearing around it
	// would leave the clearing account holding the fee and payout credits
	// with no charge debit against them.
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM vipps_import_rows r
		  WHERE r.club_id = $1 AND r.msn = $2 AND r.row_type = 'belastning'
		    AND r.booking_date = $3::date
		    AND r.dismissed_at IS NULL
		    AND NOT `+chargePostedSQL("r"),
		clubID, res.MSN, payoutBooking,
	).Scan(&res.UnreconciledCharges); err != nil {
		return nil, fmt.Errorf("counting unreconciled charges: %w", err)
	}
	if res.UnreconciledCharges > 0 {
		return res, nil // not ready — leave the payout in the queue
	}
	res.Ready = true

	// The deposit must be the imported net payout to the øre (payout rows are
	// stored negative, as money leaving the Vipps account).
	if err := checkPayoutAmount(bankAmount, payoutAmount, res.SettlementNumber); err != nil {
		return nil, err
	}

	clearing, feeAccount, err := vippsAccountsIn(ctx, tx, clubID)
	if err != nil {
		return nil, err
	}

	feeRows, feeExpense, err := unbookedSettlementFees(ctx, tx, clubID, res.MSN, payoutBooking)
	if err != nil {
		return nil, err
	}
	postFee := !floatNear(feeExpense, 0, 0.005)

	// Refuse before anything is posted: the clearing balance the ledger holds
	// now, less what the fee and bank entries credit to it, must be zero. It
	// is read through the transaction (the posted entries the lock protects),
	// not recomputed from the rows, so a charge that is not in the ledger
	// counts against the settlement however it came to be unbooked.
	if err := checkSettlementNets(ctx, tx, clubID, res, payoutBooking, bankRowID, feeExpense, bankAmount); err != nil {
		// A reference year's charges never post, so such a settlement never
		// nets; the opening balance carries it, and the sweep says so.
		if errors.Is(err, ErrVippsSettlementNotNetting) {
			if ref, rerr := settlementIsReferenceIn(ctx, tx, clubID, res.MSN, res.SettlementNumber); rerr == nil && ref {
				return nil, fmt.Errorf("%w: Utb. %s Vippsnr %s", ErrReferencePeriod, res.SettlementNumber, res.MSN)
			}
		}
		return nil, err
	}

	// Resolve both periods before posting either entry, so a period that is
	// not open is reported as such instead of as a failed post.
	feePeriod, bankPeriod, err := payoutPeriods(ctx, tx, clubID, payoutBooking, bankDate, postFee, mode.policy)
	if err != nil {
		return nil, err
	}

	// Fee entry for the settlement's still-unbooked fee rows, dated at the
	// settlement booking date: an expense is DR gebyr / CR clearing, a net
	// refund of earlier fees the reverse. Every fee row is linked to it, also
	// the zero ones, so none is booked again.
	if postFee {
		feeSource := payoutRowID
		feeTable := "vipps_import_rows"
		feeDesc := fmt.Sprintf("Vipps gebyr oppgjør %s", res.SettlementNumber)
		feeLines := []CreateJournalLineInput{
			{AccountCode: feeAccount, Debit: feeExpense, Credit: 0, Description: feeDesc},
			{AccountCode: clearing, Debit: 0, Credit: feeExpense, Description: feeDesc},
		}
		if feeExpense < 0 {
			feeDesc = fmt.Sprintf("Vipps gebyrrefusjon oppgjør %s", res.SettlementNumber)
			feeLines = []CreateJournalLineInput{
				{AccountCode: clearing, Debit: -feeExpense, Credit: 0, Description: feeDesc},
				{AccountCode: feeAccount, Debit: 0, Credit: -feeExpense, Description: feeDesc},
			}
		}
		feeJE, cerr := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
			FiscalPeriodID: feePeriod,
			EntryDate:      payoutBooking.Format("2006-01-02"),
			Description:    feeDesc,
			Source:         "vipps",
			SourceID:       &feeSource,
			SourceTable:    &feeTable,
			CreatedBy:      createdBy,
			ClubID:         clubID,
			Lines:          feeLines,
		})
		if cerr != nil {
			return nil, fmt.Errorf("creating fee entry: %w", cerr)
		}
		if err := linkFeeRowsTx(ctx, tx, clubID, feeJE.ID, feeRows, res.SettlementNumber); err != nil {
			return nil, err
		}
		if err := postJournalEntryTx(ctx, tx, clubID, feeJE.ID, createdBy); err != nil {
			return nil, fmt.Errorf("posting fee entry: %w", err)
		}
		res.FeeEntryID = feeJE.ID
	}

	// Bank entry (DR bank / CR clearing) for the net payout, dated at the bank
	// value date.
	bankSource := bankRowID
	bankTable := "bank_import_rows"
	bankDesc := fmt.Sprintf("Vipps utbetaling %s (Vippsnr %s)", res.SettlementNumber, res.MSN)
	bankJE, cerr := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
		FiscalPeriodID: bankPeriod,
		EntryDate:      bankDate.Format("2006-01-02"),
		Description:    bankDesc,
		Source:         "vipps",
		SourceID:       &bankSource,
		SourceTable:    &bankTable,
		CreatedBy:      createdBy,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: bankAccount, Debit: bankAmount, Credit: 0, Description: bankDesc},
			{AccountCode: clearing, Debit: 0, Credit: bankAmount, Description: bankDesc},
		},
	})
	if cerr != nil {
		return nil, fmt.Errorf("creating bank entry: %w", cerr)
	}
	// The link books the deposit, so it lifts a person's decline in the same
	// statement (the transaction rolls both back if the post fails). The
	// sweep's link refuses a declined deposit instead.
	linkSQL := `UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true, auto_declined_at = NULL
		  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL`
	if mode.auto {
		linkSQL += ` AND auto_declined_at IS NULL`
	}
	tag, uerr := tx.Exec(ctx, linkSQL, bankJE.ID, bankRowID, clubID)
	if uerr != nil {
		return nil, fmt.Errorf("linking bank row: %w", uerr)
	}
	if tag.RowsAffected() == 0 {
		// A person undid the deposit's clearing while the sweep waited: the
		// sweep reports it declined, not cleared.
		if mode.auto {
			if d, derr := bankRowDeclinedIn(ctx, tx, clubID, []string{bankRowID}); derr != nil {
				return nil, derr
			} else if d {
				return nil, fmt.Errorf("%w: Utb. %s Vippsnr %s", errAutoDeclined, res.SettlementNumber, res.MSN)
			}
		}
		// Booked by something else (a manual assign) since the lock was taken:
		// drop the whole clearing and report that booking.
		_ = tx.Rollback(ctx)
		return s.alreadyClearedResult(ctx, clubID, res, bankRowID, s.bankRowEntry(ctx, clubID, bankRowID), payoutBooking), nil
	}
	if err := postJournalEntryTx(ctx, tx, clubID, bankJE.ID, createdBy); err != nil {
		return nil, fmt.Errorf("posting payout entry: %w", err)
	}
	res.BankEntryID = bankJE.ID
	// Fee rows that net to nothing have no fee entry; they ride on the payout's
	// bank entry (which a void refuses just as it refuses a fee entry) so the
	// next clearing attempt does not count them again.
	if !postFee && len(feeRows) > 0 {
		if err := linkFeeRowsTx(ctx, tx, clubID, bankJE.ID, feeRows, res.SettlementNumber); err != nil {
			return nil, err
		}
	}
	// Mark the payout row reconciled against the bank entry.
	if payoutEntry == nil {
		if _, err := tx.Exec(ctx,
			`UPDATE vipps_import_rows SET journal_entry_id = $1, auto_declined_at = NULL
			  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL`,
			bankJE.ID, payoutRowID, clubID,
		); err != nil {
			return nil, fmt.Errorf("marking payout row: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit payout clearing: %w", err)
	}

	res.ClearingBalance, err = s.settlementClearingBalance(ctx, clubID, res.SettlementNumber, res.MSN, payoutBooking, bankRowID)
	if err != nil {
		return nil, err
	}
	if !floatNear(res.ClearingBalance, 0, 0.01) {
		s.log.Warn().
			Str("settlement", res.SettlementNumber).
			Float64("clearing_balance", res.ClearingBalance).
			Msg("vipps payout cleared but settlement clearing does not net to zero")
	}
	return res, nil
}

// alreadyClearedResult fills res for a deposit another booking already
// cleared: the existing entry and the settlement's clearing balance.
func (s *Service) alreadyClearedResult(ctx context.Context, clubID string, res *VippsPayoutClearResult, bankRowID, entryID string, payoutBooking time.Time) *VippsPayoutClearResult {
	res.AlreadyCleared = true
	res.Ready = true
	res.BankEntryID = entryID
	res.ClearingBalance, _ = s.settlementClearingBalance(ctx, clubID, res.SettlementNumber, res.MSN, payoutBooking, bankRowID)
	return res
}

// bankRowEntry reads the entry a bank row is booked on, or "" if none.
func (s *Service) bankRowEntry(ctx context.Context, clubID, bankRowID string) string {
	var id *string
	_ = s.db.QueryRow(ctx,
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&id)
	if id == nil {
		return ""
	}
	return *id
}

// checkPayoutAmount compares a deposit with an imported payout row in øre.
func checkPayoutAmount(bankAmount, payoutAmount float64, settlement string) error {
	if math.Round(bankAmount*100) != math.Round(-payoutAmount*100) {
		return fmt.Errorf("%w: deposit %.2f, payout %.2f (Utb. %s)", ErrVippsPayoutAmountMismatch,
			bankAmount, -payoutAmount, settlement)
	}
	return nil
}

// payoutPeriods resolves the open periods for a payout clear's fee entry
// (when there is a fee to post) and bank entry. With periodExistingOnly (the
// payout sweep) it never creates one: a date with no fiscal year is *noFiscalYearError, and a period
// that is not open is *periodNotOpenError.
func payoutPeriods(ctx context.Context, q dbtx, clubID string, feeDate, bankDate time.Time, withFee bool, policy periodPolicy) (feePeriod, bankPeriod string, err error) {
	resolve := func(date time.Time, what string) (string, error) {
		if policy == periodMayCreate {
			id, status, err := resolvePeriodIn(ctx, q, clubID, date, "")
			if err != nil {
				return "", fmt.Errorf("resolving %s period: %w", what, err)
			}
			if status != "open" {
				// A person's clearing: a locked period gets its own text.
				return "", fmt.Errorf("clearing the Vipps %s: %w", what, periodNotOpenConfirmError(date.Year(), status))
			}
			return id, nil
		}
		id, err := existingOpenPeriodIn(ctx, q, clubID, date)
		if err != nil {
			return "", fmt.Errorf("%s: %w", what, err)
		}
		return id, nil
	}
	if withFee {
		feePeriod, err = resolve(feeDate, "fee")
		if err != nil {
			return "", "", err
		}
	}
	bankPeriod, err = resolve(bankDate, "payout")
	if err != nil {
		return "", "", err
	}
	return feePeriod, bankPeriod, nil
}

// checkSettlementClearable refuses a deposit whose settlement must be left to
// a human. A settlement clears once: a payout row still linked to a standing
// entry, or another booked deposit naming the same settlement, means this
// deposit is a second one (a bank correction, a re-exported statement whose
// row hash differs). And charges and fees carry only MSN + booking date, so
// two payouts sharing both cannot be told apart.
func checkSettlementClearable(ctx context.Context, q dbtx, clubID string, res *VippsPayoutClearResult, bankRowID string, payoutBooking time.Time, payoutEntry *string) error {
	cleared, err := settlementClearedElsewhere(ctx, q, clubID, res.SettlementNumber, res.MSN, bankRowID, payoutEntry)
	if err != nil {
		return err
	}
	if cleared {
		return fmt.Errorf("%w: Utb. %s Vippsnr %s", ErrVippsSettlementAlreadyCleared, res.SettlementNumber, res.MSN)
	}
	var sameDay int
	if err := q.QueryRow(ctx,
		`SELECT count(*) FROM vipps_import_rows
		  WHERE club_id = $1 AND msn = $2 AND row_type = 'payout' AND booking_date = $3::date`,
		clubID, res.MSN, payoutBooking,
	).Scan(&sameDay); err != nil {
		return fmt.Errorf("counting same-day payouts: %w", err)
	}
	if sameDay > 1 {
		return fmt.Errorf("%w: Vippsnr %s on %s", ErrVippsSettlementAmbiguous, res.MSN, payoutBooking.Format("2006-01-02"))
	}
	return nil
}

// settlementClearedElsewhere reports whether the settlement already has a
// standing clearing: its payout row linked to a non-voided entry, or a
// booked bank deposit other than bankRowID naming it. Descriptions are
// matched with VippsSettlementPattern in Go; settlement and MSN are digit
// runs, so the ILIKE prefilter carries no pattern characters.
func settlementClearedElsewhere(ctx context.Context, q dbtx, clubID, settlement, msn, bankRowID string, payoutEntry *string) (bool, error) {
	if payoutEntry != nil {
		var standing bool
		if err := q.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM journal_entries WHERE id = $1 AND club_id = $2 AND status <> 'voided')`,
			*payoutEntry, clubID,
		).Scan(&standing); err != nil {
			return false, fmt.Errorf("checking payout entry: %w", err)
		}
		if standing {
			return true, nil
		}
	}
	rows, err := q.Query(ctx,
		`SELECT b.description FROM bank_import_rows b
		   JOIN journal_entries je ON je.id = b.journal_entry_id AND je.club_id = b.club_id
		  WHERE b.club_id = $1 AND b.id <> $2 AND je.status <> 'voided'
		    AND b.description ILIKE '%' || $3 || '%'`,
		clubID, bankRowID, settlement,
	)
	if err != nil {
		return false, fmt.Errorf("checking booked deposits: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var desc string
		if err := rows.Scan(&desc); err != nil {
			return false, fmt.Errorf("scan booked deposit: %w", err)
		}
		if m := VippsSettlementPattern.FindStringSubmatch(desc); len(m) == 3 && m[1] == settlement && m[2] == msn {
			return true, nil
		}
	}
	return false, rows.Err()
}

// unbookedSettlementFees returns the ids of the settlement's fee rows that have
// not yet been journaled and the fee expense they net to. Amounts are summed
// signed: a deduction is stored negative and a refund of earlier fees positive,
// so the expense is the negated sum and a net refund comes back negative. Only
// the amount column counts: the Report API's per-settlement fee rows also
// carry the same fee in the fee column (a +/- pair with a zero amount), which
// would double count.
func unbookedSettlementFees(ctx context.Context, q dbtx, clubID, msn string, booking time.Time) (ids []string, expense float64, err error) {
	rows, err := q.Query(ctx,
		`SELECT id, amount FROM vipps_import_rows
		  WHERE club_id = $1 AND msn = $2 AND row_type = 'fee'
		    AND booking_date = $3::date AND journal_entry_id IS NULL`,
		clubID, msn, booking,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("loading fee rows: %w", err)
	}
	defer rows.Close()
	var sum float64
	for rows.Next() {
		var id string
		var amount float64
		if err := rows.Scan(&id, &amount); err != nil {
			return nil, 0, fmt.Errorf("scan fee row: %w", err)
		}
		ids = append(ids, id)
		sum += amount
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// Whole øre, so a sum of float cents does not carry dust into the entry.
	return ids, math.Round(-sum*100) / 100, nil
}

// linkFeeRowsTx points the fee rows at the entry that books them, guarded on
// them still being unlinked: another path linking some of them meanwhile
// means booking this total would book the fee twice.
func linkFeeRowsTx(ctx context.Context, tx pgx.Tx, clubID, entryID string, feeRows []string, settlement string) error {
	tag, err := tx.Exec(ctx,
		`UPDATE vipps_import_rows SET journal_entry_id = $1, auto_declined_at = NULL
		  WHERE club_id = $2 AND journal_entry_id IS NULL AND id = ANY($3::uuid[])`,
		entryID, clubID, feeRows,
	)
	if err != nil {
		return fmt.Errorf("linking fee rows: %w", err)
	}
	if tag.RowsAffected() != int64(len(feeRows)) {
		return fmt.Errorf("%w: fee for Utb. %s", ErrVippsPayoutRaced, settlement)
	}
	return nil
}

// checkSettlementNets refuses a clearing that would leave the clearing account
// off zero. It reads the posted balance of the settlement's entries inside the
// clearing transaction (settlementClearingBalanceIn, the figure the post-commit
// check reports, which only sees committed rows) and applies the fee entry
// (credit feeExpense) and bank entry (credit bankAmount) about to be posted.
func checkSettlementNets(ctx context.Context, q dbtx, clubID string, res *VippsPayoutClearResult, booking time.Time, bankRowID string, feeExpense, bankAmount float64) error {
	balance, err := settlementClearingBalanceIn(ctx, q, clubID, res.SettlementNumber, res.MSN, booking, bankRowID)
	if err != nil {
		return err
	}
	if !floatNear(feeExpense, 0, 0.005) {
		balance -= feeExpense
	}
	balance -= bankAmount
	if !floatNear(balance, 0, 0.005) {
		return fmt.Errorf("%w: Utb. %s Vippsnr %s would leave %.2f on the clearing account", ErrVippsSettlementNotNetting,
			res.SettlementNumber, res.MSN, balance)
	}
	return nil
}

// chargePostedSQL holds when the Vipps row aliased alias is booked on a
// posted entry: the only booking that is in the ledger.
func chargePostedSQL(alias string) string {
	return `EXISTS (SELECT 1 FROM journal_entries je WHERE je.id = ` + alias + `.journal_entry_id AND je.status = 'posted')`
}

// settlementClearingBalance sums debit-minus-credit on the club's Vipps
// clearing account across the posted journal entries belonging to the
// settlement — the charge, fee, and payout postings — so callers can assert
// it nets to ~0. Drafts are not in the ledger, and a voided original is left
// out with its reversal (which no row points at).
func (s *Service) settlementClearingBalance(ctx context.Context, clubID, settlement, msn string, booking time.Time, bankRowID string) (float64, error) {
	return settlementClearingBalanceIn(ctx, s.db, clubID, settlement, msn, booking, bankRowID)
}

// settlementClearingBalanceIn is settlementClearingBalance on the caller's
// querier, so a clearing transaction reads the ledger it holds locked.
func settlementClearingBalanceIn(ctx context.Context, q dbtx, clubID, settlement, msn string, booking time.Time, bankRowID string) (float64, error) {
	clearing, _, err := vippsAccountsIn(ctx, q, clubID)
	if err != nil {
		return 0, err
	}
	var balance float64
	err = q.QueryRow(ctx,
		`SELECT COALESCE(SUM(jl.debit - jl.credit), 0)
		   FROM journal_lines jl
		   JOIN accounts a ON a.id = jl.account_id
		   JOIN journal_entries je ON je.id = jl.journal_entry_id AND je.status = 'posted'
		  WHERE a.club_id = $1 AND a.code = $2
		    AND jl.journal_entry_id IN (
		      SELECT journal_entry_id FROM vipps_import_rows
		       WHERE club_id = $1 AND msn = $3 AND journal_entry_id IS NOT NULL
		         AND ( (row_type = 'payout' AND settlement_number = $4)
		            OR (row_type IN ('belastning','fee') AND booking_date = $5::date) )
		      UNION
		      SELECT journal_entry_id FROM bank_import_rows
		       WHERE id = $6 AND club_id = $1 AND journal_entry_id IS NOT NULL
		    )`,
		clubID, clearing, msn, settlement, booking, bankRowID,
	).Scan(&balance)
	if err != nil {
		return 0, fmt.Errorf("computing clearing balance: %w", err)
	}
	return balance, nil
}
