package accounting

import (
	"context"
	"fmt"
	"time"
)

// ReconcileItem is a single line awaiting reconciliation in the Tildel queue,
// unified across sources. Today the queue is bank rows only; the Vipps
// per-charge model (BRY-224) adds one item per individual Vipps settlement
// charge so a batched payout is handled one payer at a time instead of as a
// single all-or-nothing bilag.
type ReconcileItem struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"` // "bank" | "vipps"
	Date        time.Time `json:"date"`
	Amount      float64   `json:"amount"`
	Counterpart string    `json:"counterpart"`
	Description string    `json:"description"`
	Reference   string    `json:"reference"` // arkivref (bank) or settlement number (vipps)
	// SuggestionCount is how many open unpaid invoices match this amount — the
	// same "worth opening?" hint bank rows carry. Advisory only.
	SuggestionCount int `json:"suggestion_count"`
}

// MissingVippsSettlement is an unmatched Vipps payout bank row whose settlement
// export has not been imported — so it cannot be split into member payments.
type MissingVippsSettlement struct {
	BankRowID        string    `json:"bank_row_id"`
	Date             time.Time `json:"date"`
	Amount           float64   `json:"amount"`
	SettlementNumber string    `json:"settlement_number"`
	MSN              string    `json:"msn"`
}

// ListMissingVippsSettlements finds unmatched "Utb. N Vippsnr M" bank rows whose
// settlement export hasn't been imported into vipps_import_rows. Each is a
// batched Vipps payout that can't be reconciled — so any faktura paid inside it
// still looks unpaid — until the treasurer imports that settlement report. This
// is the pre-purring / reconcile-view warning so the gap is visible in-app
// instead of discovered by hand. See BRY-223.
func (s *Service) ListMissingVippsSettlements(ctx context.Context, clubID string) ([]MissingVippsSettlement, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, row_date, amount, COALESCE(description, '')
		   FROM bank_import_rows
		  WHERE club_id = $1
		    AND journal_entry_id IS NULL
		    AND dismissed_at IS NULL
		    AND description ~* 'Utb\.\s*[0-9]+\s+Vippsnr\s+[0-9]+'
		  ORDER BY row_date`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("list vipps payout rows: %w", err)
	}

	type cand struct {
		id              string
		date            time.Time
		amount          float64
		settlement, msn string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		var desc string
		if err := rows.Scan(&c.id, &c.date, &c.amount, &desc); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan: %w", err)
		}
		m := VippsSettlementPattern.FindStringSubmatch(desc)
		if len(m) < 3 {
			continue
		}
		c.settlement, c.msn = m[1], m[2]
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate: %w", err)
	}

	out := make([]MissingVippsSettlement, 0)
	for _, c := range cands {
		var imported bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM vipps_import_rows
			                 WHERE club_id = $1 AND settlement_number = $2)`,
			clubID, c.settlement,
		).Scan(&imported); err != nil {
			return nil, fmt.Errorf("check settlement %s: %w", c.settlement, err)
		}
		if !imported {
			out = append(out, MissingVippsSettlement{
				BankRowID:        c.id,
				Date:             c.date,
				Amount:           c.amount,
				SettlementNumber: c.settlement,
				MSN:              c.msn,
			})
		}
	}
	return out, nil
}

// ListUnreconciledVippsCharges returns the individual Vipps settlement charges
// (belastning rows) not yet journaled, as unified reconcile items — the
// per-payer lines that will populate the Tildel queue under the per-charge
// model. Each carries the count of open unpaid invoices matching its amount.
// Read-only: no postings happen here (see BRY-226 for the accounting side).
func (s *Service) ListUnreconciledVippsCharges(ctx context.Context, clubID string) ([]ReconcileItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT vir.id,
		        COALESCE(vir.booking_date, vir.tx_at::date) AS d,
		        vir.amount,
		        COALESCE(vir.customer_name, ''),
		        COALESCE(vir.message, ''),
		        COALESCE(vir.settlement_number, ''),
		        (SELECT count(*) FROM invoices i
		          WHERE i.club_id = vir.club_id
		            AND i.status = 'open'
		            AND i.payment_id IS NULL
		            AND ABS(i.total_amount - vir.amount) < 0.005) AS suggestion_count
		   FROM vipps_import_rows vir
		  WHERE vir.club_id = $1
		    AND vir.row_type = $2
		    AND vir.journal_entry_id IS NULL
		    AND vir.dismissed_at IS NULL
		    AND vir.amount > 0
		  ORDER BY d NULLS LAST, vir.id`,
		clubID, string(VippsRowBelastning),
	)
	if err != nil {
		return nil, fmt.Errorf("list vipps charges: %w", err)
	}
	defer rows.Close()

	out := make([]ReconcileItem, 0)
	for rows.Next() {
		var it ReconcileItem
		var d *time.Time
		if err := rows.Scan(&it.ID, &d, &it.Amount, &it.Counterpart,
			&it.Description, &it.Reference, &it.SuggestionCount); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		it.Source = "vipps"
		if d != nil {
			it.Date = *d
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
