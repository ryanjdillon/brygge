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
	"unicode"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/brygge-klubb/brygge/internal/finance"
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
	// CounterpartCharset names a legacy encoding used only by the payer-name
	// field (some Norwegian banks store names via a 7-bit giro subsystem while
	// the rest of the file is UTF-8). "iso646-no" reverses that substitution.
	CounterpartCharset string `yaml:"counterpart_charset"`
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

		counterpart := firstNonEmpty(record, counterIdxs)
		if p.Format.CounterpartCharset == "iso646-no" {
			counterpart = decodeISO646NOName(counterpart)
		}

		row := BankRow{
			Date:        date,
			Description: joinNonEmpty(record, descIdxs, " · "),
			Amount:      amount,
			Reference:   strings.TrimSpace(safeGet(record, refIdx)),
			KID:         strings.TrimSpace(safeGet(record, kidIdx)),
			Counterpart: counterpart,
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
	// RowErrors lists rows whose auto-match failed after an invoice was
	// found. The row stays unmatched for manual Tildel; the reason is
	// surfaced instead of being swallowed.
	RowErrors []ImportRowError
	// Refreshed counts provisional rows updated in place with the settled
	// text of an incoming row carrying the same bank reference
	// (refreshProvisionalByReference).
	Refreshed int
	// Superseded counts provisional rows (provisionalRowSQL) dismissed
	// because their settled row is now imported; ProvisionalAmbiguous lists
	// provisional rows that could not be paired one to one.
	Superseded           int
	ProvisionalAmbiguous []ProvisionalBankRow
	// LastRowDate is the latest row date in the statement, and
	// ProvisionalRows the unbooked provisional rows this import still holds
	// on that day. A statement exported on the day it ends can carry
	// in-flight transfers as provisional rows.
	LastRowDate     string
	ProvisionalRows int
}

// ImportRowError is one auto-match failure on an imported bank row.
type ImportRowError struct {
	RowID  string  `json:"row_id"`
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
	Reason string  `json:"reason"`
}

func (r *ImportResult) rowError(rowID string, row BankRow, reason string) {
	r.RowErrors = append(r.RowErrors, ImportRowError{
		RowID:  rowID,
		Date:   row.Date.Format("2006-01-02"),
		Amount: row.Amount,
		Reason: reason,
	})
}

// ReimportResult reports a re-import that replaced only the unmatched rows of
// an existing import.
type ReimportResult struct {
	ImportResult
	Deleted       int64 // unmatched/non-dismissed rows removed before re-parse
	SkippedBooked int   // parsed rows skipped because a booked row already represents them
}

// ReimportBankRows re-parses a corrected upload for an existing import and
// replaces ONLY its unmatched, non-dismissed, non-refund-paired rows. Matched
// rows carry a booked bilag + payment and are never touched. A parsed row that
// a surviving BOOKED row already represents — keyed on (date, amount, arkivref
// found in EITHER reference or kid_number, so an originally mis-parsed match is
// still recognised) — is skipped, so a re-import can never duplicate an
// already-matched payment. Fresh rows go through the normal insert + auto-match
// cascade.
func (s *Service) ReimportBankRows(ctx context.Context, clubID, importID, periodOverride string, rows []BankRow) (ReimportResult, error) {
	var res ReimportResult

	tag, err := s.db.Exec(ctx,
		`DELETE FROM bank_import_rows
		  WHERE bank_import_id = $1
		    AND journal_entry_id IS NULL
		    AND dismissed_at IS NULL
		    AND refund_paired_with IS NULL`,
		importID,
	)
	if err != nil {
		return res, fmt.Errorf("clearing unmatched rows: %w", err)
	}
	res.Deleted = tag.RowsAffected()

	fresh, skipped, err := s.skipRowsAlreadyBooked(ctx, clubID, rows)
	if err != nil {
		return res, err
	}
	res.SkippedBooked = skipped

	imp, err := s.ImportBankRows(ctx, clubID, importID, periodOverride, fresh)
	if err != nil {
		return res, err
	}
	res.ImportResult = imp
	return res, nil
}

// skipRowsAlreadyBooked drops any parsed row that a surviving matched row
// already represents — same booking (row_date + amount + arkivref/KID) with a
// journal entry attached. It returns the rows safe to re-insert plus the number
// skipped. This is what stops a re-import from duplicating a booked payment when
// the file's date range overlaps transactions that were previously mis-parsed
// and separately booked.
func (s *Service) skipRowsAlreadyBooked(ctx context.Context, clubID string, rows []BankRow) ([]BankRow, int, error) {
	var fresh []BankRow
	skipped := 0
	for _, row := range rows {
		ref := strings.TrimSpace(row.Reference)
		if ref != "" {
			var booked bool
			if err := s.db.QueryRow(ctx,
				`SELECT EXISTS (
				    SELECT 1 FROM bank_import_rows bir
				     WHERE bir.club_id = $1
				       AND bir.journal_entry_id IS NOT NULL
				       AND bir.row_date = $2
				       AND bir.amount = $3
				       AND ($4 = bir.reference OR $4 = bir.kid_number)
				 )`,
				clubID, row.Date, row.Amount, ref,
			).Scan(&booked); err != nil {
				return nil, 0, fmt.Errorf("checking booked duplicate: %w", err)
			}
			if booked {
				skipped++
				continue
			}
		}
		fresh = append(fresh, row)
	}
	return fresh, skipped, nil
}

// ReimportAccountBankRows does a full-account reconcile from one complete CSV.
// It clears the account's unmatched ghost rows *only within the date range the
// CSV actually covers* — rows outside that window are left alone (this file
// doesn't re-provide them), and booked rows (bilag + payment) are never touched
// regardless. It then imports the supplied rows into targetImportID; a booked
// transaction that reappears in the file is skipped so no payment is
// duplicated. Use for a periodic "import everything since Jan 1 for this
// account" cleanup pass. See BRY-222.
func (s *Service) ReimportAccountBankRows(ctx context.Context, clubID, bankAccountCode, targetImportID, periodOverride string, rows []BankRow) (ReimportResult, error) {
	var res ReimportResult

	// Nothing parsed → clear nothing. Deleting on an empty file would be an
	// unbounded wipe with no replacement.
	if len(rows) == 0 {
		return res, nil
	}

	from, to := rows[0].Date, rows[0].Date
	for _, row := range rows {
		if row.Date.Before(from) {
			from = row.Date
		}
		if row.Date.After(to) {
			to = row.Date
		}
	}

	tag, err := s.db.Exec(ctx,
		`DELETE FROM bank_import_rows
		  WHERE journal_entry_id IS NULL
		    AND dismissed_at IS NULL
		    AND refund_paired_with IS NULL
		    AND row_date BETWEEN $3 AND $4
		    AND bank_import_id IN (
		      SELECT id FROM bank_imports WHERE club_id = $1 AND bank_account_code = $2
		    )`,
		clubID, bankAccountCode, from, to,
	)
	if err != nil {
		return res, fmt.Errorf("clearing unmatched account rows: %w", err)
	}
	res.Deleted = tag.RowsAffected()

	fresh, skipped, err := s.skipRowsAlreadyBooked(ctx, clubID, rows)
	if err != nil {
		return res, err
	}
	res.SkippedBooked = skipped

	imp, err := s.ImportBankRows(ctx, clubID, targetImportID, periodOverride, fresh)
	if err != nil {
		return res, err
	}
	res.ImportResult = imp
	return res, nil
}

// enrichBankRow recovers what a statement row leaves out of its own columns.
// Norwegian banks often leave the KID column empty for rows whose payer typed
// the reference into the description ("Forklaring") instead, so the KID is
// recovered from the description. And the Motpart column for inbound
// transfers often carries the club's own account label rather than the
// payer's name; when the description carries "Fra: <Name> Betalt:", that
// name is the counterpart. The dedup hash is computed on the raw row, before
// this, so re-imports of the same statement still dedup.
func enrichBankRow(row BankRow) BankRow {
	if row.KID == "" {
		if extracted := ExtractKIDFromDescription(row.Description); extracted != "" {
			row.KID = extracted
		}
	}
	if payer := ExtractPayerFromDescription(row.Description); payer != "" {
		row.Counterpart = payer
	}
	return row
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
// kidMatchCandidates returns the distinct non-empty KID-like values worth
// matching a bank row against an invoice's kid_number: the parsed KID, the
// arkivref (some giro-med-melding exports carry the KID there), and any KID
// embedded in the description. Matching is exact-equality against real invoice
// KIDs, so trying extra candidates can't produce a false match.
func kidMatchCandidates(row BankRow) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range []string{row.KID, row.Reference, ExtractKIDFromDescription(row.Description)} {
		c = strings.TrimSpace(c)
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

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

		// Compute the dedup hash on the RAW CSV row state — the
		// recipe is documented to stay in lockstep with the
		// migration that backfilled hashes for existing data.
		// Enrichment (KID + payer extraction below) happens after
		// so re-imports of the same CSV still dedup correctly.
		hash := BankRowHash(clubID, row)

		row = enrichBankRow(row)

		// Arkivref (bank's canonical booking ID) is stable across
		// re-exports even when Sparebank reformats the description
		// text. Check it before the hash insert so overlapping CSV
		// date ranges never create a second row for the same booking.
		// The one exception refreshes a provisional in-flight row with
		// its settled text (refreshProvisionalByReference).
		if row.Reference != "" {
			outcome, rerr := s.refreshProvisionalByReference(ctx, clubID, importedBy, importID, row, hash)
			if rerr != nil {
				return res, rerr
			}
			switch outcome {
			case refDuplicate:
				res.SkippedDup++
				continue
			case refRefreshed:
				res.Refreshed++
				continue
			}
		}

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

		if row.Amount <= 0 {
			continue
		}

		// Resolve invoice id. Try each KID-like candidate (parsed KID,
		// arkivref, KID embedded in the description) — some giro-med-melding
		// exports carry the real KID in a column other than the KID field.
		// Then fall back to "Fakturanummer NN" by invoice_number, and finally
		// to a unique counterpart-name + amount match.
		var invoiceID string
		for _, cand := range kidMatchCandidates(row) {
			_ = s.db.QueryRow(ctx,
				`SELECT i.id FROM invoices i
				 WHERE i.club_id = $1 AND i.kid_number = $2
				 AND `+finance.OwedByMemberSQL("i")+`
				 AND NOT EXISTS (
				   SELECT 1 FROM bank_import_rows bir
				   WHERE bir.club_id = i.club_id AND bir.kid_number = $2 AND bir.journal_entry_id IS NOT NULL
				 )
				 LIMIT 1`,
				clubID, cand,
			).Scan(&invoiceID)
			if invoiceID != "" {
				break
			}
		}
		if invoiceID == "" {
			if num := ExtractInvoiceNumberFromDescription(row.Description); num != "" {
				// Match by invoice_number, but only when the
				// amount matches the invoice total — otherwise
				// a payer paying part of a multi-line invoice
				// could land on the wrong row.
				_ = s.db.QueryRow(ctx,
					`SELECT i.id FROM invoices i
					 WHERE i.club_id = $1 AND i.invoice_number::text = $2
					   AND ABS(i.total_amount - $3) < 0.005
					   AND `+finance.OwedByMemberSQL("i")+`
					   AND NOT EXISTS (
					     SELECT 1 FROM bank_import_rows bir
					     WHERE bir.club_id = i.club_id AND bir.journal_entry_id IS NOT NULL
					       AND bir.kid_number = i.kid_number
					   )
					 LIMIT 1`,
					clubID, num, row.Amount,
				).Scan(&invoiceID)
			}
		}
		if invoiceID == "" && row.Counterpart != "" {
			// Tertiary: a unique open unpaid invoice whose member's name
			// equals the bank counterpart and whose total matches the amount
			// — catches giro-med-melding payments where the KID never
			// validated. Same helper the on-demand BankSync uses; ambiguity
			// (2+ candidates) leaves the row unmatched for manual review.
			invoiceID = s.findInvoiceByCounterpartAndAmount(ctx, clubID, row.Counterpart, row.Amount)
		}
		if invoiceID == "" {
			continue
		}

		// Posting needs a real user for posted_by (FK to users). Without
		// one the entry would be left as an unposted orphan draft while the
		// row stays unmatched — and a later bank sync books it again.
		if importedBy == "" {
			res.rowError(rowID, row, "import has no importing user; match it manually")
			continue
		}

		periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, row.Date, periodOverride)
		if perr != nil {
			res.rowError(rowID, row, "resolving fiscal period: "+perr.Error())
			continue
		}
		if periodStatus == "closed" {
			closedYears[row.Date.Year()] = true
			continue
		}

		sourceID := rowID
		sourceTable := "bank_import_rows"
		entry, createErr := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID,
			EntryDate:      row.Date.Format("2006-01-02"),
			Description:    kidMatchDescription(row.KID, row.Description),
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
			res.rowError(rowID, row, "creating journal entry: "+createErr.Error())
			continue
		}
		// Link the row to the draft before posting, guarded on the row still
		// being unbooked: a posted entry must never exist without its row,
		// or a later bank sync would book the same money again. If the link
		// or the post fails, unwind both.
		tag, linkErr := s.db.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true
			  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL`,
			entry.ID, rowID, clubID,
		)
		if linkErr != nil || tag.RowsAffected() != 1 {
			s.discardDraft(ctx, clubID, entry.ID)
			reason := "row was booked concurrently"
			if linkErr != nil {
				reason = linkErr.Error()
			}
			res.rowError(rowID, row, "linking row: "+reason)
			continue
		}
		if postErr := s.PostJournalEntry(ctx, clubID, entry.ID, importedBy); postErr != nil {
			s.unwindVippsPosting(ctx, clubID, entry.ID, "bank_import_rows", []string{rowID})
			res.rowError(rowID, row, "posting journal entry: "+postErr.Error())
			continue
		}
		res.Matched++
		// Mirror the GL credit-to-receivables side onto the invoice flag
		// so the dashboard, faktura list, and priceItemSummary reflect the
		// payment. Best-effort — failures here don't roll back the journal
		// because the GL itself is already correct.
		if lerr := s.linkInvoicePayment(ctx, clubID, invoiceID, row.Date); lerr != nil {
			s.log.Warn().Err(lerr).
				Str("invoice_id", invoiceID).
				Str("bank_row_id", rowID).
				Msg("import-time KID match journal posted but invoice payment back-link failed")
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

	// Settle provisional rows before transfer detection, so a stale
	// in-flight row is never paired as one side of a transfer.
	s.settleProvisionalRows(ctx, clubID, importID, importedBy, rows, &res)

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

// InferOwnAccountNumber picks the bank account number that this CSV
// represents by counting which value appears most often across the
// from_account and to_account columns of all rows. The bank itself
// always sits on one side of every transaction it lists.
func InferOwnAccountNumber(rows []BankRow) string {
	return inferOwnAccountNumber(rows)
}

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
			s.log.Warn().Err(err).Str("bank_row_id", p.r1ID).Msg("intra-bank transfer: CreateJournalEntry failed; rows stay unmatched")
			continue
		}
		tag, err := s.db.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true
			  WHERE id = ANY($2::uuid[]) AND club_id = $3 AND journal_entry_id IS NULL`,
			entry.ID, []string{p.r1ID, p.r2ID}, clubID,
		)
		if err != nil || tag.RowsAffected() != 2 {
			// One side was booked concurrently: a transfer needs both rows,
			// so release whichever row this call did link and drop the draft.
			s.log.Warn().Err(err).Str("bank_row_id", p.r1ID).Msg("intra-bank transfer: could not link both rows; draft discarded")
			_, _ = s.db.Exec(ctx,
				`UPDATE bank_import_rows SET journal_entry_id = NULL, auto_matched = false
				  WHERE journal_entry_id = $1 AND club_id = $2`,
				entry.ID, clubID)
			s.discardDraft(ctx, clubID, entry.ID)
			continue
		}
		used[p.r1ID] = true
		used[p.r2ID] = true
		matched++
	}
	for y := range closedYearSet {
		skippedClosedYears = append(skippedClosedYears, y)
	}
	return matched, skippedClosedYears, nil
}

// kidMatchDescription is the description every payment-match entry (bank
// import and bank sync, KID / invoice-number / counterpart cascade) carries.
// Bank sync's stranded-draft heal recognises these entries by this exact
// shape, so it must stay in step with kidMatchDescriptionSQL.
func kidMatchDescription(kid, desc string) string {
	if kid == "" {
		return fmt.Sprintf("Innbetaling: %s", desc)
	}
	return fmt.Sprintf("Innbetaling KID %s: %s", kid, desc)
}

// kidMatchDescriptionSQL is kidMatchDescription over a bank_import_rows
// alias b.
const kidMatchDescriptionSQL = `CASE WHEN b.kid_number <> ''
	THEN 'Innbetaling KID ' || b.kid_number || ': ' || b.description
	ELSE 'Innbetaling: ' || b.description END`

// discardDraft removes a draft journal entry an auto-match created but
// could not complete, so a failed match leaves no orphan behind. Posted
// entries are never touched.
func (s *Service) discardDraft(ctx context.Context, clubID, entryID string) {
	if _, err := s.db.Exec(ctx,
		`DELETE FROM journal_entries WHERE id = $1 AND club_id = $2 AND status = 'draft'`,
		entryID, clubID,
	); err != nil {
		s.log.Error().Err(err).Str("entry_id", entryID).Msg("failed to discard orphan draft")
	}
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

	tag, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1
		  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL`,
		entry.ID, rowID, clubID,
	)
	if err != nil || tag.RowsAffected() != 1 {
		s.discardDraft(ctx, clubID, entry.ID)
		if err != nil {
			return fmt.Errorf("linking row: %w", err)
		}
		return fmt.Errorf("row already matched")
	}
	return nil
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
		v := collapseWhitespace(safeGet(record, i))
		if v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, sep)
}

// collapseWhitespace trims and replaces any run of whitespace
// (incl. newlines and tabs) with a single space. Bank CSV "Melding"
// fields sometimes embed multi-line tabular blocks (e.g. Norsk
// Tipping invoice details) that look like garbage in the row table.
func collapseWhitespace(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
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

// iso646NONameSubst maps each 7-bit substitution symbol some Norwegian banks
// use in the payer-name field (a legacy giro subsystem, while the rest of the
// CSV is UTF-8) to its Norwegian letter as {word-start, mid-word}. A person or
// company name never legitimately contains these ASCII symbols, so the mapping
// is unambiguous *in the name field only* \u2014 it must not be applied to free-text
// meldings (which can carry a real "@" in an email). Confirmed against real
// Sparebank Norge names.
var iso646NONameSubst = map[rune][2]rune{
	'@': {'\u00d8', '\u00f8'},
	'|': {'\u00d8', '\u00f8'},
	'}': {'\u00c5', '\u00e5'},
	'{': {'\u00c6', '\u00e6'},
}

// decodeISO646NOName restores Norwegian letters in a bank-provided payer name.
// The bank upper-cases the name and then title-cases it with the substitution
// symbols treated as word boundaries, so the letter right after a symbol is
// spuriously capitalised ("Lok\u00f8y"\u2192"Lok@Y", "\u00d8ygarden"\u2192"@Ygarden",
// "B\u00e5tlag"\u2192"B}Tlag"). This reverses both effects: the Norwegian letter is
// upper-case only at a word start, and the letter following a symbol is
// lower-cased back.
func decodeISO646NOName(s string) string {
	runes := []rune(s)
	out := make([]rune, 0, len(runes))
	lowerNext := false
	for i, r := range runes {
		if pair, ok := iso646NONameSubst[r]; ok {
			if i == 0 || runes[i-1] == ' ' {
				out = append(out, pair[0]) // word start \u2192 upper-case
			} else {
				out = append(out, pair[1]) // mid-word \u2192 lower-case
			}
			lowerNext = true
			continue
		}
		if lowerNext {
			r = unicode.ToLower(r)
			lowerNext = false
		}
		out = append(out, r)
	}
	return string(out)
}
