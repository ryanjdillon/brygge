package accounting

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/finance"
)

// The member/faktura half of auto-assignment, shared by Vipps charges
// (BRY-340) and KID-less bank deposits (BRY-366): a payer resolves to one
// member only through a strong, unique name match or a taught alias, and a
// payment goes to a faktura only when it is that member's single open
// faktura for exactly the amount.

// Where an auto-assigned payment came from; it picks the undo endpoint.
const (
	AutoAssignSourceVipps = "vipps"
	AutoAssignSourceBank  = "bank"
)

// AutoAssignItem is one payment an auto-assign run booked (or, in a
// preview, would book).
type AutoAssignItem struct {
	Source  string  `json:"source"`
	RowID   string  `json:"row_id"`
	Date    string  `json:"date"`
	Amount  float64 `json:"amount"`
	Rule    string  `json:"rule"`
	Target  string  `json:"target"` // "faktura N" or an account code
	EntryID string  `json:"entry_id"`
	// InvoiceNumber and MatchPass are set for member_invoice bookings of
	// bank deposits.
	InvoiceNumber int    `json:"invoice_number,omitempty"`
	MatchPass     string `json:"match_pass,omitempty"`
}

// AutoAssignResult summarises one auto-assign run over Vipps charges or bank
// deposits.
type AutoAssignResult struct {
	Source         string           `json:"source"`
	Enabled        bool             `json:"enabled"`
	Assigned       []AutoAssignItem `json:"assigned"`
	NeedsReview    int              `json:"needs_review"`
	SkippedDetails []SkippedItem    `json:"skipped_details"`
	// ByReason counts the payments left for a human, by why (bank only).
	ByReason map[string]int `json:"by_reason,omitempty"`
	// Error is set when the run itself failed; what it booked before
	// failing is still listed.
	Error string `json:"error,omitempty"`
}

// Reasons a payment is left for a human, as reported by both rules.
const (
	reasonNoOpenFaktura       = "no_open_faktura"
	reasonSeveralOpenFakturas = "several_open_fakturas"
	reasonAmountMismatch      = "amount_mismatch"
	reasonFakturaAfterPayment = "faktura_after_payment"
	reasonFakturaPartlyPaid   = "faktura_partly_paid"
	reasonImportedFaktura     = "imported_faktura"
)

// memberCand is a member as the name passes compare it; every field is
// normalised with normalizeName.
type memberCand struct {
	id, fullN, firstLastN, lastN, emailLocal string
}

// loadMemberCands loads every user of the club once, so a run that resolves
// many payers does not reload the member list per payer.
func loadMemberCands(ctx context.Context, q pgxQuerier, clubID string) ([]memberCand, error) {
	rows, err := q.Query(ctx,
		`SELECT id, COALESCE(full_name, ''), COALESCE(first_name, ''), COALESCE(last_name, ''), COALESCE(email, '')
		   FROM users WHERE club_id = $1`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading members: %w", err)
	}
	defer rows.Close()
	var cands []memberCand
	for rows.Next() {
		var c memberCand
		var fullName, firstName, lastName, email string
		if err := rows.Scan(&c.id, &fullName, &firstName, &lastName, &email); err != nil {
			return nil, fmt.Errorf("scanning member: %w", err)
		}
		c.fullN = normalizeName(fullName)
		c.firstLastN = normalizeName(firstName + " " + lastName)
		c.lastN = normalizeName(lastName)
		if at := strings.IndexByte(email, '@'); at > 0 {
			c.emailLocal = normalizeName(email[:at])
		}
		cands = append(cands, c)
	}
	return cands, rows.Err()
}

// matchMemberName runs the name passes over cands and reports the member and
// the pass that matched (memberPassNone when none did).
//
// The exact passes (full name, first+last) count as strong only when they
// name exactly one member: two members sharing a normalised name would
// otherwise hand a payment to whichever was listed first. A shared name falls
// through to the weak passes, which the auto-assign rules never act on.
func matchMemberName(cands []memberCand, customerName string) (string, int) {
	needle := normalizeName(customerName)
	if needle == "" {
		return "", memberPassNone
	}
	if id, pass := uniqueExactName(cands, needle); id != "" {
		return id, pass
	}
	// Pass 3: last name only when unique
	if id := uniqueCand(cands, func(c memberCand) bool { return c.lastN != "" && c.lastN == needle }); id != "" {
		return id, memberPassLastName
	}
	// Pass 4: Levenshtein-1 on full name / first+last — catches typos and
	// diacritic/spacing variants normalizeName missed — when exactly one
	// candidate is that close. See DIL-367.
	if id := uniqueCand(cands, func(c memberCand) bool {
		return (c.fullN != "" && levenshteinWithin1(c.fullN, needle)) ||
			(hasFirstLast(c) && levenshteinWithin1(c.firstLastN, needle))
	}); id != "" {
		return id, memberPassFuzzy
	}
	// Pass 5: email local-part (the Vipps name matches the part before @
	// in the member's email) when unique. Compare with separators removed
	// so "kari.berg" (email) matches "Kari Berg" (Vipps name).
	needleTight := strings.ReplaceAll(needle, " ", "")
	if id := uniqueCand(cands, func(c memberCand) bool {
		return c.emailLocal != "" && strings.ReplaceAll(c.emailLocal, " ", "") == needleTight
	}); id != "" {
		return id, memberPassEmail
	}
	// Pass 6 (phone) is intentionally absent: the Vipps export only exposes
	// a MASKED customer phone, so there is nothing to match against
	// users.phone. See DIL-367.
	return "", memberPassNone
}

func hasFirstLast(c memberCand) bool {
	return c.firstLastN != "" && c.firstLastN != " "
}

// uniqueExactName is passes 1 and 2: the member whose normalised full_name
// or first+last equals needle, when exactly one member does across both.
func uniqueExactName(cands []memberCand, needle string) (string, int) {
	exact := map[string]int{}
	for _, c := range cands {
		switch {
		case c.fullN != "" && c.fullN == needle:
			exact[c.id] = memberPassFullName
		case hasFirstLast(c) && c.firstLastN == needle:
			exact[c.id] = memberPassFirstLast
		}
	}
	if len(exact) != 1 {
		return "", memberPassNone
	}
	for id, pass := range exact {
		return id, pass
	}
	return "", memberPassNone
}

// uniqueCand returns the one candidate match accepts, or "" when none or
// several do.
func uniqueCand(cands []memberCand, match func(memberCand) bool) string {
	found := ""
	for _, c := range cands {
		if !match(c) {
			continue
		}
		if found != "" {
			return ""
		}
		found = c.id
	}
	return found
}

// aliasMember returns the member a treasurer taught for this payer name, or
// "" when there is none.
func aliasMember(ctx context.Context, q pgxQuerier, clubID, payerName string) string {
	var userID string
	if err := q.QueryRow(ctx,
		`SELECT user_id FROM vipps_customer_aliases
		 WHERE club_id = $1 AND lower(customer_name) = lower($2)
		 LIMIT 1`,
		clubID, payerName,
	).Scan(&userID); err != nil {
		return ""
	}
	return userID
}

// resolveStrongMember resolves a payer name to a member only through a
// strong pass: a taught alias (always wins) or a unique exact name. It
// returns "" and memberPassNone otherwise.
func (s *Service) resolveStrongMember(ctx context.Context, clubID string, cands []memberCand, payerName string) (string, int) {
	if strings.TrimSpace(payerName) == "" {
		return "", memberPassNone
	}
	if id := aliasMember(ctx, s.db, clubID, payerName); id != "" {
		return id, memberPassAlias
	}
	id, pass := matchMemberName(cands, payerName)
	if id == "" || pass > memberPassFirstLast {
		return "", memberPassNone
	}
	return id, pass
}

// memberPassName is the audit/preview label for a strong resolution pass.
func memberPassName(pass int) string {
	switch pass {
	case memberPassAlias:
		return "alias"
	case memberPassKID:
		return "kid"
	case memberPassFullName:
		return "full_name"
	case memberPassFirstLast:
		return "first_last"
	}
	return ""
}

// toCents converts a NUMERIC kroner amount to integer øre.
func toCents(amount float64) int64 {
	return int64(math.Round(amount * 100))
}

// invoiceBookedRowSQL is true when a booked row records that it settles the
// faktura: a bank row, Vipps charge or Stripe balance transaction with its
// invoice_id, or an order-linked charge (Stripe, or Vipps by merchant
// reference) whose checkout order pays it through order_sources. An order
// charge settles the faktura in the GL before its payment back-link lands,
// so without the order clause a bank or Vipps assign in that window credits
// 1500 twice. A Stripe row is booked from its order only by the booking
// pass, which clears review_reason; a row a person booked keeps its
// review_reason and settles only the faktura in its invoice_id (if any), so
// the order clause skips it. Every check of "is this faktura already booked"
// goes through this fragment (lockInvoiceForAssign, invoiceAlreadyBookedSQL).
func invoiceBookedRowSQL(alias string) string {
	return `(EXISTS (SELECT 1 FROM bank_import_rows bb
	                   WHERE bb.club_id = ` + alias + `.club_id AND bb.invoice_id = ` + alias + `.id
	                     AND bb.journal_entry_id IS NOT NULL)
	      OR EXISTS (SELECT 1 FROM vipps_import_rows bv
	                   WHERE bv.club_id = ` + alias + `.club_id AND bv.invoice_id = ` + alias + `.id
	                     AND bv.journal_entry_id IS NOT NULL)
	      OR EXISTS (SELECT 1 FROM stripe_balance_transactions bsi
	                   WHERE bsi.club_id = ` + alias + `.club_id AND bsi.invoice_id = ` + alias + `.id
	                     AND bsi.journal_entry_id IS NOT NULL)
	      OR EXISTS (SELECT 1 FROM order_sources bos
	                   JOIN stripe_balance_transactions bst ON bst.order_id = bos.order_id
	                  WHERE bos.invoice_id = ` + alias + `.id AND bst.club_id = ` + alias + `.club_id
	                    AND bst.journal_entry_id IS NOT NULL AND bst.review_reason IS NULL)
	      OR EXISTS (SELECT 1 FROM order_sources bos
	                   JOIN orders bo ON bo.id = bos.order_id AND bo.vipps_reference <> ''
	                   JOIN vipps_import_rows bov ON bov.club_id = bo.club_id AND bov.order_id = bo.vipps_reference
	                  WHERE bos.invoice_id = ` + alias + `.id AND bov.club_id = ` + alias + `.club_id
	                    AND bov.journal_entry_id IS NOT NULL))`
}

// invoiceAlreadyBookedSQL is true when a booked row already settles part of
// the faktura: one recorded against it (invoiceBookedRowSQL), or a bank row
// KID-matched to it whose payment back-link never landed. A second payment
// to such a faktura is a remainder or a double payment, never a clean match.
func invoiceAlreadyBookedSQL(alias string) string {
	return `(` + invoiceBookedRowSQL(alias) + `
	      OR EXISTS (SELECT 1 FROM bank_import_rows ab
	                   WHERE ab.club_id = ` + alias + `.club_id AND ab.journal_entry_id IS NOT NULL
	                     AND COALESCE(` + alias + `.kid_number, '') <> '' AND ab.kid_number = ` + alias + `.kid_number))`
}

// memberInvoiceDecision is what decideMemberInvoice concludes. InvoiceID is
// set only when the payment can be booked; Reason then is empty.
type memberInvoiceDecision struct {
	InvoiceID     string
	InvoiceNumber int
	Reason        string
	// SameAmount counts the member's open fakturas (imported ones included,
	// regardless of date) whose total equals the amount: the Vipps rule
	// reads it as "this may be one of them", so it never price-classifies
	// the charge instead.
	SameAmount int
}

// decideMemberInvoice decides whether a payment of amountCents made on paidOn
// by memberID settles exactly one of the member's fakturas. A faktura is open
// when it is sent and still owed by the member (finance.OwedByMemberSQL).
// The payment is booked only when exactly one open faktura was issued on or
// before paidOn, it has no payment booked against it yet, its total equals
// the amount to the øre, and no imported faktura has the same total.
// Imported fakturas (import_source set) are never targets: they have no GL
// receivable to clear.
func (s *Service) decideMemberInvoice(ctx context.Context, clubID, memberID string, amountCents int64, paidOn time.Time) (memberInvoiceDecision, error) {
	var d memberInvoiceDecision
	rows, err := s.db.Query(ctx,
		`SELECT i.id, i.invoice_number, round(i.total_amount * 100)::bigint, i.issue_date,
		        i.import_source IS NOT NULL, `+invoiceAlreadyBookedSQL("i")+`
		   FROM invoices i
		  WHERE i.club_id = $1 AND i.user_id = $2
		    AND (i.sent_at IS NOT NULL OR i.import_source IS NOT NULL)
		    AND `+finance.OwedByMemberSQL("i")+`
		  ORDER BY i.issue_date, i.invoice_number`,
		clubID, memberID,
	)
	if err != nil {
		return d, fmt.Errorf("listing open fakturas: %w", err)
	}
	defer rows.Close()
	type openInvoice struct {
		id       string
		number   int
		cents    int64
		issued   time.Time
		imported bool
		booked   bool
	}
	var live, dated []openInvoice
	importedAny, importedSame := false, false
	for rows.Next() {
		var o openInvoice
		if err := rows.Scan(&o.id, &o.number, &o.cents, &o.issued, &o.imported, &o.booked); err != nil {
			return d, fmt.Errorf("scanning open faktura: %w", err)
		}
		if o.cents == amountCents {
			d.SameAmount++
		}
		if o.imported {
			importedAny = true
			importedSame = importedSame || o.cents == amountCents
			continue
		}
		live = append(live, o)
		if !dayOf(o.issued).After(dayOf(paidOn)) {
			dated = append(dated, o)
		}
	}
	if err := rows.Err(); err != nil {
		return d, err
	}

	switch {
	case importedSame:
		d.Reason = reasonImportedFaktura
	case len(dated) == 0 && len(live) > 0:
		d.Reason = reasonFakturaAfterPayment
	case len(dated) == 0 && importedAny:
		d.Reason = reasonImportedFaktura
	case len(dated) == 0:
		d.Reason = reasonNoOpenFaktura
	case len(dated) > 1:
		d.Reason = reasonSeveralOpenFakturas
	case dated[0].booked:
		d.Reason = reasonFakturaPartlyPaid
	case dated[0].cents != amountCents:
		d.Reason = reasonAmountMismatch
	default:
		d.InvoiceID, d.InvoiceNumber = dated[0].id, dated[0].number
	}
	return d, nil
}
