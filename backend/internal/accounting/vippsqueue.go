package accounting

import (
	"context"
	"time"
)

// VippsQueueRow is an unresolved Vipps belastning line surfaced in the
// review queue (Admin → Accounting → Vipps avstemming). These are rows
// that landed in the fallback 3900 account and need manual classification.
type VippsQueueRow struct {
	ID           string    `json:"id"`
	TxAt         *time.Time `json:"tx_at,omitempty"`
	BookingDate  *string   `json:"booking_date,omitempty"`
	Amount       float64   `json:"amount"`
	CustomerName string    `json:"customer_name"`
	Message      string    `json:"message"`
	MSN          string    `json:"msn"`
	ImportID     string    `json:"import_id"`
}

// ListVippsQueue returns belastning rows that have no journal_entry_id yet,
// ordered by booking date descending. These are candidates for the review queue.
func (s *Service) ListVippsQueue(ctx context.Context, clubID string, limit int) ([]VippsQueueRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx,
		`SELECT vir.id, vir.tx_at, vir.booking_date::text, vir.amount,
		        vir.customer_name, vir.message, vir.msn, vir.vipps_import_id
		   FROM vipps_import_rows vir
		  WHERE vir.club_id = $1
		    AND vir.row_type = 'belastning'
		    AND vir.journal_entry_id IS NULL
		    AND NOT `+referenceDateSQL("vir.club_id", "COALESCE(vir.booking_date, vir.tx_at::date)")+`
		  ORDER BY vir.booking_date DESC NULLS LAST, vir.tx_at DESC NULLS LAST
		  LIMIT $2`,
		clubID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []VippsQueueRow
	for rows.Next() {
		var q VippsQueueRow
		if err := rows.Scan(&q.ID, &q.TxAt, &q.BookingDate, &q.Amount,
			&q.CustomerName, &q.Message, &q.MSN, &q.ImportID); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, nil
}
