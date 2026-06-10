package accounting

import (
	"context"
	"fmt"
)

// VippsResyncResult summarizes one full-club Vipps reclassification run.
type VippsResyncResult struct {
	// Total draft Vipps bilags found.
	Scanned int `json:"scanned"`
	// Bilags re-classified — i.e. the previous version had a line on
	// 2900 or 3900 (i.e. fallback / clearing) and the cascade was
	// re-run.
	Resynced int `json:"resynced"`
	// Bilags left alone (already-classified, posted, or operator-
	// edited away from the cascade's template shape).
	Skipped int `json:"skipped"`
	// Bilags that the cascade refused to balance the second time —
	// rare, surfaced so the operator can hunt them.
	Failed   []string `json:"failed"`
}

// ResyncVippsBilags re-runs the Vipps classification cascade on every
// draft bilag in the club that still has a line crediting the legacy
// clearing account (2900) or the new fallback revenue account (3900).
// Operator-edited or posted bilags are left untouched. See DIL-367.
//
// Mechanism:
//   1. Find candidate draft bilags (source='vipps', status='draft',
//      any line credits 2900 or 3900).
//   2. For each: delete the journal entry (cascade-removes lines),
//      null the bank_import_row.journal_entry_id link.
//   3. Re-run ReconcileVippsPreview against the freed bank row and
//      ReconcileVippsConfirm to post the new draft.
func (s *Service) ResyncVippsBilags(ctx context.Context, clubID, createdBy string) (*VippsResyncResult, error) {
	// Initialize Failed as an empty (not nil) slice so JSON
	// serializes to [] rather than null — frontend templates that
	// touch `.length` shouldn't have to be null-safe.
	res := &VippsResyncResult{Failed: []string{}}

	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT je.id, je.source_id
		   FROM journal_entries je
		   JOIN journal_lines jl ON jl.journal_entry_id = je.id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1
		    AND je.source = 'vipps'
		    AND je.status = 'draft'
		    AND a.code IN ($2, $3)
		    AND je.source_table = 'bank_import_rows'
		    AND je.source_id IS NOT NULL`,
		clubID, vippsClearingAccountCode, vippsFallbackRevenueCode,
	)
	if err != nil {
		return nil, fmt.Errorf("listing candidates: %w", err)
	}
	defer rows.Close()

	type cand struct {
		entryID, bankRowID string
	}
	var work []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.entryID, &c.bankRowID); err != nil {
			continue
		}
		work = append(work, c)
	}
	rows.Close()

	for _, c := range work {
		res.Scanned++

		// Detach the bank row from the old draft entry first so the
		// fresh ReconcileVippsConfirm doesn't trip the "already linked"
		// guard. Then delete the entry; journal_lines cascade.
		if _, err := s.db.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id = NULL, auto_matched = false
			  WHERE id = $1 AND club_id = $2`,
			c.bankRowID, clubID,
		); err != nil {
			res.Failed = append(res.Failed, c.entryID)
			continue
		}
		// Unlink any Vipps rows attached to the old draft so the
		// cascade can re-tag them.
		_, _ = s.db.Exec(ctx,
			`UPDATE vipps_import_rows SET journal_entry_id = NULL
			  WHERE journal_entry_id = $1 AND club_id = $2`,
			c.entryID, clubID,
		)
		if _, err := s.db.Exec(ctx,
			`DELETE FROM journal_entries WHERE id = $1 AND club_id = $2 AND status = 'draft'`,
			c.entryID, clubID,
		); err != nil {
			res.Failed = append(res.Failed, c.entryID)
			continue
		}

		// Re-run the cascade and post a fresh draft.
		preview, err := s.ReconcileVippsPreview(ctx, clubID, c.bankRowID)
		if err != nil {
			res.Failed = append(res.Failed, c.bankRowID)
			continue
		}
		if !preview.Balanced {
			// Cascade still can't balance — leave the bank row
			// detached for manual handling.
			res.Skipped++
			continue
		}
		if _, err := s.ReconcileVippsConfirm(ctx, clubID, c.bankRowID, "", createdBy, preview.Lines); err != nil {
			res.Failed = append(res.Failed, c.bankRowID)
			continue
		}
		res.Resynced++
	}
	return res, nil
}
