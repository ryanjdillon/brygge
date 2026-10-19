package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
)

// Auto-assignment of KID-less bank deposits (BRY-366). After a bank import
// or a bank sync, an incoming row with no usable reference is booked to a
// faktura without a human only when it is unambiguous: the payer resolves
// to exactly one member through a strong pass, and decideMemberInvoice finds
// that member's single open faktura for exactly the amount. Everything else
// stays in Tildel, with the reason counted.
//
// It is opt-in per club (clubs.bank_auto_assign), separate from the Vipps
// switch. While it is off, the KID cascade's older name+amount step
// (findInvoiceByCounterpartAndAmount) still runs; once it is on, that step
// is skipped and this rule replaces it. Each booking is audited
// (bank_row.auto_assigned) and undone with UnassignBankRow.

// Reasons a deposit is left in Tildel, besides the decideMemberInvoice ones.
const (
	reasonNoPeriod              = "no_period"
	reasonPeriodClosed          = "period_closed"
	reasonHasReference          = "has_reference"
	reasonNoPayer               = "no_payer"
	reasonOwnClub               = "own_club"
	reasonNoStrongMember        = "no_strong_member"
	reasonHasPartner            = "has_partner"
	reasonPossibleDoublePayment = "possible_double_payment"
	reasonNoActor               = "no_actor"
	reasonBookingFailed         = "booking_failed"
)

// What ran the bank rule, recorded on each audit row.
const (
	BankAutoAssignTriggerImport = "bank_import"
	BankAutoAssignTriggerSync   = "bank_sync"
)

const bankAutoAssignFailedText = "Automatisk tildeling av bankinnbetalinger feilet – prøv igjen, eller se loggen"

// BankAutoAssignOpts says how AutoAssignBankDeposits runs.
type BankAutoAssignOpts struct {
	// DryRun decides without booking, as if the club had opted in.
	DryRun  bool
	Trigger string
	// PeriodOverride, when set, is the period an import forces its own
	// rows (ImportID) into; other rows use the period of their date.
	ImportID       string
	PeriodOverride string
}

// bankDeposit is a candidate row as the rule sees it.
type bankDeposit struct {
	id, kid, reference, desc, counterpart string
	fromAccount, toAccount, bankAcct      string
	importID, periodStatus                string
	date                                  time.Time
	cents                                 int64
}

// bankDepositDecision is a deposit the rule would book.
type bankDepositDecision struct {
	row       bankDeposit
	invoiceID string
	number    int
	pass      int
}

// BankAutoAssignEnabled reports whether the club has opted into booking
// KID-less deposits automatically.
func (s *Service) BankAutoAssignEnabled(ctx context.Context, clubID string) (bool, error) {
	var on bool
	err := s.db.QueryRow(ctx, `SELECT bank_auto_assign FROM clubs WHERE id = $1`, clubID).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("club not found")
	}
	if err != nil {
		return false, fmt.Errorf("loading bank auto-assign setting: %w", err)
	}
	return on, nil
}

// AutoAssignBankDepositsReported runs the rule for an import or a sync and
// never fails its caller: a failure is logged and set as the result's
// Error. It returns nil when the club has not opted in, so callers report
// nothing.
func (s *Service) AutoAssignBankDepositsReported(ctx context.Context, clubID, actorID string, opts BankAutoAssignOpts) *AutoAssignResult {
	on, err := s.BankAutoAssignEnabled(ctx, clubID)
	if err != nil {
		s.log.Error().Err(err).Str("club_id", clubID).Msg("bank auto-assign: loading the setting failed")
		return &AutoAssignResult{Source: AutoAssignSourceBank, Assigned: []AutoAssignItem{}, SkippedDetails: []SkippedItem{}, Error: bankAutoAssignFailedText}
	}
	if !on {
		return nil
	}
	res, err := s.AutoAssignBankDeposits(ctx, clubID, actorID, opts)
	if err != nil {
		s.log.Error().Err(err).Str("club_id", clubID).Msg("bank auto-assign failed")
		if res == nil {
			res = &AutoAssignResult{Source: AutoAssignSourceBank, Enabled: true, Assigned: []AutoAssignItem{}, SkippedDetails: []SkippedItem{}}
		}
		res.Error = bankAutoAssignFailedText
	}
	return res
}

// AutoAssignBankDeposits books every unambiguous KID-less deposit of the
// club and reports what it booked and why the rest stays in Tildel. It is
// club-wide and idempotent: a booked row leaves the candidate set. With
// opts.DryRun it books nothing and decides as if the club had opted in;
// Enabled still reports the real setting.
func (s *Service) AutoAssignBankDeposits(ctx context.Context, clubID, actorID string, opts BankAutoAssignOpts) (*AutoAssignResult, error) {
	res := &AutoAssignResult{Source: AutoAssignSourceBank, Assigned: []AutoAssignItem{}, SkippedDetails: []SkippedItem{}, ByReason: map[string]int{}}
	on, err := s.BankAutoAssignEnabled(ctx, clubID)
	if err != nil {
		return nil, err
	}
	res.Enabled = on
	if !on && !opts.DryRun {
		return res, nil
	}

	rows, err := s.loadBankDeposits(ctx, clubID, opts)
	if err != nil {
		return nil, err
	}
	res.NeedsReview = len(rows)
	if len(rows) == 0 {
		return res, nil
	}
	if actorID == "" && !opts.DryRun {
		// Posting needs a real user; there is no unattended bank sync.
		res.ByReason[reasonNoActor] = len(rows)
		return res, nil
	}

	decided, err := s.decideBankDeposits(ctx, clubID, rows, res.ByReason)
	if err != nil {
		return nil, err
	}
	if opts.DryRun {
		for _, d := range decided {
			res.Assigned = append(res.Assigned, d.item(""))
		}
		res.NeedsReview -= len(decided)
		return res, nil
	}

	touched := map[string]bool{}
	for _, d := range decided {
		override := ""
		if opts.ImportID != "" && d.row.importID == opts.ImportID {
			override = opts.PeriodOverride
		}
		entryID, _, err := s.assignBankRowToInvoice(ctx, clubID, actorID, d.row.id, d.invoiceID, assignOpts{auto: true, periodOverride: override})
		if err != nil {
			s.log.Warn().Err(err).Str("bank_row_id", d.row.id).Msg("bank auto-assign: booking failed; row stays in Tildel")
			res.ByReason[reasonBookingFailed]++
			res.SkippedDetails = append(res.SkippedDetails, SkippedItem{
				Ref:    fmt.Sprintf("%s %.2f kr", d.row.date.Format("2006-01-02"), float64(d.row.cents)/100),
				Reason: err.Error(),
			})
			continue
		}
		item := d.item(entryID)
		res.Assigned = append(res.Assigned, item)
		res.NeedsReview--
		touched[d.row.importID] = true
		if s.audit != nil {
			s.audit.LogAction(ctx, clubID, actorID, "", audit.ActionBankRowAutoAssigned, "bank_import_row", d.row.id,
				map[string]any{
					"rule":             autoAssignRuleMemberInvoice,
					"target":           item.Target,
					"invoice_id":       d.invoiceID,
					"journal_entry_id": entryID,
					"match_pass":       item.MatchPass,
					"trigger":          opts.Trigger,
				})
		}
	}
	s.refreshMatchedCounts(ctx, clubID, touched)
	return res, nil
}

func (d bankDepositDecision) item(entryID string) AutoAssignItem {
	return AutoAssignItem{
		Source:        AutoAssignSourceBank,
		RowID:         d.row.id,
		Date:          d.row.date.Format("2006-01-02"),
		Amount:        float64(d.row.cents) / 100,
		Rule:          autoAssignRuleMemberInvoice,
		Target:        fmt.Sprintf("faktura %d", d.number),
		EntryID:       entryID,
		InvoiceNumber: d.number,
		MatchPass:     memberPassName(d.pass),
	}
}

// loadBankDeposits loads the club's incoming rows the rule may book: unbooked,
// undismissed, not refund-paired, not a Vipps payout, not provisional
// (partnerEligibleSQL), without one of our KIDs and not a bounced transfer.
// Each carries the status of the fiscal period it would post to, or "" when
// none exists.
func (s *Service) loadBankDeposits(ctx context.Context, clubID string, opts BankAutoAssignOpts) ([]bankDeposit, error) {
	rows, err := s.db.Query(ctx,
		`SELECT bir.id, bir.row_date, round(bir.amount * 100)::bigint,
		        COALESCE(bir.kid_number, ''), COALESCE(bir.reference, ''), COALESCE(bir.description, ''),
		        COALESCE(bir.counterpart, ''), COALESCE(bir.from_account, ''), COALESCE(bir.to_account, ''),
		        bi.bank_account_code, bir.bank_import_id::text, COALESCE(fp.status::text, '')
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		   LEFT JOIN LATERAL (
		        SELECT p.status FROM fiscal_periods p
		         WHERE p.club_id = bir.club_id
		           AND CASE WHEN $2 <> '' AND bir.bank_import_id::text = $3 THEN p.id::text = $2
		                    ELSE bir.row_date BETWEEN p.start_date AND p.end_date END
		         ORDER BY p.start_date DESC
		         LIMIT 1) fp ON true
		  WHERE bir.club_id = $1 AND bir.amount > 0
		    AND `+partnerEligibleSQL("bir")+`
		  ORDER BY bir.row_date, bir.id`,
		clubID, opts.PeriodOverride, opts.ImportID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing deposits: %w", err)
	}
	defer rows.Close()
	var out []bankDeposit
	for rows.Next() {
		var r bankDeposit
		if err := rows.Scan(&r.id, &r.date, &r.cents, &r.kid, &r.reference, &r.desc,
			&r.counterpart, &r.fromAccount, &r.toAccount, &r.bankAcct, &r.importID, &r.periodStatus); err != nil {
			return nil, fmt.Errorf("scanning deposit: %w", err)
		}
		// A row carrying one of our KIDs belongs to the KID cascade, and a
		// bounced transfer is money that never arrived.
		if isBryggeKID(strings.TrimSpace(r.kid)) || IsRejectedKIDDescription(r.desc) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// decideBankDeposits applies the checks in order; the first a row fails
// leaves it in Tildel and counts the reason. It returns the rows to book.
func (s *Service) decideBankDeposits(ctx context.Context, clubID string, rows []bankDeposit, byReason map[string]int) ([]bankDepositDecision, error) {
	cands, err := loadMemberCands(ctx, s.db, clubID)
	if err != nil {
		return nil, err
	}
	sc, err := s.loadPartnerScoring(ctx, s.db, clubID)
	if err != nil {
		return nil, err
	}

	var decided []bankDepositDecision
	for _, r := range rows {
		d, reason, err := s.decideBankDeposit(ctx, clubID, cands, sc, r)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			byReason[reason]++
			continue
		}
		decided = append(decided, d)
	}
	if len(decided) == 0 {
		return nil, nil
	}

	// A strong partner (BRY-380) means the deposit may belong with another
	// row, such as a refund; booking it alone is a human's call.
	ids := make([]string, len(decided))
	for i, d := range decided {
		ids[i] = d.row.id
	}
	partners, err := s.partnersFor(ctx, s.db, clubID, ids)
	if err != nil {
		return nil, fmt.Errorf("checking partners: %w", err)
	}
	perInvoice := map[string]int{}
	kept := decided[:0]
	for _, d := range decided {
		if hasStrongPartner(partners[d.row.id]) {
			byReason[reasonHasPartner]++
			continue
		}
		kept = append(kept, d)
		perInvoice[d.invoiceID]++
	}

	// Two deposits in one run for the same faktura: one of them is a double
	// payment, and which one is not ours to pick.
	out := kept[:0]
	for _, d := range kept {
		if perInvoice[d.invoiceID] > 1 {
			byReason[reasonPossibleDoublePayment]++
			s.log.Warn().Str("bank_row_id", d.row.id).Str("invoice_id", d.invoiceID).
				Msg("bank auto-assign: several deposits for one faktura; possible double payment left in Tildel")
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// decideBankDeposit runs the per-row checks. It returns the decision, or the
// reason the row stays in Tildel.
func (s *Service) decideBankDeposit(ctx context.Context, clubID string, cands []memberCand, sc partnerScoring, r bankDeposit) (bankDepositDecision, string, error) {
	// The row-only checks come first, so a row that could never be
	// auto-booked counts as that rather than as a period problem.
	if ExtractKIDFromDescription(r.desc) != "" || ExtractInvoiceNumberFromDescription(r.desc) != "" ||
		isBryggeKID(strings.TrimSpace(r.reference)) {
		return bankDepositDecision{}, reasonHasReference, nil
	}
	payer := bankRowPayer(r.desc, r.counterpart)
	if payer == "" {
		return bankDepositDecision{}, reasonNoPayer, nil
	}
	switch r.periodStatus {
	case "":
		return bankDepositDecision{}, reasonNoPeriod, nil
	case "open":
	default:
		return bankDepositDecision{}, reasonPeriodClosed, nil
	}
	if sc.isOwn(partnerRow{Cents: r.cents, Counterpart: payer, FromAccount: r.fromAccount, ToAccount: r.toAccount}) {
		return bankDepositDecision{}, reasonOwnClub, nil
	}
	memberID, pass := s.resolveStrongMember(ctx, clubID, cands, payer)
	if memberID == "" {
		return bankDepositDecision{}, reasonNoStrongMember, nil
	}
	mi, err := s.decideMemberInvoice(ctx, clubID, memberID, r.cents, r.date)
	if err != nil {
		return bankDepositDecision{}, "", err
	}
	if mi.InvoiceID == "" {
		return bankDepositDecision{}, mi.Reason, nil
	}
	return bankDepositDecision{row: r, invoiceID: mi.InvoiceID, number: mi.InvoiceNumber, pass: pass}, "", nil
}

// bankRowPayer is the payer name of a bank row: the "Fra:" name when the
// description has one, as import enrichment stores it, else the
// counterpart. It reads the description itself so a preview writes nothing.
// Auto-assign resolves and Tildel teaches (BRY-383) the same name, so an
// alias taught from a row matches that payer's next row.
func bankRowPayer(desc, counterpart string) string {
	if payer := ExtractPayerFromDescription(desc); payer != "" {
		return payer
	}
	return strings.TrimSpace(counterpart)
}

func hasStrongPartner(ps []PartnerSuggestion) bool {
	for _, p := range ps {
		if p.Strength == PartnerStrong {
			return true
		}
	}
	return false
}

// refreshMatchedCounts recomputes bank_imports.matched_count for the imports
// whose rows the rule booked, so the import list counts them as matched.
func (s *Service) refreshMatchedCounts(ctx context.Context, clubID string, importIDs map[string]bool) {
	if len(importIDs) == 0 {
		return
	}
	ids := make([]string, 0, len(importIDs))
	for id := range importIDs {
		ids = append(ids, id)
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE bank_imports bi
		    SET matched_count = (SELECT count(*) FROM bank_import_rows r
		                          WHERE r.bank_import_id = bi.id AND r.journal_entry_id IS NOT NULL)
		  WHERE bi.club_id = $1 AND bi.id = ANY($2::uuid[])`,
		clubID, ids,
	); err != nil {
		s.log.Warn().Err(err).Msg("bank auto-assign: refreshing matched counts failed")
	}
}

// BankAutoAssignPreviewItem is one deposit the rule would book. It names no
// payer: the faktura number is what a board reader already sees.
type BankAutoAssignPreviewItem struct {
	RowID         string  `json:"row_id"`
	Date          string  `json:"date"`
	Amount        float64 `json:"amount"`
	InvoiceNumber int     `json:"invoice_number"`
	MatchPass     string  `json:"match_pass"`
}

// BankAutoAssignPreview is the read-only projection of the bank rule.
type BankAutoAssignPreview struct {
	Enabled           bool                        `json:"enabled"`
	Candidates        int                         `json:"candidates"`
	WouldAssign       int                         `json:"would_assign"`
	WouldAssignAmount float64                     `json:"would_assign_amount"`
	ByReason          map[string]int              `json:"by_reason"`
	Items             []BankAutoAssignPreviewItem `json:"items"`
}

// PreviewBankAutoAssign reports what the bank rule would book now, as if the
// club had opted in. It writes nothing.
func (s *Service) PreviewBankAutoAssign(ctx context.Context, clubID string) (*BankAutoAssignPreview, error) {
	res, err := s.AutoAssignBankDeposits(ctx, clubID, "", BankAutoAssignOpts{DryRun: true})
	if err != nil {
		return nil, err
	}
	p := &BankAutoAssignPreview{
		Enabled:     res.Enabled,
		Candidates:  res.NeedsReview + len(res.Assigned),
		WouldAssign: len(res.Assigned),
		ByReason:    res.ByReason,
		Items:       make([]BankAutoAssignPreviewItem, 0, len(res.Assigned)),
	}
	var cents int64
	for _, a := range res.Assigned {
		cents += toCents(a.Amount)
		p.Items = append(p.Items, BankAutoAssignPreviewItem{
			RowID: a.RowID, Date: a.Date, Amount: a.Amount, InvoiceNumber: a.InvoiceNumber, MatchPass: a.MatchPass,
		})
	}
	p.WouldAssignAmount = float64(cents) / 100
	return p, nil
}
