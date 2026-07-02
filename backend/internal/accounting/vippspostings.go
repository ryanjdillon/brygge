package accounting

import (
	"context"
	"fmt"
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
// equals the fee credit plus the net-payout credit. Every entry is a draft
// (mirroring the legacy Vipps reconcile), and each posting marks the source
// row's journal_entry_id so it is reconciled exactly once. Concurrent double
// bookings are caught by a guarded UPDATE ... WHERE journal_entry_id IS NULL:
// the loser deletes the draft it just created and returns the winner's entry.

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
	// Ready is false when the settlement still has unreconciled charges; no
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

// vippsAccounts returns the club's configured Vipps clearing and fee account
// codes, falling back to the model defaults (2900 / 7700) when unset.
func (s *Service) vippsAccounts(ctx context.Context, clubID string) (clearing, fee string, err error) {
	err = s.db.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(vipps_clearing_account, ''), $2),
		        COALESCE(NULLIF(vipps_fee_account, ''), $3)
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
	entryID      *string
}

// loadVippsBelastning reads one belastning row and validates it is an
// unbooked, positive customer payment. entryID is non-nil when the row has
// already been reconciled.
func (s *Service) loadVippsBelastning(ctx context.Context, clubID, rowID string) (*vippsBelastning, error) {
	var b vippsBelastning
	var rowType string
	err := s.db.QueryRow(ctx,
		`SELECT id, row_type, amount,
		        COALESCE(booking_date, tx_at::date),
		        COALESCE(customer_name, ''), COALESCE(message, ''),
		        journal_entry_id
		   FROM vipps_import_rows
		  WHERE id = $1 AND club_id = $2`,
		rowID, clubID,
	).Scan(&b.id, &rowType, &b.amount, &b.date, &b.customerName, &b.message, &b.entryID)
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

// postVippsCharge books one belastning as DR clearing / CR <creditAccount>,
// marks the row reconciled, and returns the entry. The guarded mark makes the
// operation safe against a concurrent second booking of the same charge.
func (s *Service) postVippsCharge(ctx context.Context, clubID, createdBy, creditAccount, description string, b *vippsBelastning) (string, error) {
	clearing, _, err := s.vippsAccounts(ctx, clubID)
	if err != nil {
		return "", err
	}

	periodID, periodStatus, err := s.resolvePeriod(ctx, clubID, b.date, "")
	if err != nil {
		return "", fmt.Errorf("resolving fiscal period: %w", err)
	}
	if periodStatus == "closed" {
		return "", fmt.Errorf("fiscal period %d is closed — reopen it or pick a different period", b.date.Year())
	}

	sourceID := b.id
	sourceTable := "vipps_import_rows"
	entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      b.date.Format("2006-01-02"),
		Description:    description,
		Source:         "vipps",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      createdBy,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: clearing, Debit: b.amount, Credit: 0, Description: description},
			{AccountCode: creditAccount, Debit: 0, Credit: b.amount, Description: description},
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating journal entry: %w", err)
	}

	tag, err := s.db.Exec(ctx,
		`UPDATE vipps_import_rows SET journal_entry_id = $1
		  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL`,
		entry.ID, b.id, clubID,
	)
	if err != nil {
		return "", fmt.Errorf("marking charge reconciled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Lost a race: the row was booked concurrently. Drop the draft we
		// just created and report the winning entry instead.
		_, _ = s.db.Exec(ctx,
			`DELETE FROM journal_entries WHERE id = $1 AND club_id = $2 AND status = 'draft'`,
			entry.ID, clubID,
		)
		var existing string
		if err := s.db.QueryRow(ctx,
			`SELECT journal_entry_id FROM vipps_import_rows WHERE id = $1 AND club_id = $2`,
			b.id, clubID,
		).Scan(&existing); err != nil {
			return "", fmt.Errorf("charge already reconciled but reading winner failed: %w", err)
		}
		return existing, errChargeRaced
	}
	return entry.ID, nil
}

// errChargeRaced signals that postVippsCharge lost the guarded-mark race and
// the returned entry ID belongs to the concurrent winner.
var errChargeRaced = fmt.Errorf("vipps charge booked concurrently")

// AssignVippsChargeToInvoice books a single Vipps belastning against an open
// invoice: DR clearing / CR 1500, marks the charge reconciled, and back-links
// invoices.payment_id so the faktura reads as paid. Idempotent per charge.
func (s *Service) AssignVippsChargeToInvoice(ctx context.Context, clubID, vippsRowID, invoiceID, createdBy string) (*VippsChargePostResult, error) {
	b, err := s.loadVippsBelastning(ctx, clubID, vippsRowID)
	if err != nil {
		return nil, err
	}
	if b.entryID != nil {
		return &VippsChargePostResult{EntryID: *b.entryID, AlreadyReconciled: true}, nil
	}

	// Validate the target invoice is a payable match. Requiring the amounts
	// to agree keeps the payment_id back-link honest — a differently priced
	// invoice should go through Lag bilag, not be silently marked paid.
	var (
		invUserID  *string
		invAmount  float64
		invPayment *string
		invStatus  string
	)
	err = s.db.QueryRow(ctx,
		`SELECT user_id, total_amount, payment_id, status
		   FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID,
	).Scan(&invUserID, &invAmount, &invPayment, &invStatus)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("invoice not found")
	}
	if err != nil {
		return nil, fmt.Errorf("loading invoice: %w", err)
	}
	if invPayment != nil {
		return nil, fmt.Errorf("invoice already has a payment linked")
	}
	if invStatus == "voided" {
		return nil, fmt.Errorf("invoice is voided")
	}
	if !floatNear(invAmount, b.amount, 1.00) {
		return nil, fmt.Errorf("charge %.2f does not match invoice total %.2f — use Lag bilag instead", b.amount, invAmount)
	}

	desc := fmt.Sprintf("Vipps innbetaling: %s", b.customerName)
	entryID, err := s.postVippsCharge(ctx, clubID, createdBy, receivablesAccountCode, desc, b)
	if err == errChargeRaced {
		return &VippsChargePostResult{EntryID: entryID, AlreadyReconciled: true}, nil
	}
	if err != nil {
		return nil, err
	}

	// Back-link the invoice payment. Best-effort: the GL is already correct
	// if this fails, mirroring the legacy reconcile path.
	if err := s.linkInvoicePayment(ctx, clubID, invoiceID, b.date); err != nil {
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
// payments with no faktura. Idempotent per charge.
func (s *Service) CreateVippsChargeBilag(ctx context.Context, clubID, vippsRowID, accountCode, note, createdBy string) (*VippsChargePostResult, error) {
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

	desc := fmt.Sprintf("Vipps innbetaling: %s", b.customerName)
	if note != "" {
		desc = fmt.Sprintf("%s — %s", desc, note)
	}
	entryID, err := s.postVippsCharge(ctx, clubID, createdBy, accountCode, desc, b)
	if err == errChargeRaced {
		return &VippsChargePostResult{EntryID: entryID, AlreadyReconciled: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return &VippsChargePostResult{EntryID: entryID}, nil
}

// ClearVippsPayout performs Stage 2 for the settlement named by a bank payout
// row: it books the per-settlement fee (DR gebyr / CR clearing) and the bank
// payout (DR bank / CR clearing), keyed on the settlement number parsed from
// the bank row description. It refuses to clear until every belastning charge
// in the settlement has been booked — so the clearing account nets to ~0 — and
// is idempotent: an already-cleared bank row returns its existing entries.
func (s *Service) ClearVippsPayout(ctx context.Context, clubID, bankRowID, createdBy string) (*VippsPayoutClearResult, error) {
	var (
		bankDate    time.Time
		description string
		bankAmount  float64
		bankAccount string
		bankEntry   *string
	)
	err := s.db.QueryRow(ctx,
		`SELECT bir.row_date, bir.description, bir.amount, bi.bank_account_code, bir.journal_entry_id
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.id = $1 AND bir.club_id = $2`,
		bankRowID, clubID,
	).Scan(&bankDate, &description, &bankAmount, &bankAccount, &bankEntry)
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
		  LIMIT 1`,
		clubID, res.MSN, res.SettlementNumber,
	).Scan(&payoutRowID, &payoutBooking, &payoutAmount, &payoutEntry)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("no imported settlement for Utb. %s Vippsnr %s — import the Vipps export first", res.SettlementNumber, res.MSN)
	}
	if err != nil {
		return nil, fmt.Errorf("loading payout row: %w", err)
	}

	// If the bank row is already linked, the settlement is cleared. Report the
	// existing entries and the clearing balance so callers stay idempotent.
	if bankEntry != nil {
		res.AlreadyCleared = true
		res.Ready = true
		res.BankEntryID = *bankEntry
		res.ClearingBalance, _ = s.settlementClearingBalance(ctx, clubID, res.SettlementNumber, res.MSN, payoutBooking, bankRowID)
		return res, nil
	}

	// Gate: every belastning in the settlement must be booked first.
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM vipps_import_rows
		  WHERE club_id = $1 AND msn = $2 AND row_type = 'belastning'
		    AND booking_date = $3::date AND journal_entry_id IS NULL`,
		clubID, res.MSN, payoutBooking,
	).Scan(&res.UnreconciledCharges); err != nil {
		return nil, fmt.Errorf("counting unreconciled charges: %w", err)
	}
	if res.UnreconciledCharges > 0 {
		return res, nil // not ready — leave the payout in the queue
	}
	res.Ready = true

	clearing, feeAccount, err := s.vippsAccounts(ctx, clubID)
	if err != nil {
		return nil, err
	}

	// Fee entry (DR gebyr / CR clearing) for the settlement's still-unbooked
	// fee rows. Dated at the settlement booking date.
	feeRows, feeTotal, err := s.unbookedSettlementFees(ctx, clubID, res.MSN, payoutBooking)
	if err != nil {
		return nil, err
	}
	if feeTotal > 0.005 {
		feePeriod, feeStatus, perr := s.resolvePeriod(ctx, clubID, payoutBooking, "")
		if perr != nil {
			return nil, fmt.Errorf("resolving fee period: %w", perr)
		}
		if feeStatus == "closed" {
			return nil, fmt.Errorf("fiscal period %d is closed — cannot post Vipps fee", payoutBooking.Year())
		}
		feeSource := payoutRowID
		feeTable := "vipps_import_rows"
		feeDesc := fmt.Sprintf("Vipps gebyr oppgjør %s", res.SettlementNumber)
		feeJE, cerr := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: feePeriod,
			EntryDate:      payoutBooking.Format("2006-01-02"),
			Description:    feeDesc,
			Source:         "vipps",
			SourceID:       &feeSource,
			SourceTable:    &feeTable,
			CreatedBy:      createdBy,
			ClubID:         clubID,
			Lines: []CreateJournalLineInput{
				{AccountCode: feeAccount, Debit: feeTotal, Credit: 0, Description: feeDesc},
				{AccountCode: clearing, Debit: 0, Credit: feeTotal, Description: feeDesc},
			},
		})
		if cerr != nil {
			return nil, fmt.Errorf("creating fee entry: %w", cerr)
		}
		res.FeeEntryID = feeJE.ID
		for _, id := range feeRows {
			_, _ = s.db.Exec(ctx,
				`UPDATE vipps_import_rows SET journal_entry_id = $1
				  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL`,
				feeJE.ID, id, clubID,
			)
		}
	}

	// Bank entry (DR bank / CR clearing) for the net payout, dated at the bank
	// value date. The guarded mark protects against a concurrent clear.
	bankPeriod, bankStatus, perr := s.resolvePeriod(ctx, clubID, bankDate, "")
	if perr != nil {
		return nil, fmt.Errorf("resolving bank period: %w", perr)
	}
	if bankStatus == "closed" {
		return nil, fmt.Errorf("fiscal period %d is closed — cannot post Vipps payout", bankDate.Year())
	}
	bankSource := bankRowID
	bankTable := "bank_import_rows"
	bankDesc := fmt.Sprintf("Vipps utbetaling %s (Vippsnr %s)", res.SettlementNumber, res.MSN)
	bankJE, cerr := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
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
	tag, uerr := s.db.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true
		  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL`,
		bankJE.ID, bankRowID, clubID,
	)
	if uerr != nil {
		return nil, fmt.Errorf("linking bank row: %w", uerr)
	}
	if tag.RowsAffected() == 0 {
		// Concurrent clear won — drop our draft and report the winner.
		_, _ = s.db.Exec(ctx,
			`DELETE FROM journal_entries WHERE id = $1 AND club_id = $2 AND status = 'draft'`,
			bankJE.ID, clubID,
		)
		res.AlreadyCleared = true
		_ = s.db.QueryRow(ctx,
			`SELECT journal_entry_id FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
			bankRowID, clubID,
		).Scan(&res.BankEntryID)
	} else {
		res.BankEntryID = bankJE.ID
		// Mark the payout row reconciled against the bank entry.
		if payoutEntry == nil {
			_, _ = s.db.Exec(ctx,
				`UPDATE vipps_import_rows SET journal_entry_id = $1
				  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL`,
				bankJE.ID, payoutRowID, clubID,
			)
		}
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

// unbookedSettlementFees returns the ids and summed absolute amount of the
// settlement's fee rows that have not yet been journaled. Fee-row amounts are
// stored negative (a deduction), so the total is negated to a positive expense.
func (s *Service) unbookedSettlementFees(ctx context.Context, clubID, msn string, booking time.Time) (ids []string, total float64, err error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, amount FROM vipps_import_rows
		  WHERE club_id = $1 AND msn = $2 AND row_type = 'fee'
		    AND booking_date = $3::date AND journal_entry_id IS NULL`,
		clubID, msn, booking,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("loading fee rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var amount float64
		if err := rows.Scan(&id, &amount); err != nil {
			return nil, 0, fmt.Errorf("scan fee row: %w", err)
		}
		if amount < 0 {
			amount = -amount
		}
		ids = append(ids, id)
		total += amount
	}
	return ids, total, rows.Err()
}

// settlementClearingBalance sums debit-minus-credit on the club's Vipps
// clearing account across every journal entry belonging to the settlement —
// the charge, fee, and payout postings — so callers can assert it nets to ~0.
func (s *Service) settlementClearingBalance(ctx context.Context, clubID, settlement, msn string, booking time.Time, bankRowID string) (float64, error) {
	clearing, _, err := s.vippsAccounts(ctx, clubID)
	if err != nil {
		return 0, err
	}
	var balance float64
	err = s.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(jl.debit - jl.credit), 0)
		   FROM journal_lines jl
		   JOIN accounts a ON a.id = jl.account_id
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
