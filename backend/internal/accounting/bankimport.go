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
	FromAccount string // sender bank account number (for intra-bank transfer detection)
	ToAccount   string // receiver bank account number
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
	FromAccountColumn  string   `yaml:"from_account_column"`
	ToAccountColumn    string   `yaml:"to_account_column"`
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
	fromAcctIdx := findCol(colIdx, p.Format.FromAccountColumn)
	toAcctIdx := findCol(colIdx, p.Format.ToAccountColumn)

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
			FromAccount: strings.TrimSpace(safeGet(record, fromAcctIdx)),
			ToAccount:   strings.TrimSpace(safeGet(record, toAcctIdx)),
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
	Imported       int
	SkippedDup     int
	Matched        int
	Transfers      int      // intra-bank transfer pairs auto-linked
	ClosedPeriods  []string // labels of closed periods that prevented auto-match (e.g. "2025")
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
// KID auto-matching on newly inserted rows. Each created journal entry is
// posted to the fiscal period whose date range contains the row's date
// (auto-created as calendar-year if missing). Rows whose target period is
// closed are stored but not auto-matched.
//
// The optional periodOverride argument, if non-empty, forces every row to
// land in that one period regardless of date — kept for migration / special
// cases where the treasurer needs to consolidate.
func (s *Service) ImportBankRows(ctx context.Context, clubID, importID, periodOverride string, rows []BankRow) (ImportResult, error) {
	var res ImportResult
	closedYears := map[int]bool{}

	bankAccount := bankAccountCode
	var importedBy string
	if err := s.db.QueryRow(ctx,
		`SELECT bank_account_code, imported_by FROM bank_imports WHERE id = $1 AND club_id = $2`,
		importID, clubID,
	).Scan(&bankAccount, &importedBy); err != nil {
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
			   (bank_import_id, club_id, row_date, description, amount, balance, reference, kid_number, counterpart, row_hash, from_account, to_account)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			 ON CONFLICT (club_id, row_hash) DO NOTHING
			 RETURNING id`,
			importID, clubID, row.Date, row.Description, row.Amount, balance,
			row.Reference, row.KID, row.Counterpart, hash, row.FromAccount, row.ToAccount,
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

		periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, row.Date, periodOverride)
		if perr != nil {
			continue
		}
		if periodStatus == "closed" {
			closedYears[row.Date.Year()] = true
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
			CreatedBy:      importedBy,
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

	// Infer this import's own bank account number from the imported rows
	// (it's the value that appears across many rows' from/to columns) and
	// persist it on bank_imports so the transfer detector can use it.
	if num := inferOwnAccountNumber(rows); num != "" {
		_, _ = s.db.Exec(ctx,
			`UPDATE bank_imports SET bank_account_number = $1 WHERE id = $2 AND bank_account_number = ''`,
			num, importID,
		)
	}

	// Detect intra-bank transfers: rows on this import that match an
	// unmatched row on another of the club's bank imports (different
	// bank_account_code), same date, opposite amounts, account numbers
	// crossing. Auto-link both rows to a single draft entry.
	if importedBy != "" {
		transfers, skipped, derr := s.detectIntraBankTransfers(ctx, clubID, importID, periodOverride, importedBy)
		if derr != nil {
			return res, fmt.Errorf("intra-bank transfer detection: %w", derr)
		}
		res.Transfers = transfers
		for _, y := range skipped {
			closedYears[y] = true
		}
	}

	for y := range closedYears {
		res.ClosedPeriods = append(res.ClosedPeriods, fmt.Sprintf("%d", y))
	}
	sort.Strings(res.ClosedPeriods)

	return res, nil
}

// resolvePeriod returns the fiscal period that should hold an entry for the
// given row date. If override is non-empty, it's used verbatim. Otherwise
// the period containing the date is found-or-created.
func (s *Service) resolvePeriod(ctx context.Context, clubID string, date time.Time, override string) (id, status string, err error) {
	if override != "" {
		err = s.db.QueryRow(ctx,
			`SELECT id, status FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
			override, clubID,
		).Scan(&id, &status)
		if err != nil {
			return "", "", fmt.Errorf("loading override period: %w", err)
		}
		return id, status, nil
	}
	return s.ResolvePeriodForDate(ctx, clubID, date)
}

// inferOwnAccountNumber picks the bank account number that this CSV
// represents by counting which value appears most often across the
// from_account and to_account columns of all rows. The bank itself
// always sits on one side of every transaction it lists.
func inferOwnAccountNumber(rows []BankRow) string {
	counts := map[string]int{}
	for _, r := range rows {
		if r.FromAccount != "" {
			counts[r.FromAccount]++
		}
		if r.ToAccount != "" {
			counts[r.ToAccount]++
		}
	}
	var best string
	var bestN int
	for k, n := range counts {
		if n > bestN {
			best = k
			bestN = n
		}
	}
	return best
}

// detectIntraBankTransfers pairs unmatched rows on this import with
// unmatched rows on another import (different bank_account_code) where:
//   - dates match
//   - amounts sum to zero (opposite signs)
//   - the two rows' from_account/to_account references identify the
//     other import's bank account number
//
// For each pair, create one draft journal entry crediting the sending
// account and debiting the receiving account, then link both rows.
func (s *Service) detectIntraBankTransfers(ctx context.Context, clubID, importID, periodOverride, createdBy string) (matched int, skippedClosedYears []int, _ error) {
	rows, err := s.db.Query(ctx,
		`WITH this_import AS (
		   SELECT bank_account_code, bank_account_number FROM bank_imports WHERE id = $1
		 )
		 SELECT
		   r1.id, r1.row_date, r1.amount, ti.bank_account_code, ti.bank_account_number,
		   r2.id, bi2.bank_account_code, bi2.bank_account_number, r1.description
		 FROM bank_import_rows r1
		 CROSS JOIN this_import ti
		 JOIN bank_import_rows r2 ON r2.club_id = r1.club_id
		   AND r2.row_date  = r1.row_date
		   AND r2.amount    = -r1.amount
		   AND r2.journal_entry_id IS NULL
		   AND r2.id <> r1.id
		 JOIN bank_imports bi2 ON bi2.id = r2.bank_import_id
		   AND bi2.bank_account_code <> ti.bank_account_code
		   AND (bi2.bank_account_number = '' OR ti.bank_account_number = '' OR (
		         (r1.from_account = bi2.bank_account_number OR r1.to_account = bi2.bank_account_number)
		         AND (r2.from_account = ti.bank_account_number OR r2.to_account = ti.bank_account_number)
		       ))
		 WHERE r1.bank_import_id = $1
		   AND r1.club_id        = $2
		   AND r1.journal_entry_id IS NULL`,
		importID, clubID,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("querying transfer candidates: %w", err)
	}
	defer rows.Close()

	type pair struct {
		r1ID, r2ID, codeA, numA, codeB, numB, desc string
		date                                       time.Time
		amount                                     float64
	}
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.r1ID, &p.date, &p.amount, &p.codeA, &p.numA, &p.r2ID, &p.codeB, &p.numB, &p.desc); err != nil {
			s.log.Warn().Err(err).Msg("transfer detect scan error")
			continue
		}
		pairs = append(pairs, p)
	}
	rows.Close()

	closedYearSet := map[int]bool{}
	used := map[string]bool{}
	for _, p := range pairs {
		if used[p.r1ID] || used[p.r2ID] {
			continue
		}
		periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, p.date, periodOverride)
		if perr != nil {
			continue
		}
		if periodStatus == "closed" {
			closedYearSet[p.date.Year()] = true
			continue
		}
		debitCode, creditCode := p.codeB, p.codeA
		if p.amount > 0 {
			debitCode, creditCode = p.codeA, p.codeB
		}
		amount := p.amount
		if amount < 0 {
			amount = -amount
		}
		sourceID := p.r1ID
		sourceTable := "bank_import_rows"
		entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID,
			EntryDate:      p.date.Format("2006-01-02"),
			Description:    fmt.Sprintf("Intra-bank transfer: %s", p.desc),
			Source:         "bank_import",
			SourceID:       &sourceID,
			SourceTable:    &sourceTable,
			CreatedBy:      createdBy,
			ClubID:         clubID,
			Lines: []CreateJournalLineInput{
				{AccountCode: debitCode, Debit: amount, Credit: 0},
				{AccountCode: creditCode, Debit: 0, Credit: amount},
			},
		})
		if err != nil {
			continue
		}
		if _, err := s.db.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true WHERE id = ANY($2::uuid[])`,
			entry.ID, []string{p.r1ID, p.r2ID},
		); err == nil {
			used[p.r1ID] = true
			used[p.r2ID] = true
			matched++
		}
	}
	for y := range closedYearSet {
		skippedClosedYears = append(skippedClosedYears, y)
	}
	return matched, skippedClosedYears, nil
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
