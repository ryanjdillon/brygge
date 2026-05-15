package accounting

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// BankSyncResult summarizes one full-scope bank sync run.
type BankSyncResult struct {
	KIDMatched       int      `json:"kid_matched"`
	VippsReconciled  int      `json:"vipps_reconciled"`
	VippsUnbalanced  int      `json:"vipps_unbalanced"`
	TransfersLinked  int      `json:"transfers_linked"`
	ClosedPeriods    []string `json:"closed_periods"`
}

// BankSync re-runs auto-correlation across every unmatched bank import row
// for the club, in three passes:
//
//  1. KID matching against open invoices.
//  2. Vipps payout reconciliation for rows whose description matches the
//     Utb./Vippsnr pattern (only when a balanced bilag can be produced
//     against an open fiscal period).
//  3. Intra-bank transfer pairing across all of the club's imports.
//
// This is the on-demand counterpart to the per-upload auto-match that
// already runs at import time. It lets the treasurer pull in matches that
// only became possible after subsequent uploads (e.g. bank first, Vipps
// later) or after fixing data (e.g. registering a member by name).
func (s *Service) BankSync(ctx context.Context, clubID, createdBy string) (*BankSyncResult, error) {
	res := &BankSyncResult{}
	closedYears := map[int]bool{}

	// Pass 1: KID auto-match on all unmatched rows.
	kidMatched, kidClosed, err := s.syncKIDMatches(ctx, clubID, createdBy)
	if err != nil {
		return nil, fmt.Errorf("kid sync: %w", err)
	}
	res.KIDMatched = kidMatched
	for _, y := range kidClosed {
		closedYears[y] = true
	}

	// Pass 2: Vipps reconciliation on all unmatched bank rows that match
	// the Utb./Vippsnr pattern. Only confirm when balanced and the
	// target period is open.
	reconciled, unbalanced, vippsClosed, err := s.syncVippsReconciliations(ctx, clubID, createdBy)
	if err != nil {
		return nil, fmt.Errorf("vipps sync: %w", err)
	}
	res.VippsReconciled = reconciled
	res.VippsUnbalanced = unbalanced
	for _, y := range vippsClosed {
		closedYears[y] = true
	}

	// Pass 3: re-run intra-bank transfer detection across ALL of the
	// club's imports (the import-time detector only runs against the
	// current import).
	var importIDs []string
	importRows, err := s.db.Query(ctx,
		`SELECT id FROM bank_imports WHERE club_id = $1`, clubID,
	)
	if err == nil {
		for importRows.Next() {
			var id string
			if err := importRows.Scan(&id); err == nil {
				importIDs = append(importIDs, id)
			}
		}
		importRows.Close()
	}
	for _, importID := range importIDs {
		n, skipped, _ := s.detectIntraBankTransfers(ctx, clubID, importID, "", createdBy)
		res.TransfersLinked += n
		for _, y := range skipped {
			closedYears[y] = true
		}
	}

	for y := range closedYears {
		res.ClosedPeriods = append(res.ClosedPeriods, fmt.Sprintf("%d", y))
	}
	return res, nil
}

func (s *Service) syncKIDMatches(ctx context.Context, clubID, createdBy string) (int, []int, error) {
	// Drop the kid_number <> '' filter so we can ALSO process rows
	// that lost their KID at import time because the bank's CSV
	// left the KID column blank and embedded the reference in the
	// description string instead (DNB online-bank rows of the
	// "Fra: <name> Betalt: <date> · <kid>" shape). Per-row we then
	// extract the KID + invoice-number from the description and
	// persist them so future passes — and the "0 received" tile
	// in oversikt — pick the row up.
	rows, err := s.db.Query(ctx,
		`SELECT bir.id, bir.row_date, bir.amount,
		        COALESCE(bir.kid_number, ''),
		        COALESCE(bir.counterpart, ''),
		        bir.description, bi.bank_account_code
		 FROM bank_import_rows bir
		 JOIN bank_imports bi ON bi.id = bir.bank_import_id
		 WHERE bir.club_id = $1
		   AND bir.amount > 0
		   AND bir.journal_entry_id IS NULL`,
		clubID,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("listing unmatched rows: %w", err)
	}
	defer rows.Close()

	type pending struct {
		rowID, kid, counterpart, desc, bankAccount string
		date                                       time.Time
		amount                                     float64
	}
	var work []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.rowID, &p.date, &p.amount, &p.kid, &p.counterpart, &p.desc, &p.bankAccount); err != nil {
			continue
		}
		work = append(work, p)
	}
	rows.Close()

	matched := 0
	closedYears := map[int]bool{}
	for _, p := range work {
		// Recover KID + payer from description if missing. Persist
		// whichever pieces actually changed so the row's stored
		// state reflects reality and the next pass / a manual
		// review sees the right values.
		newKID := p.kid
		if newKID == "" {
			newKID = ExtractKIDFromDescription(p.desc)
		}
		newCounterpart := p.counterpart
		if payer := ExtractPayerFromDescription(p.desc); payer != "" {
			newCounterpart = payer
		}
		if newKID != p.kid || newCounterpart != p.counterpart {
			_, _ = s.db.Exec(ctx,
				`UPDATE bank_import_rows SET kid_number = $1, counterpart = $2 WHERE id = $3`,
				newKID, newCounterpart, p.rowID,
			)
			p.kid = newKID
			p.counterpart = newCounterpart
		}

		// Match: KID first, then "Fakturanummer NN" + amount.
		var invoiceID string
		if p.kid != "" {
			_ = s.db.QueryRow(ctx,
				`SELECT i.id FROM invoices i
				 WHERE i.club_id = $1 AND i.kid_number = $2
				 AND NOT EXISTS (
				   SELECT 1 FROM bank_import_rows bir
				   WHERE bir.kid_number = $2 AND bir.journal_entry_id IS NOT NULL
				 )
				 LIMIT 1`,
				clubID, p.kid,
			).Scan(&invoiceID)
		}
		if invoiceID == "" {
			if num := ExtractInvoiceNumberFromDescription(p.desc); num != "" {
				_ = s.db.QueryRow(ctx,
					`SELECT i.id FROM invoices i
					 WHERE i.club_id = $1 AND i.invoice_number::text = $2
					   AND ABS(i.total_amount - $3) < 0.005
					   AND NOT EXISTS (
					     SELECT 1 FROM bank_import_rows bir
					     WHERE bir.journal_entry_id IS NOT NULL
					       AND bir.kid_number = i.kid_number
					   )
					 LIMIT 1`,
					clubID, num, p.amount,
				).Scan(&invoiceID)
			}
		}
		if invoiceID == "" {
			continue
		}

		periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, p.date, "")
		if perr != nil {
			continue
		}
		if periodStatus == "closed" {
			closedYears[p.date.Year()] = true
			continue
		}

		sourceID := p.rowID
		sourceTable := "bank_import_rows"
		descLabel := fmt.Sprintf("Innbetaling: %s", p.desc)
		if p.kid != "" {
			descLabel = fmt.Sprintf("Innbetaling KID %s: %s", p.kid, p.desc)
		}
		entry, cerr := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID,
			EntryDate:      p.date.Format("2006-01-02"),
			Description:    descLabel,
			Source:         "bank_import",
			SourceID:       &sourceID,
			SourceTable:    &sourceTable,
			CreatedBy:      createdBy,
			ClubID:         clubID,
			Lines: []CreateJournalLineInput{
				{AccountCode: p.bankAccount, Debit: p.amount, Credit: 0},
				{AccountCode: receivablesAccountCode, Debit: 0, Credit: p.amount},
			},
		})
		if cerr != nil {
			continue
		}
		if perr := s.PostJournalEntry(ctx, entry.ID, createdBy); perr != nil {
			continue
		}
		if _, uerr := s.db.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true WHERE id = $2`,
			entry.ID, p.rowID,
		); uerr == nil {
			matched++
		}
	}

	var years []int
	for y := range closedYears {
		years = append(years, y)
	}
	return matched, years, nil
}

func (s *Service) syncVippsReconciliations(ctx context.Context, clubID, createdBy string) (matched, unbalanced int, closed []int, err error) {
	rows, qerr := s.db.Query(ctx,
		`SELECT id FROM bank_import_rows
		 WHERE club_id = $1
		   AND journal_entry_id IS NULL
		   AND description ~* '^utb\.\s*[0-9]+\s+vippsnr\s+[0-9]+'`,
		clubID,
	)
	if qerr != nil {
		return 0, 0, nil, fmt.Errorf("listing candidate rows: %w", qerr)
	}
	defer rows.Close()

	var rowIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			rowIDs = append(rowIDs, id)
		}
	}
	rows.Close()

	closedYears := map[int]bool{}
	for _, rowID := range rowIDs {
		preview, perr := s.ReconcileVippsPreview(ctx, clubID, rowID)
		if perr != nil {
			if perr == pgx.ErrNoRows {
				continue
			}
			continue
		}
		if !preview.Balanced {
			unbalanced++
			continue
		}
		if preview.PeriodClosed {
			closedYears[preview.PeriodYear] = true
			continue
		}
		if _, cerr := s.ReconcileVippsConfirm(ctx, clubID, rowID, "", createdBy, preview.Lines); cerr != nil {
			unbalanced++
			continue
		}
		matched++
	}

	for y := range closedYears {
		closed = append(closed, y)
	}
	return matched, unbalanced, closed, nil
}
