package accounting

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DIL-392 Phase 1: read paths backing the Tildel-tab UI.
//
// Counterpart of bank_row_reconcile.go (writes). Nothing in this file
// mutates state — handlers can call without the RequireFreshTOTP gate.

// BankRowSummary is the per-row payload rendered in the Tildel tab
// queue. Fields that the UI surfaces directly only — no GL ids etc.
//
// Two narrow auto-detection signals are derived per row:
//
//   - LikelyDuplicateOfMatched: another bank row with the SAME
//     `reference` (bank Arkivref. — guaranteed unique per bank
//     booking) is already journaled. Same physical transaction
//     re-exported. Safe to dismiss as `duplicate`.
//
//   - PossibleDoublePayment: another bank row with the same
//     (row_date, amount, kid_number) is already journaled BUT a
//     different non-empty `reference`. Two distinct bank operations
//     hit the same KID — the member paid twice. Money has to go back;
//     do NOT just dismiss as duplicate. UI surfaces this with a red
//     badge so the operator initiates a refund.
//
// When both `reference` and `kid_number` are empty (anonymous rows
// like "Overførsel") NEITHER signal fires — we can't reliably tell
// duplicates from coincident-amount payments without an identifier.
// Operator handles those manually.
type BankRowSummary struct {
	ID                       string     `json:"id"`
	RowDate                  time.Time  `json:"row_date"`
	Amount                   float64    `json:"amount"`
	KIDNumber                string     `json:"kid_number"`
	Counterpart              string     `json:"counterpart"`
	Description              string     `json:"description"`
	BankAccountCode          string     `json:"bank_account_code"`
	DismissedAt              *time.Time `json:"dismissed_at,omitempty"`
	DismissedReason          *string    `json:"dismissed_reason,omitempty"`
	LikelyDuplicateOfMatched bool       `json:"likely_duplicate_of_matched"`
	PossibleDoublePayment    bool       `json:"possible_double_payment"`
}

// ListUnmatchedBankRows returns the paginated set of rows that still
// need operator attention. kind = "all" | "incoming" | "outgoing" |
// "dismissed". q filters substrings against description + counterpart
// or matches the raw amount. year (0 = no filter) restricts to rows
// whose row_date falls in that calendar year.
//
// Each returned row carries `likely_duplicate_of_matched` — true when
// another bank row in the same club has identical (row_date, amount,
// kid_number) AND is already journaled. The UI uses this to badge
// duplicates so the operator dismisses them as `duplicate` instead of
// re-matching the same payment.
func (s *Service) ListUnmatchedBankRows(
	ctx context.Context, clubID, kind, q string, year, limit, offset int,
) ([]BankRowSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	where := []string{
		"bir.club_id = $1",
		"bir.journal_entry_id IS NULL",
	}
	args := []any{clubID}
	next := 2

	const vippsClause = `(bir.counterpart ILIKE '%vipps%' OR bir.description ~* 'Utb\.\s*\d+\s+Vippsnr\s+\d+')`
	switch kind {
	case "", "all":
		where = append(where, "bir.dismissed_at IS NULL")
	case "incoming":
		where = append(where, "bir.dismissed_at IS NULL", "bir.amount > 0")
	case "outgoing":
		where = append(where, "bir.dismissed_at IS NULL", "bir.amount < 0")
	case "dismissed":
		where = append(where, "bir.dismissed_at IS NOT NULL")
	case "vipps":
		where = append(where, "bir.dismissed_at IS NULL", vippsClause)
	case "bank":
		where = append(where, "bir.dismissed_at IS NULL", "NOT "+vippsClause)
	case "duplicate":
		where = append(where, "bir.dismissed_at IS NULL",
			`bir.reference <> '' AND EXISTS (
			   SELECT 1 FROM bank_import_rows other
			    WHERE other.id <> bir.id
			      AND other.club_id = bir.club_id
			      AND other.reference = bir.reference
			      AND other.journal_entry_id IS NOT NULL
			 )`)
	case "double_payment":
		where = append(where, "bir.dismissed_at IS NULL",
			`bir.kid_number <> '' AND EXISTS (
			   SELECT 1 FROM bank_import_rows other
			    WHERE other.id <> bir.id
			      AND other.club_id = bir.club_id
			      AND other.row_date = bir.row_date
			      AND other.amount = bir.amount
			      AND other.kid_number = bir.kid_number
			      AND COALESCE(other.reference, '') <> COALESCE(bir.reference, '')
			      AND other.journal_entry_id IS NOT NULL
			 )`)
	default:
		return nil, fmt.Errorf("invalid kind %q", kind)
	}

	if q := strings.TrimSpace(q); q != "" {
		ph := fmt.Sprintf("$%d", next)
		where = append(where,
			"(bir.description ILIKE "+ph+" OR bir.counterpart ILIKE "+ph+" OR bir.amount::text = "+fmt.Sprintf("$%d", next+1)+")")
		args = append(args, "%"+q+"%", q)
		next += 2
	}

	if year > 0 {
		where = append(where, fmt.Sprintf("EXTRACT(YEAR FROM bir.row_date)::int = $%d", next))
		args = append(args, year)
		next++
	}

	args = append(args, limit, offset)
	limitPh := fmt.Sprintf("$%d", next)
	offsetPh := fmt.Sprintf("$%d", next+1)

	query := `
		SELECT bir.id, bir.row_date, bir.amount,
		       COALESCE(bir.kid_number, ''),
		       COALESCE(bir.counterpart, ''),
		       COALESCE(bir.description, ''),
		       bi.bank_account_code,
		       bir.dismissed_at, bir.dismissed_reason,
		       -- TRUE duplicate: same Arkivref. (bank-guaranteed unique
		       -- per booking) on a journaled sibling row. Same physical
		       -- transaction surfaced twice.
		       (bir.reference <> '' AND EXISTS (
		         SELECT 1 FROM bank_import_rows other
		          WHERE other.id <> bir.id
		            AND other.club_id = bir.club_id
		            AND other.reference = bir.reference
		            AND other.journal_entry_id IS NOT NULL
		       )) AS likely_duplicate,
		       -- POSSIBLE double payment: same date+amount+KID matches a
		       -- journaled sibling, but a different reference (or both
		       -- references empty). Two distinct bank bookings hit the
		       -- same KID — refund required, not safe to just dismiss.
		       -- KID must be non-empty so anonymous "Overførsel" rows
		       -- don't false-fire on coincident 450-kr membership fees.
		       (bir.kid_number <> '' AND EXISTS (
		         SELECT 1 FROM bank_import_rows other
		          WHERE other.id <> bir.id
		            AND other.club_id = bir.club_id
		            AND other.row_date = bir.row_date
		            AND other.amount = bir.amount
		            AND other.kid_number = bir.kid_number
		            AND COALESCE(other.reference, '') <> COALESCE(bir.reference, '')
		            AND other.journal_entry_id IS NOT NULL
		       )) AS possible_double_payment
		  FROM bank_import_rows bir
		  JOIN bank_imports bi ON bi.id = bir.bank_import_id
		 WHERE ` + strings.Join(where, " AND ") + `
		 ORDER BY bir.row_date DESC, bir.id
		 LIMIT ` + limitPh + ` OFFSET ` + offsetPh

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list unmatched: %w", err)
	}
	defer rows.Close()

	out := make([]BankRowSummary, 0)
	for rows.Next() {
		var r BankRowSummary
		if err := rows.Scan(&r.ID, &r.RowDate, &r.Amount, &r.KIDNumber,
			&r.Counterpart, &r.Description, &r.BankAccountCode,
			&r.DismissedAt, &r.DismissedReason, &r.LikelyDuplicateOfMatched,
			&r.PossibleDoublePayment); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountUnmatchedBankRows backs the bubble badge: how many rows still
// need a decision (live + dismissed, since per the user spec liberal
// counting is still "actionable" — operator can clear dismissed-but-
// not-truly-dismissed rows later).
func (s *Service) CountUnmatchedBankRows(ctx context.Context, clubID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM bank_import_rows
		  WHERE club_id = $1 AND journal_entry_id IS NULL`,
		clubID,
	).Scan(&n)
	return n, err
}

// InvoiceSuggestion is one ranked candidate returned by the
// suggestion endpoint. The UI uses ConfidenceLabel to colour the
// card (sterk / sannsynleg / svak).
type InvoiceSuggestion struct {
	InvoiceID       string  `json:"invoice_id"`
	InvoiceNumber   int     `json:"invoice_number"`
	MemberName      string  `json:"member_name"`
	MemberEmail     string  `json:"member_email"`
	PriceItemName   string  `json:"price_item_name"`
	IssueDate       string  `json:"issue_date"`
	DueDate         string  `json:"due_date"`
	TotalAmount     float64 `json:"total_amount"`
	KIDNumber       string  `json:"kid_number"`
	Score           int     `json:"score"`
	ConfidenceLabel string  `json:"confidence_label"`
	WhyTooltip      string  `json:"why_tooltip"`
}

// AccountSuggestion is one ranked GL account for outgoing rows.
type AccountSuggestion struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	AccountType     string `json:"account_type"`
	Score           int    `json:"score"`
	ConfidenceLabel string `json:"confidence_label"`
	WhyTooltip      string `json:"why_tooltip"`
}

// BankRowSuggestions wraps both candidate lists. Incoming rows
// populate Invoices; outgoing rows populate Accounts; "other" incoming
// rows (where the operator wants to assign to a revenue account
// instead of an invoice) also get Accounts.
type BankRowSuggestions struct {
	Invoices []InvoiceSuggestion `json:"invoices"`
	Accounts []AccountSuggestion `json:"accounts"`
}

// outgoingHints — small in-memory rules for GL category guessing.
// Description regex → account code. Conservative; misses become a
// "no suggestions, search manually" experience rather than a wrong
// auto-suggestion.
var outgoingHints = []struct {
	pattern string
	code    string
	why     string
}{
	{"Strøm", "6300", "Description mentions Strøm"},
	{"Stroem", "6300", "Description mentions Stroem"},
	{"Forsikring", "6900", "Description mentions Forsikring"},
	{"Lønn", "5000", "Description mentions Lønn"},
	{"Vipps", "7780", "Description mentions Vipps"},
	{"Gebyr", "7790", "Description mentions Gebyr (bank fee)"},
	{"Telenor", "6310", "Telephone / connectivity"},
	{"Telia", "6310", "Telephone / connectivity"},
	{"Posten", "6800", "Postal services"},
}

// BankRowSuggestionsFor returns the ranked candidate set for one bank
// row. Incoming rows: ranked invoices by amount + name + date.
// Outgoing rows: ranked accounts by description regex + recently-used.
// "Other incoming" surfaces revenue accounts as a secondary list when
// no invoice matches.
func (s *Service) BankRowSuggestionsFor(
	ctx context.Context, clubID, bankRowID string,
) (*BankRowSuggestions, error) {
	var (
		rowDate     time.Time
		rowAmount   float64
		rowDesc     string
		counterpart string
	)
	if err := s.db.QueryRow(ctx,
		`SELECT row_date, amount, COALESCE(description, ''), COALESCE(counterpart, '')
		   FROM bank_import_rows
		  WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&rowDate, &rowAmount, &rowDesc, &counterpart); err != nil {
		return nil, fmt.Errorf("load row: %w", err)
	}

	out := &BankRowSuggestions{Invoices: []InvoiceSuggestion{}, Accounts: []AccountSuggestion{}}

	if rowAmount > 0 {
		invs, err := s.rankIncomingInvoices(ctx, clubID, rowAmount, rowDate, counterpart)
		if err != nil {
			return nil, err
		}
		out.Invoices = invs
	}

	// Account suggestions: outgoing rows get expense accounts;
	// incoming rows can ALSO show revenue suggestions as a secondary
	// "or assign to category" path when invoice suggestions miss.
	accs, err := s.rankAccountSuggestions(ctx, clubID, rowAmount, rowDesc)
	if err != nil {
		return nil, err
	}
	out.Accounts = accs
	return out, nil
}

func (s *Service) rankIncomingInvoices(
	ctx context.Context, clubID string, amount float64, rowDate time.Time, counterpart string,
) ([]InvoiceSuggestion, error) {
	type candidate struct {
		ID            string
		Number        int
		MemberName    string
		MemberEmail   string
		PriceItemName string
		IssueDate     time.Time
		DueDate       time.Time
		TotalAmount   float64
		KIDNumber     string
		UserFullName  string
		UserFirstName string
		UserLastName  string
	}

	rows, err := s.db.Query(ctx,
		`SELECT i.id, i.invoice_number,
		        COALESCE(u.full_name, ''),
		        COALESCE(u.email, ''),
		        COALESCE(pi.name, ''),
		        i.issue_date, i.due_date, i.total_amount, i.kid_number,
		        COALESCE(u.full_name, ''),
		        COALESCE(u.first_name, ''),
		        COALESCE(u.last_name, '')
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		   LEFT JOIN price_items pi ON pi.id = i.price_item_id
		  WHERE i.club_id = $1
		    AND i.status = 'open'
		    AND i.payment_id IS NULL
		    AND ABS(i.total_amount - $2) < 0.005
		  ORDER BY i.issue_date DESC
		  LIMIT 50`,
		clubID, amount,
	)
	if err != nil {
		return nil, fmt.Errorf("rank incoming: %w", err)
	}
	defer rows.Close()

	cps := normalizeName(counterpart)
	out := make([]InvoiceSuggestion, 0, 5)
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.ID, &c.Number, &c.MemberName, &c.MemberEmail,
			&c.PriceItemName, &c.IssueDate, &c.DueDate, &c.TotalAmount, &c.KIDNumber,
			&c.UserFullName, &c.UserFirstName, &c.UserLastName); err != nil {
			continue
		}
		score := 0
		why := []string{"Beløp samsvarer"}
		if cps != "" {
			if normalizeName(c.UserFullName) == cps {
				score += 100
				why = append(why, "Eksakt namn-match")
			} else if c.UserFirstName != "" && strings.Contains(cps, normalizeName(c.UserFirstName)) {
				score += 50
				why = append(why, "Førenamn nemt i motpart")
			} else if c.UserLastName != "" && strings.Contains(cps, normalizeName(c.UserLastName)) {
				score += 50
				why = append(why, "Etternamn nemt i motpart")
			}
		}
		daysDiff := absInt(int(rowDate.Sub(c.IssueDate).Hours() / 24))
		if daysDiff <= 60 {
			score += 20
			why = append(why, "Faktura sendt nyleg")
		}
		conf := confidenceLabel(score)
		out = append(out, InvoiceSuggestion{
			InvoiceID:       c.ID,
			InvoiceNumber:   c.Number,
			MemberName:      c.MemberName,
			MemberEmail:     c.MemberEmail,
			PriceItemName:   c.PriceItemName,
			IssueDate:       c.IssueDate.Format("2006-01-02"),
			DueDate:         c.DueDate.Format("2006-01-02"),
			TotalAmount:     c.TotalAmount,
			KIDNumber:       c.KIDNumber,
			Score:           score,
			ConfidenceLabel: conf,
			WhyTooltip:      strings.Join(why, " · "),
		})
	}
	// Highest score first.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Score > out[i].Score {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out, nil
}

func (s *Service) rankAccountSuggestions(
	ctx context.Context, clubID string, amount float64, desc string,
) ([]AccountSuggestion, error) {
	rows, err := s.db.Query(ctx,
		`SELECT code, name, account_type::text
		   FROM accounts
		  WHERE club_id = $1 AND is_active`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("rank accounts: %w", err)
	}
	defer rows.Close()

	type acct struct {
		code, name, kind string
	}
	all := []acct{}
	for rows.Next() {
		var a acct
		if err := rows.Scan(&a.code, &a.name, &a.kind); err != nil {
			continue
		}
		all = append(all, a)
	}

	// Hint matching first.
	suggestions := make([]AccountSuggestion, 0, 5)
	for _, h := range outgoingHints {
		if !strings.Contains(strings.ToLower(desc), strings.ToLower(h.pattern)) {
			continue
		}
		for _, a := range all {
			if a.code != h.code {
				continue
			}
			suggestions = append(suggestions, AccountSuggestion{
				Code:            a.code,
				Name:            a.name,
				AccountType:     a.kind,
				Score:           100,
				ConfidenceLabel: "sterk",
				WhyTooltip:      h.why,
			})
			break
		}
	}

	// If still empty, surface a small default cohort filtered by sign.
	// Outgoing → expense accounts; incoming → revenue accounts. Keeps
	// the modal from being completely empty for rows the hints missed.
	if len(suggestions) == 0 {
		wantKind := "expense"
		if amount > 0 {
			wantKind = "revenue"
		}
		count := 0
		for _, a := range all {
			if a.kind != wantKind {
				continue
			}
			suggestions = append(suggestions, AccountSuggestion{
				Code:            a.code,
				Name:            a.name,
				AccountType:     a.kind,
				Score:           10,
				ConfidenceLabel: "svak",
				WhyTooltip:      "Standardforslag for " + wantKind,
			})
			count++
			if count >= 5 {
				break
			}
		}
	}
	if len(suggestions) > 5 {
		suggestions = suggestions[:5]
	}
	return suggestions, nil
}

// PotentialInvoicesForRow returns open invoices filtered ONLY by
// amount equality (±0.5 NOK) — the explicit "search by amount, manual
// confirmation" path. q narrows further by invoice number or member
// name. UI banners these as unverified.
func (s *Service) PotentialInvoicesForRow(
	ctx context.Context, clubID, bankRowID, q string, limit int,
) ([]InvoiceSuggestion, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var amount float64
	if err := s.db.QueryRow(ctx,
		`SELECT amount FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&amount); err != nil {
		return nil, fmt.Errorf("load row: %w", err)
	}
	if amount <= 0 {
		return nil, fmt.Errorf("potential-invoices only applies to incoming rows")
	}

	where := []string{
		"i.club_id = $1",
		"i.status = 'open'",
		"i.payment_id IS NULL",
		"ABS(i.total_amount - $2) < 0.005",
	}
	args := []any{clubID, amount}
	if q := strings.TrimSpace(q); q != "" {
		where = append(where,
			"(i.invoice_number::text ILIKE $3 OR u.full_name ILIKE $3 OR u.email ILIKE $3)")
		args = append(args, "%"+q+"%")
	}
	args = append(args, limit)

	query := `
		SELECT i.id, i.invoice_number,
		       COALESCE(u.full_name, ''),
		       COALESCE(u.email, ''),
		       COALESCE(pi.name, ''),
		       i.issue_date, i.due_date, i.total_amount, i.kid_number
		  FROM invoices i
		  LEFT JOIN users u ON u.id = i.user_id
		  LEFT JOIN price_items pi ON pi.id = i.price_item_id
		 WHERE ` + strings.Join(where, " AND ") + `
		 ORDER BY i.issue_date DESC
		 LIMIT $` + fmt.Sprintf("%d", len(args))

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list potential: %w", err)
	}
	defer rows.Close()
	out := make([]InvoiceSuggestion, 0)
	for rows.Next() {
		var s InvoiceSuggestion
		var issue, due time.Time
		if err := rows.Scan(&s.InvoiceID, &s.InvoiceNumber, &s.MemberName, &s.MemberEmail,
			&s.PriceItemName, &issue, &due, &s.TotalAmount, &s.KIDNumber); err != nil {
			continue
		}
		s.IssueDate = issue.Format("2006-01-02")
		s.DueDate = due.Format("2006-01-02")
		s.ConfidenceLabel = "potensiell"
		s.WhyTooltip = "Berre beløp samsvarer — ikkje verifisert"
		out = append(out, s)
	}
	return out, rows.Err()
}

func confidenceLabel(score int) string {
	switch {
	case score >= 100:
		return "sterk"
	case score >= 40:
		return "sannsynleg"
	default:
		return "svak"
	}
}

func absInt(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
