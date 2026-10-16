package accounting

import (
	"context"
	"fmt"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// VippsReportSyncResult summarises one Report API → vipps_import_rows sync.
type VippsReportSyncResult struct {
	ImportID   string
	LedgerID   string
	Rows       int  // rows produced from the report before dedup
	Imported   int  // rows newly inserted
	SkippedDup int  // rows skipped as duplicates
	TryLater   bool // report for the date not finalised yet
	Orders     *VippsOrderReconcileResult
}

// SyncVippsReport pulls the Report API v2 funds + fees for a settlement date and
// ingests them through the same idempotent pipeline as the Vipps CSV export
// (BRY-4). It backs both the operator-triggered endpoint and the nightly cron.
//
// actorID is the acting user; pass "" for an automated/system import (recorded
// as NULL imported_by). Pass ledgerID to skip resolution, or "" to use the
// merchant's first ledger. Re-running for the same date is idempotent (row_hash
// dedup), so recent days can be re-pulled safely.
func (s *Service) SyncVippsReport(ctx context.Context, vc *vipps.Client, clubID, actorID, ledgerID, date string) (VippsReportSyncResult, error) {
	var res VippsReportSyncResult
	if vc == nil {
		return res, fmt.Errorf("vipps client not configured")
	}

	if ledgerID == "" {
		ledgers, err := vc.LedgersParsed(ctx)
		if err != nil {
			return res, fmt.Errorf("resolve ledger: %w", err)
		}
		if len(ledgers) == 0 {
			return res, fmt.Errorf("no vipps ledgers for merchant")
		}
		ledgerID = ledgers[0].LedgerID
	}
	res.LedgerID = ledgerID

	funds, fundsTryLater, err := vc.FundsForDate(ctx, ledgerID, date)
	if err != nil {
		return res, fmt.Errorf("funds report: %w", err)
	}
	fees, feesTryLater, err := vc.FeesForDate(ctx, ledgerID, date)
	if err != nil {
		return res, fmt.Errorf("fees report: %w", err)
	}
	if fundsTryLater || feesTryLater {
		res.TryLater = true
		return res, nil
	}

	rows := VippsReportRows(vc.MSN(), funds, fees)
	res.Rows = len(rows)
	if len(rows) == 0 {
		return res, nil
	}

	// nil actor → NULL imported_by (automated import).
	var actor any
	if actorID != "" {
		actor = actorID
	}
	if err := s.DB().QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by, row_count)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		clubID, "Vipps Report API "+date, vc.MSN(), actor, len(rows),
	).Scan(&res.ImportID); err != nil {
		return res, fmt.Errorf("create import: %w", err)
	}

	imp, err := s.ImportVippsRows(ctx, clubID, res.ImportID, rows)
	if err != nil {
		return res, fmt.Errorf("import rows: %w", err)
	}
	res.Imported = imp.Imported
	res.SkippedDup = imp.SkippedDup

	// Book order-linked charges right away when someone is acting; the nightly
	// run has no user to record as creator, so it only repairs row types and
	// leaves the booking to the next operator sync.
	if actorID != "" {
		orders, err := s.AutoReconcileOrderCharges(ctx, clubID, actorID)
		if err != nil {
			return res, fmt.Errorf("booking order-linked charges: %w", err)
		}
		res.Orders = orders
	} else if _, err := s.RepairVippsReportRows(ctx, clubID); err != nil {
		return res, fmt.Errorf("repairing report rows: %w", err)
	}

	if _, err := s.DB().Exec(ctx,
		`UPDATE vipps_imports SET row_count = $1 WHERE id = $2`,
		imp.Imported, res.ImportID); err != nil {
		return res, fmt.Errorf("update import count: %w", err)
	}
	return res, nil
}
