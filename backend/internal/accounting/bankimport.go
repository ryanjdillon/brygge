package accounting

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
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
// Loaded from YAML manifests in formats/*.yaml.
type BankFormat struct {
	Code               string   `yaml:"code"`
	Name               string   `yaml:"name"`
	Delimiter          string   `yaml:"delimiter"`
	DateColumn         string   `yaml:"date_column"`
	DateFormat         string   `yaml:"date_format"`
	DescriptionColumns []string `yaml:"description_columns"`
	AmountColumn       string   `yaml:"amount_column"`
	AmountSign         string   `yaml:"amount_sign"` // positive_in (default) | negated_out
	DebitColumn        string   `yaml:"debit_column"`
	CreditColumn       string   `yaml:"credit_column"`
	BalanceColumn      string   `yaml:"balance_column"`
	KIDColumn          string   `yaml:"kid_column"`
	RefColumn          string   `yaml:"ref_column"`
	CounterpartColumns []string `yaml:"counterpart_columns"`
	SkipRows           int      `yaml:"skip_rows"`
}

func (f BankFormat) delimRune() rune {
	if f.Delimiter == "" {
		return ';'
	}
	return rune(f.Delimiter[0])
}

//go:embed formats/*.yaml
var formatFiles embed.FS

// BankFormats is the registry of available bank formats, keyed by code.
// Populated at init from embedded YAML manifests.
var BankFormats = map[string]BankFormat{}

func init() {
	if err := loadFormats(formatFiles, "formats"); err != nil {
		panic(fmt.Errorf("loading bank formats: %w", err))
	}
}

func loadFormats(fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
		var f BankFormat
		if err := yaml.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
		if f.Code == "" {
			return fmt.Errorf("%s: missing code", e.Name())
		}
		if _, dup := BankFormats[f.Code]; dup {
			return fmt.Errorf("duplicate bank format code: %s", f.Code)
		}
		BankFormats[f.Code] = f
	}
	return nil
}

// CSVParser parses a bank CSV file using a specific BankFormat configuration.
type CSVParser struct {
	Format BankFormat
}

// Parse reads a CSV and returns normalized BankRows.
func (p *CSVParser) Parse(reader io.Reader) ([]BankRow, error) {
	r := csv.NewReader(reader)
	r.Comma = p.Format.delimRune()
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1

	for i := 0; i < p.Format.SkipRows; i++ {
		if _, err := r.Read(); err != nil {
			return nil, fmt.Errorf("skipping header row %d: %w", i, err)
		}
	}

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}

	colIdx := make(map[string]int)
	for i, col := range header {
		colIdx[strings.TrimSpace(stripBOM(col))] = i
	}

	dateIdx := findCol(colIdx, p.Format.DateColumn)
	descIdxs := findCols(colIdx, p.Format.DescriptionColumns)
	amountIdx := findCol(colIdx, p.Format.AmountColumn)
	debitIdx := findCol(colIdx, p.Format.DebitColumn)
	creditIdx := findCol(colIdx, p.Format.CreditColumn)
	balanceIdx := findCol(colIdx, p.Format.BalanceColumn)
	kidIdx := findCol(colIdx, p.Format.KIDColumn)
	refIdx := findCol(colIdx, p.Format.RefColumn)
	counterIdxs := findCols(colIdx, p.Format.CounterpartColumns)

	if dateIdx < 0 {
		return nil, fmt.Errorf("date column %q not found in header", p.Format.DateColumn)
	}
	if len(descIdxs) == 0 {
		return nil, fmt.Errorf("no description columns matched in header: %v", p.Format.DescriptionColumns)
	}

	var rows []BankRow
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		date, err := time.Parse(p.Format.DateFormat, strings.TrimSpace(safeGet(record, dateIdx)))
		if err != nil {
			continue
		}

		var amount float64
		switch {
		case amountIdx >= 0:
			amount = parseNorwegianNumber(safeGet(record, amountIdx))
			if p.Format.AmountSign == "negated_out" {
				amount = -amount
			}
		default:
			debitVal := parseNorwegianNumber(safeGet(record, debitIdx))
			creditVal := parseNorwegianNumber(safeGet(record, creditIdx))
			if debitVal != 0 {
				amount = -debitVal
			} else {
				amount = creditVal
			}
		}

		row := BankRow{
			Date:        date,
			Description: joinNonEmpty(record, descIdxs, " · "),
			Amount:      amount,
			Reference:   strings.TrimSpace(safeGet(record, refIdx)),
			KID:         strings.TrimSpace(safeGet(record, kidIdx)),
			Counterpart: firstNonEmpty(record, counterIdxs),
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

// ListBankFormats returns the available bank format codes, sorted for stable output.
func ListBankFormats() []string {
	names := make([]string, 0, len(BankFormats))
	for k := range BankFormats {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ImportResult summarizes one bank-import call.
type ImportResult struct {
	Imported   int
	SkippedDup int
	Matched    int
}

// BankRowHash computes the dedup hash for a normalized bank row, scoped per club.
// Recipe must stay in sync with the SQL backfill in the dedup migration.
func BankRowHash(clubID string, row BankRow) string {
	parts := []string{
		row.Date.Format("2006-01-02"),
		strconv.FormatFloat(row.Amount, 'f', 2, 64),
		row.Reference,
		row.Description,
		row.Counterpart,
	}
	h := sha256.Sum256([]byte(strings.ToLower(strings.Join(parts, "|"))))
	return hex.EncodeToString(h[:])
}

// ImportBankRows stores parsed bank rows (dedup by club_id + row_hash) and runs
// KID auto-matching on newly inserted rows. The KID-match journal entries
// debit the bank account associated with the import (bank_imports.bank_account_code),
// not a hard-coded constant.
func (s *Service) ImportBankRows(ctx context.Context, clubID, importID, periodID string, rows []BankRow) (ImportResult, error) {
	var res ImportResult

	bankAccount := bankAccountCode
	if err := s.db.QueryRow(ctx,
		`SELECT bank_account_code FROM bank_imports WHERE id = $1 AND club_id = $2`,
		importID, clubID,
	).Scan(&bankAccount); err != nil {
		// Fall back to default if the column isn't readable (pre-migration data).
		bankAccount = bankAccountCode
	}

	for _, row := range rows {
		var balance *float64
		if row.Balance != nil {
			balance = row.Balance
		}

		hash := BankRowHash(clubID, row)

		var rowID string
		err := s.db.QueryRow(ctx,
			`INSERT INTO bank_import_rows
			   (bank_import_id, club_id, row_date, description, amount, balance, reference, kid_number, counterpart, row_hash)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (club_id, row_hash) DO NOTHING
			 RETURNING id`,
			importID, clubID, row.Date, row.Description, row.Amount, balance,
			row.Reference, row.KID, row.Counterpart, hash,
		).Scan(&rowID)
		if err == pgx.ErrNoRows {
			res.SkippedDup++
			continue
		}
		if err != nil {
			return res, fmt.Errorf("inserting bank row: %w", err)
		}
		res.Imported++

		if row.KID == "" || row.Amount <= 0 {
			continue
		}

		var invoiceID string
		err = s.db.QueryRow(ctx,
			`SELECT i.id FROM invoices i
			 WHERE i.club_id = $1 AND i.kid_number = $2
			 AND NOT EXISTS (
			   SELECT 1 FROM bank_import_rows bir
			   WHERE bir.kid_number = $2 AND bir.journal_entry_id IS NOT NULL
			 )
			 LIMIT 1`,
			clubID, row.KID,
		).Scan(&invoiceID)
		if err != nil {
			continue
		}

		sourceID := importID
		sourceTable := "bank_import"
		entry, createErr := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID,
			EntryDate:      row.Date.Format("2006-01-02"),
			Description:    fmt.Sprintf("Innbetaling KID %s: %s", row.KID, row.Description),
			Source:         "bank_import",
			SourceID:       &sourceID,
			SourceTable:    &sourceTable,
			CreatedBy:      clubID,
			ClubID:         clubID,
			Lines: []CreateJournalLineInput{
				{AccountCode: bankAccount, Debit: row.Amount, Credit: 0},
				{AccountCode: receivablesAccountCode, Debit: 0, Credit: row.Amount},
			},
		})
		if createErr != nil {
			continue
		}
		if postErr := s.PostJournalEntry(ctx, entry.ID, clubID); postErr != nil {
			continue
		}
		if _, err := s.db.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true WHERE id = $2`,
			entry.ID, rowID,
		); err == nil {
			res.Matched++
		}
	}

	return res, nil
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

	debit := amount
	credit := amount
	if amount < 0 {
		debit = -amount
		credit = -amount
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

func parseNorwegianNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, " ", "")
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

func findCols(idx map[string]int, names []string) []int {
	out := make([]int, 0, len(names))
	for _, n := range names {
		if i := findCol(idx, n); i >= 0 {
			out = append(out, i)
		}
	}
	return out
}

func safeGet(record []string, idx int) string {
	if idx < 0 || idx >= len(record) {
		return ""
	}
	return record[idx]
}

func joinNonEmpty(record []string, idxs []int, sep string) string {
	parts := make([]string, 0, len(idxs))
	for _, i := range idxs {
		v := strings.TrimSpace(safeGet(record, i))
		if v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, sep)
}

func firstNonEmpty(record []string, idxs []int) string {
	for _, i := range idxs {
		if v := strings.TrimSpace(safeGet(record, i)); v != "" {
			return v
		}
	}
	return ""
}

func stripBOM(s string) string {
	return strings.TrimPrefix(s, "\ufeff")
}
