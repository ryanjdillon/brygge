package accounting

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// paymentTypeAccountMap maps Brygge payment_type values to kontoplan account codes.
var paymentTypeAccountMap = map[string]string{
	"dues":              "3100", // Medlemskontingent
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

	// Query completed payments within the period that haven't been synced
	rows, err := s.db.Query(ctx,
		`SELECT p.id, p.type, p.amount, p.created_at::date::text, u.full_name
		 FROM payments p
		 LEFT JOIN users u ON u.id = p.user_id
		 WHERE p.club_id = $1
		   AND p.status = 'completed'
		   AND p.created_at >= $2 AND p.created_at < $3 + INTERVAL '1 day'
		   AND NOT EXISTS (
		     SELECT 1 FROM journal_entries je
		     WHERE je.source = 'payment_sync' AND je.source_id = p.id::text
		   )
		 ORDER BY p.created_at`,
		clubID, startDate, endDate,
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
			Lines: []CreateJournalLineInput{
				{AccountCode: receivablesAccountCode, Debit: amount, Credit: 0, Description: ""},
				{AccountCode: defaultRevenueCode, Debit: 0, Credit: amount, Description: ""},
			},
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
