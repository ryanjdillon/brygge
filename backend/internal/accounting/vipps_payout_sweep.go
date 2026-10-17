package accounting

import (
	"context"
	"errors"
	"fmt"
)

// VippsPayoutSweepItem is one settlement a payout sweep looked at.
type VippsPayoutSweepItem struct {
	SettlementNumber string `json:"settlement_number"`
	MSN              string `json:"msn"`
	BankRowID        string `json:"bank_row_id,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

// VippsPayoutSweepResult summarises one ClearReadyVippsPayouts run.
type VippsPayoutSweepResult struct {
	// Cleared settlements had their fee and bank entries posted this run.
	Cleared []VippsPayoutSweepItem `json:"cleared"`
	// Waiting settlements still have charges to book in Tildel.
	Waiting []VippsPayoutSweepItem `json:"waiting"`
	// Skipped settlements cannot be cleared from what is imported: a bank
	// deposit naming a settlement that was never imported, or an imported
	// payout with no bank deposit carrying its Utb./Vippsnr reference.
	Skipped []VippsPayoutSweepItem `json:"skipped"`
	// Failed settlements were ready but posting refused (closed period, a
	// concurrent clear); the next sweep retries them.
	Failed []VippsPayoutSweepItem `json:"failed"`
}

// ClearReadyVippsPayouts runs Stage 2 of the per-charge model on its own:
// every unbooked bank deposit carrying a Vipps settlement reference
// (Utb. <n> Vippsnr <msn>) whose settlement has every charge booked or
// dismissed is cleared through ClearVippsPayout. Settlements still waiting on
// charges are left for a later sweep; a failure on one deposit is recorded
// against it and the sweep moves on. Imported payouts with no matching bank
// deposit are reported as skipped so the treasurer can see why they stay
// open. Already-linked deposits are never candidates, so re-running is a
// no-op. Clubs on the legacy single-bilag model get an empty result: their
// payouts are matched by syncVippsReconciliations instead.
//
// Callers must sit behind the same gate as any other posting path.
func (s *Service) ClearReadyVippsPayouts(ctx context.Context, clubID, actor string) (*VippsPayoutSweepResult, error) {
	res := &VippsPayoutSweepResult{
		Cleared: []VippsPayoutSweepItem{},
		Waiting: []VippsPayoutSweepItem{},
		Skipped: []VippsPayoutSweepItem{},
		Failed:  []VippsPayoutSweepItem{},
	}
	perCharge, err := s.VippsPerChargeEnabled(ctx, clubID)
	if err != nil {
		return nil, fmt.Errorf("checking vipps model: %w", err)
	}
	if !perCharge {
		return res, nil
	}

	rows, err := s.db.Query(ctx,
		`SELECT id, description FROM bank_import_rows
		  WHERE club_id = $1 AND journal_entry_id IS NULL AND dismissed_at IS NULL
		    AND description ~* 'utb\.\s*[0-9]+\s+vippsnr\s+[0-9]+'
		  ORDER BY row_date, id`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing vipps payout deposits: %w", err)
	}
	type deposit struct{ id, settlement, msn string }
	var deposits []deposit
	for rows.Next() {
		var id, desc string
		if err := rows.Scan(&id, &desc); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning vipps payout deposit: %w", err)
		}
		if m := VippsSettlementPattern.FindStringSubmatch(desc); len(m) == 3 {
			deposits = append(deposits, deposit{id: id, settlement: m[1], msn: m[2]})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing vipps payout deposits: %w", err)
	}

	seen := map[string]bool{}
	for _, d := range deposits {
		seen[d.msn+"/"+d.settlement] = true
		item := VippsPayoutSweepItem{SettlementNumber: d.settlement, MSN: d.msn, BankRowID: d.id}
		r, err := s.ClearVippsPayout(ctx, clubID, d.id, actor)
		switch {
		case errors.Is(err, ErrVippsSettlementNotImported):
			item.Reason = "Vipps-oppgjøret er ikke importert"
			res.Skipped = append(res.Skipped, item)
		case err != nil:
			item.Reason = err.Error()
			res.Failed = append(res.Failed, item)
			s.log.Warn().Err(err).Str("bank_row_id", d.id).Msg("vipps payout sweep: clearing failed")
		case !r.Ready:
			item.Reason = fmt.Sprintf("%d belastninger venter i Tildel", r.UnreconciledCharges)
			res.Waiting = append(res.Waiting, item)
		case r.AlreadyCleared:
			// A concurrent clear won the bank row; nothing new was posted.
		default:
			res.Cleared = append(res.Cleared, item)
		}
	}

	// Payouts imported from Vipps that no deposit above names: the bank side
	// is missing (not imported yet) or carries no settlement reference.
	prows, err := s.db.Query(ctx,
		`SELECT msn, settlement_number FROM vipps_import_rows
		  WHERE club_id = $1 AND row_type = 'payout'
		    AND journal_entry_id IS NULL AND dismissed_at IS NULL
		    AND settlement_number IS NOT NULL AND settlement_number <> ''
		    AND NOT EXISTS (
		        SELECT 1 FROM bank_import_rows b
		         WHERE b.club_id = vipps_import_rows.club_id
		           AND b.journal_entry_id IS NOT NULL
		           AND b.description ~* ('utb\.\s*' || settlement_number || '\s+vippsnr\s+' || msn || '([^0-9]|$)'))
		  ORDER BY booking_date, settlement_number`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing open vipps payouts: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var msn, settlement string
		if err := prows.Scan(&msn, &settlement); err != nil {
			return nil, fmt.Errorf("scanning open vipps payout: %w", err)
		}
		if seen[msn+"/"+settlement] {
			continue
		}
		res.Skipped = append(res.Skipped, VippsPayoutSweepItem{
			SettlementNumber: settlement, MSN: msn,
			Reason: fmt.Sprintf("ingen bankinnbetaling med «Utb. %s Vippsnr %s»", settlement, msn),
		})
	}
	return res, prows.Err()
}
