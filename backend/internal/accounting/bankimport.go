package accounting

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// BankRow is the internal normalized representation of a bank transaction.
// All parsers (CSV, API) produce this same struct.
type BankRow struct {
	Date        time.Time
	Description string
	Amount      float64 // positive = inflow, negative = outflow
	Balance     *float64
	Reference   string
	KID         string
	Counterpart string
}

// BankFormat defines how to parse a specific bank's CSV format.
// Each bank has different column names and ordering.
type BankFormat struct {
	Name          string
	Delimiter     rune
	DateColumn    string
	DateFormat    string
	DescColumn    string
	AmountColumn  string   // single amount column (positive/negative)
	DebitColumn   string   // "out" column (alternative to single amount)
	CreditColumn  string   // "in" column (alternative to single amount)
	BalanceColumn string
	KIDColumn     string
	RefColumn     string
	CounterColumn string
	SkipRows      int // header rows to skip before the column header
}

// Registered bank formats. Clubs select which format to use when uploading.
var BankFormats = map[string]BankFormat{
	"sparebanken": {
		Name:          "Sparebanken",
		Delimiter:     ';',
		DateColumn:    "Dato",
		DateFormat:    "02.01.2006",
		DescColumn:    "Forklaring",
		DebitColumn:   "Ut av konto",
		CreditColumn:  "Inn på konto",
		BalanceColumn: "",
		KIDColumn:     "KID",
		RefColumn:     "Arkivref",
		CounterColumn: "",
	},
	"dnb": {
		Name:          "DNB",
		Delimiter:     ';',
		DateColumn:    "Dato",
		DateFormat:    "02.01.2006",
		DescColumn:    "Forklaring",
		AmountColumn:  "Beløp",
		BalanceColumn: "",
		KIDColumn:     "KID",
		RefColumn:     "",
		CounterColumn: "Motpart",
	},
	"sparebank1": {
		Name:          "SpareBank 1",
		Delimiter:     ';',
		DateColumn:    "Bokført dato",
		DateFormat:    "02.01.2006",
		DescColumn:    "Beskrivelse",
		DebitColumn:   "Ut",
		CreditColumn:  "Inn",
		BalanceColumn: "Saldo",
		KIDColumn:     "KID",
		RefColumn:     "",
		CounterColumn: "",
	},
}

// CSVParser parses a bank CSV file using a specific BankFormat configuration.
type CSVParser struct {
	Format BankFormat
}

// Parse reads a CSV and returns normalized BankRows.
func (p *CSVParser) Parse(reader io.Reader) ([]BankRow, error) {
	r := csv.NewReader(reader)
	r.Comma = p.Format.Delimiter
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1 // allow variable field count (bank CSVs are inconsistent)

	// Skip header rows
	for i := 0; i < p.Format.SkipRows; i++ {
		if _, err := r.Read(); err != nil {
			return nil, fmt.Errorf("skipping header row %d: %w", i, err)
		}
	}

	// Read column header
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}

	colIdx := make(map[string]int)
	for i, col := range header {
		colIdx[strings.TrimSpace(col)] = i
	}

	// Resolve column indices
	dateIdx := findCol(colIdx, p.Format.DateColumn)
	descIdx := findCol(colIdx, p.Format.DescColumn)
	amountIdx := findCol(colIdx, p.Format.AmountColumn)
	debitIdx := findCol(colIdx, p.Format.DebitColumn)
	creditIdx := findCol(colIdx, p.Format.CreditColumn)
	balanceIdx := findCol(colIdx, p.Format.BalanceColumn)
	kidIdx := findCol(colIdx, p.Format.KIDColumn)
	refIdx := findCol(colIdx, p.Format.RefColumn)
	counterIdx := findCol(colIdx, p.Format.CounterColumn)

	if dateIdx < 0 {
		return nil, fmt.Errorf("date column %q not found in header", p.Format.DateColumn)
	}
	if descIdx < 0 {
		return nil, fmt.Errorf("description column %q not found in header", p.Format.DescColumn)
	}

	var rows []BankRow
	lineNum := 1 + p.Format.SkipRows

	for {
		lineNum++
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed rows
		}

		date, err := time.Parse(p.Format.DateFormat, strings.TrimSpace(safeGet(record, dateIdx)))
		if err != nil {
			continue // skip rows with unparseable dates
		}

		var amount float64
		if amountIdx >= 0 {
			amount = parseNorwegianNumber(safeGet(record, amountIdx))
		} else {
			// Separate debit/credit columns
			debitVal := parseNorwegianNumber(safeGet(record, debitIdx))
			creditVal := parseNorwegianNumber(safeGet(record, creditIdx))
			if debitVal != 0 {
				amount = -debitVal // outflow is negative
			} else {
				amount = creditVal // inflow is positive
			}
		}

		row := BankRow{
			Date:        date,
			Description: strings.TrimSpace(safeGet(record, descIdx)),
			Amount:      amount,
			Reference:   strings.TrimSpace(safeGet(record, refIdx)),
			KID:         strings.TrimSpace(safeGet(record, kidIdx)),
			Counterpart: strings.TrimSpace(safeGet(record, counterIdx)),
		}

		if balanceIdx >= 0 {
			balStr := strings.TrimSpace(safeGet(record, balanceIdx))
			if balStr != "" {
				bal := parseNorwegianNumber(balStr)
				row.Balance = &bal
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// ListBankFormats returns the available bank format names.
func ListBankFormats() []string {
	names := make([]string, 0, len(BankFormats))
	for k := range BankFormats {
		names = append(names, k)
	}
	return names
}

// ImportRows stores parsed bank rows and runs KID auto-matching.
func (s *Service) ImportBankRows(ctx context.Context, clubID, importID, periodID string, rows []BankRow) (matched int, err error) {
	for _, row := range rows {
		var journalEntryID *string
		autoMatched := false

		// KID auto-match: check if this KID matches an outstanding invoice
		if row.KID != "" && row.Amount > 0 {
			var invoiceID string
			err := s.db.QueryRow(ctx,
				`SELECT i.id FROM invoices i
				 WHERE i.club_id = $1 AND i.kid_number = $2
				 AND NOT EXISTS (
				   SELECT 1 FROM bank_import_rows bir
				   WHERE bir.kid_number = $2 AND bir.journal_entry_id IS NOT NULL
				 )
				 LIMIT 1`,
				clubID, row.KID,
			).Scan(&invoiceID)

			if err == nil {
				// Found matching invoice — create settlement entry
				sourceID := importID
				sourceTable := "bank_import"
				entry, createErr := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
					FiscalPeriodID: periodID,
					EntryDate:      row.Date.Format("2006-01-02"),
					Description:    fmt.Sprintf("Innbetaling KID %s: %s", row.KID, row.Description),
					Source:         "bank_import",
					SourceID:       &sourceID,
					SourceTable:    &sourceTable,
					CreatedBy:      clubID, // system-generated
					ClubID:         clubID,
					Lines: []CreateJournalLineInput{
						{AccountCode: bankAccountCode, Debit: row.Amount, Credit: 0},
						{AccountCode: receivablesAccountCode, Debit: 0, Credit: row.Amount},
					},
				})
				if createErr == nil {
					if postErr := s.PostJournalEntry(ctx, entry.ID, clubID); postErr == nil {
						journalEntryID = &entry.ID
						autoMatched = true
						matched++
					}
				}
			}
		}

		var balance *float64
		if row.Balance != nil {
			balance = row.Balance
		}

		_, err = s.db.Exec(ctx,
			`INSERT INTO bank_import_rows (bank_import_id, row_date, description, amount, balance, reference, kid_number, counterpart, journal_entry_id, auto_matched)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			importID, row.Date, row.Description, row.Amount, balance, row.Reference, row.KID, row.Counterpart, journalEntryID, autoMatched,
		)
		if err != nil {
			return matched, fmt.Errorf("inserting bank row: %w", err)
		}
	}

	return matched, nil
}

// MatchBankRow manually creates a journal entry for an unmatched bank import row.
func (s *Service) MatchBankRow(ctx context.Context, clubID, periodID, rowID, debitCode, creditCode, matchedBy string, mvaAmount float64) error {
	var rowDate time.Time
	var description string
	var amount float64
	var existingEntry *string

	err := s.db.QueryRow(ctx,
		`SELECT bir.row_date, bir.description, bir.amount, bir.journal_entry_id
		 FROM bank_import_rows bir
		 JOIN bank_imports bi ON bi.id = bir.bank_import_id
		 WHERE bir.id = $1 AND bi.club_id = $2`,
		rowID, clubID,
	).Scan(&rowDate, &description, &amount, &existingEntry)
	if err != nil {
		return fmt.Errorf("getting bank row: %w", err)
	}
	if existingEntry != nil {
		return fmt.Errorf("row already matched")
	}

	// For outflows (negative amount), swap debit/credit
	debit := amount
	credit := amount
	if amount < 0 {
		debit = -amount // expense: debit the expense account
		credit = -amount // credit the bank account
	}

	sourceID := rowID
	sourceTable := "bank_import_rows"
	entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      rowDate.Format("2006-01-02"),
		Description:    description,
		Source:         "bank_import",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      matchedBy,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: debitCode, Debit: debit, Credit: 0, MVAAmount: mvaAmount},
			{AccountCode: creditCode, Debit: 0, Credit: credit},
		},
	})
	if err != nil {
		return fmt.Errorf("creating journal entry: %w", err)
	}

	_, err = s.db.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1 WHERE id = $2`,
		entry.ID, rowID,
	)
	return err
}

// parseNorwegianNumber converts "1 234,56" or "1234.56" to float64.
func parseNorwegianNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Remove thousand separators (space, non-breaking space, period used as thousands)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u00a0", "") // non-breaking space
	// Replace comma decimal with period
	s = strings.ReplaceAll(s, ",", ".")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func findCol(idx map[string]int, name string) int {
	if name == "" {
		return -1
	}
	if i, ok := idx[name]; ok {
		return i
	}
	return -1
}

func safeGet(record []string, idx int) string {
	if idx < 0 || idx >= len(record) {
		return ""
	}
	return record[idx]
}
