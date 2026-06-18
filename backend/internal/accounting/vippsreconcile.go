package accounting

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Account codes used by the Vipps reconciliation draft bilag.
const (
	vippsFeeAccountCode = "7700" // Bankgebyrer
	// vippsFallbackRevenueCode is where unmatchable Vipps belastning
	// lines land when neither the amount-to-price-article matcher nor
	// the customer-to-member matcher finds anything. Previously these
	// landed in 2900 (Annen kortsiktig gjeld), which is wrong: 2900
	// implies we owe the payer money. Vipps walk-up payments are
	// genuine revenue we just haven't classified yet; surfacing them
	// in a review queue starts from 3900 (Andre inntekter) so the
	// balance sheet doesn't lie while we sort categorization out.
	// See DIL-367.
	vippsFallbackRevenueCode = "3900"
	// vippsClearingAccountCode is the legacy account name kept for
	// backwards-compat with any operator-written reports that still
	// reference 2900. New lines route to the fallback above.
	vippsClearingAccountCode = "2900"
)

// vippsCategoryAccountMap mirrors `paymentTypeAccountMap` in sync.go
// but is duplicated here intentionally so a future refactor can adjust
// vipps-specific mapping (e.g. all Vipps gjestehavn always goes to
// 3200 regardless of price_items.category quirks) without disturbing
// invoice-side bookkeeping.
var vippsCategoryAccountMap = map[string]string{
	"membership":        "3100",
	"harbor_membership": "3110",
	"slip_fee":          "3120",
	"seasonal_rental":   "3120",
	"guest":             "3200",
	"motorhome":         "3200",
	"room_hire":         "3200",
	"merchandise":       "3300",
}

// VippsSettlementPattern extracts (settlement_number, msn) from a bank row description.
// Bank rows for Vipps payouts look like: "Utb. 2000591 Vippsnr 698382".
var VippsSettlementPattern = regexp.MustCompile(`(?i)Utb\.\s*(\d+)\s+Vippsnr\s+(\d+)`)

// VippsReconcileLine is one proposed line in the draft journal entry.
type VippsReconcileLine struct {
	VippsRowID       string  `json:"vipps_row_id,omitempty"`
	Kind             string  `json:"kind"` // bank_in | receivable | revenue | fee | clearing | fallback_revenue
	AccountCode      string  `json:"account_code"`
	Debit            float64 `json:"debit"`
	Credit           float64 `json:"credit"`
	Description      string  `json:"description"`
	CustomerName     string  `json:"customer_name,omitempty"`
	ResolvedMemberID string  `json:"resolved_member_id,omitempty"`
	// LinkedInvoiceID, when set, tells ReconcileVippsConfirm to also
	// insert a payments row and back-link invoices.payment_id after
	// posting the journal entry — so dashboard faktura widgets reflect
	// the Vipps-paid invoice. See DIL-367 sub 4.
	LinkedInvoiceID string `json:"linked_invoice_id,omitempty"`
	Resolved        bool   `json:"resolved"`
}

// VippsReconcilePreview is the response of the preview endpoint.
type VippsReconcilePreview struct {
	BankRowID        string               `json:"bank_row_id"`
	BankAmount       float64              `json:"bank_amount"`
	BankDate         string               `json:"bank_date"`
	SettlementNumber string               `json:"settlement_number"`
	MSN              string               `json:"msn"`
	PayoutAmount     float64              `json:"payout_amount"`
	TotalCharges     float64              `json:"total_charges"`
	TotalFees        float64              `json:"total_fees"`
	UnresolvedCount  int                  `json:"unresolved_count"`
	Balanced         bool                 `json:"balanced"`
	Reason           string               `json:"reason,omitempty"`
	// PeriodYear is the year of the resolved fiscal period (or the bank row's
	// year if the period would be auto-created).
	PeriodYear int `json:"period_year"`
	// PeriodClosed is true when the target fiscal period exists and is
	// closed — confirming the bilag will fail. The UI uses this to disable
	// the confirm button.
	PeriodClosed bool                 `json:"period_closed"`
	Lines        []VippsReconcileLine `json:"lines"`
}

// ReconcileVippsPreview reads a bank row, extracts the Utb./Vippsnr key from its
// description, finds the matching settlement, and proposes a balanced bilag.
func (s *Service) ReconcileVippsPreview(ctx context.Context, clubID, bankRowID string) (*VippsReconcilePreview, error) {
	var (
		bankDate    time.Time
		description string
		bankAmount  float64
		bankAccount string
		existing    *string
	)
	err := s.db.QueryRow(ctx,
		`SELECT bir.row_date, bir.description, bir.amount, bi.bank_account_code, bir.journal_entry_id
		 FROM bank_import_rows bir
		 JOIN bank_imports bi ON bi.id = bir.bank_import_id
		 WHERE bir.id = $1 AND bir.club_id = $2`,
		bankRowID, clubID,
	).Scan(&bankDate, &description, &bankAmount, &bankAccount, &existing)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("bank row not found")
	}
	if err != nil {
		return nil, fmt.Errorf("loading bank row: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("bank row already linked to journal entry %s", *existing)
	}

	matches := VippsSettlementPattern.FindStringSubmatch(description)
	if len(matches) != 3 {
		return nil, fmt.Errorf("bank row description does not look like a Vipps payout: %q", description)
	}
	settlement := matches[1]
	msn := matches[2]

	// Only the "Utbetaling planlagt" row in Vipps' CSV carries the
	// settlement_number. The belastning + fee rows for the same payout
	// are linked by sharing the payout's booking_date and MSN.
	rows, err := s.db.Query(ctx,
		`WITH payout AS (
		   SELECT booking_date
		   FROM vipps_import_rows
		   WHERE club_id = $1 AND msn = $3 AND row_type = 'payout' AND settlement_number = $2
		   LIMIT 1
		 )
		 SELECT vir.id, vir.row_type, vir.amount, vir.fee, vir.net_amount, vir.customer_name, vir.message
		 FROM vipps_import_rows vir, payout p
		 WHERE vir.club_id = $1 AND vir.msn = $3
		   AND (
		     (vir.row_type = 'payout' AND vir.settlement_number = $2)
		     OR (vir.row_type IN ('belastning', 'fee') AND vir.booking_date = p.booking_date)
		   )
		 ORDER BY CASE vir.row_type
		   WHEN 'belastning' THEN 1
		   WHEN 'fee' THEN 2
		   WHEN 'payout' THEN 3
		   ELSE 4
		 END, vir.tx_at NULLS LAST, vir.id`,
		clubID, settlement, msn,
	)
	if err != nil {
		return nil, fmt.Errorf("querying settlement rows: %w", err)
	}
	defer rows.Close()

	type vrow struct {
		ID                                 string
		RowType, CustomerName, Message     string
		Amount, Fee, NetAmount             float64
	}
	var detail []vrow
	for rows.Next() {
		var v vrow
		if err := rows.Scan(&v.ID, &v.RowType, &v.Amount, &v.Fee, &v.NetAmount, &v.CustomerName, &v.Message); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		detail = append(detail, v)
	}
	if len(detail) == 0 {
		return nil, fmt.Errorf("no Vipps settlement rows found for Utb. %s Vippsnr %s — import the Vipps export first", settlement, msn)
	}

	preview := &VippsReconcilePreview{
		BankRowID:        bankRowID,
		BankAmount:       bankAmount,
		BankDate:         bankDate.Format("2006-01-02"),
		SettlementNumber: settlement,
		MSN:              msn,
		PeriodYear:       bankDate.Year(),
		Lines:            []VippsReconcileLine{},
	}

	// Check the target period without auto-creating it (preview shouldn't
	// have side effects). If a closed period covers the bank-row date,
	// flag it so the UI can disable confirm.
	var periodStatus string
	perr := s.db.QueryRow(ctx,
		`SELECT status FROM fiscal_periods
		 WHERE club_id = $1 AND $2::date BETWEEN start_date AND end_date
		 LIMIT 1`,
		clubID, bankDate.Format("2006-01-02"),
	).Scan(&periodStatus)
	if perr == nil && periodStatus == "closed" {
		preview.PeriodClosed = true
	}

	if bankAccount == "" {
		bankAccount = bankAccountCode
	}

	// DR bank
	preview.Lines = append(preview.Lines, VippsReconcileLine{
		Kind:        "bank_in",
		AccountCode: bankAccount,
		Debit:       bankAmount,
		Description: fmt.Sprintf("Vipps utbetaling %s (Vippsnr %s)", settlement, msn),
	})

	for _, v := range detail {
		switch VippsRowType(v.RowType) {
		case VippsRowBelastning:
			preview.TotalCharges += v.Amount
			// Include the payer's free-form Vipps message when it
			// adds signal beyond the customer name — that's usually
			// where the harbor master / walk-up payer typed the
			// purpose ("Gjest A12 5.juli", "Sesong sommer", invoice
			// number). See DIL-367.
			desc := fmt.Sprintf("Vipps innbetaling: %s", v.CustomerName)
			if msg := strings.TrimSpace(v.Message); msg != "" {
				desc = fmt.Sprintf("Vipps innbetaling: %s — %s", v.CustomerName, msg)
			}
			line := VippsReconcileLine{
				VippsRowID:   v.ID,
				Credit:       v.Amount,
				Description:  desc,
				CustomerName: v.CustomerName,
			}

			// Classification cascade — order matters here.
			//
			// (1) Member + matching open invoice → CR 1500, mark
			//     LinkedInvoiceID so confirm back-links payment_id.
			//     This must beat the price-article matcher because
			//     a member paying their medlemskap by Vipps has both
			//     a member match AND a matching price-article; the
			//     invoice link is the more useful answer.
			// (2) amount → price-article (the workhorse: Vipps is
			//     mostly guest slips, motorhomes, slipping where the
			//     payer is non-member but the amount is a clean
			//     multiple of a published price).
			// (3) Member resolved but no matching invoice or
			//     price-article → CR 1500 so the treasurer can
			//     match it manually.
			// (4) Nothing matched → fallback 3900 so the balance
			//     sheet stays honest. See DIL-367.
			memberID, _ := s.resolveCustomerToMember(ctx, clubID, v.CustomerName, v.Message)
			var invoiceID string
			if memberID != "" {
				invoiceID = s.findOpenInvoiceForMember(ctx, clubID, memberID, v.Amount)
			}
			switch {
			case invoiceID != "":
				line.Kind = "receivable"
				line.AccountCode = receivablesAccountCode
				line.ResolvedMemberID = memberID
				line.LinkedInvoiceID = invoiceID
				line.Resolved = true
			default:
				if account, name := s.matchVippsAmountToPriceArticle(ctx, clubID, v.Amount); account != "" {
					line.Kind = "revenue"
					line.AccountCode = account
					line.Resolved = true
					if name != "" {
						line.Description = fmt.Sprintf("%s (%s)", desc, name)
					}
				} else if memberID != "" {
					// Known member, no matching invoice/article —
					// park in receivables so the treasurer can
					// reconcile to whatever they owe.
					line.Kind = "receivable"
					line.AccountCode = receivablesAccountCode
					line.ResolvedMemberID = memberID
					line.Resolved = true
				} else {
					line.Kind = "fallback_revenue"
					line.AccountCode = vippsFallbackRevenueCode
					preview.UnresolvedCount++
				}
			}
			preview.Lines = append(preview.Lines, line)
		case VippsRowFee:
			// Fee rows carry negative amount; DR the fee expense for the absolute value.
			fee := v.Amount
			if fee < 0 {
				fee = -fee
			}
			preview.TotalFees += fee
			preview.Lines = append(preview.Lines, VippsReconcileLine{
				VippsRowID:  v.ID,
				Kind:        "fee",
				AccountCode: vippsFeeAccountCode,
				Debit:       fee,
				Description: "Vipps gebyr",
			})
		case VippsRowPayout:
			preview.PayoutAmount += -v.Amount
		default:
			// Unknown row type — surface but don't include in bilag.
		}
	}

	var dr, cr float64
	for _, l := range preview.Lines {
		dr += l.Debit
		cr += l.Credit
	}
	preview.Balanced = floatNear(dr, cr, 0.005)
	if !preview.Balanced {
		preview.Reason = fmt.Sprintf("debit %.2f != credit %.2f", dr, cr)
	}
	if preview.PayoutAmount != 0 && !floatNear(preview.PayoutAmount, bankAmount, 0.005) {
		preview.Reason = fmt.Sprintf("vipps payout %.2f != bank deposit %.2f", preview.PayoutAmount, bankAmount)
		preview.Balanced = false
	}

	return preview, nil
}

// ReconcileVippsConfirm creates a draft journal entry from a (possibly edited)
// preview. The caller is responsible for passing the final line set.
// If periodOverride is empty, the period is auto-resolved from the bank row's
// date (and auto-created as calendar-year if missing).
func (s *Service) ReconcileVippsConfirm(ctx context.Context, clubID, bankRowID, periodOverride, createdBy string, lines []VippsReconcileLine) (string, error) {
	if len(lines) == 0 {
		return "", fmt.Errorf("at least one line is required")
	}

	var (
		bankDate    time.Time
		description string
		existing    *string
	)
	err := s.db.QueryRow(ctx,
		`SELECT row_date, description, journal_entry_id
		 FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&bankDate, &description, &existing)
	if err != nil {
		return "", fmt.Errorf("loading bank row: %w", err)
	}
	if existing != nil {
		return "", fmt.Errorf("bank row already linked to journal entry")
	}

	periodID, periodStatus, perr := s.resolvePeriod(ctx, clubID, bankDate, periodOverride)
	if perr != nil {
		return "", fmt.Errorf("resolving fiscal period: %w", perr)
	}
	if periodStatus == "closed" {
		return "", fmt.Errorf("fiscal period %d is closed — reopen it or pick a different period", bankDate.Year())
	}

	var dr, cr float64
	entryLines := make([]CreateJournalLineInput, 0, len(lines))
	for _, l := range lines {
		dr += l.Debit
		cr += l.Credit
		entryLines = append(entryLines, CreateJournalLineInput{
			AccountCode: l.AccountCode,
			Debit:       l.Debit,
			Credit:      l.Credit,
			Description: l.Description,
		})
	}
	if !floatNear(dr, cr, 0.005) {
		return "", fmt.Errorf("lines do not balance: debit %.2f != credit %.2f", dr, cr)
	}

	sourceID := bankRowID
	sourceTable := "bank_import_rows"
	entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      bankDate.Format("2006-01-02"),
		Description:    fmt.Sprintf("Vipps reconciliation: %s", description),
		Source:         "vipps",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      createdBy,
		ClubID:         clubID,
		Lines:          entryLines,
	})
	if err != nil {
		return "", fmt.Errorf("creating journal entry: %w", err)
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true WHERE id = $2`,
		entry.ID, bankRowID,
	); err != nil {
		return entry.ID, fmt.Errorf("linking bank row: %w", err)
	}

	for _, l := range lines {
		if l.VippsRowID == "" {
			continue
		}
		_, _ = s.db.Exec(ctx,
			`UPDATE vipps_import_rows SET journal_entry_id = $1 WHERE id = $2 AND club_id = $3`,
			entry.ID, l.VippsRowID, clubID,
		)
	}

	// Back-link any invoice that this reconciliation paid off so the
	// dashboard's faktura-status widgets reflect the payment. Best-
	// effort: the GL is already correct if this fails. See DIL-367.
	for _, l := range lines {
		if l.LinkedInvoiceID == "" {
			continue
		}
		if err := s.linkInvoicePayment(ctx, clubID, l.LinkedInvoiceID, bankDate); err != nil {
			s.log.Warn().Err(err).
				Str("invoice_id", l.LinkedInvoiceID).
				Str("journal_entry_id", entry.ID).
				Msg("vipps reconciliation: invoice payment back-link failed")
		}
	}

	return entry.ID, nil
}

// matchVippsAmountToPriceArticle returns the GL revenue account and a
// human-readable price-item name when exactly ONE active price_items
// row produces the supplied amount. Two flavours of match are
// considered:
//
//   - exact match against `amount` (covers once / year / season units)
//   - integer multiple match against per-time units (`night`, `day`,
//     `hour`) — guest slip @ 250/night fits a payment of 750 as
//     "3 nights × 250". Multiplier capped at 60 to avoid silly false
//     positives.
//
// If zero or more than one price_item produces the amount, returns
// "" so the caller falls through to the next pass in the cascade.
// See DIL-367.
func (s *Service) matchVippsAmountToPriceArticle(ctx context.Context, clubID string, amount float64) (accountCode, name string) {
	if amount <= 0 {
		return "", ""
	}
	rows, err := s.db.Query(ctx,
		`SELECT name, category, amount, unit
		   FROM price_items
		  WHERE club_id = $1 AND is_active = TRUE AND amount > 0`,
		clubID,
	)
	if err != nil {
		return "", ""
	}
	defer rows.Close()

	type candidate struct {
		name, category string
		multiplier     int
	}
	var matches []candidate
	for rows.Next() {
		var n, cat, unit string
		var unitAmount float64
		if err := rows.Scan(&n, &cat, &unitAmount, &unit); err != nil {
			continue
		}
		if floatNear(unitAmount, amount, 0.005) {
			matches = append(matches, candidate{name: n, category: cat, multiplier: 1})
			continue
		}
		// Per-time-unit price items support multiplied matches —
		// guest slip @ 250/night fits 250, 500, 750, 1000, …
		if unit == "night" || unit == "day" || unit == "hour" {
			if unitAmount <= 0 {
				continue
			}
			ratio := amount / unitAmount
			mult := int(ratio + 0.0001)
			if mult < 2 || mult > 60 {
				continue
			}
			if floatNear(float64(mult)*unitAmount, amount, 0.005) {
				matches = append(matches, candidate{name: n, category: cat, multiplier: mult})
			}
		}
	}
	if len(matches) != 1 {
		return "", ""
	}
	m := matches[0]
	code, ok := vippsCategoryAccountMap[m.category]
	if !ok {
		code = vippsFallbackRevenueCode
	}
	if m.multiplier > 1 {
		return code, fmt.Sprintf("%dx %s", m.multiplier, m.name)
	}
	return code, m.name
}

// resolveCustomerToMember tries to map a Vipps customer to a member.
// Resolution cascade:
//
//  1. KID in the free-form message → invoice.user_id
//  2. Normalized exact match on users.full_name
//  3. Normalized exact match on first_name + ' ' + last_name
//  4. Last-name-only when exactly one member in the club has that
//     normalized last name (catches "Per Hansen" when the Vipps CSV
//     dropped the first name and only "Per Hansen" exists)
//
// Normalization handles æøå / accents / hyphens / case so common
// Norwegian spelling variants don't block the match. See DIL-367.
func (s *Service) resolveCustomerToMember(ctx context.Context, clubID, customerName, message string) (string, error) {
	if kid := extractKID(message); kid != "" {
		var memberID string
		err := s.db.QueryRow(ctx,
			`SELECT i.user_id FROM invoices i
			 WHERE i.club_id = $1 AND i.kid_number = $2 AND i.user_id IS NOT NULL
			 ORDER BY i.issue_date DESC LIMIT 1`,
			clubID, kid,
		).Scan(&memberID)
		if err == nil && memberID != "" {
			return memberID, nil
		}
	}

	needle := normalizeName(customerName)
	if needle == "" {
		return "", nil
	}

	// Load all members (small set per club) and match in Go so we can
	// run the cascade without three round-trips and without depending
	// on a Postgres fuzzy-string extension.
	rows, err := s.db.Query(ctx,
		`SELECT id, COALESCE(full_name, ''), COALESCE(first_name, ''), COALESCE(last_name, ''), COALESCE(email, '')
		   FROM users WHERE club_id = $1`,
		clubID,
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	type cand struct {
		id, fullN, firstLastN, lastN, emailLocal string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		var fullName, firstName, lastName, email string
		if err := rows.Scan(&c.id, &fullName, &firstName, &lastName, &email); err != nil {
			continue
		}
		c.fullN = normalizeName(fullName)
		c.firstLastN = normalizeName(firstName + " " + lastName)
		c.lastN = normalizeName(lastName)
		if at := strings.IndexByte(email, '@'); at > 0 {
			c.emailLocal = normalizeName(email[:at])
		}
		cands = append(cands, c)
	}

	// Pass 1: exact normalized full_name
	for _, c := range cands {
		if c.fullN != "" && c.fullN == needle {
			return c.id, nil
		}
	}
	// Pass 2: exact normalized first+last
	for _, c := range cands {
		if c.firstLastN != "" && c.firstLastN != " " && c.firstLastN == needle {
			return c.id, nil
		}
	}
	// Pass 3: last name only when unique
	matches := 0
	var matchedID string
	for _, c := range cands {
		if c.lastN != "" && c.lastN == needle {
			matches++
			matchedID = c.id
		}
	}
	if matches == 1 {
		return matchedID, nil
	}
	// Pass 4: Levenshtein-1 on full name / first+last — catches typos and
	// diacritic/spacing variants normalizeName missed — when exactly one
	// candidate is that close. See DIL-367.
	matches, matchedID = 0, ""
	for _, c := range cands {
		if (c.fullN != "" && levenshteinWithin1(c.fullN, needle)) ||
			(c.firstLastN != "" && c.firstLastN != " " && levenshteinWithin1(c.firstLastN, needle)) {
			matches++
			matchedID = c.id
		}
	}
	if matches == 1 {
		return matchedID, nil
	}
	// Pass 5: email local-part (the Vipps name matches the part before @
	// in the member's email) when unique. Compare with separators removed
	// so "kari.berg" (email) matches "Kari Berg" (Vipps name).
	needleTight := strings.ReplaceAll(needle, " ", "")
	matches, matchedID = 0, ""
	for _, c := range cands {
		if c.emailLocal != "" && strings.ReplaceAll(c.emailLocal, " ", "") == needleTight {
			matches++
			matchedID = c.id
		}
	}
	if matches == 1 {
		return matchedID, nil
	}
	// Pass 6 (phone) is intentionally absent: the Vipps export only exposes
	// a MASKED customer phone, so there is nothing to match against
	// users.phone. See DIL-367.
	return "", nil
}

// levenshteinWithin1 reports whether a and b are within Levenshtein
// distance 1 (equal, or one insertion/deletion/substitution apart).
// normalizeName has already folded both to ASCII, so byte comparison is
// safe. Cheap early-outs keep it O(len) for the common near-equal case.
func levenshteinWithin1(a, b string) bool {
	la, lb := len(a), len(b)
	if la == lb {
		diff := 0
		for i := 0; i < la; i++ {
			if a[i] != b[i] {
				if diff == 1 {
					return false
				}
				diff++
			}
		}
		return true
	}
	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}
	if lb-la != 1 {
		return false
	}
	// b is one char longer: allow a single skip in b.
	i, j, skipped := 0, 0, false
	for i < la && j < lb {
		if a[i] == b[j] {
			i++
			j++
			continue
		}
		if skipped {
			return false
		}
		skipped = true
		j++
	}
	return true
}

// normalizeName lowercases, replaces Norwegian/Swedish/Danish accented
// characters with their ASCII equivalents, swaps hyphens for spaces,
// and collapses whitespace. Used by the Vipps customer-to-member
// matcher so spelling variants don't block the link. See DIL-367.
func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	r := strings.NewReplacer(
		"æ", "ae", "ø", "o", "å", "a",
		"ä", "a", "ö", "o", "ü", "u",
		"é", "e", "è", "e", "ê", "e",
		"á", "a", "à", "a", "â", "a",
		"í", "i", "ì", "i",
		"ó", "o", "ò", "o", "ô", "o",
		"ú", "u", "ù", "u",
		"ñ", "n", "ç", "c",
		"-", " ", "_", " ",
		".", "", ",", "",
	)
	s = r.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// findOpenInvoiceForMember returns the single open invoice for the
// supplied member whose total matches the Vipps payment amount within
// ±1.00 NOK and was issued in the last 90 days. Returns "" when zero
// or multiple matches — ambiguity is left for the next pass in the
// cascade or the review queue.
func (s *Service) findOpenInvoiceForMember(ctx context.Context, clubID, memberID string, amount float64) string {
	rows, err := s.db.Query(ctx,
		`SELECT id FROM invoices
		  WHERE club_id = $1
		    AND user_id = $2
		    AND payment_id IS NULL
		    AND status <> 'voided'
		    AND ABS(total_amount - $3) < 1.00
		    AND issue_date > CURRENT_DATE - INTERVAL '90 days'
		  ORDER BY issue_date DESC
		  LIMIT 2`,
		clubID, memberID, amount,
	)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 {
		return ""
	}
	return ids[0]
}

var kidPattern = regexp.MustCompile(`\b(\d{6,25})\b`)

func extractKID(s string) string {
	m := kidPattern.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func floatNear(a, b, tol float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= tol
}
