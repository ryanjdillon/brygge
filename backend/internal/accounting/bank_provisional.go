package accounting

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/audit"
)

// A bank statement exported while a transfer is still in flight carries it as
// a provisional row: a bare generic text ("Overførsel", or the "Bokført på
// vei" placeholder), no counterpart and no KID, dated on the export day, which
// is the last day the export covers. A later export carries the settled row
// for the same transfer, with the real text (a Vipps payout's "Utb. N Vippsnr
// M", a payer's name). Left as it is, the provisional row sits in Tildel with
// nothing to match on, and a Vipps payout whose only deposit is provisional
// can never clear.
//
// Usually both rows carry the same bank reference (arkivref), so the import
// refreshes the provisional row in place with the settled text
// (refreshProvisionalByReference). When the settled row comes with a
// different reference, both rows exist side by side and
// SupersedeProvisionalBankRows dismisses the provisional one.

// bareTransferTexts are the generic texts the bank gives an in-flight row,
// lower-cased.
var bareTransferTexts = []string{"overførsel", "bokført på vei"}

// isBareTransfer reports whether a row carries nothing but a generic transfer
// text: no counterpart and no KID.
func isBareTransfer(description, counterpart, kid string) bool {
	if strings.TrimSpace(counterpart) != "" || strings.TrimSpace(kid) != "" {
		return false
	}
	d := strings.ToLower(strings.TrimSpace(description))
	for _, b := range bareTransferTexts {
		if d == b {
			return true
		}
	}
	return false
}

// provisionalRowSQL returns the predicate for a provisional row, over the
// bank row alias a. It is deliberately narrow: only the bare texts the bank
// is known to use, nothing identifying on the row, dated on the last day of
// its own statement (bank_imports.period_to; the latest surviving row for an
// import older than that column), and never seen again bare by a later
// statement (reseen_at).
func provisionalRowSQL(a string) string {
	return bareTransferSQL(a) + `
	AND ` + a + `.reseen_at IS NULL
	AND ` + a + `.row_date = (SELECT COALESCE(b.period_to,
	        (SELECT max(x.row_date) FROM bank_import_rows x WHERE x.bank_import_id = b.id))
	      FROM bank_imports b WHERE b.id = ` + a + `.bank_import_id)`
}

// bareTransferSQL is isBareTransfer in SQL, over the bank row alias a.
func bareTransferSQL(a string) string {
	return `lower(btrim(` + a + `.description)) IN ('` + strings.Join(bareTransferTexts, "', '") + `')
	AND btrim(` + a + `.counterpart) = '' AND btrim(` + a + `.kid_number) = ''`
}

// refOutcome is what the import does with a row whose reference is known.
type refOutcome int

const (
	refNew       refOutcome = iota // no row carries the reference: insert
	refDuplicate                   // already imported: skip
	refRefreshed                   // a provisional row was refreshed in place
)

// statementImport is the import a row is being imported into.
type statementImport struct {
	id      string
	account string
	// periodTo is the statement's last day (bank_imports.period_to).
	periodTo time.Time
}

// ReferenceMismatch is a provisional row whose settled row, carrying the
// same bank reference, came with a different amount or a date more than
// refreshDateGap later (or earlier). Neither row is changed; the settled
// row is skipped by the reference dedup, so the treasurer has to look.
type ReferenceMismatch struct {
	RowID     string  `json:"row_id"`
	Reference string  `json:"reference"`
	Date      string  `json:"date"`
	Amount    float64 `json:"amount"`
	NewDate   string  `json:"new_date"`
	NewAmount float64 `json:"new_amount"`
}

// refreshDateGap is how much later than its provisional row a settled row
// may be dated: the bank books an in-flight transfer a banking day or two
// after the export showed it.
const refreshDateGap = 2 * 24 * time.Hour

// refreshProvisionalByReference handles an incoming row whose bank reference
// another row of the club already carries. That is normally the same booking
// from an overlapping export, and it is skipped. Two cases differ:
//
//   - The settled form of a provisional row. When the existing row is
//     provisional (provisionalRowSQL), unbooked, undismissed and not
//     refund-paired, on the importing bank account, and the incoming row is
//     not bare, has the same amount to the øre and is dated the same day or
//     up to refreshDateGap later, the existing row takes the incoming date,
//     text, counterpart, KID and hash in place. Its id, import and history
//     stay. The same pair with another amount or date is returned as a
//     ReferenceMismatch and left alone.
//   - A bare row seen again, still bare, by a later statement that runs past
//     its date: the row is a settled transfer with a terse text, so it is
//     marked reseen_at and is never treated as provisional again.
//
// Anything else with the reference is skipped as before. Without an actor
// nothing is changed, since every change is audited. row is the incoming row
// after enrichment and hash its dedup hash.
func (s *Service) refreshProvisionalByReference(ctx context.Context, clubID, actorID string, imp statementImport, row BankRow, hash string) (refOutcome, *ReferenceMismatch, error) {
	found, err := s.rowsWithReference(ctx, clubID, row.Reference)
	if err != nil {
		return refNew, nil, err
	}
	if len(found) == 0 {
		return refNew, nil, nil
	}
	if len(found) > 1 || actorID == "" {
		return refDuplicate, nil, nil
	}
	e := found[0]
	incomingBare := isBareTransfer(row.Description, row.Counterpart, row.KID)

	if incomingBare {
		if isBareTransfer(e.desc, e.counterpart, e.kid) && e.importID != imp.id && imp.periodTo.After(e.date) {
			if err := s.markReseen(ctx, clubID, e.id); err != nil {
				return refNew, nil, err
			}
		}
		return refDuplicate, nil, nil
	}
	if !e.open || !e.provisional || e.account != imp.account {
		return refDuplicate, nil, nil
	}
	gap := dayOf(row.Date).Sub(dayOf(e.date))
	if math.Round(e.amount*100) != math.Round(row.Amount*100) || gap < 0 || gap > refreshDateGap {
		s.log.Info().Str("bank_row_id", e.id).Str("reference", row.Reference).
			Msg("bank import: reference matches a provisional row with another date or amount; skipped")
		return refDuplicate, &ReferenceMismatch{
			RowID: e.id, Reference: row.Reference,
			Date: e.date.Format("2006-01-02"), Amount: e.amount,
			NewDate: row.Date.Format("2006-01-02"), NewAmount: row.Amount,
		}, nil
	}
	// The hash guard keeps the (club_id, row_hash) dedup intact: if the
	// settled row already exists under another reference, the provisional
	// row is left for SupersedeProvisionalBankRows.
	tag, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows
		    SET row_date = $3, description = $4, counterpart = $5, kid_number = $6, row_hash = $7,
		        from_account = $8, to_account = $9
		  WHERE id = $1 AND club_id = $2
		    AND journal_entry_id IS NULL AND dismissed_at IS NULL AND refund_paired_with IS NULL
		    AND NOT EXISTS (SELECT 1 FROM bank_import_rows o
		                     WHERE o.club_id = $2 AND o.row_hash = $7 AND o.id <> $1)`,
		e.id, clubID, row.Date, row.Description, row.Counterpart, row.KID, hash, row.FromAccount, row.ToAccount,
	)
	if err != nil {
		return refNew, nil, fmt.Errorf("refreshing provisional bank row: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return refDuplicate, nil, nil
	}
	if s.audit != nil {
		details := map[string]any{"reference": row.Reference, "bank_import_id": imp.id}
		if gap > 0 {
			details["old_date"] = e.date.Format("2006-01-02")
			details["new_date"] = row.Date.Format("2006-01-02")
		}
		s.audit.LogAction(ctx, clubID, actorID, "", audit.ActionBankRowRefreshed, "bank_import_row", e.id, details)
	}
	return refRefreshed, nil, nil
}

// markReseen records that a later statement carried a bare row again, still
// bare, past its date, so it is a settled transfer and not provisional.
func (s *Service) markReseen(ctx context.Context, clubID, rowID string) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows SET reseen_at = now()
		  WHERE id = $1 AND club_id = $2 AND reseen_at IS NULL`,
		rowID, clubID,
	); err != nil {
		return fmt.Errorf("marking bank row reseen: %w", err)
	}
	return nil
}

// markReseenByHash is markReseen for a bare row without a bank reference that
// an incoming row hit through the hash dedup, from another import whose
// statement runs past the row's date.
func (s *Service) markReseenByHash(ctx context.Context, clubID string, imp statementImport, row BankRow, hash string) error {
	if !isBareTransfer(row.Description, row.Counterpart, row.KID) || !imp.periodTo.After(dayOf(row.Date)) {
		return nil
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows SET reseen_at = now()
		  WHERE club_id = $1 AND row_hash = $2 AND bank_import_id <> $3 AND reseen_at IS NULL`,
		clubID, hash, imp.id,
	); err != nil {
		return fmt.Errorf("marking bank row reseen: %w", err)
	}
	return nil
}

// recordStatementPeriod widens the import's statement period to cover rows.
// Callers that import a subset of a statement (the reimport paths) record
// the full statement first.
func (s *Service) recordStatementPeriod(ctx context.Context, clubID, importID string, rows []BankRow) error {
	if len(rows) == 0 {
		return nil
	}
	from, to := dayOf(rows[0].Date), dayOf(rows[0].Date)
	for _, r := range rows {
		d := dayOf(r.Date)
		if d.Before(from) {
			from = d
		}
		if d.After(to) {
			to = d
		}
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE bank_imports
		    SET period_from = LEAST(COALESCE(period_from, $3::date), $3::date),
		        period_to = GREATEST(COALESCE(period_to, $4::date), $4::date)
		  WHERE id = $1 AND club_id = $2`,
		importID, clubID, from, to,
	); err != nil {
		return fmt.Errorf("recording statement period: %w", err)
	}
	return nil
}

func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ProvisionalBankRow is one provisional row the supersede pass could not
// pair on its own, with how many settled rows it could belong to.
type ProvisionalBankRow struct {
	RowID      string  `json:"row_id"`
	Date       string  `json:"date"`
	Amount     float64 `json:"amount"`
	Candidates int     `json:"candidates"`
}

// SupersedeResult reports one SupersedeProvisionalBankRows run.
type SupersedeResult struct {
	// Superseded counts provisional rows dismissed as 'superseded'.
	Superseded int `json:"superseded"`
	// Ambiguous lists provisional rows with more than one possible settled
	// row, or whose settled row could replace more than one provisional row.
	// Both sides are left for the treasurer.
	Ambiguous []ProvisionalBankRow `json:"ambiguous"`
}

// SupersedeProvisionalBankRows dismisses, with reason 'superseded', every
// unbooked provisional bank row (provisionalRowSQL) whose settled row has
// arrived in another import: same club, same bank account, same date, same
// amount, a row that is not itself provisional and not dismissed. The settled
// row may be booked already. A pairing is acted on only when it is one to one:
// exactly one settled row for the provisional row, and exactly one provisional
// row for that settled row. Anything else is reported as ambiguous and left
// alone. Booked rows are never touched, and a row booked between the lookup
// and the update is skipped. Re-running finds nothing new. Order does not
// matter: the settled row may be imported before or after the provisional one.
//
// actorID is recorded as dismissed_by and on the audit row.
func (s *Service) SupersedeProvisionalBankRows(ctx context.Context, clubID, actorID string) (*SupersedeResult, error) {
	res := &SupersedeResult{Ambiguous: []ProvisionalBankRow{}}
	if actorID == "" {
		return res, nil
	}
	rows, err := s.db.Query(ctx,
		`WITH prov AS (
		     SELECT r.id, r.bank_import_id, bi.bank_account_code AS acct, r.row_date, r.amount
		       FROM bank_import_rows r
		       JOIN bank_imports bi ON bi.id = r.bank_import_id
		      WHERE r.club_id = $1 AND r.journal_entry_id IS NULL AND r.dismissed_at IS NULL
		        AND `+provisionalRowSQL("r")+`
		 ), pairs AS (
		     SELECT p.id AS prov_id, p.row_date, p.amount, f.id AS final_id
		       FROM prov p
		       JOIN bank_import_rows f
		         ON f.club_id = $1 AND f.row_date = p.row_date AND f.amount = p.amount
		        AND f.bank_import_id <> p.bank_import_id AND f.dismissed_at IS NULL
		       JOIN bank_imports fbi ON fbi.id = f.bank_import_id AND fbi.bank_account_code = p.acct
		      WHERE f.id NOT IN (SELECT id FROM prov)
		        AND NOT (`+bareTransferSQL("f")+`)
		 )
		 SELECT prov_id, final_id, row_date, amount,
		        count(*) OVER (PARTITION BY prov_id)  AS finals_per_prov,
		        count(*) OVER (PARTITION BY final_id) AS provs_per_final
		   FROM pairs
		  ORDER BY row_date, prov_id`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing provisional bank rows: %w", err)
	}
	type pair struct {
		provID, finalID string
		date            time.Time
		amount          float64
		perProv, perFin int
	}
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.provID, &p.finalID, &p.date, &p.amount, &p.perProv, &p.perFin); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning provisional bank row: %w", err)
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing provisional bank rows: %w", err)
	}

	reported := map[string]bool{}
	for _, p := range pairs {
		if p.perProv != 1 || p.perFin != 1 {
			if !reported[p.provID] {
				reported[p.provID] = true
				res.Ambiguous = append(res.Ambiguous, ProvisionalBankRow{
					RowID: p.provID, Date: p.date.Format("2006-01-02"), Amount: p.amount, Candidates: p.perProv,
				})
			}
			continue
		}
		tag, err := s.db.Exec(ctx,
			`UPDATE bank_import_rows
			    SET dismissed_at = now(), dismissed_by = $1, dismissed_reason = 'superseded'
			  WHERE id = $2 AND club_id = $3
			    AND journal_entry_id IS NULL AND dismissed_at IS NULL`,
			actorID, p.provID, clubID,
		)
		if err != nil {
			return res, fmt.Errorf("dismissing provisional bank row: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		res.Superseded++
		if s.audit != nil {
			s.audit.LogAction(ctx, clubID, actorID, "", audit.ActionBankRowDismissed, "bank_import_row", p.provID,
				map[string]any{"reason": "superseded", "superseded_by": p.finalID, "automatic": true})
		}
	}
	return res, nil
}

// SupersedeProvisionalBankRowsReported runs SupersedeProvisionalBankRows as
// a step of an import or sync. A failure is logged and an empty result
// returned; the caller's own work is unaffected and the next run retries.
func (s *Service) SupersedeProvisionalBankRowsReported(ctx context.Context, clubID, actorID string) *SupersedeResult {
	res, err := s.SupersedeProvisionalBankRows(ctx, clubID, actorID)
	if err != nil {
		s.log.Error().Err(err).Str("club_id", clubID).Msg("superseding provisional bank rows failed")
		if res == nil {
			res = &SupersedeResult{Ambiguous: []ProvisionalBankRow{}}
		}
	}
	return res
}

// provisionalDepositDate returns the date of an open provisional bank row
// that could be an imported payout's money (the payout's amount to the øre,
// dated on or up to ten days after its booking date), or the zero time when
// there is none. The payout sweep only names it; the settled row from a
// fresh export is what clears the payout.
func (s *Service) provisionalDepositDate(ctx context.Context, clubID string, payoutAmount float64, booking time.Time) (time.Time, error) {
	var d *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT min(r.row_date) FROM bank_import_rows r
		  WHERE r.club_id = $1 AND r.journal_entry_id IS NULL AND r.dismissed_at IS NULL
		    AND round(r.amount * 100) = round(-$2::numeric * 100)
		    AND r.row_date BETWEEN $3::date AND $3::date + 10
		    AND `+provisionalRowSQL("r"),
		clubID, payoutAmount, booking,
	).Scan(&d)
	if err != nil {
		return time.Time{}, fmt.Errorf("looking up provisional deposit: %w", err)
	}
	if d == nil {
		return time.Time{}, nil
	}
	return *d, nil
}

// provisionalRowsOnLastDay counts the unbooked provisional rows an import
// holds on its last day, for the import notice.
func (s *Service) provisionalRowsOnLastDay(ctx context.Context, clubID, importID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM bank_import_rows r
		  WHERE r.club_id = $1 AND r.bank_import_id = $2
		    AND r.journal_entry_id IS NULL AND r.dismissed_at IS NULL
		    AND `+provisionalRowSQL("r"),
		clubID, importID,
	).Scan(&n)
	return n, err
}

// settleProvisionalRows is the import's provisional-row step: supersede what
// this import settled, and note the statement's last day and the provisional
// rows the import still holds on it.
func (s *Service) settleProvisionalRows(ctx context.Context, clubID, importedBy string, imp statementImport, res *ImportResult) {
	if importedBy != "" {
		sup := s.SupersedeProvisionalBankRowsReported(ctx, clubID, importedBy)
		res.Superseded = sup.Superseded
		res.ProvisionalAmbiguous = sup.Ambiguous
	}
	if !imp.periodTo.IsZero() {
		res.LastRowDate = imp.periodTo.Format("2006-01-02")
	}
	n, err := s.provisionalRowsOnLastDay(ctx, clubID, imp.id)
	if err != nil {
		s.log.Warn().Err(err).Str("import_id", imp.id).Msg("counting provisional bank rows failed")
		return
	}
	res.ProvisionalRows = n
}

// statementImportFor records the statement period for rows on the import and
// returns what the per-row import steps need to know about it.
func (s *Service) statementImportFor(ctx context.Context, clubID, importID, account string, rows []BankRow) (statementImport, error) {
	imp := statementImport{id: importID, account: account}
	if err := s.recordStatementPeriod(ctx, clubID, importID, rows); err != nil {
		return imp, err
	}
	var to *time.Time
	if err := s.db.QueryRow(ctx,
		`SELECT period_to FROM bank_imports WHERE id = $1 AND club_id = $2`, importID, clubID,
	).Scan(&to); err != nil {
		return imp, fmt.Errorf("reading statement period: %w", err)
	}
	if to != nil {
		imp.periodTo = dayOf(*to)
	}
	return imp, nil
}

// referencedRow is an existing bank row carrying an incoming row's reference.
type referencedRow struct {
	id, importID, desc, counterpart, kid, account string
	date                                          time.Time
	amount                                        float64
	open, provisional                             bool
}

func (s *Service) rowsWithReference(ctx context.Context, clubID, reference string) ([]referencedRow, error) {
	rows, err := s.db.Query(ctx,
		`SELECT r.id, r.bank_import_id, r.row_date, r.amount, r.description, r.counterpart, r.kid_number,
		        bi.bank_account_code,
		        r.journal_entry_id IS NULL AND r.dismissed_at IS NULL AND r.refund_paired_with IS NULL,
		        (`+provisionalRowSQL("r")+`)
		   FROM bank_import_rows r
		   JOIN bank_imports bi ON bi.id = r.bank_import_id
		  WHERE r.club_id = $1 AND r.reference = $2`,
		clubID, reference,
	)
	if err != nil {
		return nil, fmt.Errorf("looking up bank reference: %w", err)
	}
	defer rows.Close()
	var found []referencedRow
	for rows.Next() {
		var e referencedRow
		if err := rows.Scan(&e.id, &e.importID, &e.date, &e.amount, &e.desc, &e.counterpart, &e.kid,
			&e.account, &e.open, &e.provisional); err != nil {
			return nil, fmt.Errorf("scanning bank reference: %w", err)
		}
		found = append(found, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("looking up bank reference: %w", err)
	}
	return found, nil
}

// importByReference is the import's reference step for one row: it returns
// true when the row is fully handled (skipped as already imported, or used to
// refresh a provisional row) and must not be inserted.
func (s *Service) importByReference(ctx context.Context, clubID, actorID string, imp statementImport, row BankRow, hash string, res *ImportResult) (bool, error) {
	if row.Reference == "" {
		return false, nil
	}
	outcome, mismatch, err := s.refreshProvisionalByReference(ctx, clubID, actorID, imp, row, hash)
	if err != nil {
		return false, err
	}
	if mismatch != nil {
		res.ReferenceMismatches = append(res.ReferenceMismatches, *mismatch)
	}
	switch outcome {
	case refDuplicate:
		res.SkippedDup++
		return true, nil
	case refRefreshed:
		res.Refreshed++
		return true, nil
	}
	return false, nil
}
