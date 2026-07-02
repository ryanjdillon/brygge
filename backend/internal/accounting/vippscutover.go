package accounting

import (
	"context"
	"fmt"
	"time"
)

// Cutover from the legacy single-bilag Vipps reconciliation to the per-charge
// model (BRY-228). Per the user directive, apart from matched-faktura payment
// links, the auto-generated Vipps bilags carry no state worth preserving — so
// the cutover simply regenerates them under the per-charge model rather than
// migrating them in place.
//
// Only OPEN-period reconciliations are regenerated: posting into a closed
// period is refused, so closed periods stay on the legacy single-bilag form
// (their bilags are left exactly as they are). This is the documented boundary.
//
// Faktura links are preserved by construction: the regeneration re-posts each
// charge to the SAME account the legacy bilag credited it to (paired by
// amount), and never touches invoices.payment_id or the payments rows. A charge
// the legacy bilag booked to receivables (1500) is re-posted DR clearing / CR
// 1500, and the already-set invoices.payment_id stays put.

// VippsCutoverResult summarises one regeneration run.
type VippsCutoverResult struct {
	DryRun              bool     `json:"dry_run"`
	ScannedBilags       int      `json:"scanned_bilags"`
	Regenerated         int      `json:"regenerated"`
	ChargesBooked       int      `json:"charges_booked"`
	ReceivableCharges   int      `json:"receivable_charges"`
	SkippedClosedPeriod int      `json:"skipped_closed_period"`
	Failed              []string `json:"failed"`
}

// VippsPerChargeEnabled reports whether the club is on the per-charge Vipps
// reconciliation model.
func (s *Service) VippsPerChargeEnabled(ctx context.Context, clubID string) (bool, error) {
	var enabled bool
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(vipps_per_charge_enabled, false) FROM clubs WHERE id = $1`,
		clubID,
	).Scan(&enabled)
	if err != nil {
		return false, fmt.Errorf("loading vipps_per_charge_enabled: %w", err)
	}
	return enabled, nil
}

type cutoverCharge struct {
	account string
	amount  float64
}

// RegenerateOpenPeriodVippsReconciliations reverses each legacy single-bilag
// Vipps reconciliation in an OPEN fiscal period and re-posts it under the
// per-charge model through the clearing account, preserving faktura payment
// links. Closed-period bilags are counted but left untouched. With dryRun the
// candidates are scanned and counted but nothing is written.
//
// This is a supervised one-time cutover: run with dryRun first to see the
// counts, then again to write. It is safe to re-run — already-regenerated
// settlements no longer have a legacy bilag to pick up.
func (s *Service) RegenerateOpenPeriodVippsReconciliations(ctx context.Context, clubID, createdBy string, dryRun bool) (*VippsCutoverResult, error) {
	res := &VippsCutoverResult{DryRun: dryRun, Failed: []string{}}

	// A legacy single-bilag credits revenue/receivable and never touches the
	// clearing account. The per-charge bank-payout entry this cutover creates
	// shares source='vipps'/source_table='bank_import_rows' but its only credit
	// is TO the clearing account — so excluding clearing-credit entries keeps
	// the scan from re-detecting its own output, making re-runs idempotent.
	const notPerChargeEntry = `
		    AND NOT EXISTS (
		      SELECT 1 FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		       WHERE jl.journal_entry_id = je.id AND jl.credit > 0
		         AND a.code = COALESCE(NULLIF(
		               (SELECT vipps_clearing_account FROM clubs WHERE id = $1), ''), '2900')
		    )`

	// Count closed-period legacy bilags so the operator sees what is being
	// deliberately left on the old model.
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM journal_entries je
		   JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		  WHERE je.club_id = $1 AND je.source = 'vipps'
		    AND je.source_table = 'bank_import_rows' AND je.source_id IS NOT NULL
		    AND je.status IN ('draft','posted')
		    AND fp.status = 'closed'`+notPerChargeEntry,
		clubID,
	).Scan(&res.SkippedClosedPeriod); err != nil {
		return nil, fmt.Errorf("counting closed-period bilags: %w", err)
	}

	rows, err := s.db.Query(ctx,
		`SELECT je.id, je.source_id, je.status
		   FROM journal_entries je
		   JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		  WHERE je.club_id = $1 AND je.source = 'vipps'
		    AND je.source_table = 'bank_import_rows' AND je.source_id IS NOT NULL
		    AND je.status IN ('draft','posted')
		    AND fp.status = 'open'`+notPerChargeEntry+`
		  ORDER BY je.entry_date, je.id`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing open-period bilags: %w", err)
	}
	type cand struct{ entryID, bankRowID, status string }
	var work []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.entryID, &c.bankRowID, &c.status); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		work = append(work, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidates: %w", err)
	}

	for _, c := range work {
		res.ScannedBilags++

		charges, err := s.loadBilagCharges(ctx, c.entryID)
		if err != nil {
			res.Failed = append(res.Failed, c.bankRowID)
			continue
		}

		if dryRun {
			res.Regenerated++
			res.ChargesBooked += len(charges)
			for _, ch := range charges {
				if ch.account == receivablesAccountCode {
					res.ReceivableCharges++
				}
			}
			continue
		}

		booked, receivable, err := s.regenerateOneSettlement(ctx, clubID, createdBy, c.entryID, c.bankRowID, c.status, charges)
		if err != nil {
			s.log.Warn().Err(err).
				Str("entry_id", c.entryID).
				Str("bank_row_id", c.bankRowID).
				Msg("vipps cutover: settlement regeneration failed")
			res.Failed = append(res.Failed, c.bankRowID)
			continue
		}
		res.Regenerated++
		res.ChargesBooked += booked
		res.ReceivableCharges += receivable
	}
	return res, nil
}

// loadBilagCharges returns the per-charge credit lines of a legacy Vipps bilag
// — every credit line is one belastning charge (bank + fee are the debit side).
func (s *Service) loadBilagCharges(ctx context.Context, entryID string) ([]cutoverCharge, error) {
	rows, err := s.db.Query(ctx,
		`SELECT a.code, jl.credit
		   FROM journal_lines jl
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1 AND jl.credit > 0
		  ORDER BY jl.credit DESC`,
		entryID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading bilag charges: %w", err)
	}
	defer rows.Close()
	var out []cutoverCharge
	for rows.Next() {
		var ch cutoverCharge
		if err := rows.Scan(&ch.account, &ch.amount); err != nil {
			return nil, fmt.Errorf("scan charge line: %w", err)
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// regenerateOneSettlement reverses one legacy bilag and re-posts its settlement
// per-charge. Returns the count of charges booked and how many landed on
// receivables (faktura-linked).
func (s *Service) regenerateOneSettlement(ctx context.Context, clubID, createdBy, entryID, bankRowID, status string, charges []cutoverCharge) (booked, receivable int, err error) {
	// Reverse the legacy bilag. Drafts are deleted outright; posted entries are
	// voided (leaving the original + a reversal for the audit trail).
	if status == "posted" {
		if _, verr := s.VoidJournalEntry(ctx, entryID, createdBy); verr != nil {
			return 0, 0, fmt.Errorf("voiding legacy bilag: %w", verr)
		}
	}
	// Detach the settlement's rows from the legacy entry so they can be
	// re-booked. invoices.payment_id is deliberately NOT touched.
	if _, derr := s.db.Exec(ctx,
		`UPDATE vipps_import_rows SET journal_entry_id = NULL
		  WHERE journal_entry_id = $1 AND club_id = $2`,
		entryID, clubID,
	); derr != nil {
		return 0, 0, fmt.Errorf("detaching vipps rows: %w", derr)
	}
	if _, derr := s.db.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = NULL, auto_matched = false
		  WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	); derr != nil {
		return 0, 0, fmt.Errorf("detaching bank row: %w", derr)
	}
	if status != "posted" {
		if _, derr := s.db.Exec(ctx,
			`DELETE FROM journal_entries WHERE id = $1 AND club_id = $2 AND status = 'draft'`,
			entryID, clubID,
		); derr != nil {
			return 0, 0, fmt.Errorf("deleting legacy draft: %w", derr)
		}
	}

	// Resolve the settlement group from the bank row.
	settlement, msn, booking, perr := s.settlementForBankRow(ctx, clubID, bankRowID)
	if perr != nil {
		return 0, 0, perr
	}

	// Load the now-detached belastning rows and pair each to a legacy credit
	// line by amount, re-posting DR clearing / CR <same account>.
	belastnings, berr := s.detachedBelastninger(ctx, clubID, msn, booking)
	if berr != nil {
		return 0, 0, berr
	}
	remaining := append([]cutoverCharge(nil), charges...)
	for _, b := range belastnings {
		account := popChargeByAmount(&remaining, b.amount)
		if account == "" {
			// No matching legacy credit line — book to the fallback revenue
			// account so the charge still clears and the payout can net.
			account = vippsFallbackRevenueCode
		}
		bel := b // capture
		desc := fmt.Sprintf("Vipps innbetaling: %s", bel.customerName)
		if _, err := s.postVippsCharge(ctx, clubID, createdBy, account, desc, &bel); err != nil {
			if err == errChargeRaced {
				continue
			}
			return booked, receivable, fmt.Errorf("re-posting charge %s: %w", bel.id, err)
		}
		booked++
		if account == receivablesAccountCode {
			receivable++
		}
	}

	// Book the fee + bank payout through the clearing account.
	if _, err := s.ClearVippsPayout(ctx, clubID, bankRowID, createdBy); err != nil {
		return booked, receivable, fmt.Errorf("clearing payout: %w", err)
	}
	_ = settlement
	return booked, receivable, nil
}

// settlementForBankRow parses the settlement/MSN from a Vipps payout bank row
// and returns them plus the settlement's booking date (from the payout row).
func (s *Service) settlementForBankRow(ctx context.Context, clubID, bankRowID string) (settlement, msn string, booking time.Time, err error) {
	var description string
	if e := s.db.QueryRow(ctx,
		`SELECT description FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&description); e != nil {
		return "", "", time.Time{}, fmt.Errorf("loading bank row: %w", e)
	}
	m := VippsSettlementPattern.FindStringSubmatch(description)
	if len(m) != 3 {
		return "", "", time.Time{}, fmt.Errorf("bank row is not a Vipps payout: %q", description)
	}
	settlement, msn = m[1], m[2]
	if e := s.db.QueryRow(ctx,
		`SELECT booking_date FROM vipps_import_rows
		  WHERE club_id = $1 AND msn = $2 AND row_type = 'payout' AND settlement_number = $3
		  LIMIT 1`,
		clubID, msn, settlement,
	).Scan(&booking); e != nil {
		return "", "", time.Time{}, fmt.Errorf("loading payout row for settlement %s: %w", settlement, e)
	}
	return settlement, msn, booking, nil
}

// detachedBelastninger loads the settlement's unbooked, non-dismissed charges.
func (s *Service) detachedBelastninger(ctx context.Context, clubID, msn string, booking time.Time) ([]vippsBelastning, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, amount, COALESCE(booking_date, tx_at::date),
		        COALESCE(customer_name, ''), COALESCE(message, '')
		   FROM vipps_import_rows
		  WHERE club_id = $1 AND msn = $2 AND row_type = 'belastning'
		    AND booking_date = $3::date
		    AND journal_entry_id IS NULL AND dismissed_at IS NULL
		  ORDER BY amount DESC, id`,
		clubID, msn, booking,
	)
	if err != nil {
		return nil, fmt.Errorf("loading detached charges: %w", err)
	}
	defer rows.Close()
	var out []vippsBelastning
	for rows.Next() {
		var b vippsBelastning
		if err := rows.Scan(&b.id, &b.amount, &b.date, &b.customerName, &b.message); err != nil {
			return nil, fmt.Errorf("scan charge: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// popChargeByAmount removes and returns the account of the first legacy credit
// line whose amount matches, or "" when none does.
func popChargeByAmount(charges *[]cutoverCharge, amount float64) string {
	for i, ch := range *charges {
		if floatNear(ch.amount, amount, 0.005) {
			account := ch.account
			*charges = append((*charges)[:i], (*charges)[i+1:]...)
			return account
		}
	}
	return ""
}
