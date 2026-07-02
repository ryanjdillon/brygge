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
