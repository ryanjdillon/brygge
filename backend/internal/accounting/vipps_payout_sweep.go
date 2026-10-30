package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"
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
	// deposit naming a settlement that was never imported or already cleared
	// by another deposit, several settlements sharing a booking date, or an
	// imported payout with no usable bank deposit carrying its reference.
	Skipped []VippsPayoutSweepItem `json:"skipped"`
	// Failed settlements were ready but posting refused (amount mismatch,
	// closed period, a concurrent clear); the next sweep retries them.
	Failed []VippsPayoutSweepItem `json:"failed"`
	// Error is set when the sweep itself could not run; the lists above are
	// then whatever it got through before stopping.
	Error string `json:"error,omitempty"`
}

// sweepFailedText is shown when the sweep could not run at all. The cause is
// logged; the treasurer sees that payouts were not cleared this time.
const sweepFailedText = "Automatisk avstemming av Vipps-utbetalinger feilet – prøv igjen, eller se loggen"

// ClearReadyVippsPayoutsReported runs ClearReadyVippsPayouts as the last step
// of a sync or import. A failure is logged and carried in the result's Error
// instead of failing the caller, whose own work is already done.
func (s *Service) ClearReadyVippsPayoutsReported(ctx context.Context, clubID, actor string) *VippsPayoutSweepResult {
	res, err := s.ClearReadyVippsPayouts(ctx, clubID, actor)
	if err != nil {
		s.log.Error().Err(err).Str("club_id", clubID).Msg("vipps payout sweep failed")
		if res == nil {
			res = newVippsPayoutSweepResult()
		}
		res.Error = sweepFailedText
	}
	return res
}

func newVippsPayoutSweepResult() *VippsPayoutSweepResult {
	return &VippsPayoutSweepResult{
		Cleared: []VippsPayoutSweepItem{},
		Waiting: []VippsPayoutSweepItem{},
		Skipped: []VippsPayoutSweepItem{},
		Failed:  []VippsPayoutSweepItem{},
	}
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
// On error the partial result is returned alongside it.
//
// Callers must sit behind the same gate as any other posting path.
func (s *Service) ClearReadyVippsPayouts(ctx context.Context, clubID, actor string) (*VippsPayoutSweepResult, error) {
	res := newVippsPayoutSweepResult()
	perCharge, err := s.VippsPerChargeEnabled(ctx, clubID)
	if err != nil {
		return res, fmt.Errorf("checking vipps model: %w", err)
	}
	if !perCharge {
		return res, nil
	}

	deposits, err := s.vippsPayoutDeposits(ctx, clubID)
	if err != nil {
		return res, err
	}

	seen := map[string]bool{}
	for _, d := range deposits {
		if d.booked || d.dismissed {
			continue
		}
		seen[d.key()] = true
		item := VippsPayoutSweepItem{SettlementNumber: d.settlement, MSN: d.msn, BankRowID: d.id}
		r, err := s.ClearVippsPayout(ctx, clubID, d.id, actor)
		switch {
		case err != nil:
			reason, skip := payoutSweepReason(err)
			item.Reason = reason
			if skip {
				res.Skipped = append(res.Skipped, item)
			} else {
				res.Failed = append(res.Failed, item)
				s.log.Warn().Err(err).Str("bank_row_id", d.id).Msg("vipps payout sweep: clearing failed")
			}
		case !r.Ready:
			if ref, rerr := s.vippsSettlementIsReference(ctx, clubID, d.msn, d.settlement); rerr == nil && ref {
				// A reference year's charges are drafts that never post.
				item.Reason, _ = payoutSweepReason(ErrReferencePeriod)
				res.Skipped = append(res.Skipped, item)
				break
			}
			item.Reason = waitingReason(r.UnreconciledCharges)
			res.Waiting = append(res.Waiting, item)
		case r.AlreadyCleared:
			// A concurrent clear won the bank row; nothing new was posted.
		default:
			res.Cleared = append(res.Cleared, item)
		}
	}

	skipped, err := s.payoutsWithoutOpenDeposit(ctx, clubID, deposits, seen)
	res.Skipped = append(res.Skipped, skipped...)
	return res, err
}

// payoutsWithoutOpenDeposit reports imported payouts that no open deposit
// names: the bank side is missing (not imported yet), was dismissed, or
// carries no settlement reference.
func (s *Service) payoutsWithoutOpenDeposit(ctx context.Context, clubID string, deposits []vippsPayoutDeposit, seen map[string]bool) ([]VippsPayoutSweepItem, error) {
	booked, dismissed := map[string]bool{}, map[string]bool{}
	for _, d := range deposits {
		if d.booked {
			booked[d.key()] = true
		} else if d.dismissed {
			dismissed[d.key()] = true
		}
	}
	prows, err := s.db.Query(ctx,
		`SELECT msn, settlement_number, amount, booking_date FROM vipps_import_rows
		  WHERE club_id = $1 AND row_type = 'payout'
		    AND journal_entry_id IS NULL AND dismissed_at IS NULL
		    AND settlement_number IS NOT NULL AND settlement_number <> ''
		    AND NOT `+referenceDateSQL("vipps_import_rows.club_id", "vipps_import_rows.booking_date")+`
		  ORDER BY booking_date, settlement_number`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing open vipps payouts: %w", err)
	}
	type openPayout struct {
		msn, settlement string
		amount          float64
		booking         time.Time
	}
	var open []openPayout
	for prows.Next() {
		var p openPayout
		if err := prows.Scan(&p.msn, &p.settlement, &p.amount, &p.booking); err != nil {
			prows.Close()
			return nil, fmt.Errorf("scanning open vipps payout: %w", err)
		}
		open = append(open, p)
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("listing open vipps payouts: %w", err)
	}

	var out []VippsPayoutSweepItem
	for _, p := range open {
		key := p.msn + "/" + p.settlement
		if seen[key] || booked[key] {
			continue
		}
		reason := fmt.Sprintf("ingen bankinnbetaling med «Utb. %s Vippsnr %s»", p.settlement, p.msn)
		if dismissed[key] {
			reason = fmt.Sprintf("bankinnbetalingen med «Utb. %s Vippsnr %s» er avvist i Tildel", p.settlement, p.msn)
		} else {
			// The deposit may be in the bank only as a provisional row from an
			// export taken while it was in flight; a fresh export of that day
			// brings the settled row with the reference.
			d, err := s.provisionalDepositDate(ctx, clubID, p.amount, p.booking)
			if err != nil {
				return out, err
			}
			if !d.IsZero() {
				day := d.Format("02.01.2006")
				reason = fmt.Sprintf("bare en foreløpig «Overførsel» %s – importer kontoutskriften for %s på nytt", day, day)
			}
		}
		out = append(out, VippsPayoutSweepItem{SettlementNumber: p.settlement, MSN: p.msn, Reason: reason})
	}
	return out, nil
}

type vippsPayoutDeposit struct {
	id, settlement, msn string
	booked, dismissed   bool
}

func (d vippsPayoutDeposit) key() string { return d.msn + "/" + d.settlement }

// vippsPayoutDeposits lists every bank row whose description carries a Vipps
// settlement reference, booked or not. The reference is parsed in Go with
// VippsSettlementPattern; imported values never become part of a SQL regex.
func (s *Service) vippsPayoutDeposits(ctx context.Context, clubID string) ([]vippsPayoutDeposit, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, description, journal_entry_id IS NOT NULL, dismissed_at IS NOT NULL
		   FROM bank_import_rows
		  WHERE club_id = $1
		    AND description ~* 'utb\.\s*[0-9]+\s+vippsnr\s+[0-9]+'
		    AND NOT `+referenceDateSQL("bank_import_rows.club_id", "bank_import_rows.row_date")+`
		  ORDER BY row_date, id`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing vipps payout deposits: %w", err)
	}
	defer rows.Close()
	var out []vippsPayoutDeposit
	for rows.Next() {
		var d vippsPayoutDeposit
		var desc string
		if err := rows.Scan(&d.id, &desc, &d.booked, &d.dismissed); err != nil {
			return nil, fmt.Errorf("scanning vipps payout deposit: %w", err)
		}
		m := VippsSettlementPattern.FindStringSubmatch(desc)
		if len(m) != 3 {
			continue
		}
		d.settlement, d.msn = m[1], m[2]
		out = append(out, d)
	}
	return out, rows.Err()
}

func waitingReason(n int) string {
	if n == 1 {
		return "1 belastning er ikke bokført (i Tildel, som utkast eller på et annullert bilag)"
	}
	return fmt.Sprintf("%d belastninger er ikke bokført (i Tildel, som utkast eller på et annullert bilag)", n)
}

// payoutSweepReason turns a ClearVippsPayout error into the reason shown to
// the treasurer, and whether the deposit is skipped (needs a human) rather
// than failed (retried by the next sweep). Unknown errors get a generic text;
// the cause is logged by the caller.
func payoutSweepReason(err error) (reason string, skip bool) {
	switch {
	case errors.Is(err, ErrReferencePeriod):
		return "oppgjøret hører til et referanseår: før beløpet i inngående balanse, og bokfør innbetalingen med et bilag", true
	case errors.Is(err, ErrVippsSettlementNotImported):
		return "Vipps-oppgjøret er ikke importert", true
	case errors.Is(err, ErrVippsSettlementAlreadyCleared):
		return "oppgjøret er allerede avstemt via en annen innbetaling", true
	case errors.Is(err, ErrVippsSettlementAmbiguous):
		return "flere Vipps-oppgjør samme dag på samme Vippsnr – avstem manuelt", true
	case errors.Is(err, ErrVippsPayoutAmountMismatch):
		return "beløpet på bankinnbetalingen stemmer ikke med Vipps-utbetalingen", false
	case errors.Is(err, ErrVippsPeriodClosed):
		return "regnskapsperioden er lukket", false
	case errors.Is(err, ErrVippsPayoutRaced):
		return "ble bokført samtidig av en annen handling – synkroniser igjen", false
	default:
		return "kunne ikke bokføres – se loggen", false
	}
}
