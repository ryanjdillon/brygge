package accounting

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/finance"
)

// isBryggeKID is `true` only for strings that match Brygge's own KID
// shape: exactly 12 ASCII digits with a valid Luhn check digit. This
// is intentionally narrower than `finance.ValidateKID` (which only
// asserts the Luhn) because the bank's Melding column carries lots
// of free-text garbage that happens to fail length but might fluke
// the Luhn — easier to reject the whole class.
func isBryggeKID(s string) bool {
	if len(s) != 12 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return finance.ValidateKID(s)
}

// findInvoiceByCounterpartAndAmount is the tertiary auto-match path
// for the bank-sync loop: when neither KID nor invoice-number caught
// the row, look up the open unpaid invoice that belongs to the
// member whose normalized full name equals the bank counterpart and
// whose total matches the bank-row amount within 0.5 NOK. Returns
// "" when the lookup is ambiguous (2+ candidates) or finds no match
// — both cases leave the row for manual review.
func (s *Service) findInvoiceByCounterpartAndAmount(ctx context.Context, clubID, counterpart string, amount float64) string {
	needle := normalizeName(counterpart)
	if needle == "" {
		return ""
	}
	rows, err := s.db.Query(ctx,
		`SELECT i.id, u.full_name
		   FROM invoices i
		   JOIN users u ON u.id = i.user_id
		  WHERE i.club_id = $1
		    AND i.status = 'open'
		    AND i.payment_id IS NULL
		    AND ABS(i.total_amount - $2) < 0.005
		    AND NOT EXISTS (
		      SELECT 1 FROM bank_import_rows bir
		      WHERE bir.kid_number = i.kid_number
		        AND bir.journal_entry_id IS NOT NULL
		    )`,
		clubID, amount,
	)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var firstMatch string
	matches := 0
	for rows.Next() {
		var id, fullName string
		if err := rows.Scan(&id, &fullName); err != nil {
			continue
		}
		if normalizeName(fullName) != needle {
			continue
		}
		matches++
		if matches == 1 {
			firstMatch = id
		}
		if matches > 1 {
			return "" // ambiguous — bail
		}
	}
	if matches == 1 {
		return firstMatch
	}
	return ""
}

// BankSyncResult summarizes one full-scope bank sync run.
type BankSyncResult struct {
	KIDMatched       int      `json:"kid_matched"`
	VippsReconciled  int      `json:"vipps_reconciled"`
	VippsUnbalanced  int      `json:"vipps_unbalanced"`
	TransfersLinked  int      `json:"transfers_linked"`
	PaymentsLinked   int      `json:"payments_linked"`
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

	// Pass 4: backfill payment back-links for invoices whose bank row
	// was KID-matched at the GL level but never got the
	// `linkInvoicePayment` step — typically because the row was
	// imported before DIL-363 shipped. Self-healing: "Run sync" now
	// also retroactively reconciles these legacy mismatches.
	backfilled, berr := s.backfillUnlinkedPayments(ctx, clubID)
	if berr != nil {
		s.log.Warn().Err(berr).Msg("backfill unlinked payments failed")
	}
	res.PaymentsLinked = backfilled

	for y := range closedYears {
		res.ClosedPeriods = append(res.ClosedPeriods, fmt.Sprintf("%d", y))
	}
	return res, nil
}

// backfillUnlinkedPayments fixes up invoices whose KID-matching bank
// row already has a journal entry (so the GL is correct) but whose
// `invoices.payment_id` was never set — leaving the dashboard,
// FakturaList "paid" filter, and priceItemSummary all reading them as
// unpaid. The common cause is bank rows imported before DIL-363
// wired `linkInvoicePayment` into the per-row match.
//
// Only full-amount matches are backfilled (bir.amount = i.total_amount).
// Partial payments would require a "partial" status on the payments
// table that doesn't exist yet, so those rows are left alone.
func (s *Service) backfillUnlinkedPayments(ctx context.Context, clubID string) (int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT ON (i.id) i.id, bir.row_date
		   FROM invoices i
		   JOIN bank_import_rows bir ON bir.kid_number = i.kid_number
		   JOIN bank_imports bi      ON bi.id = bir.bank_import_id
		  WHERE bi.club_id = $1
		    AND i.club_id  = $1
		    AND i.payment_id IS NULL
		    AND i.status = 'open'
		    AND bir.amount > 0
		    AND bir.amount = i.total_amount
		    AND bir.journal_entry_id IS NOT NULL
		  ORDER BY i.id, bir.row_date ASC`,
		clubID,
	)
	if err != nil {
		return 0, err
	}
	type pending struct {
		invoiceID string
		paidAt    time.Time
	}
	var pendings []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.invoiceID, &p.paidAt); err != nil {
			rows.Close()
			return 0, err
		}
		pendings = append(pendings, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, p := range pendings {
		if err := s.linkInvoicePayment(ctx, clubID, p.invoiceID, p.paidAt); err != nil {
			s.log.Warn().Err(err).
				Str("invoice_id", p.invoiceID).
				Msg("backfill linkInvoicePayment failed")
			continue
		}
		n++
	}
	return n, nil
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
		// Rejected-on-receiving-account rows ("KID … ble ikke
		// akseptert på denne kontoen") are notification rows the bank
		// posts when a transfer to a savings/høyrente account bounces
		// at the inbound-cap. The substance is "money never arrived;
		// payer still owes." Skip entirely — neither auto-match nor
		// journal.
		if IsRejectedKIDDescription(p.desc) {
			continue
		}

		// Recover KID + payer from description if missing. The
		// Melding column from the bank carries free text for many
		// rows ("Medlemskontingent Olav Alpen", "Faktura 44", ".")
		// which is NOT a KID. Drop anything that isn't our exact
		// shape (12 digits + Luhn) and let the description-extractor
		// have a try.
		newKID := strings.TrimSpace(p.kid)
		if newKID != "" && !isBryggeKID(newKID) {
			newKID = ""
		}
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

		// Match cascade: KID → "Fakturanummer NN" + amount →
		// unique counterpart-name + amount.
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
		if invoiceID == "" && p.counterpart != "" {
			// Tertiary: counterpart name (the bank's Debitornavn /
			// Fra-prefix payer) + amount. Only auto-match when the
			// (normalized name, amount) pair maps to EXACTLY one open
			// unpaid invoice. Ambiguity (2+ candidates) falls through
			// and the row stays unmatched for manual review.
			invoiceID = s.findInvoiceByCounterpartAndAmount(ctx, clubID, p.counterpart, p.amount)
		}
		if invoiceID == "" {
			continue
		}

		periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, p.date, "")
		if perr != nil {
			s.log.Warn().Err(perr).
				Str("bank_row_id", p.rowID).
				Time("row_date", p.date).
				Msg("KID match: resolvePeriod failed; row stays unmatched")
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
			// Most common cause: bank_account_code not present in the
			// club's chart of accounts (typically a new bank role
			// added via DIL-340 with a gl_code that was never seeded).
			// Surface this loudly so the operator/agent can fix the
			// chart instead of staring at a silently-unmatched row.
			s.log.Warn().Err(cerr).
				Str("bank_row_id", p.rowID).
				Str("bank_account_code", p.bankAccount).
				Str("kid", p.kid).
				Msg("KID match: CreateJournalEntry failed; row stays unmatched")
			continue
		}
		if perr := s.PostJournalEntry(ctx, entry.ID, createdBy); perr != nil {
			s.log.Warn().Err(perr).
				Str("bank_row_id", p.rowID).
				Str("entry_id", entry.ID).
				Msg("KID match: PostJournalEntry failed; row stays unmatched")
			continue
		}
		if _, uerr := s.db.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true WHERE id = $2`,
			entry.ID, p.rowID,
		); uerr == nil {
			matched++
			// Mirror the GL credit-to-receivables side onto the
			// invoice flag so the dashboard, faktura list, and
			// priceItemSummary reflect the payment. Best-effort —
			// failures here don't roll back the journal because
			// the GL itself is already correct.
			if lerr := s.linkInvoicePayment(ctx, clubID, invoiceID, p.date); lerr != nil {
				s.log.Warn().Err(lerr).
					Str("invoice_id", invoiceID).
					Str("bank_row_id", p.rowID).
					Msg("KID match journal posted but invoice payment back-link failed")
			}
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
