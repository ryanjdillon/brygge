package accounting

import (
	"context"
	"errors"
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
	AutoAssign *AutoAssignResult
	Payouts    *VippsPayoutSweepResult
	// BookingDeferred is true when the booking slot stayed busy past the
	// person's wait: the rows are imported, nothing was booked, and the next
	// sync books them.
	BookingDeferred bool
}

// VippsReportSource is the part of the Vipps Report API the settlement sync
// reads. *vipps.Client implements it; tests substitute a fake so the sync runs
// without the network. Callers holding a possibly-nil client build the source
// with NewVippsReportSource, never by assigning a *vipps.Client directly, so a
// missing client reaches the sync as a nil interface.
type VippsReportSource interface {
	MSN() string
	LedgersParsed(ctx context.Context) ([]vipps.Ledger, error)
	FundsForDate(ctx context.Context, ledgerID, date string) (entries []vipps.FundsEntry, tryLater bool, err error)
	FeesForDate(ctx context.Context, ledgerID, date string) (entries []vipps.FeeEntry, tryLater bool, err error)
}

var _ VippsReportSource = (*vipps.Client)(nil)

// NewVippsReportSource wraps a Vipps client as a report source. A nil client
// yields a nil interface: a typed-nil *vipps.Client stored in the interface
// would compare non-nil and pass SyncVippsReport's "not configured" guard.
func NewVippsReportSource(c *vipps.Client) VippsReportSource {
	if c == nil {
		return nil
	}
	return c
}

// SyncVippsReport pulls the Report API v2 funds + fees for a settlement date and
// ingests them through the same idempotent pipeline as the Vipps CSV export
// (BRY-4). It backs both the operator-triggered endpoint and the nightly cron.
//
// actorID is the acting user; pass "" for an automated/system import (recorded
// as NULL imported_by). Pass ledgerID to skip resolution, or "" to use the
// merchant's first ledger. Re-running for the same date is idempotent (row_hash
// dedup), so recent days can be re-pulled safely.
func (s *Service) SyncVippsReport(ctx context.Context, src VippsReportSource, clubID, actorID, ledgerID, date string) (VippsReportSyncResult, error) {
	var res VippsReportSyncResult
	if src == nil {
		return res, fmt.Errorf("vipps client not configured")
	}

	if ledgerID == "" {
		ledgers, err := src.LedgersParsed(ctx)
		if err != nil {
			return res, fmt.Errorf("resolve ledger: %w", err)
		}
		if len(ledgers) == 0 {
			return res, fmt.Errorf("no vipps ledgers for merchant")
		}
		ledgerID = ledgers[0].LedgerID
	}
	res.LedgerID = ledgerID

	funds, fundsTryLater, err := src.FundsForDate(ctx, ledgerID, date)
	if err != nil {
		return res, fmt.Errorf("funds report: %w", err)
	}
	fees, feesTryLater, err := src.FeesForDate(ctx, ledgerID, date)
	if err != nil {
		return res, fmt.Errorf("fees report: %w", err)
	}
	if fundsTryLater || feesTryLater {
		res.TryLater = true
		return res, nil
	}

	rows := VippsReportRows(src.MSN(), funds, fees)
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
		clubID, "Vipps Report API "+date, src.MSN(), actor, len(rows),
	).Scan(&res.ImportID); err != nil {
		return res, fmt.Errorf("create import: %w", err)
	}

	imp, err := s.ImportVippsRows(ctx, clubID, res.ImportID, rows)
	if err != nil {
		return res, fmt.Errorf("import rows: %w", err)
	}
	res.Imported = imp.Imported
	res.SkippedDup = imp.SkippedDup

	if err := s.bookAfterReportSync(ctx, clubID, actorID, &res); err != nil {
		return res, err
	}

	if _, err := s.DB().Exec(ctx,
		`UPDATE vipps_imports SET row_count = $1 WHERE id = $2`,
		imp.Imported, res.ImportID); err != nil {
		return res, fmt.Errorf("update import count: %w", err)
	}
	return res, nil
}

// bookAfterReportSync books order-linked charges, and clears the payouts they
// complete, right after a Report API ingest when someone is acting and the
// club is on the per-charge model, as the CSV upload and Synk Vipps do; a
// legacy-model club is never booked here. Without an actor (the automatic
// run's ingest) it only repairs row types: RunAutomaticBooking books
// afterwards as the club's system actor, when the club has automatic booking
// on. The booking runs in the club's booking slot; when it stays busy the
// import stands and the booking is deferred.
func (s *Service) bookAfterReportSync(ctx context.Context, clubID, actorID string, res *VippsReportSyncResult) error {
	perCharge := false
	if actorID != "" {
		var err error
		if perCharge, err = s.VippsPerChargeEnabled(ctx, clubID); err != nil {
			return fmt.Errorf("checking vipps model: %w", err)
		}
	}
	if !perCharge {
		if _, _, err := s.RepairVippsReportRows(ctx, clubID); err != nil {
			return fmt.Errorf("repairing report rows: %w", err)
		}
		return nil
	}
	booked, err := s.BookVippsQueue(ctx, clubID, actorID, VippsBookingOpts{Trigger: VippsAutoAssignTriggerReportSync})
	switch {
	case errors.Is(err, ErrVippsBookingBusy):
		res.BookingDeferred = true
	case err != nil:
		if booked != nil {
			res.Orders, res.AutoAssign = booked.Orders, booked.AutoAssign
		}
		return err
	default:
		res.Orders, res.AutoAssign, res.Payouts = booked.Orders, booked.AutoAssign, booked.Payouts
	}
	return nil
}
