package accounting

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// paymentTypeAccountMap maps Brygge payment_type values AND price-item
// category strings to kontoplan account codes. The two vocabularies
// overlap on `harbor_membership` and `slip_fee` but diverge elsewhere:
// `payment_type` (enum on payments.type) uses `dues` for annual
// membership, while `price_items.category` (the text used at invoice
// time and now denormalized onto invoice_lines.category) uses
// `membership`. Both keys point at the same account so a lookup with
// either vocabulary resolves correctly.
var paymentTypeAccountMap = map[string]string{
	"dues":              "3100", // Medlemskontingent (payment_type)
	"membership":        "3100", // Medlemskontingent (price_items.category)
	"harbor_membership": "3110", // Havneavgift
	"slip_fee":          "3120", // Plassleie
	"booking":           "3200", // Gjestehavninntekter
	"merchandise":       "3300", // Salgsinntekter
}

const (
	bankAccountCode       = "1920" // Bankkonto drift
	receivablesAccountCode = "1500" // Kundefordringer
	defaultRevenueCode     = "3100" // Fallback for unmatched types
)

// SyncResult holds the outcome of a sync operation.
type SyncResult struct {
	Synced  int `json:"synced"`
	Skipped int `json:"skipped"`
}

// SyncPayments creates journal entries from completed payments that haven't been synced yet.
// Each entry is automatically posted (completed payments are real transactions).
func (s *Service) SyncPayments(ctx context.Context, clubID, periodID, syncedBy string) (*SyncResult, error) {
	// Get period date range
	var startDate, endDate time.Time
	err := s.db.QueryRow(ctx,
		`SELECT start_date, end_date FROM fiscal_periods WHERE id = $1 AND club_id = $2 AND status = 'open'`,
		periodID, clubID,
	).Scan(&startDate, &endDate)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("fiscal period not found or not open")
	}
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}

	// Add one day to end date for inclusive range
	endDatePlusOne := endDate.AddDate(0, 0, 1)

	// Query completed payments within the period that haven't been synced
	rows, err := s.db.Query(ctx,
		`SELECT p.id, p.type, p.amount, p.created_at::date::text, u.full_name
		 FROM payments p
		 LEFT JOIN users u ON u.id = p.user_id
		 WHERE p.club_id = $1
		   AND p.status = 'completed'
		   AND p.created_at >= $2 AND p.created_at < $3
		   AND NOT EXISTS (
		     SELECT 1 FROM journal_entries je
		     WHERE je.source = 'payment_sync' AND je.source_id = p.id::text
		   )
		 ORDER BY p.created_at`,
		clubID, startDate, endDatePlusOne,
	)
	if err != nil {
		return nil, fmt.Errorf("querying payments: %w", err)
	}
	defer rows.Close()

	result := &SyncResult{}

	for rows.Next() {
		var paymentID, paymentType, entryDate string
		var amount float64
		var memberName *string
		if err := rows.Scan(&paymentID, &paymentType, &amount, &entryDate, &memberName); err != nil {
			return nil, fmt.Errorf("scanning payment: %w", err)
		}

		revenueCode, ok := paymentTypeAccountMap[paymentType]
		if !ok {
			revenueCode = defaultRevenueCode
		}

		description := fmt.Sprintf("Betaling: %s", paymentType)
		if memberName != nil {
			description = fmt.Sprintf("Betaling %s: %s", paymentType, *memberName)
		}

		sourceID := paymentID
		sourceTable := "payments"

		entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID,
			EntryDate:      entryDate,
			Description:    description,
			Source:         "payment_sync",
			SourceID:       &sourceID,
			SourceTable:    &sourceTable,
			CreatedBy:      syncedBy,
			ClubID:         clubID,
			Lines: []CreateJournalLineInput{
				{AccountCode: bankAccountCode, Debit: amount, Credit: 0, Description: ""},
				{AccountCode: revenueCode, Debit: 0, Credit: amount, Description: ""},
			},
		})
		if err != nil {
			s.log.Error().Err(err).Str("payment_id", paymentID).Msg("failed to sync payment")
			result.Skipped++
			continue
		}

		// Auto-post (these are completed, real transactions)
		if err := s.PostJournalEntry(ctx, entry.ID, syncedBy); err != nil {
			s.log.Error().Err(err).Str("entry_id", entry.ID).Msg("failed to auto-post synced payment")
			result.Skipped++
			continue
		}

		result.Synced++
	}

	return result, rows.Err()
}

// RebuildInvoiceBilags deletes all existing invoice_sync journal entries
// for the given fiscal period (cascading to their journal_lines) and
// re-runs SyncInvoices so they're re-created with the current logic.
// Useful after schema/code changes that change how the entries are shaped
// (e.g. the per-category CR split introduced by DIL-290).
//
// Refuses to operate on a closed period — the caller must reopen it first.
func (s *Service) RebuildInvoiceBilags(ctx context.Context, clubID, periodID, syncedBy string) (*SyncResult, int, error) {
	var status string
	if err := s.db.QueryRow(ctx,
		`SELECT status FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		periodID, clubID,
	).Scan(&status); err != nil {
		if err == pgx.ErrNoRows {
			return nil, 0, fmt.Errorf("fiscal period not found")
		}
		return nil, 0, fmt.Errorf("loading period: %w", err)
	}
	if status != "open" {
		return nil, 0, fmt.Errorf("fiscal period is %s — reopen it before rebuilding bilags", status)
	}

	tag, err := s.db.Exec(ctx,
		`DELETE FROM journal_entries
		 WHERE club_id = $1 AND fiscal_period_id = $2 AND source = 'invoice_sync'`,
		clubID, periodID,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("deleting existing invoice bilags: %w", err)
	}
	deleted := int(tag.RowsAffected())

	result, err := s.SyncInvoices(ctx, clubID, periodID, syncedBy)
	if err != nil {
		return nil, deleted, fmt.Errorf("re-syncing invoices: %w", err)
	}
	return result, deleted, nil
}

// SyncInvoices creates receivable journal entries from invoices that haven't been synced yet.
// Each entry debits Kundefordringer (1500) and credits the appropriate revenue account.
func (s *Service) SyncInvoices(ctx context.Context, clubID, periodID, syncedBy string) (*SyncResult, error) {
	// Get period date range
	var startDate, endDate time.Time
	err := s.db.QueryRow(ctx,
		`SELECT start_date, end_date FROM fiscal_periods WHERE id = $1 AND club_id = $2 AND status = 'open'`,
		periodID, clubID,
	).Scan(&startDate, &endDate)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("fiscal period not found or not open")
	}
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}

	// Query invoices within the period that haven't been synced
	rows, err := s.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.total_amount, i.issue_date::text, u.full_name
		 FROM invoices i
		 LEFT JOIN users u ON u.id = i.user_id
		 WHERE i.club_id = $1
		   AND i.issue_date >= $2 AND i.issue_date <= $3
		   AND NOT EXISTS (
		     SELECT 1 FROM journal_entries je
		     WHERE je.source = 'invoice_sync' AND je.source_id = i.id::text
		   )
		 ORDER BY i.issue_date`,
		clubID, startDate, endDate,
	)
	if err != nil {
		return nil, fmt.Errorf("querying invoices: %w", err)
	}
	defer rows.Close()

	result := &SyncResult{}

	for rows.Next() {
		var invoiceID, entryDate string
		var invoiceNumber int
		var amount float64
		var memberName *string
		if err := rows.Scan(&invoiceID, &invoiceNumber, &amount, &entryDate, &memberName); err != nil {
			return nil, fmt.Errorf("scanning invoice: %w", err)
		}

		description := fmt.Sprintf("Faktura #%d", invoiceNumber)
		if memberName != nil {
			description = fmt.Sprintf("Faktura #%d: %s", invoiceNumber, *memberName)
		}

		// Per-category revenue split: sum invoice_lines by price-item
		// category, mapped through paymentTypeAccountMap. Lines without
		// a price_item or with an unmapped category fall through to
		// defaultRevenueCode.
		creditsByAccount, err := s.invoiceRevenueCredits(ctx, invoiceID, amount)
		if err != nil {
			s.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("failed to aggregate invoice lines")
			result.Skipped++
			continue
		}

		journalLines := []CreateJournalLineInput{
			{AccountCode: receivablesAccountCode, Debit: amount, Credit: 0, Description: ""},
		}
		for code, credit := range creditsByAccount {
			journalLines = append(journalLines, CreateJournalLineInput{
				AccountCode: code,
				Debit:       0,
				Credit:      credit,
				Description: "",
			})
		}

		sourceID := invoiceID
		sourceTable := "invoices"

		entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID,
			EntryDate:      entryDate,
			Description:    description,
			Source:         "invoice_sync",
			SourceID:       &sourceID,
			SourceTable:    &sourceTable,
			CreatedBy:      syncedBy,
			ClubID:         clubID,
			Lines:          journalLines,
		})
		if err != nil {
			s.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("failed to sync invoice")
			result.Skipped++
			continue
		}

		// Auto-post
		if err := s.PostJournalEntry(ctx, entry.ID, syncedBy); err != nil {
			s.log.Error().Err(err).Str("entry_id", entry.ID).Msg("failed to auto-post synced invoice")
			result.Skipped++
			continue
		}

		result.Synced++
	}

	return result, rows.Err()
}

// invoiceRevenueCredits computes per-account credit amounts for an
// invoice by walking its line items, mapping each line price item
// category to a revenue account, and summing line_total per account.
// Lines without a price_item or with an unmapped category fall through
// to defaultRevenueCode. If the line-level sum diverges from the
// invoice total (rounding or missing lines), the residual is assigned
// to defaultRevenueCode so the bilag still balances.
func (s *Service) invoiceRevenueCredits(ctx context.Context, invoiceID string, invoiceTotal float64) (map[string]float64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT il.line_total, pi.category
		 FROM invoice_lines il
		 LEFT JOIN price_items pi ON pi.id = il.price_item_id
		 WHERE il.invoice_id = $1`,
		invoiceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying invoice lines: %w", err)
	}
	defer rows.Close()

	credits := map[string]float64{}
	var lineSum float64
	for rows.Next() {
		var lineTotal float64
		var category *string
		if err := rows.Scan(&lineTotal, &category); err != nil {
			return nil, fmt.Errorf("scanning line: %w", err)
		}
		code := defaultRevenueCode
		if category != nil {
			if c, ok := paymentTypeAccountMap[*category]; ok {
				code = c
			}
		}
		credits[code] += lineTotal
		lineSum += lineTotal
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if residual := invoiceTotal - lineSum; residual != 0 {
		credits[defaultRevenueCode] += residual
	}
	if len(credits) == 0 {
		credits[defaultRevenueCode] = invoiceTotal
	}
	return credits, nil
}
