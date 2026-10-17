package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/finance"
)

// Auto-assignment of Vipps charges (BRY-340). After every import the
// per-charge queue is walked and a charge is booked without a human only
// when the outcome is unambiguous; everything else stays in Tildel. Two
// rules, in order:
//
//  1. member_invoice — the payer resolves to exactly one member through a
//     strong pass (taught alias, KID, or the whole normalized name), that
//     member has exactly one open faktura, and the amounts are equal. Booked
//     through AssignVippsChargeToInvoice, so the faktura reads as paid.
//  2. price_item — the amount fits the club's active price items, and they
//     all share one category (several items in one category book to the same
//     account). When they span categories, the club's configured tie
//     category wins only if it is among them and every match is a per-unit
//     (night/day/hour) price; an exact one-off price in any tied category,
//     or no configured tie category, leaves the charge manual. An item for members only also needs the payer to resolve
//     to a member. Booked through CreateVippsChargeBilag to the category's
//     revenue account.
//
// The whole feature is opt-in per club (clubs.vipps_auto_assign). Postings
// follow the per-charge model and post on creation (BRY-339).
// Each booking is audited and can be undone with UnassignVippsCharge.

const (
	autoAssignRuleMemberInvoice = "member_invoice"
	autoAssignRulePriceItem     = "price_item"
)

// VippsAutoAssignItem is one charge an auto-assign run booked.
type VippsAutoAssignItem struct {
	RowID   string  `json:"row_id"`
	Date    string  `json:"date"`
	Amount  float64 `json:"amount"`
	Rule    string  `json:"rule"`
	Target  string  `json:"target"` // "faktura N" or an account code
	EntryID string  `json:"entry_id"`
}

// VippsAutoAssignResult summarises one AutoAssignVippsCharges run.
type VippsAutoAssignResult struct {
	Enabled        bool                  `json:"enabled"`
	Assigned       []VippsAutoAssignItem `json:"assigned"`
	NeedsReview    int                   `json:"needs_review"`
	SkippedDetails []SkippedItem         `json:"skipped_details"`
}

type vippsAutoAssignConfig struct {
	enabled     bool
	tieCategory string
}

func (s *Service) vippsAutoAssignConfig(ctx context.Context, clubID string) (vippsAutoAssignConfig, error) {
	var c vippsAutoAssignConfig
	var tie *string
	err := s.db.QueryRow(ctx,
		`SELECT vipps_auto_assign, vipps_auto_assign_tie_category FROM clubs WHERE id = $1`,
		clubID,
	).Scan(&c.enabled, &tie)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fmt.Errorf("club not found")
	}
	if err != nil {
		return c, fmt.Errorf("loading auto-assign settings: %w", err)
	}
	if tie != nil {
		c.tieCategory = *tie
	}
	return c, nil
}

// AutoAssignVippsCharges books every unambiguous charge in the queue (see the
// rules above) and reports what it booked and how many charges still need a
// human. Charges naming a checkout order are left to
// AutoReconcileOrderCharges. Idempotent: booked charges leave the queue.
func (s *Service) AutoAssignVippsCharges(ctx context.Context, clubID, createdBy string) (*VippsAutoAssignResult, error) {
	res := &VippsAutoAssignResult{Assigned: []VippsAutoAssignItem{}, SkippedDetails: []SkippedItem{}}
	cfg, err := s.vippsAutoAssignConfig(ctx, clubID)
	if err != nil {
		return nil, err
	}
	res.Enabled = cfg.enabled

	rows, err := s.db.Query(ctx,
		`SELECT r.id, r.amount, COALESCE(r.booking_date, r.tx_at::date),
		        COALESCE(r.customer_name, ''), COALESCE(r.message, '')
		   FROM vipps_import_rows r
		  WHERE r.club_id = $1 AND r.row_type = 'belastning' AND r.amount > 0
		    AND r.journal_entry_id IS NULL AND r.dismissed_at IS NULL
		    AND NOT (r.order_id <> '' AND EXISTS (
		        SELECT 1 FROM orders o WHERE o.club_id = r.club_id AND o.vipps_reference = r.order_id))
		  ORDER BY r.tx_at`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing queued charges: %w", err)
	}
	type charge struct {
		id, customer, message string
		amount                float64
		date                  string
	}
	var queue []charge
	for rows.Next() {
		var c charge
		var d time.Time
		if err := rows.Scan(&c.id, &c.amount, &d, &c.customer, &c.message); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning queued charge: %w", err)
		}
		c.date = d.Format("2006-01-02")
		queue = append(queue, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if !cfg.enabled {
		res.NeedsReview = len(queue)
		return res, nil
	}

	for _, c := range queue {
		item, _, err := s.autoAssignOne(ctx, clubID, createdBy, cfg, c.id, c.customer, c.message, c.amount)
		if err != nil {
			res.SkippedDetails = append(res.SkippedDetails, SkippedItem{Ref: fmt.Sprintf("%s %.2f kr", c.date, c.amount), Reason: err.Error()})
			res.NeedsReview++
			continue
		}
		if item == nil { // ambiguous: a human decides in Tildel
			res.NeedsReview++
			continue
		}
		item.Date = c.date
		item.Amount = c.amount
		res.Assigned = append(res.Assigned, *item)
	}
	return res, nil
}

func allPerUnit(cands []priceArticleCandidate) bool {
	for _, c := range cands {
		if !c.perUnit {
			return false
		}
	}
	return true
}

// vippsChargeDecision is what the auto-assign rules conclude for one charge.
// Rule is empty when the charge is ambiguous; Reason then says why.
type vippsChargeDecision struct {
	Rule          string
	InvoiceID     string
	InvoiceNumber int
	Account       string
	Label         string
	Reason        string
}

// Target is the human-readable booking target: "faktura N" or an account.
func (d *vippsChargeDecision) Target() string {
	if d.Rule == autoAssignRuleMemberInvoice {
		return fmt.Sprintf("faktura %d", d.InvoiceNumber)
	}
	return d.Account
}

// decideVippsCharge applies the auto-assign rules to one charge without
// booking anything, so callers can preview the outcome.
func (s *Service) decideVippsCharge(ctx context.Context, clubID string, cfg vippsAutoAssignConfig, customer, message string, amount float64) (*vippsChargeDecision, error) {
	memberID, pass, err := s.resolveCustomerToMemberPass(ctx, clubID, customer, message)
	if err != nil {
		return nil, fmt.Errorf("resolving payer: %w", err)
	}
	strongMember := memberID != "" && pass <= memberPassFirstLast

	if strongMember {
		invoiceID, invoiceNumber, sameAmount, open, err := s.singleOpenInvoice(ctx, clubID, memberID, amount)
		if err != nil {
			return nil, err
		}
		if open == 1 && invoiceID != "" {
			return &vippsChargeDecision{Rule: autoAssignRuleMemberInvoice, InvoiceID: invoiceID, InvoiceNumber: invoiceNumber}, nil
		}
		// The payer has an open faktura for exactly this amount among others:
		// it may be that faktura, so don't guess a revenue account instead.
		if sameAmount > 0 {
			return &vippsChargeDecision{Reason: "member has several open fakturas for this amount"}, nil
		}
	}

	cands := s.priceArticleCandidates(ctx, clubID, amount)
	if len(cands) == 0 {
		return &vippsChargeDecision{Reason: "no faktura or price item fits"}, nil
	}
	byCategory := map[string][]priceArticleCandidate{}
	for _, c := range cands {
		byCategory[c.category] = append(byCategory[c.category], c)
	}
	category := ""
	if len(byCategory) == 1 {
		category = cands[0].category
	} else if _, ok := byCategory[cfg.tieCategory]; ok && cfg.tieCategory != "" && allPerUnit(cands) {
		// The tie category only settles per-night style ties (e.g. guest
		// slip vs motorhome at the same nightly rate). When the amount is
		// also an exact one-off price elsewhere (a season, slipping, slip
		// rent), it could be that item, so a human decides.
		category = cfg.tieCategory
	} else {
		return &vippsChargeDecision{Reason: "amount fits price items in several categories"}, nil
	}
	chosen := byCategory[category]
	membersOnly := true
	for _, c := range chosen {
		if c.audience != "member" {
			membersOnly = false
			break
		}
	}
	if membersOnly && !strongMember {
		return &vippsChargeDecision{Reason: "members-only price but the payer is not a known member"}, nil
	}
	account, ok := vippsCategoryAccountMap[category]
	if !ok {
		return &vippsChargeDecision{Reason: "price category has no revenue account"}, nil
	}
	return &vippsChargeDecision{Rule: autoAssignRulePriceItem, Account: account, Label: chosen[0].label()}, nil
}

// bookVippsChargeDecision books an unbooked charge per a decision and
// records the rule. It returns the posted entry.
func (s *Service) bookVippsChargeDecision(ctx context.Context, clubID, createdBy, rowID string, d *vippsChargeDecision) (string, error) {
	var entryID string
	switch d.Rule {
	case autoAssignRuleMemberInvoice:
		r, err := s.AssignVippsChargeToInvoice(ctx, clubID, rowID, d.InvoiceID, createdBy)
		if err != nil {
			return "", err
		}
		entryID = r.EntryID
	case autoAssignRulePriceItem:
		r, err := s.CreateVippsChargeBilag(ctx, clubID, rowID, d.Account, "auto: "+d.Label, createdBy)
		if err != nil {
			return "", err
		}
		entryID = r.EntryID
	default:
		return "", fmt.Errorf("no booking rule for this charge")
	}
	s.markAutoAssigned(ctx, clubID, createdBy, rowID, d.Rule, d.Target(), entryID)
	return entryID, nil
}

// autoAssignOne applies the rules to one charge. It returns the booking, or
// nil with the reason it was left for a human.
func (s *Service) autoAssignOne(ctx context.Context, clubID, createdBy string, cfg vippsAutoAssignConfig, rowID, customer, message string, amount float64) (*VippsAutoAssignItem, string, error) {
	d, err := s.decideVippsCharge(ctx, clubID, cfg, customer, message, amount)
	if err != nil {
		return nil, "", err
	}
	if d.Rule == "" {
		return nil, d.Reason, nil
	}
	entryID, err := s.bookVippsChargeDecision(ctx, clubID, createdBy, rowID, d)
	if err != nil {
		return nil, "", err
	}
	return &VippsAutoAssignItem{RowID: rowID, Rule: d.Rule, Target: d.Target(), EntryID: entryID}, "", nil
}

// singleOpenInvoice looks at the member's fakturas that are sent and not yet
// settled for the member. It returns the faktura to book when there is
// exactly one and its total equals the amount, the number of open fakturas
// with that amount, and the number of open fakturas overall.
func (s *Service) singleOpenInvoice(ctx context.Context, clubID, memberID string, amount float64) (invoiceID string, invoiceNumber, sameAmount, open int, err error) {
	rows, err := s.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.total_amount
		   FROM invoices i
		  WHERE i.club_id = $1 AND i.user_id = $2 AND i.sent_at IS NOT NULL
		    AND `+finance.OwedByMemberSQL("i"),
		clubID, memberID,
	)
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("listing open fakturas: %w", err)
	}
	defer rows.Close()
	var lastID string
	var lastNumber int
	for rows.Next() {
		var id string
		var number int
		var total float64
		if err := rows.Scan(&id, &number, &total); err != nil {
			return "", 0, 0, 0, err
		}
		open++
		if floatNear(total, amount, 0.005) {
			sameAmount++
			lastID, lastNumber = id, number
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, 0, 0, err
	}
	if open == 1 && sameAmount == 1 {
		return lastID, lastNumber, sameAmount, open, nil
	}
	return "", 0, sameAmount, open, nil
}

// markAutoAssigned records which rule booked the charge and writes the audit
// row. Failures are logged: the booking itself already stands.
func (s *Service) markAutoAssigned(ctx context.Context, clubID, actorID, rowID, rule, target, entryID string) {
	if _, err := s.db.Exec(ctx,
		`UPDATE vipps_import_rows SET auto_assign_rule = $3 WHERE id = $1 AND club_id = $2`,
		rowID, clubID, rule,
	); err != nil {
		s.log.Warn().Err(err).Str("vipps_row_id", rowID).Msg("auto-assign: recording the rule failed")
	}
	if s.audit != nil {
		s.audit.LogAction(ctx, clubID, actorID, "", audit.ActionVippsChargeAutoAssigned, "vipps_import_row", rowID,
			map[string]any{"rule": rule, "target": target, "journal_entry_id": entryID})
	}
}

// UnassignVippsCharge undoes a charge booking so the charge returns to the
// Tildel queue: a draft entry is deleted, a posted one voided (the original
// and its reversal stay, as bokføringsloven requires), and a faktura the
// charge settled is reopened. Charges booked from a checkout order settle
// fakturas through the order and are refused; void their bilag instead.
func (s *Service) UnassignVippsCharge(ctx context.Context, clubID, actorID, rowID string) (priorEntryID, priorInvoiceID string, err error) {
	var entryID, invoiceID *string
	var orderRef string
	err = s.db.QueryRow(ctx,
		`SELECT journal_entry_id::text, invoice_id::text, COALESCE(order_id, '')
		   FROM vipps_import_rows
		  WHERE id = $1 AND club_id = $2 AND row_type = 'belastning'`,
		rowID, clubID,
	).Scan(&entryID, &invoiceID, &orderRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("vipps charge not found")
	}
	if err != nil {
		return "", "", fmt.Errorf("loading vipps charge: %w", err)
	}
	if entryID == nil {
		return "", "", fmt.Errorf("charge is not booked")
	}
	if invoiceID == nil && orderRef != "" {
		var isOrder bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM orders WHERE club_id = $1 AND vipps_reference = $2)`,
			clubID, orderRef,
		).Scan(&isOrder); err != nil {
			return "", "", fmt.Errorf("checking order: %w", err)
		}
		if isOrder {
			return "", "", fmt.Errorf("charge was booked from checkout order %s — void its bilag instead", orderRef)
		}
	}
	priorEntryID = *entryID
	if invoiceID != nil {
		priorInvoiceID = *invoiceID
	}

	var status string
	if err := s.db.QueryRow(ctx,
		`SELECT status FROM journal_entries WHERE id = $1 AND club_id = $2`,
		priorEntryID, clubID,
	).Scan(&status); err != nil {
		return "", "", fmt.Errorf("loading journal entry: %w", err)
	}

	// A posted booking is voided before anything is unlinked, so the GL never
	// counts an entry for a charge that is already back in Tildel. The
	// reversal lands in the entry's own period; VoidJournalEntry refuses a
	// closed one under the period lock. If the clearing below then fails, the
	// entry is voided and a retry (status 'voided') just finishes the clearing.
	if status == "posted" {
		if _, err := s.VoidJournalEntry(ctx, clubID, priorEntryID, actorID); err != nil {
			if errors.Is(err, ErrPeriodClosed) {
				return "", "", fmt.Errorf("charge is booked in a closed fiscal period — correct it with a new entry in an open period: %w", ErrPeriodClosed)
			}
			return "", "", fmt.Errorf("voiding bilag: %w", err)
		}
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", "", fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE vipps_import_rows
		    SET journal_entry_id = NULL, invoice_id = NULL, auto_assign_rule = NULL
		  WHERE id = $1 AND club_id = $2`,
		rowID, clubID,
	); err != nil {
		return "", "", fmt.Errorf("clearing charge: %w", err)
	}
	if priorInvoiceID != "" {
		var paymentID *string
		if err := tx.QueryRow(ctx,
			`SELECT payment_id::text FROM invoices WHERE id = $1 AND club_id = $2`,
			priorInvoiceID, clubID,
		).Scan(&paymentID); err != nil {
			return "", "", fmt.Errorf("loading invoice payment: %w", err)
		}
		if paymentID != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE invoices SET payment_id = NULL WHERE id = $1 AND club_id = $2`,
				priorInvoiceID, clubID,
			); err != nil {
				return "", "", fmt.Errorf("reopening invoice: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`DELETE FROM payments WHERE id = $1 AND club_id = $2`,
				*paymentID, clubID,
			); err != nil {
				return "", "", fmt.Errorf("removing payment: %w", err)
			}
		}
	}
	if status == "draft" {
		if _, err := tx.Exec(ctx,
			`DELETE FROM journal_entries WHERE id = $1 AND club_id = $2 AND status = 'draft'`,
			priorEntryID, clubID,
		); err != nil {
			return "", "", fmt.Errorf("deleting draft: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return priorEntryID, priorInvoiceID, fmt.Errorf("bilag voided but clearing the charge failed, run Angre again: %w", err)
	}
	return priorEntryID, priorInvoiceID, nil
}
