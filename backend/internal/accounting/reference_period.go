package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// A reference period is a fiscal year whose books of record live in another
// system (BRY-418). Its data stays and its reports still open, but it is left
// out of every queue, count, automatic pass and carried balance. The only
// bookings it takes are costs: an entry that touches nothing but cost and
// bank accounts, with at least one cost line, so the year's momskompensasjon
// can still be filed from Brygge.

var (
	// ErrReferencePeriod is returned for a booking into a reference period
	// (or dated in one) that is not expense-shaped. The text is Norwegian
	// because several SPA toasts print it verbatim.
	ErrReferencePeriod = errors.New("året er et referanseår: her kan bare kostnader bokføres")
	// ErrPeriodNotFound is a period id that does not exist or is another
	// club's.
	ErrPeriodNotFound = errors.New("fiscal period not found")
	// ErrReferenceCurrentYear refuses marking the year that covers today:
	// the year being kept in Brygge cannot be a reference year.
	ErrReferenceCurrentYear = errors.New("inneværende år kan ikke være et referanseår")
	// ErrReferenceHasOpeningBalance refuses marking a year that holds an
	// opening balance: the cutover year is Brygge's books of record.
	ErrReferenceHasOpeningBalance = errors.New("året har inngående balanse og kan ikke være et referanseår")
)

// referenceDateSQL is the one predicate for "this row's own date falls in one
// of the club's reference periods". clubExpr and dateExpr are SQL expressions
// of the enclosing query and must be qualified with their table: a bare
// club_id inside the subquery would resolve to rp.club_id, which is always
// true.
func referenceDateSQL(clubExpr, dateExpr string) string {
	return `EXISTS (SELECT 1 FROM fiscal_periods rp
	          WHERE rp.club_id = ` + clubExpr + ` AND rp.reference_only
	            AND ` + dateExpr + ` BETWEEN rp.start_date AND rp.end_date)`
}

// notReferenceEntrySQL is true for a journal entry (alias je) that is neither
// in a reference period nor dated in one. The passes that delete a draft and
// rebook it use it on their candidates: a reference year's drafts are kept as
// they are, and rebooking one would be refused by the guard after the delete.
func notReferenceEntrySQL(je string) string {
	return `(NOT EXISTS (SELECT 1 FROM fiscal_periods rpp
	                     WHERE rpp.id = ` + je + `.fiscal_period_id AND rpp.reference_only)
	        AND NOT ` + referenceDateSQL(je+".club_id", je+".entry_date") + `)`
}

// ReferenceDateSQL is referenceDateSQL for the handlers' own queries.
func ReferenceDateSQL(clubExpr, dateExpr string) string { return referenceDateSQL(clubExpr, dateExpr) }

// NotReferenceEntrySQL is notReferenceEntrySQL for the handlers' own queries.
func NotReferenceEntrySQL(je string) string { return notReferenceEntrySQL(je) }

// refuseReferencePeriod is the up-front refusal of a whole-period operation
// (a sync, a resplit, a backlog posting, an auto-match) on a reference period:
// it would only be refused entry by entry by the guard.
func (s *Service) refuseReferencePeriod(ctx context.Context, clubID, periodID string) error {
	ref, err := periodIsReferenceIn(ctx, s.db, clubID, periodID)
	if err != nil {
		return err
	}
	if ref {
		return ErrReferencePeriod
	}
	return nil
}

// refuseReferenceVippsCharge refuses to undo the booking of a Vipps charge
// dated in a reference year, whatever its entry's state: the unassign reopens
// the fakturas the charge settled, and a reference-year faktura reopened
// would reappear as open in the year Brygge keeps (reminders, KID matching).
// An unknown charge is left to the caller.
func (s *Service) refuseReferenceVippsCharge(ctx context.Context, clubID, rowID string) error {
	var ref bool
	err := s.db.QueryRow(ctx,
		`SELECT `+referenceDateSQL("r.club_id", "COALESCE(r.booking_date, r.tx_at::date)")+`
		   FROM vipps_import_rows r
		  WHERE r.id = $1 AND r.club_id = $2`,
		rowID, clubID,
	).Scan(&ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking reference date: %w", err)
	}
	if ref {
		return ErrReferencePeriod
	}
	return nil
}

// vippsSettlementIsReference reports whether a Vipps settlement's payout row
// is dated in a reference year. Its charges are that year's drafts, which never
// post, so the settlement's deposit would wait for them for ever.
func (s *Service) vippsSettlementIsReference(ctx context.Context, clubID, msn, settlement string) (bool, error) {
	var ref bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM vipps_import_rows p
		                 WHERE p.club_id = $1 AND p.msn = $2 AND p.row_type = 'payout' AND p.settlement_number = $3
		                   AND `+referenceDateSQL("p.club_id", "p.booking_date")+`)`,
		clubID, msn, settlement,
	).Scan(&ref)
	if err != nil {
		return false, fmt.Errorf("checking settlement date: %w", err)
	}
	return ref, nil
}

// ErrBankRowNotUndismissable is returned by UndismissReferenceBankRow for a
// row that is not a dismissed, unbooked, outgoing row dated in a reference
// year, or whose dismissal is final.
var ErrBankRowNotUndismissable = errors.New("bank row is not a dismissed reference-year row")

// finalDismissalReasons are dismissals that are never undone: a duplicate or a
// row superseded by its settled twin stands for money booked once already, so
// bringing it back would count the cost twice in a filed momskompensasjon.
const finalDismissalSQL = `('superseded', 'duplicate')`

// UndismissedBankRow is the dismissal an undo removed, for the audit row.
type UndismissedBankRow struct {
	Reason string
	By     *string
	At     time.Time
}

// UndismissReferenceBankRow returns a dismissed row of a reference year to the
// Referanseår list, so a mistaken dismissal can be undone, and reports what
// the dismissal was. Dismissals elsewhere stay permanent, and so do the final
// ones (finalDismissalSQL). The three dismissal columns clear together, as
// their CHECK requires.
func (s *Service) UndismissReferenceBankRow(ctx context.Context, clubID, bankRowID string) (*UndismissedBankRow, error) {
	var prior UndismissedBankRow
	err := s.db.QueryRow(ctx,
		`UPDATE bank_import_rows b
		    SET dismissed_at = NULL, dismissed_by = NULL, dismissed_reason = NULL
		   FROM (SELECT id, dismissed_reason, dismissed_by, dismissed_at FROM bank_import_rows
		          WHERE id = $1 AND club_id = $2
		            AND journal_entry_id IS NULL AND dismissed_at IS NOT NULL AND amount < 0
		            AND dismissed_reason NOT IN `+finalDismissalSQL+`
		            AND `+referenceDateSQL("bank_import_rows.club_id", "bank_import_rows.row_date")+`
		            FOR UPDATE) old
		  WHERE b.id = old.id
		 RETURNING old.dismissed_reason, old.dismissed_by::text, old.dismissed_at`,
		bankRowID, clubID,
	).Scan(&prior.Reason, &prior.By, &prior.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBankRowNotUndismissable
	}
	if err != nil {
		return nil, fmt.Errorf("undismiss: %w", err)
	}
	return &prior, nil
}

// periodIsReferenceIn reports whether the club's period is a reference
// period; an unknown or foreign id is not.
func periodIsReferenceIn(ctx context.Context, q dbtx, clubID, periodID string) (bool, error) {
	var ref bool
	err := q.QueryRow(ctx,
		`SELECT reference_only FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		periodID, clubID,
	).Scan(&ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking reference period: %w", err)
	}
	return ref, nil
}

// shapeLine is one journal line as the shape rule sees it.
type shapeLine struct {
	code        string
	accountType string
}

// cashRangeSQL is the cash and bank range of the kontoplan (1900, 1920–1949)
// applied to a code column; reports use it to recognise cash-basis entries.
func cashRangeSQL(codeCol string) string {
	return "(" + codeCol + " = '1900' OR (" + codeCol + " >= '1920' AND " + codeCol + " <= '1949'))"
}

// inCashRange is cashRangeSQL for a Go value.
func inCashRange(code string) bool {
	return code == "1900" || (code >= "1920" && code <= "1949")
}

// bankAccountCodesIn lists the GL codes the club books bank movements to:
// every bank import's account and every configured bank account. Codes in the
// cash range that no import or account names are not in it; use inCashRange.
func bankAccountCodesIn(ctx context.Context, q dbtx, clubID string) (map[string]bool, error) {
	rows, err := q.Query(ctx,
		`SELECT bank_account_code FROM bank_imports WHERE club_id = $1 AND bank_account_code <> ''
		 UNION
		 SELECT gl_code FROM club_bank_accounts WHERE club_id = $1 AND gl_code <> ''`, clubID)
	if err != nil {
		return nil, fmt.Errorf("listing bank accounts: %w", err)
	}
	codes, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("listing bank accounts: %w", err)
	}
	out := make(map[string]bool, len(codes))
	for _, c := range codes {
		out[c] = true
	}
	return out, nil
}

// shapeRules is what the expense-shape rule needs to know about a club's
// chart, read once per decision.
type shapeRules struct {
	clearing map[string]bool
	bank     map[string]bool
}

// loadShapeRules reads the club's provider clearing accounts and bank
// accounts. The clearing accounts are never bank accounts here, even inside
// the cash range, and never cost accounts either: money in transit between a
// provider and the bank is not a cost the year's momskompensasjon needs.
func loadShapeRules(ctx context.Context, q dbtx, clubID string) (*shapeRules, error) {
	vippsClearing, _, err := vippsAccountsIn(ctx, q, clubID)
	if err != nil {
		return nil, err
	}
	stripeClearing, _, err := stripeAccountsIn(ctx, q, clubID)
	if err != nil {
		return nil, err
	}
	bank, err := bankAccountCodesIn(ctx, q, clubID)
	if err != nil {
		return nil, err
	}
	return &shapeRules{
		clearing: map[string]bool{vippsClearingAccountCode: true, vippsClearing: true, stripeClearing: true},
		bank:     bank,
	}, nil
}

// shaped decides the reference-period rule: every line is on a cost account
// (account_type 'expense') or a bank account, and at least one is a cost line.
func (r *shapeRules) shaped(lines []shapeLine) bool {
	cost := false
	for _, l := range lines {
		switch {
		case r.clearing[l.code]:
			return false
		case l.accountType == string(AccountTypeExpense):
			cost = true
		case r.bank[l.code] || inCashRange(l.code):
		default:
			return false
		}
	}
	return cost
}

// refuseNonExpenseShapeTx returns ErrReferencePeriod unless lines on the given
// account codes are expense-shaped. An unknown code is refused: the entry
// could not be created anyway.
func refuseNonExpenseShapeTx(ctx context.Context, q dbtx, clubID string, codes []string) error {
	rows, err := q.Query(ctx,
		`SELECT code, account_type::text FROM accounts WHERE club_id = $1 AND code = ANY($2)`,
		clubID, codes)
	if err != nil {
		return fmt.Errorf("reading accounts: %w", err)
	}
	types, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (shapeLine, error) {
		var l shapeLine
		return l, r.Scan(&l.code, &l.accountType)
	})
	if err != nil {
		return fmt.Errorf("reading accounts: %w", err)
	}
	byCode := make(map[string]string, len(types))
	for _, t := range types {
		byCode[t.code] = t.accountType
	}
	lines := make([]shapeLine, 0, len(codes))
	for _, c := range codes {
		t, ok := byCode[c]
		if !ok {
			return ErrReferencePeriod
		}
		lines = append(lines, shapeLine{code: c, accountType: t})
	}
	return finishShape(ctx, q, clubID, lines)
}

// refuseNonExpenseShapeEntryTx is refuseNonExpenseShapeTx for an existing
// entry's lines (a post or a void: the reversal has the same accounts).
func refuseNonExpenseShapeEntryTx(ctx context.Context, q dbtx, clubID, entryID string) error {
	rows, err := q.Query(ctx,
		`SELECT a.code, a.account_type::text
		   FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1`, entryID)
	if err != nil {
		return fmt.Errorf("reading entry lines: %w", err)
	}
	lines, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (shapeLine, error) {
		var l shapeLine
		return l, r.Scan(&l.code, &l.accountType)
	})
	if err != nil {
		return fmt.Errorf("reading entry lines: %w", err)
	}
	return finishShape(ctx, q, clubID, lines)
}

func finishShape(ctx context.Context, q dbtx, clubID string, lines []shapeLine) error {
	rules, err := loadShapeRules(ctx, q, clubID)
	if err != nil {
		return err
	}
	if !rules.shaped(lines) {
		return ErrReferencePeriod
	}
	return nil
}

// dateInReferenceIn reports whether the date lies in one of the club's
// reference periods, whichever period an entry is being booked into. It takes
// that period FOR SHARE, as a writer does its own, so SetPeriodReference
// waits for the booking instead of flipping the flag under it.
func dateInReferenceIn(ctx context.Context, q dbtx, clubID, date string) (bool, error) {
	var ref bool
	err := q.QueryRow(ctx,
		`SELECT rp.reference_only FROM fiscal_periods rp
		  WHERE rp.club_id = $1 AND $2::date BETWEEN rp.start_date AND rp.end_date
		  ORDER BY rp.reference_only DESC LIMIT 1
		  FOR SHARE`, clubID, date,
	).Scan(&ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking reference date: %w", err)
	}
	return ref, nil
}

// refuseIfReferenceEntryTx applies the shape rule to an existing entry (a post
// or a void) when its period is a reference period or its date lies in one.
func refuseIfReferenceEntryTx(ctx context.Context, q dbtx, clubID, entryID, entryDate string, periodReference bool) error {
	if !periodReference {
		var err error
		if periodReference, err = dateInReferenceIn(ctx, q, clubID, entryDate); err != nil {
			return err
		}
	}
	if !periodReference {
		return nil
	}
	return refuseNonExpenseShapeEntryTx(ctx, q, clubID, entryID)
}

// SetPeriodReference marks or unmarks a fiscal period as reference only, in
// one transaction that locks the period FOR UPDATE: it waits for every
// in-flight booking, which holds the period FOR SHARE, so the flag cannot
// flip under a writer. changed is false when the period already had the
// value (a no-op that writes nothing).
//
// Marking is refused for the year that covers today and for a year holding an
// opening balance (including a draft one). Unmarking needs the period open,
// so a closed period never holds drafts that are postable again.
func (s *Service) SetPeriodReference(ctx context.Context, clubID, periodID string, on bool) (period *FiscalPeriod, changed bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var current, ref bool
	err = tx.QueryRow(ctx,
		`SELECT status, reference_only, CURRENT_DATE BETWEEN start_date AND end_date
		   FROM fiscal_periods WHERE id = $1 AND club_id = $2 FOR UPDATE`,
		periodID, clubID,
	).Scan(&status, &ref, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrPeriodNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("locking period: %w", err)
	}

	if ref != on {
		if on {
			if current {
				return nil, false, ErrReferenceCurrentYear
			}
			var hasOpening bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM journal_entries
				                 WHERE club_id = $1 AND fiscal_period_id = $2
				                   AND source = 'opening_balance' AND status <> 'voided')`,
				clubID, periodID,
			).Scan(&hasOpening); err != nil {
				return nil, false, fmt.Errorf("checking opening balance: %w", err)
			}
			if hasOpening {
				return nil, false, ErrReferenceHasOpeningBalance
			}
		} else if status != "open" {
			return nil, false, ErrPeriodClosed
		}
		if _, err := tx.Exec(ctx,
			`UPDATE fiscal_periods SET reference_only = $3, updated_at = now() WHERE id = $1 AND club_id = $2`,
			periodID, clubID, on,
		); err != nil {
			return nil, false, fmt.Errorf("setting reference flag: %w", err)
		}
		changed = true
	}

	p, err := scanPeriod(tx.QueryRow(ctx, periodSelectSQL+` WHERE fp.id = $1 AND fp.club_id = $2`, periodID, clubID))
	if err != nil {
		return nil, false, fmt.Errorf("reading period: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("committing transaction: %w", err)
	}
	return p, changed, nil
}

// PeriodDraftCount is the number of draft entries a period holds, for the
// audit row of a marking.
func (s *Service) PeriodDraftCount(ctx context.Context, clubID, periodID string) (int, error) {
	drafts, err := periodDrafts(ctx, s.db, clubID, periodID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range drafts {
		n += d.Count
	}
	return n, nil
}

// referenceExpenseDrafts reports whether a reference period holds a draft
// that is expense-shaped by the guard's own rule, the only kind that could
// still be posted. Other drafts (the Vipps drafts of a year kept elsewhere)
// stay drafts for good and must not mark the year's momskompensasjon
// incomplete.
func referenceExpenseDrafts(ctx context.Context, q dbtx, clubID, periodID string) (bool, error) {
	rows, err := q.Query(ctx,
		`SELECT jl.journal_entry_id::text, a.code, a.account_type::text
		   FROM journal_entries je
		   JOIN journal_lines jl ON jl.journal_entry_id = je.id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1 AND je.fiscal_period_id = $2 AND je.status = 'draft'
		    AND EXISTS (SELECT 1 FROM journal_lines c JOIN accounts ca ON ca.id = c.account_id
		                 WHERE c.journal_entry_id = je.id AND ca.account_type = 'expense')`,
		clubID, periodID)
	if err != nil {
		return false, fmt.Errorf("reading reference drafts: %w", err)
	}
	type row struct {
		entry string
		line  shapeLine
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.entry, &x.line.code, &x.line.accountType)
	})
	if err != nil {
		return false, fmt.Errorf("reading reference drafts: %w", err)
	}
	byEntry := map[string][]shapeLine{}
	for _, x := range all {
		byEntry[x.entry] = append(byEntry[x.entry], x.line)
	}
	if len(byEntry) == 0 {
		return false, nil
	}
	rules, err := loadShapeRules(ctx, q, clubID)
	if err != nil {
		return false, err
	}
	for _, lines := range byEntry {
		if rules.shaped(lines) {
			return true, nil
		}
	}
	return false, nil
}
