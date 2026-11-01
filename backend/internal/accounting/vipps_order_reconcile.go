package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Order-linked Vipps charges (BRY-337). An ePayment charge (faktura pay-link,
// QR checkout) carries our merchant reference (chk-…) as the import row's
// order_id, so it needs no guessing: the order behind it says exactly what the
// money paid for. The charge is booked through the per-charge clearing model
// like a Tildel assignment — DR clearing / CR 1500 per faktura the order
// settles (with the invoice payment back-link) and CR the category revenue
// account per catalog line — so its settlement's payout clears the usual way.

// VippsOrderReconcileResult summarises one AutoReconcileOrderCharges run.
type VippsOrderReconcileResult struct {
	Repaired       int           `json:"repaired"`
	Booked         int           `json:"booked"`
	SkippedDetails []SkippedItem `json:"skipped_details"`
}

// assignVippsChargeToOrder books one belastning against the checkout order its
// merchant reference names. It refuses when the order's split does not add up
// to the charge, or when a faktura it would settle can no longer take a
// payment (voided, credited, a credit note, already paid, a booked row
// recorded against it, or imported without its opening balance): any of these
// needs a human, not a second credit to receivables. The fakturas are locked
// and checked inside the booking transaction, as a manual assign does, so a
// credit note, payment or booking that landed after the caller last looked is
// seen. Idempotent per charge. found=false means no order carries the
// charge's reference.
//
// Its only caller is AutoReconcileOrderCharges, an automatic pass, so it books
// into an existing open period (periodExistingOnly) and never opens a fiscal
// year: a charge dated in a year the treasurer has not created stays unbooked
// (*noFiscalYearError).
func (s *Service) assignVippsChargeToOrder(ctx context.Context, clubID, vippsRowID, createdBy string) (res *VippsChargePostResult, found bool, err error) {
	b, err := s.loadVippsBelastning(ctx, clubID, vippsRowID)
	if err != nil {
		return nil, false, err
	}
	if b.entryID != nil {
		return &VippsChargePostResult{EntryID: *b.entryID, AlreadyReconciled: true}, true, nil
	}
	if b.dismissedAt != nil {
		return nil, true, fmt.Errorf("charge is dismissed")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	split, found, err := resolveOrderChargeSplit(ctx, tx, clubID, b.orderRef)
	if err != nil || !found {
		return nil, found, err
	}
	if len(split) == 0 {
		return nil, true, fmt.Errorf("order %s has no fakturas or lines to book", b.orderRef)
	}

	// Fakturas first: the lock order every settle path shares (invariants.md,
	// "Lock order"). This path takes no order or attempt lock.
	var settled []settledInvoice
	for _, l := range split {
		if l.LinkedInvoiceID != "" {
			settled = append(settled, settledInvoice{id: l.LinkedInvoiceID, bookingDate: b.date})
		}
	}
	if _, err := lockInvoicesForManualAssign(ctx, tx, clubID, settled); err != nil {
		return nil, true, orderReceivableRefusal(b.orderRef, err)
	}

	credits, invoiceIDs, err := checkOrderChargeSplit(ctx, tx, clubID, split, b.amount, b.date)
	if err != nil {
		return nil, true, orderReceivableRefusal(b.orderRef, err)
	}

	desc := fmt.Sprintf("Vipps innbetaling ordre %s", b.orderRef)
	entryID, err := postVippsChargeSplitTx(ctx, tx, clubID, createdBy, desc, b, credits, periodExistingOnly)
	if errors.Is(err, errChargeRaced) {
		return &VippsChargePostResult{EntryID: entryID, AlreadyReconciled: true}, true, nil
	}
	if err != nil {
		return nil, true, err
	}
	// The back-links update the fakturas, so they run once the commit has
	// released the locks.
	if err := tx.Commit(ctx); err != nil {
		return nil, true, fmt.Errorf("commit vipps order charge: %w", err)
	}
	for _, id := range invoiceIDs {
		// Best-effort, as in AssignVippsChargeToInvoice: the GL is already right.
		if err := s.linkInvoicePayment(ctx, clubID, id, b.date); err != nil {
			s.log.Warn().Err(err).Str("invoice_id", id).Str("vipps_row_id", vippsRowID).
				Msg("vipps order charge: invoice payment back-link failed")
		}
	}
	return &VippsChargePostResult{EntryID: entryID}, true, nil
}

// orderChargeRefusal is the message skipped_details shows when a faktura on
// the order can no longer take the charge; Unwrap keeps the specific cause
// (ErrInvoiceCredited, ErrInvoiceVoided, …) for errors.Is.
type orderChargeRefusal struct {
	orderRef string
	cause    error
}

func (e *orderChargeRefusal) Error() string {
	return fmt.Sprintf("a faktura on order %s is already paid, voided or credited — book the charge by hand", e.orderRef)
}

func (e *orderChargeRefusal) Unwrap() error { return e.cause }

// orderReceivableRefusal wraps the refusals that mean "a faktura on the
// order is not open" in orderChargeRefusal; every other error (a split that
// does not add up, ErrImportedNoOpeningBalance, a database error) passes
// through unchanged.
func orderReceivableRefusal(orderRef string, err error) error {
	for _, cause := range []error{
		errOrderReceivableNotOpen, ErrInvoiceVoided, ErrInvoiceCredited, ErrInvoiceNotDebt,
		errInvoicePaid, errInvoiceHasBookedRow,
	} {
		if errors.Is(err, cause) {
			return &orderChargeRefusal{orderRef: orderRef, cause: err}
		}
	}
	return err
}

// AutoReconcileOrderCharges first repairs Report API rows imported before the
// funds entries were classified (RepairVippsReportRows), then books every
// unbooked, non-dismissed charge whose merchant reference names a checkout
// order. Charges that cannot be booked are reported, not swallowed.
func (s *Service) AutoReconcileOrderCharges(ctx context.Context, clubID, createdBy string) (*VippsOrderReconcileResult, error) {
	res := &VippsOrderReconcileResult{SkippedDetails: []SkippedItem{}}

	repaired, repairSkipped, err := s.RepairVippsReportRows(ctx, clubID)
	if err != nil {
		return nil, err
	}
	res.Repaired = repaired
	res.SkippedDetails = append(res.SkippedDetails, repairSkipped...)

	rows, err := s.db.Query(ctx,
		`SELECT r.id, r.order_id
		   FROM vipps_import_rows r
		  WHERE r.club_id = $1 AND r.row_type = 'belastning'
		    AND r.journal_entry_id IS NULL AND r.dismissed_at IS NULL
		    AND r.order_id <> ''
		    AND NOT `+referenceDateSQL("r.club_id", "COALESCE(r.booking_date, r.tx_at::date)")+`
		    AND EXISTS (SELECT 1 FROM orders o WHERE o.club_id = r.club_id AND o.vipps_reference = r.order_id)
		  ORDER BY r.tx_at`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing order-linked charges: %w", err)
	}
	type charge struct{ id, ref string }
	var charges []charge
	for rows.Next() {
		var c charge
		if err := rows.Scan(&c.id, &c.ref); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning order-linked charge: %w", err)
		}
		charges = append(charges, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, c := range charges {
		r, _, err := s.assignVippsChargeToOrder(ctx, clubID, c.id, createdBy)
		if err != nil {
			res.SkippedDetails = append(res.SkippedDetails, SkippedItem{Ref: "ordre " + c.ref, Reason: err.Error()})
			continue
		}
		if !r.AlreadyReconciled {
			res.Booked++
		}
	}
	return res, nil
}

// RepairVippsReportRows reclassifies Report API rows stored as "other" before
// the funds settlement entries were mapped (classifyReportFunds): the payout
// becomes a payout row with its settlement number and the retained fees a fee
// row. The row hash is recomputed so a later re-pull of the same date dedups
// against the repaired row; when the re-pull already inserted the corrected
// row, the stale "other" row is removed instead. Only unbooked rows from
// Report API imports are touched — a CSV row's message is the payer's free
// text and must never be read as an entry type. Idempotent.
//
// A row that cannot be repaired is returned in the skipped list and logged,
// never turned into an error: the nightly sync and AutoReconcileOrderCharges
// run this first, so one bad row would otherwise stop all order booking. An
// error is returned only when the pass itself cannot run (the listing query).
func (s *Service) RepairVippsReportRows(ctx context.Context, clubID string) (int, []SkippedItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT r.id, r.tx_at, r.amount, COALESCE(r.order_id, ''), COALESCE(r.psp_ref, ''), COALESCE(r.message, '')
		   FROM vipps_import_rows r
		   JOIN vipps_imports vi ON vi.id = r.vipps_import_id
		  WHERE r.club_id = $1 AND r.row_type = 'other'
		    AND r.journal_entry_id IS NULL AND r.dismissed_at IS NULL
		    AND vi.filename LIKE 'Vipps Report API %'
		  ORDER BY r.created_at, r.id`,
		clubID,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("listing report rows to repair: %w", err)
	}
	type fix struct {
		id         string
		typ        VippsRowType
		settlement string
		hash       string
	}
	var fixes []fix
	for rows.Next() {
		var row VippsRow
		var id string
		// tx_at is NULL for an entry the Report API gave no time. The import
		// hashed such a row with the zero time (ImportVippsRows stores NULL for
		// a zero TxAt), so the zero value here reproduces that hash and a
		// re-pull of the date still dedups against the repaired row.
		var txAt *time.Time
		if err := rows.Scan(&id, &txAt, &row.Amount, &row.OrderID, &row.PSPRef, &row.Message); err != nil {
			rows.Close()
			return 0, nil, fmt.Errorf("scanning report row: %w", err)
		}
		if txAt != nil {
			row.TxAt = *txAt
		}
		row.RowType, row.SettlementNumber = classifyReportFunds(row.Message, row.OrderID, row.PSPRef)
		if row.RowType == VippsRowOther {
			continue
		}
		fixes = append(fixes, fix{id: id, typ: row.RowType, settlement: row.SettlementNumber, hash: VippsRowHash(clubID, row)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}

	repaired := 0
	var skipped []SkippedItem
	skip := func(id string, err error) {
		s.log.Warn().Err(err).Str("vipps_row_id", id).Msg("vipps report row repair skipped")
		skipped = append(skipped, SkippedItem{Ref: "vipps-rad " + id, Reason: err.Error()})
	}
	for _, f := range fixes {
		if err := ctx.Err(); err != nil {
			return repaired, skipped, fmt.Errorf("repairing report rows: %w", err)
		}
		_, err := s.db.Exec(ctx,
			`UPDATE vipps_import_rows SET row_type = $3, settlement_number = $4, row_hash = $5
			  WHERE id = $1 AND club_id = $2 AND row_type = 'other' AND journal_entry_id IS NULL`,
			f.id, clubID, string(f.typ), f.settlement, f.hash,
		)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// The corrected row already exists from a re-pull; drop the stale one.
			if _, derr := s.db.Exec(ctx,
				`DELETE FROM vipps_import_rows WHERE id = $1 AND club_id = $2 AND row_type = 'other' AND journal_entry_id IS NULL`,
				f.id, clubID,
			); derr != nil {
				if !isRowRefusal(ctx, derr) {
					return repaired, skipped, fmt.Errorf("removing superseded report row: %w", derr)
				}
				skip(f.id, fmt.Errorf("removing superseded report row: %w", derr))
				continue
			}
			repaired++
			continue
		}
		if err != nil {
			if !isRowRefusal(ctx, err) {
				return repaired, skipped, fmt.Errorf("repairing report row: %w", err)
			}
			skip(f.id, fmt.Errorf("repairing report row: %w", err))
			continue
		}
		repaired++
	}
	return repaired, skipped, nil
}

// isRowRefusal reports whether err is the server refusing this one statement
// (a constraint, a trigger, a lock timeout), which a person can look at later.
// A cancelled context, a lost connection or a server shutdown (SQLSTATE class
// 08 and 57) says nothing about the row and must stop the pass instead.
func isRowRefusal(ctx context.Context, err error) bool {
	var pgErr *pgconn.PgError
	if ctx.Err() != nil || !errors.As(err, &pgErr) {
		return false
	}
	return !strings.HasPrefix(pgErr.Code, "08") && !strings.HasPrefix(pgErr.Code, "57")
}
