package accounting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reclassifying posted Vipps revenue parked on the 3900 fallback (BRY-557).
//
// Before auto-assign (BRY-340) the legacy cascade parked every Vipps charge
// it could not place on 3900 (Andre inntekter) and Bokfør Vipps-utkast
// (BRY-341) posted the drafts there. PostVippsDraftBacklog can only rebook a
// draft; these charges are already posted, usually in a cleared settlement.
// This walks the posted charge entries of the open fiscal periods whose one
// revenue line is on 3900 and, per charge, runs the same rules as
// auto-assign (decideVippsCharge):
//
//   - a price-item rule moves the revenue line to the category's account;
//   - a member-faktura rule is NOT applied here (a reclass is revenue-only):
//     the charge is listed as needing Tildel, with the faktura number;
//   - anything ambiguous, or with no rule, stays on 3900 with its reason.
//
// The move is reverse-and-repost of the charge's own entry in one
// transaction: the original is voided (VoidJournalEntry's reversal stays in
// the books), a new posted entry DR clearing / CR category replaces it, and
// the charge row is relinked to it. The clearing account and the payout's
// fee and bank entries are never touched, so the settlement stays cleared
// and the trial balance changes only by 3900 → category. The replacement's
// description names the voided bilag and its reversal, and the batch audit
// row lists every pair.
//
// Preview changes nothing. Apply takes the previewed account per entry and
// reclassifies only those still proposed with that same account, so it books
// what the treasurer saw; every guard is re-checked under the charge's row
// lock. A second run finds nothing: the replacement is no longer on 3900.

// ErrVippsAutoAssignOff is the refusal to reclassify while the club has not
// enabled auto-assign: the club's opt-in and tie category govern which price
// item a charge belongs to, exactly as for new charges. Shown as is, so a
// full Norwegian sentence.
var ErrVippsAutoAssignOff = errors.New( //nolint:staticcheck // ST1005: a user-facing sentence, see above
	"Vipps-autotildeling er av for klubben. Slå den på før Vipps-inntekter på 3900 reklassifiseres.")

// errFallbackMoved means the charge is no longer booked on the entry the
// plan saw (a concurrent run or an unassign got there first); nothing is done.
var errFallbackMoved = errors.New("the charge is no longer booked on that bilag")

// Status of a charge in a VippsFallbackReclassResult.
const (
	FallbackReclassReclassify  = "reclassify"
	FallbackReclassNeedsTildel = "needs_tildel"
	FallbackReclassStays       = "stays"
)

// Outcome of a charge in an apply.
const (
	FallbackOutcomeReclassified = "reclassified"
	// FallbackOutcomeSkipped: not applied because it was not in the
	// request, now has another target, or was already moved.
	FallbackOutcomeSkipped = "skipped"
	FallbackOutcomeFailed  = "failed"
)

// Reason codes; the UI translates them, Reason carries the English text.
const (
	reasonCodeAutoAssignOff  = "auto_assign_off"
	reasonCodeDecisionFailed = "decision_failed"
	reasonCodeNeedsTildel    = "needs_tildel"
	reasonCodeSameAccount    = "same_account"
	reasonCodeMaybePaid      = "maybe_paid_faktura"
	reasonCodeHasFaktura     = "has_faktura"
	reasonCodeOrderCharge    = "order_charge"
	reasonCodeBankLinked     = "bank_linked"
	reasonCodeShape          = "unexpected_shape"
	reasonCodeNoTargetAcct   = "target_account_missing"
	reasonCodeAlreadyMoved   = "already_moved"
	reasonCodeNotInRequest   = "not_in_request"
	reasonCodeTargetChanged  = "target_changed"
	reasonCodePeriodNotOpen  = "period_not_open"
	reasonCodeReclassifyFail = "failed"
)

// VippsFallbackReclassItem is one posted charge on the fallback account.
type VippsFallbackReclassItem struct {
	EntryID     string  `json:"entry_id"`
	RowID       string  `json:"row_id"`
	EntryNumber int     `json:"entry_number"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
	From        string  `json:"from"`
	Status      string  `json:"status"`
	// To and PriceItem are set for a proposed reclassification.
	To        string `json:"to,omitempty"`
	PriceItem string `json:"price_item,omitempty"`
	// InvoiceNumber is the faktura a needs_tildel charge matches, or the
	// one a maybe_paid_faktura charge may already have paid.
	InvoiceNumber int    `json:"invoice_number,omitempty"`
	ReasonCode    string `json:"reason_code,omitempty"`
	Reason        string `json:"reason,omitempty"`
	// Outcome and the entry numbers are set in an apply result only.
	Outcome             string `json:"outcome,omitempty"`
	NewEntryNumber      int    `json:"new_entry_number,omitempty"`
	ReversalEntryNumber int    `json:"reversal_entry_number,omitempty"`
}

// VippsFallbackReclassTotal is the charges that move (preview) or moved
// (apply) to one account.
type VippsFallbackReclassTotal struct {
	AccountCode string  `json:"account_code"`
	Count       int     `json:"count"`
	Amount      float64 `json:"amount"`
}

// VippsFallbackReclassResult reports a preview or an apply.
type VippsFallbackReclassResult struct {
	Applied           bool   `json:"applied"`
	AutoAssignEnabled bool   `json:"auto_assign_enabled"`
	FromAccount       string `json:"from_account"`
	// ClearingAccount is the club's Vipps clearing account, which the repost
	// debits again and nothing else touches.
	ClearingAccount string `json:"clearing_account"`
	// Items lists every posted charge on the fallback account in an open
	// period, with its status; an apply adds each one's outcome.
	Items []VippsFallbackReclassItem `json:"items"`
	// Proposed counts the reclassifications the rules give now.
	Proposed int `json:"proposed"`
	// Totals groups the proposed (preview) or moved (apply) charges by
	// target account.
	Totals            []VippsFallbackReclassTotal `json:"totals"`
	NeedsTildel       int                         `json:"needs_tildel"`
	NeedsTildelAmount float64                     `json:"needs_tildel_amount"`
	Stays             int                         `json:"stays"`
	StaysAmount       float64                     `json:"stays_amount"`
	// Reclassified, Skipped and Failed count an apply's outcomes.
	Reclassified int `json:"reclassified"`
	Skipped      int `json:"skipped"`
	Failed       int `json:"failed"`
	// StaleIDs are requested entry ids that are no proposed reclassification
	// now; nothing is done with them.
	StaleIDs []string `json:"stale_ids"`
	// ClosedPeriodCharges and ClosedPeriodAmount count the charges parked on
	// the fallback account in closed or locked periods, which are not touched.
	ClosedPeriodCharges int     `json:"closed_period_charges"`
	ClosedPeriodAmount  float64 `json:"closed_period_amount"`
}

// fallbackCharge is a posted charge entry crediting the fallback account.
type fallbackCharge struct {
	entryID     string
	entryNumber int
	entryDate   time.Time
	periodID    string
	description string
	rowID       string
	amount      float64
	paidOn      time.Time
	customer    string
	message     string
	invoiceID   *string
	isOrder     bool
	bankLinked  bool
	lines       []AccountTotal
}

// PreviewVippsFallbackReclass reports, changing nothing, what an apply would
// do to the posted Vipps charges on 3900. periodID, when set, limits it to
// one (open) period; otherwise every open period is scanned.
func (s *Service) PreviewVippsFallbackReclass(ctx context.Context, clubID, periodID string) (*VippsFallbackReclassResult, error) {
	res, plan, err := s.planVippsFallbackReclass(ctx, clubID, periodID)
	if err != nil {
		return nil, err
	}
	res.Items = plan.itemList()
	res.tally(false)
	return res, nil
}

// ApplyVippsFallbackReclass reclassifies the charges the caller previewed.
// targets maps a charge's entry id to the account the preview proposed for
// it; a charge not listed, or whose proposal is now another account, is left
// alone. With auto-assign off it refuses everything (ErrVippsAutoAssignOff).
func (s *Service) ApplyVippsFallbackReclass(ctx context.Context, clubID, actor, periodID string, targets map[string]string) (*VippsFallbackReclassResult, error) {
	res, plan, err := s.planVippsFallbackReclass(ctx, clubID, periodID)
	if err != nil {
		return nil, err
	}
	if !res.AutoAssignEnabled {
		return nil, ErrVippsAutoAssignOff
	}
	res.Applied = true

	taken := map[string]bool{}
	for i := range plan.items {
		it := &plan.items[i]
		if it.ReasonCode == reasonCodeDecisionFailed {
			// The charge could not be decided: if the caller asked for it,
			// report it failed (to retry), never guess.
			if _, listed := targets[it.EntryID]; listed {
				taken[it.EntryID] = true
				it.Outcome = FallbackOutcomeFailed
			}
			continue
		}
		if it.Status != FallbackReclassReclassify {
			continue
		}
		want, listed := targets[it.EntryID]
		switch {
		case !listed:
			it.skip(reasonCodeNotInRequest, "not in the request")
			continue
		case want != it.To:
			taken[it.EntryID] = true
			it.skip(reasonCodeTargetChanged, fmt.Sprintf("the proposal is now %s, not %s", it.To, want))
			continue
		}
		taken[it.EntryID] = true
		s.applyFallbackReclassOne(ctx, clubID, actor, plan, it)
	}
	res.StaleIDs = []string{}
	for id := range targets {
		if !taken[id] {
			res.StaleIDs = append(res.StaleIDs, id)
		}
	}
	sort.Strings(res.StaleIDs)

	res.Items = plan.itemList()
	res.tally(true)
	return res, nil
}

// fallbackPlan is the charges found on the fallback account with their
// classification, and what a booking needs to redo that classification.
type fallbackPlan struct {
	items     []VippsFallbackReclassItem
	charges   map[string]*fallbackCharge
	decisions map[string]*vippsChargeDecision
	cfg       vippsAutoAssignConfig
	clearing  string
	accounts  map[string]bool
}

func (p *fallbackPlan) itemList() []VippsFallbackReclassItem {
	if p.items == nil {
		return []VippsFallbackReclassItem{}
	}
	return p.items
}

// skip marks an item as not applied.
func (it *VippsFallbackReclassItem) skip(code, reason string) {
	it.Outcome = FallbackOutcomeSkipped
	it.ReasonCode = code
	it.Reason = reason
}

func (r *VippsFallbackReclassResult) tally(applied bool) {
	totals := map[string]*VippsFallbackReclassTotal{}
	add := func(account string, amount float64) {
		t := totals[account]
		if t == nil {
			t = &VippsFallbackReclassTotal{AccountCode: account}
			totals[account] = t
		}
		t.Count++
		t.Amount += amount
	}
	for _, it := range r.Items {
		switch it.Status {
		case FallbackReclassReclassify:
			r.Proposed++
			if !applied || it.Outcome == FallbackOutcomeReclassified {
				add(it.To, it.Amount)
			}
		case FallbackReclassNeedsTildel:
			r.NeedsTildel++
			r.NeedsTildelAmount += it.Amount
		default:
			r.Stays++
			r.StaysAmount += it.Amount
		}
		switch it.Outcome {
		case FallbackOutcomeReclassified:
			r.Reclassified++
		case FallbackOutcomeSkipped:
			r.Skipped++
		case FallbackOutcomeFailed:
			r.Failed++
		}
	}
	r.NeedsTildelAmount = roundCents(r.NeedsTildelAmount)
	r.StaysAmount = roundCents(r.StaysAmount)
	r.Totals = make([]VippsFallbackReclassTotal, 0, len(totals))
	for _, t := range totals {
		t.Amount = roundCents(t.Amount)
		r.Totals = append(r.Totals, *t)
	}
	sort.Slice(r.Totals, func(i, j int) bool { return r.Totals[i].AccountCode < r.Totals[j].AccountCode })
	if r.StaleIDs == nil {
		r.StaleIDs = []string{}
	}
}

func (s *Service) planVippsFallbackReclass(ctx context.Context, clubID, periodID string) (*VippsFallbackReclassResult, *fallbackPlan, error) {
	res := &VippsFallbackReclassResult{
		FromAccount: fallbackRevenueCode,
		Items:       []VippsFallbackReclassItem{},
		Totals:      []VippsFallbackReclassTotal{},
		StaleIDs:    []string{},
	}
	if periodID != "" {
		var year int
		var status string
		err := s.db.QueryRow(ctx,
			`SELECT year, status FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
			periodID, clubID,
		).Scan(&year, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, fmt.Errorf("fiscal period not found")
		}
		if err != nil {
			return nil, nil, fmt.Errorf("loading period: %w", err)
		}
		if status != "open" {
			return nil, nil, fmt.Errorf("fiscal period %d is %s: %w", year, status, ErrPeriodClosed)
		}
	}

	plan := &fallbackPlan{charges: map[string]*fallbackCharge{}, decisions: map[string]*vippsChargeDecision{}}
	var err error
	if plan.cfg, err = s.vippsAutoAssignConfig(ctx, clubID); err != nil {
		return nil, nil, err
	}
	res.AutoAssignEnabled = plan.cfg.enabled
	if plan.clearing, _, err = s.vippsAccounts(ctx, clubID); err != nil {
		return nil, nil, err
	}
	res.ClearingAccount = plan.clearing
	if plan.accounts, err = s.activeAccountCodes(ctx, clubID); err != nil {
		return nil, nil, err
	}

	charges, err := s.loadFallbackCharges(ctx, clubID, periodID)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range charges {
		item, dec, err := s.classifyFallbackCharge(ctx, clubID, plan.cfg, plan.clearing, plan.accounts, c)
		if err != nil {
			// A cancelled request ends the plan; any other failure leaves
			// this one charge undecided (never guessed) so a retry sees it.
			if ctx.Err() != nil {
				return nil, nil, err
			}
			item = c.item()
			item.ReasonCode, item.Reason = reasonCodeDecisionFailed, err.Error()
			dec = nil
		}
		plan.items = append(plan.items, item)
		plan.charges[c.entryID] = c
		plan.decisions[c.entryID] = dec
	}

	if err := s.db.QueryRow(ctx,
		`SELECT count(DISTINCT je.id), COALESCE(sum(jl.credit), 0)
		   FROM journal_entries je
		   JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		   JOIN vipps_import_rows r ON r.club_id = je.club_id AND r.journal_entry_id = je.id
		                           AND r.id::text = je.source_id AND r.row_type = 'belastning'
		   JOIN journal_lines jl ON jl.journal_entry_id = je.id AND jl.credit > 0
		   JOIN accounts a ON a.id = jl.account_id AND a.club_id = je.club_id AND a.code = $2
		  WHERE je.club_id = $1 AND je.source = 'vipps' AND je.status = 'posted'
		    AND je.source_table = 'vipps_import_rows' AND fp.status <> 'open'
		    AND ($3 = '' OR fp.id::text = $3)`,
		clubID, fallbackRevenueCode, periodID,
	).Scan(&res.ClosedPeriodCharges, &res.ClosedPeriodAmount); err != nil {
		return nil, nil, fmt.Errorf("counting charges in closed periods: %w", err)
	}
	res.ClosedPeriodAmount = roundCents(res.ClosedPeriodAmount)
	return res, plan, nil
}

func (s *Service) activeAccountCodes(ctx context.Context, clubID string) (map[string]bool, error) {
	rows, err := s.db.Query(ctx, `SELECT code FROM accounts WHERE club_id = $1 AND is_active`, clubID)
	if err != nil {
		return nil, fmt.Errorf("loading accounts: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("scanning account: %w", err)
		}
		out[code] = true
	}
	return out, rows.Err()
}

// loadFallbackCharges returns the posted Vipps charge entries of the open
// periods (or one period) with a credit line on the fallback account, each
// with its charge row and lines, oldest first.
func (s *Service) loadFallbackCharges(ctx context.Context, clubID, periodID string) ([]*fallbackCharge, error) {
	rows, err := s.db.Query(ctx,
		`SELECT je.id, je.entry_number, je.entry_date, je.fiscal_period_id::text, je.description,
		        r.id, r.amount, COALESCE(r.booking_date, r.tx_at::date),
		        COALESCE(r.customer_name, ''), COALESCE(r.message, ''), r.invoice_id::text,
		        EXISTS (SELECT 1 FROM orders o WHERE o.club_id = r.club_id AND r.order_id <> '' AND o.vipps_reference = r.order_id),
		        EXISTS (SELECT 1 FROM bank_import_rows b WHERE b.club_id = je.club_id AND b.journal_entry_id = je.id)
		   FROM journal_entries je
		   JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		   JOIN vipps_import_rows r ON r.club_id = je.club_id AND r.journal_entry_id = je.id
		                           AND r.id::text = je.source_id AND r.row_type = 'belastning'
		  WHERE je.club_id = $1 AND je.source = 'vipps' AND je.status = 'posted'
		    AND je.source_table = 'vipps_import_rows' AND fp.status = 'open'
		    AND ($3 = '' OR fp.id::text = $3)
		    AND EXISTS (SELECT 1 FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		                 WHERE jl.journal_entry_id = je.id AND a.club_id = je.club_id
		                   AND a.code = $2 AND jl.credit > 0)
		  ORDER BY je.entry_date, je.entry_number`,
		clubID, fallbackRevenueCode, periodID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading charges on %s: %w", fallbackRevenueCode, err)
	}
	var out []*fallbackCharge
	for rows.Next() {
		var c fallbackCharge
		if err := rows.Scan(&c.entryID, &c.entryNumber, &c.entryDate, &c.periodID, &c.description,
			&c.rowID, &c.amount, &c.paidOn, &c.customer, &c.message, &c.invoiceID, &c.isOrder, &c.bankLinked); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning charge on %s: %w", fallbackRevenueCode, err)
		}
		out = append(out, &c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, c := range out {
		if c.lines, err = entryLineTotals(ctx, s.db, c.entryID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// entryLineTotals returns an entry's lines as account code and amounts.
func entryLineTotals(ctx context.Context, q dbtx, entryID string) ([]AccountTotal, error) {
	rows, err := q.Query(ctx,
		`SELECT a.code, jl.debit, jl.credit
		   FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1
		  ORDER BY a.code`, entryID)
	if err != nil {
		return nil, fmt.Errorf("loading entry lines: %w", err)
	}
	defer rows.Close()
	var out []AccountTotal
	for rows.Next() {
		var l AccountTotal
		if err := rows.Scan(&l.AccountCode, &l.Debit, &l.Credit); err != nil {
			return nil, fmt.Errorf("scanning entry line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// isSimpleFallbackCharge reports whether lines are exactly DR clearing /
// CR fallback for the charge amount: the only shape that is reclassified.
func isSimpleFallbackCharge(lines []AccountTotal, clearing string, amount float64) bool {
	if len(lines) != 2 || parkedCredit(lines, clearing) != fallbackRevenueCode {
		return false
	}
	for _, l := range lines {
		if toCents(l.Debit)+toCents(l.Credit) != toCents(amount) {
			return false
		}
	}
	return true
}

// item is the charge as a staying item, before it is classified.
func (c *fallbackCharge) item() VippsFallbackReclassItem {
	return VippsFallbackReclassItem{
		EntryID:     c.entryID,
		RowID:       c.rowID,
		EntryNumber: c.entryNumber,
		Date:        c.entryDate.Format("2006-01-02"),
		Amount:      c.amount,
		From:        fallbackRevenueCode,
		Status:      FallbackReclassStays,
	}
}

// classifyFallbackCharge decides what happens to one charge. It reads on the
// pool and takes no lock, so it is also the cheap re-check an apply runs
// right before the charge's transaction. The decision is returned for a
// proposed reclassification.
func (s *Service) classifyFallbackCharge(ctx context.Context, clubID string, cfg vippsAutoAssignConfig, clearing string, accounts map[string]bool, c *fallbackCharge) (VippsFallbackReclassItem, *vippsChargeDecision, error) {
	item := c.item()
	stay := func(code, reason string) (VippsFallbackReclassItem, *vippsChargeDecision, error) {
		item.ReasonCode, item.Reason = code, reason
		return item, nil, nil
	}
	switch {
	case c.bankLinked:
		return stay(reasonCodeBankLinked, "booked together with a bank row (a legacy settlement entry); undo it from the bank row")
	case c.invoiceID != nil:
		return stay(reasonCodeHasFaktura, "the charge is recorded against a faktura")
	case c.isOrder:
		return stay(reasonCodeOrderCharge, "the charge belongs to a checkout order")
	case !isSimpleFallbackCharge(c.lines, clearing, c.amount):
		return stay(reasonCodeShape, "the bilag is not a single debit to clearing and a single credit to "+fallbackRevenueCode)
	case !cfg.enabled:
		return stay(reasonCodeAutoAssignOff, "auto-assign is off for the club")
	}

	dec, err := s.decideVippsCharge(ctx, clubID, cfg, c.customer, c.message, c.amount, c.paidOn)
	if err != nil {
		return item, nil, err
	}
	switch dec.Rule {
	case autoAssignRuleMemberInvoice:
		item.Status = FallbackReclassNeedsTildel
		item.InvoiceNumber = dec.InvoiceNumber
		item.ReasonCode = reasonCodeNeedsTildel
		item.Reason = fmt.Sprintf("the payer's open faktura %d matches; undo this bilag and assign the charge to it in Tildel", dec.InvoiceNumber)
		return item, nil, nil
	case autoAssignRulePriceItem:
		// proposed below
	default:
		code := dec.Code
		if code == "" {
			code = decideCodeNoMatch
		}
		return stay(code, dec.Reason)
	}
	if dec.Account == fallbackRevenueCode {
		return stay(reasonCodeSameAccount, "the price item's category books to "+fallbackRevenueCode+" as well")
	}
	if !accounts[dec.Account] {
		return stay(reasonCodeNoTargetAcct, "account "+dec.Account+" is missing or inactive")
	}
	// Revenue money that a faktura already counts (a faktura marked paid for
	// this amount that no booking explains) would be buried, not corrected,
	// by a reclass: leave it for a person.
	settled, err := s.receivableChargeSettledFaktura(ctx, clubID, c.customer, c.message, c.amount, c.paidOn)
	if err != nil {
		return item, nil, err
	}
	if settled != nil {
		item.InvoiceNumber = settled.number
		return stay(reasonCodeMaybePaid, fmt.Sprintf("faktura %d is marked paid by a %.2f kr payment that no other booking explains; it may be this charge", settled.number, settled.paid))
	}
	item.Status = FallbackReclassReclassify
	item.To = dec.Account
	item.PriceItem = dec.Label
	return item, dec, nil
}

// applyFallbackReclassOne re-decides one proposed charge and, when the
// proposal still stands, moves it in its own transaction. Whatever happens is
// recorded on the item; one charge failing never stops the batch.
func (s *Service) applyFallbackReclassOne(ctx context.Context, clubID, actor string, plan *fallbackPlan, it *VippsFallbackReclassItem) {
	c := plan.charges[it.EntryID]
	// The plan is seconds old: decide again, so a faktura issued or a rule
	// changed since is seen. This runs before the transaction because the
	// decision reads on the pool, and a transaction holding row locks must
	// not take a second pool connection.
	now, _, err := s.classifyFallbackCharge(ctx, clubID, plan.cfg, plan.clearing, plan.accounts, c)
	if err != nil {
		it.fail(err)
		return
	}
	if now.Status != FallbackReclassReclassify || now.To != it.To {
		it.skip(reasonCodeTargetChanged, "the proposal changed since the preview")
		return
	}
	out, err := s.reclassifyFallbackCharge(ctx, clubID, actor, plan.clearing, c, it.To, it.PriceItem)
	switch {
	case errors.Is(err, errFallbackMoved):
		it.skip(reasonCodeAlreadyMoved, "the charge was already moved or its bilag changed")
	case errors.Is(err, ErrPeriodClosed):
		it.fail(fmt.Errorf("the fiscal period is not open: %w", err))
		it.ReasonCode = reasonCodePeriodNotOpen
	case err != nil:
		it.fail(err)
	default:
		it.Outcome = FallbackOutcomeReclassified
		it.NewEntryNumber = out.newNumber
		it.ReversalEntryNumber = out.reversalNumber
	}
}

func (it *VippsFallbackReclassItem) fail(err error) {
	it.Outcome = FallbackOutcomeFailed
	it.ReasonCode = reasonCodeReclassifyFail
	it.Reason = err.Error()
}

type fallbackReclassed struct {
	newNumber      int
	reversalNumber int
}

// reclassifyFallbackCharge moves one charge's revenue off 3900 by the
// sanctioned rebooking (rebookVippsChargeTx), in one transaction. The charge
// row is locked first, so a charge another run or an unassign already moved
// reads as errFallbackMoved instead of failing inside the helper; the helper
// then takes the settlement's payout rows and the entry, in that order. No
// faktura, order or attempt lock is taken: a reclass settles nothing, and a
// charge tied to a faktura or an order is refused here. The helper voids the
// entry (a closed or locked period refuses it under the period lock) and
// posts the replacement on the entry's date, and refuses a replacement whose
// clearing debit differs from the entry's, so the settlement's fee and
// payout entries are never touched. The entry's shape is read here, without
// locking it: a posted entry's lines are fixed, and locking the entry before
// the payout rows would invert the order the helper keeps.
func (s *Service) reclassifyFallbackCharge(ctx context.Context, clubID, actor, clearing string, c *fallbackCharge, to, label string) (*fallbackReclassed, error) { //nolint:gocognit,cyclop // lock, guard, rebook and relink in one transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		curEntry  *string
		invoiceID *string
		dismissed bool
		isOrder   bool
		amount    float64
	)
	err = tx.QueryRow(ctx,
		`SELECT r.journal_entry_id::text, r.invoice_id::text, r.dismissed_at IS NOT NULL, r.amount,
		        EXISTS (SELECT 1 FROM orders o WHERE o.club_id = r.club_id AND r.order_id <> '' AND o.vipps_reference = r.order_id)
		   FROM vipps_import_rows r
		  WHERE r.id = $1 AND r.club_id = $2 AND r.row_type = 'belastning'
		    FOR UPDATE OF r`,
		c.rowID, clubID,
	).Scan(&curEntry, &invoiceID, &dismissed, &amount, &isOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errFallbackMoved
	}
	if err != nil {
		return nil, fmt.Errorf("locking charge: %w", err)
	}
	if curEntry == nil || *curEntry != c.entryID {
		return nil, errFallbackMoved
	}
	if invoiceID != nil || isOrder || dismissed {
		return nil, errors.New("the charge is now tied to a faktura or a checkout order")
	}

	var (
		status, source, sourceTable, sourceID, periodStatus string
		entryNumber                                         int
		description                                         string
		bankLinked                                          bool
	)
	err = tx.QueryRow(ctx,
		`SELECT je.status::text, je.source::text, COALESCE(je.source_table, ''), COALESCE(je.source_id, ''),
		        fp.status::text, je.entry_number, je.description,
		        EXISTS (SELECT 1 FROM bank_import_rows b WHERE b.club_id = je.club_id AND b.journal_entry_id = je.id)
		   FROM journal_entries je JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		  WHERE je.id = $1 AND je.club_id = $2`,
		c.entryID, clubID,
	).Scan(&status, &source, &sourceTable, &sourceID, &periodStatus, &entryNumber, &description, &bankLinked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errFallbackMoved
	}
	if err != nil {
		return nil, fmt.Errorf("reading bilag: %w", err)
	}
	if status != "posted" {
		return nil, errFallbackMoved
	}
	if source != "vipps" || sourceTable != "vipps_import_rows" || sourceID != c.rowID || bankLinked {
		return nil, errors.New("the bilag is not this charge's own booking")
	}
	if periodStatus != "open" {
		return nil, ErrPeriodClosed
	}
	lines, err := entryLineTotals(ctx, tx, c.entryID)
	if err != nil {
		return nil, err
	}
	if !isSimpleFallbackCharge(lines, clearing, amount) {
		return nil, errFallbackMoved
	}

	desc := fmt.Sprintf("Reklassifisert fra %s (bilag #%d): %s — %s", fallbackRevenueCode, entryNumber, description, label)
	rb, err := rebookVippsChargeTx(ctx, tx, clubID, actor, c.rowID,
		[]CreateJournalLineInput{{AccountCode: to, Credit: amount, Description: desc}}, "")
	if err != nil {
		return nil, err
	}
	// The helper unlinks the rule with the old booking; the charge is now
	// booked by a price-item rule.
	tag, err := tx.Exec(ctx,
		`UPDATE vipps_import_rows SET auto_assign_rule = $3
		  WHERE id = $1 AND club_id = $2 AND journal_entry_id = $4`,
		c.rowID, clubID, autoAssignRulePriceItem, rb.EntryID)
	if err != nil {
		return nil, fmt.Errorf("recording the rule: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return nil, errFallbackMoved
	}

	var out fallbackReclassed
	if err := tx.QueryRow(ctx,
		`SELECT entry_number FROM journal_entries WHERE id = $1 AND club_id = $2`, rb.EntryID, clubID,
	).Scan(&out.newNumber); err != nil {
		return nil, fmt.Errorf("reading the replacement bilag: %w", err)
	}
	// The reversal is the void's own entry, named after the original.
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(max(entry_number), 0) FROM journal_entries
		  WHERE club_id = $1 AND fiscal_period_id = (SELECT fiscal_period_id FROM journal_entries WHERE id = $2)
		    AND description LIKE $3`,
		clubID, rb.EntryID, fmt.Sprintf("Reversering av bilag #%d: %%", entryNumber),
	).Scan(&out.reversalNumber); err != nil {
		return nil, fmt.Errorf("reading the reversal bilag: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &out, nil
}
