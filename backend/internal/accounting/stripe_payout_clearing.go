package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Stripe payout matching and clearing. Stripe's charges and fees are booked
// to the clearing account (1580) one by one (stripe_postings.go); a payout
// moves what they left there to the bank. Its bank deposit is booked
//
//	DR bank (the import's account) / CR stripe clearing, the payout amount
//
// once every balance transaction the payout paid out is booked or
// dismissed, so the clearing account nets to zero over the payout.
//
// A payout is matched to a bank row by evidence only: the row is incoming,
// unbooked, undismissed, not refund-paired, not provisional, carries the
// club's payout descriptor (providerPayouts), equals the payout to the øre
// and is dated from the day before Stripe's arrival date to three days
// after it. Only a one-to-one match clears: a payout two rows could be, or
// a row two payouts could be, is left to a person.

// Payout date window around Stripe's arrival date. Stripe dates the
// arrival in UTC and the bank books on its own banking day, so a deposit
// may show a day early or, over a weekend or holiday, a few days late.
const (
	stripePayoutDaysBefore = 1
	stripePayoutDaysAfter  = 3
)

// Reasons the sweep gives (Norwegian, shown as is).
const (
	stripeReasonManual         = "manuell utbetaling – avstem manuelt"
	stripeReasonManyPayouts    = "flere Stripe-utbetalinger passer – avstem manuelt"
	stripeReasonManyRows       = "flere bankrader passer – avstem manuelt"
	stripeReasonNotListed      = "Stripe har ikke listet innholdet i utbetalingen ennå – kjør Synk Stripe"
	stripeReasonNoPayout       = "ingen Stripe-utbetaling med dette beløpet rundt denne datoen – kjør Synk Stripe"
	stripeReasonReady          = "klar – avstemmes ved neste synk"
	stripeReasonPostFailed     = "kunne ikke bokføres – se loggen"
	stripePayoutSweepFailedTxt = "Automatisk avstemming av Stripe-utbetalinger feilet – prøv igjen, eller se loggen"
)

// StripePayoutSweepItem is one payout (or one Stripe deposit with no
// payout) a sweep looked at.
type StripePayoutSweepItem struct {
	// PayoutID is Stripe's payout id; empty for a deposit no payout matches.
	PayoutID string `json:"payout_id,omitempty"`
	// Amount is the payout's (or deposit's) amount in kroner.
	Amount      float64 `json:"amount"`
	ArrivalDate string  `json:"arrival_date,omitempty"`
	BankRowID   string  `json:"bank_row_id,omitempty"`
	BankDate    string  `json:"bank_date,omitempty"`
	EntryID     string  `json:"journal_entry_id,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

// StripePayoutSweepResult summarises one ClearStripePayouts run, like
// VippsPayoutSweepResult.
type StripePayoutSweepResult struct {
	// Cleared payouts had their bank entry posted this run.
	Cleared []StripePayoutSweepItem `json:"cleared"`
	// Waiting payouts clear on a later run once what blocks them is done:
	// members to book, a deposit not imported yet, a period to open.
	Waiting []StripePayoutSweepItem `json:"waiting"`
	// Skipped payouts and deposits need a person: a manual payout, an
	// ambiguous match, a deposit no payout matches, members that do not add
	// up to the payout.
	Skipped []StripePayoutSweepItem `json:"skipped"`
	// Failed payouts were ready but posting failed; the next run retries.
	Failed []StripePayoutSweepItem `json:"failed"`
	// Error is set when the sweep itself could not run.
	Error string `json:"error,omitempty"`

	considered int
}

func newStripePayoutSweepResult() *StripePayoutSweepResult {
	return &StripePayoutSweepResult{
		Cleared: []StripePayoutSweepItem{},
		Waiting: []StripePayoutSweepItem{},
		Skipped: []StripePayoutSweepItem{},
		Failed:  []StripePayoutSweepItem{},
	}
}

// ClearStripePayouts clears every Stripe payout that is ready, as actorID.
// It waits while another Stripe sync or booking of the club runs; the
// request paths run it through BookStripeBalanceReported and
// TrySyncAndBookStripeBalance, after the booking pass and in the same hold
// of the club's slot.
func (s *Service) ClearStripePayouts(ctx context.Context, clubID, actorID string) (*StripePayoutSweepResult, error) {
	if actorID == "" {
		return newStripePayoutSweepResult(), errors.New("clearing stripe payouts needs an acting user")
	}
	release, err := s.stripeSync.acquire(ctx, clubID)
	if err != nil {
		return newStripePayoutSweepResult(), err
	}
	defer release()
	return s.clearStripePayouts(ctx, clubID, actorID)
}

// clearStripePayoutsReported runs the sweep for a caller that already holds
// the club's slot. A failure is logged and set as Error; nil means there
// was nothing to look at.
func (s *Service) clearStripePayoutsReported(ctx context.Context, clubID, actorID string) *StripePayoutSweepResult {
	res, err := s.clearStripePayouts(ctx, clubID, actorID)
	if err != nil {
		s.log.Error().Err(err).Str("club_id", clubID).Msg("stripe payout sweep failed")
		res.Error = stripePayoutSweepFailedTxt
		return res
	}
	if res.considered == 0 {
		return nil
	}
	return res
}

func (s *Service) clearStripePayouts(ctx context.Context, clubID, actorID string) (*StripePayoutSweepResult, error) {
	res := newStripePayoutSweepResult()
	payouts, err := loadProviderPayouts(ctx, s.db, clubID)
	if err != nil {
		return res, err
	}
	if payouts.stripe == nil {
		return res, nil
	}
	plan, err := s.planStripePayouts(ctx, clubID, payouts)
	if err != nil {
		return res, err
	}
	res.considered = len(plan.payouts) + len(plan.orphans)
	for i := range plan.payouts {
		pm := &plan.payouts[i]
		item := pm.item()
		switch pm.state {
		case stripePlanWaiting:
			res.Waiting = append(res.Waiting, item)
			continue
		case stripePlanSkipped:
			res.Skipped = append(res.Skipped, item)
			continue
		}
		out, err := s.clearStripePayout(ctx, clubID, actorID, pm.payout.id, pm.row.id, automaticStripeClear)
		switch {
		case err != nil:
			s.log.Warn().Err(err).Str("payout_id", pm.payout.payoutID).Str("bank_row_id", pm.row.id).
				Msg("stripe payout sweep: clearing failed")
			item.Reason = stripeReasonPostFailed
			res.Failed = append(res.Failed, item)
		case out.state == stripePlanReady:
			item.EntryID, item.Reason = out.entryID, ""
			res.Cleared = append(res.Cleared, item)
		case out.state == stripePlanWaiting:
			item.Reason = out.reason
			res.Waiting = append(res.Waiting, item)
		case out.state == stripePlanSkipped:
			item.Reason = out.reason
			res.Skipped = append(res.Skipped, item)
		default:
			// Another clearing (or a person) took the row or the payout
			// first; nothing new was posted, and the next sweep reports
			// whatever is left.
		}
	}
	for _, r := range plan.orphans {
		res.Skipped = append(res.Skipped, StripePayoutSweepItem{
			Amount: oreToKr(r.cents), BankRowID: r.id, BankDate: r.date.Format("2006-01-02"), Reason: stripeReasonNoPayout,
		})
	}
	return res, nil
}

// stripeRowStatus is what Tildel shows on a Stripe deposit.
type stripeRowStatus struct {
	reason string
	// byHand is true when no sweep will clear the row (a manual payout, an
	// ambiguous match, members that do not add up, no payout at all): a
	// person clears it (ClearStripePayoutManually) or dismisses it.
	byHand bool
}

// stripePayoutRowReasons is, per unbooked Stripe deposit, what the next
// sweep would say about it; Tildel shows it on the row. Read only.
func (s *Service) stripePayoutRowReasons(ctx context.Context, clubID string, payouts providerPayouts) (map[string]stripeRowStatus, error) {
	out := map[string]stripeRowStatus{}
	if payouts.stripe == nil {
		return out, nil
	}
	plan, err := s.planStripePayouts(ctx, clubID, payouts)
	if err != nil {
		return nil, err
	}
	for _, pm := range plan.payouts {
		st := stripeRowStatus{reason: pm.reason, byHand: pm.state == stripePlanSkipped}
		if pm.state == stripePlanReady {
			st.reason = stripeReasonReady
		}
		for _, id := range pm.rowIDs {
			out[id] = st
		}
	}
	for _, r := range plan.orphans {
		out[r.id] = stripeRowStatus{reason: stripeReasonNoPayout, byHand: true}
	}
	return out, nil
}

// States of a planned (or attempted) payout.
const (
	stripePlanReady = iota + 1
	stripePlanWaiting
	stripePlanSkipped
	stripePlanRaced
)

// stripePayoutCand is a paid, uncleared NOK payout.
type stripePayoutCand struct {
	id, payoutID string
	amountOre    int64
	arrival      time.Time
	automatic    bool
	listed       bool
}

// stripeDeposit is an unbooked bank row carrying the Stripe descriptor.
type stripeDeposit struct {
	id    string
	date  time.Time
	cents int64
}

// stripePayoutMatch is one payout with what the plan decided for it.
type stripePayoutMatch struct {
	payout stripePayoutCand
	// row is the one deposit it matches (nil for none or several).
	row *stripeDeposit
	// rowIDs are every deposit the reason concerns, for Tildel.
	rowIDs []string
	state  int
	reason string
}

func (m *stripePayoutMatch) item() StripePayoutSweepItem {
	it := StripePayoutSweepItem{
		PayoutID:    m.payout.payoutID,
		Amount:      oreToKr(m.payout.amountOre),
		ArrivalDate: m.payout.arrival.Format("2006-01-02"),
		Reason:      m.reason,
	}
	if m.row != nil {
		it.BankRowID = m.row.id
		it.BankDate = m.row.date.Format("2006-01-02")
	}
	return it
}

type stripePayoutPlan struct {
	payouts []stripePayoutMatch
	// orphans are Stripe deposits no candidate payout matches.
	orphans []stripeDeposit
}

// planStripePayouts matches the club's uncleared payouts to its Stripe
// deposits and decides, without writing, which are ready to clear.
func (s *Service) planStripePayouts(ctx context.Context, clubID string, payouts providerPayouts) (*stripePayoutPlan, error) {
	cands, err := loadStripePayoutCands(ctx, s.db, clubID)
	if err != nil {
		return nil, err
	}
	deposits, err := loadStripeDeposits(ctx, s.db, clubID, payouts)
	if err != nil {
		return nil, err
	}
	plan := matchStripePayouts(cands, deposits)
	for i := range plan.payouts {
		pm := &plan.payouts[i]
		if pm.state != stripePlanReady {
			continue
		}
		v, err := stripePayoutReadiness(ctx, s.db, clubID, pm.payout, pm.row.date, false, automaticStripeClear)
		if err != nil {
			return nil, err
		}
		pm.state, pm.reason = v.state, v.reason
	}
	return plan, nil
}

// matchStripePayouts pairs payouts and deposits by amount and date window.
// Manual payouts take part, so a deposit that could be either kind is
// ambiguous rather than cleared against the automatic one.
func matchStripePayouts(cands []stripePayoutCand, deposits []stripeDeposit) *stripePayoutPlan {
	rowsOf := make([][]int, len(cands))
	payoutsOf := make([][]int, len(deposits))
	for pi, p := range cands {
		for ri, r := range deposits {
			if r.cents == p.amountOre && inStripePayoutWindow(r.date, p.arrival) {
				rowsOf[pi] = append(rowsOf[pi], ri)
				payoutsOf[ri] = append(payoutsOf[ri], pi)
			}
		}
	}

	plan := &stripePayoutPlan{}
	for pi, p := range cands {
		pm := stripePayoutMatch{payout: p}
		for _, ri := range rowsOf[pi] {
			pm.rowIDs = append(pm.rowIDs, deposits[ri].id)
		}
		switch {
		case len(rowsOf[pi]) > 1:
			pm.state, pm.reason = stripePlanSkipped, stripeReasonManyRows
		case len(rowsOf[pi]) == 1 && len(payoutsOf[rowsOf[pi][0]]) > 1:
			pm.state, pm.reason = stripePlanSkipped, stripeReasonManyPayouts
		case len(rowsOf[pi]) == 1:
			pm.row = &deposits[rowsOf[pi][0]]
			pm.state = stripePlanReady
		default:
			pm.state, pm.reason = stripePlanWaiting, noStripeDepositReason(p)
		}
		// A manual payout is never cleared here: Stripe cannot list what it
		// paid out, so nothing proves the clearing account holds it.
		if !p.automatic && pm.state != stripePlanSkipped {
			pm.state, pm.reason = stripePlanSkipped, stripeReasonManual
		}
		plan.payouts = append(plan.payouts, pm)
	}
	for ri, r := range deposits {
		if len(payoutsOf[ri]) == 0 {
			plan.orphans = append(plan.orphans, r)
		}
	}
	return plan
}

// inStripePayoutWindow reports whether a bank date is within the window
// around a payout's arrival date.
func inStripePayoutWindow(bankDate, arrival time.Time) bool {
	d, a := dayOf(bankDate), dayOf(arrival)
	return !d.Before(a.AddDate(0, 0, -stripePayoutDaysBefore)) && !d.After(a.AddDate(0, 0, stripePayoutDaysAfter))
}

func noStripeDepositReason(p stripePayoutCand) string {
	from := p.arrival.AddDate(0, 0, -stripePayoutDaysBefore).Format("02.01.2006")
	to := p.arrival.AddDate(0, 0, stripePayoutDaysAfter).Format("02.01.2006")
	return fmt.Sprintf("venter på bankinnbetalingen: %s med Stripe-teksten, datert %s–%s", formatOre(p.amountOre), from, to)
}

// formatOre renders øre as Norwegian kroner, e.g. "1234,50 kr".
func formatOre(ore int64) string {
	sign := ""
	if ore < 0 {
		sign, ore = "-", -ore
	}
	return fmt.Sprintf("%s%d,%02d kr", sign, ore/100, ore%100)
}

// loadStripePayoutCands lists the club's paid NOK payouts not yet cleared.
// A failed or canceled payout paid nothing and is ignored, and a pending or
// in-transit one has not arrived.
func loadStripePayoutCands(ctx context.Context, q dbtx, clubID string) ([]stripePayoutCand, error) {
	rows, err := q.Query(ctx,
		`SELECT id, payout_id, amount_ore, arrival_date, automatic, members_listed_at IS NOT NULL
		   FROM stripe_payouts
		  WHERE club_id = $1 AND status = 'paid' AND currency = 'nok' AND amount_ore > 0
		    AND journal_entry_id IS NULL AND bank_row_id IS NULL
		  ORDER BY arrival_date, payout_id`,
		clubID)
	if err != nil {
		return nil, fmt.Errorf("listing stripe payouts: %w", err)
	}
	defer rows.Close()
	var out []stripePayoutCand
	for rows.Next() {
		var p stripePayoutCand
		if err := rows.Scan(&p.id, &p.payoutID, &p.amountOre, &p.arrival, &p.automatic, &p.listed); err != nil {
			return nil, fmt.Errorf("scanning stripe payout: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// stripeDepositEligibleSQL is the SQL half of the deposit predicate, over
// the bank row alias bir; the descriptor is applied in Go.
func stripeDepositEligibleSQL() string {
	return `bir.amount > 0
	    AND bir.journal_entry_id IS NULL
	    AND bir.dismissed_at IS NULL
	    AND bir.refund_paired_with IS NULL
	    AND NOT (` + vippsPayoutRowSQL("bir") + `)
	    AND NOT COALESCE((` + provisionalRowSQL("bir") + `), false)`
}

// loadStripeDeposits lists the club's unbooked incoming rows carrying the
// Stripe descriptor. Every unbooked deposit is read, not only those of a
// payout's amount, so a Stripe deposit no payout matches is reported too:
// it is kept out of every other booking path and would otherwise wait in
// Tildel unexplained.
func loadStripeDeposits(ctx context.Context, q dbtx, clubID string, payouts providerPayouts) ([]stripeDeposit, error) {
	rows, err := q.Query(ctx,
		`SELECT bir.id, bir.row_date, round(bir.amount * 100)::bigint,
		        COALESCE(bir.description, ''), COALESCE(bir.counterpart, '')
		   FROM bank_import_rows bir
		  WHERE bir.club_id = $1 AND `+stripeDepositEligibleSQL()+`
		  ORDER BY bir.row_date, bir.id`,
		clubID)
	if err != nil {
		return nil, fmt.Errorf("listing stripe deposits: %w", err)
	}
	defer rows.Close()
	var out []stripeDeposit
	for rows.Next() {
		var d stripeDeposit
		var desc, counterpart string
		if err := rows.Scan(&d.id, &d.date, &d.cents, &desc, &counterpart); err != nil {
			return nil, fmt.Errorf("scanning stripe deposit: %w", err)
		}
		if payouts.stripeOnly(desc, counterpart, d.cents) {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

// stripeMembers is what blocks a payout's clearing, from its members: the
// balance transactions it paid out, other than its own payout row and
// payout reversals (Stripe moving the payout itself, not money it paid
// out). A member counts as done when it is booked or dismissed.
type stripeMembers struct {
	count          int
	netOre         int64
	chargesOpen    int
	chargesReview  int
	feesOpen       int
	refundsOpen    int
	refundsReview  int
	disputesOpen   int
	disputesReview int
	otherOpen      int
	pendingBlocker bool
}

// loadStripeMembers reads a payout's members. With lock (in the clearing
// transaction) it takes them FOR SHARE in id order, so a booking pass
// marking one waits for, or is waited on by, the clearing.
func loadStripeMembers(ctx context.Context, q dbtx, clubID, payoutRowID string, lock bool) (stripeMembers, error) {
	sql := `SELECT reporting_category, status, net_ore
	          FROM stripe_balance_transactions
	         WHERE club_id = $1 AND payout_id = $2
	           AND reporting_category NOT IN ('payout', 'payout_reversal')
	         ORDER BY id`
	if lock {
		sql += ` FOR SHARE`
	}
	rows, err := q.Query(ctx, sql, clubID, payoutRowID)
	if err != nil {
		return stripeMembers{}, fmt.Errorf("listing payout members: %w", err)
	}
	defer rows.Close()
	var m stripeMembers
	for rows.Next() {
		var category, status string
		var net int64
		if err := rows.Scan(&category, &status, &net); err != nil {
			return stripeMembers{}, fmt.Errorf("scanning payout member: %w", err)
		}
		m.add(category, status, net)
	}
	return m, rows.Err()
}

func (m *stripeMembers) add(category, status string, net int64) {
	m.count++
	m.netOre += net
	if status == StripeTxnBooked || status == StripeTxnDismissed {
		return
	}
	m.pendingBlocker = true
	switch category {
	case "charge":
		if status == StripeTxnNeedsReview {
			m.chargesReview++
		} else {
			m.chargesOpen++
		}
	case "fee":
		m.feesOpen++
	case "refund", "refund_failure":
		if status == StripeTxnNeedsReview {
			m.refundsReview++
		} else {
			m.refundsOpen++
		}
	case "dispute", "dispute_reversal":
		if status == StripeTxnNeedsReview {
			m.disputesReview++
		} else {
			m.disputesOpen++
		}
	default:
		m.otherOpen++
	}
}

// open counts the members not yet booked or dismissed.
func (m *stripeMembers) open() int {
	return m.chargesOpen + m.chargesReview + m.feesOpen + m.refundsOpen + m.refundsReview + m.disputesOpen + m.disputesReview + m.otherOpen
}

// openUnlinkedStripeRowsSQL selects the club's open balance rows linked to
// no payout and available by the arrival date: what could have funded a
// manual payout. club and arrival are SQL expressions.
func openUnlinkedStripeRowsSQL(club, arrival string) string {
	return `SELECT u.id FROM stripe_balance_transactions u
	         WHERE u.club_id = ` + club + ` AND u.payout_id IS NULL
	           AND u.reporting_category NOT IN ('payout', 'payout_reversal')
	           AND COALESCE(u.available_on, u.created_at::date) <= ` + arrival + `
	           AND u.status NOT IN ('booked', 'dismissed')`
}

// countOpenUnlinkedStripeRows counts openUnlinkedStripeRowsSQL for a
// payout arriving on arrival. With lock (in the clearing transaction) the
// rows are taken FOR SHARE in id order, as the members are, so a booking
// pass marking one waits for, or is waited on by, the clearing.
func countOpenUnlinkedStripeRows(ctx context.Context, q dbtx, clubID string, arrival time.Time, lock bool) (int, error) {
	inner := openUnlinkedStripeRowsSQL("$1::uuid", "$2::date") + ` ORDER BY u.id`
	if lock {
		inner += ` FOR SHARE`
	}
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM (`+inner+`) open_rows`,
		clubID, arrival.Format("2006-01-02")).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting open stripe balance rows: %w", err)
	}
	return n, nil
}

// waitingReason lists what still has to be booked, in Norwegian.
func (m *stripeMembers) waitingReason() string {
	var parts []string
	if m.chargesOpen > 0 {
		parts = append(parts, plural(m.chargesOpen, "kortbetaling venter", "kortbetalinger venter")+" på bokføring")
	}
	if m.chargesReview > 0 {
		parts = append(parts, plural(m.chargesReview, "kortbetaling", "kortbetalinger")+" til gjennomgang")
	}
	if m.feesOpen > 0 {
		parts = append(parts, plural(m.feesOpen, "Stripe-gebyr venter", "Stripe-gebyrer venter")+" på bokføring")
	}
	if m.refundsOpen > 0 {
		parts = append(parts, plural(m.refundsOpen, "refusjon venter", "refusjoner venter")+" på bokføring")
	}
	if m.refundsReview > 0 {
		parts = append(parts, plural(m.refundsReview, "refusjon", "refusjoner")+" til gjennomgang")
	}
	if m.disputesOpen > 0 {
		parts = append(parts, plural(m.disputesOpen, "tvist venter", "tvister venter")+" på bokføring")
	}
	if m.disputesReview > 0 {
		parts = append(parts, plural(m.disputesReview, "tvist", "tvister")+" til gjennomgang")
	}
	if m.otherOpen > 0 {
		parts = append(parts, plural(m.otherOpen, "annen Stripe-transaksjon", "andre Stripe-transaksjoner")+" til gjennomgang")
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// stripeClearOpts says which of the clearing's checks apply. The sweep
// (automaticStripeClear) clears only on evidence; a person
// (manualStripeClear) has chosen the payout and the deposit, so what only
// served to find the pair, or that a person is better placed to judge, is
// left out. What keeps the books right stays for both: the deposit is an
// eligible Stripe deposit equal to the payout to the øre, the payout is a
// paid, uncleared NOK payout, every member is booked or dismissed, and the
// bank date's period exists and is open.
type stripeClearOpts struct {
	// requireAutomatic refuses a manual payout and a deposit outside the
	// arrival window. Stripe cannot list what a manual payout paid out, so
	// nothing but a person proves the clearing account holds it.
	requireAutomatic bool
	// requireNetSum wants the members listed and adding up to the payout.
	// A person may clear one that does not (funds of a failed payout
	// carried into a later one, a member kept on an earlier payout); the
	// clearing account remainder is then reported back.
	requireNetSum bool
	// checkDescriptor re-checks, under the lock, that the deposit carries
	// the club's Stripe descriptor. The sweep only ever picks such rows.
	checkDescriptor bool
	// autoMatched is what the deposit's auto_matched is set to.
	autoMatched bool
}

var (
	automaticStripeClear = stripeClearOpts{requireAutomatic: true, requireNetSum: true, autoMatched: true}
	manualStripeClear    = stripeClearOpts{checkDescriptor: true}
)

// Codes of a refused clearing (StripeClearRefusal.Code). The API answers
// them as 409 codes, so they are part of its contract.
const (
	StripeClearNotStripeDeposit   = "not_stripe_deposit"
	StripeClearBankRowNotEligible = "bank_row_not_eligible"
	StripeClearAmountMismatch     = "amount_mismatch"
	StripeClearPayoutNotClearable = "payout_not_clearable"
	StripeClearMembersOpen        = "members_open"
	StripeClearMembersNotListed   = "members_not_listed"
	StripeClearBalanceOpen        = "balance_open"
	StripeClearNoPeriod           = "no_period"
	StripeClearPeriodClosed       = "period_closed"
	// StripeClearReferencePeriod: the deposit's date lies in a reference
	// year (BRY-418). Its booking would put money in transit into a year that
	// takes costs only, so the payout waits for ever and nothing is booked.
	StripeClearReferencePeriod = "reference_period"
)

// Codes only the sweep's checks give, and not-found markers the manual
// path turns into errors.
const (
	stripeClearMembersMismatch = "members_mismatch"
	stripeClearNotMatched      = "not_matched"
	stripeClearRowNotFound     = "bank_row_not_found"
	stripeClearPayoutNotFound  = "payout_not_found"
	stripeClearRaced           = "raced"
)

// stripeVerdict is what stripePayoutReadiness decided.
type stripeVerdict struct {
	state   int
	reason  string
	code    string
	members stripeMembers
	// openBalance counts, for a manual payout, the unlinked balance rows up
	// to its arrival that are still open (balance_open).
	openBalance int
}

// stripePayoutReadiness decides whether a matched payout can clear now: its
// members are listed and add up to it (when opts.requireNetSum), all are
// booked or dismissed, and the bank date's fiscal period exists and is
// open. It never creates a period. lock is passed to loadStripeMembers.
//
// Without members the "all booked" test would hold vacuously, so a payout
// that should have them must have them listed (an automatic one), and a
// manual payout, which Stripe never lists, instead needs every balance row
// that could have funded it (unlinked to a payout, available by its
// arrival) booked or dismissed.
func stripePayoutReadiness(ctx context.Context, q dbtx, clubID string, p stripePayoutCand, bankDate time.Time, lock bool, opts stripeClearOpts) (stripeVerdict, error) { //nolint:cyclop // one refusal per readiness rule, each with its own code
	if (opts.requireNetSum || p.automatic) && !p.listed {
		return stripeVerdict{state: stripePlanWaiting, reason: stripeReasonNotListed, code: StripeClearMembersNotListed}, nil
	}
	m, err := loadStripeMembers(ctx, q, clubID, p.id, lock)
	if err != nil {
		return stripeVerdict{}, err
	}
	if !p.automatic {
		n, err := countOpenUnlinkedStripeRows(ctx, q, clubID, p.arrival, lock)
		if err != nil {
			return stripeVerdict{}, err
		}
		if n > 0 {
			return stripeVerdict{state: stripePlanWaiting, code: StripeClearBalanceOpen, members: m, openBalance: n,
				reason: plural(n, "Stripe-transaksjon", "Stripe-transaksjoner") + " fram til utbetalingsdatoen er ikke bokført eller avvist"}, nil
		}
	}
	// Stripe pays out exactly the net of the transactions it lists for an
	// automatic payout. Anything else means the stored membership is not
	// the payout's (a member kept on an earlier, cleared payout; funds of a
	// failed payout carried into this one), and clearing would leave the
	// difference on the clearing account.
	if opts.requireNetSum && m.netOre != p.amountOre {
		return stripeVerdict{state: stripePlanSkipped, code: stripeClearMembersMismatch, members: m,
			reason: fmt.Sprintf("Stripe-transaksjonene i utbetalingen summerer til %s, ikke %s – avstem manuelt",
				formatOre(m.netOre), formatOre(p.amountOre))}, nil
	}
	if m.pendingBlocker {
		return stripeVerdict{state: stripePlanWaiting, reason: m.waitingReason(), code: StripeClearMembersOpen, members: m}, nil
	}
	periodID, status, err := existingPeriodIn(ctx, q, clubID, bankDate, "")
	if errors.Is(err, errNoFiscalPeriod) {
		return stripeVerdict{state: stripePlanWaiting, reason: "ingen regnskapsperiode for " + bankDate.Format("02.01.2006"),
			code: StripeClearNoPeriod, members: m}, nil
	}
	if err != nil {
		return stripeVerdict{}, fmt.Errorf("resolving fiscal period: %w", err)
	}
	if status != "open" {
		return stripeVerdict{state: stripePlanWaiting, reason: periodClosedReason(bankDate), code: StripeClearPeriodClosed, members: m}, nil
	}
	if ref, err := periodIsReferenceIn(ctx, q, clubID, periodID); err != nil {
		return stripeVerdict{}, err
	} else if ref {
		return stripeVerdict{state: stripePlanWaiting, reason: referencePeriodReason(bankDate), code: StripeClearReferencePeriod, members: m}, nil
	}
	return stripeVerdict{state: stripePlanReady, members: m}, nil
}

// referencePeriodReason is the Norwegian reason a deposit in a reference year
// is left alone.
func referencePeriodReason(day time.Time) string {
	return "bankdatoen " + day.Format("02.01.2006") + " ligger i et referanseår"
}

func periodClosedReason(day time.Time) string {
	return "regnskapsperioden for " + day.Format("02.01.2006") + " er lukket"
}

// stripeClearOutcome is what one clearing attempt did.
type stripeClearOutcome struct {
	state   int
	reason  string
	code    string
	entryID string
	// payoutID is Stripe's payout id, once the payout was locked.
	payoutID string
	// members are the payout's members as the clearing read them.
	members     stripeMembers
	openBalance int
	// clearingBalanceOre is the clearing account remainder over the
	// members and the new entry, nil when it could not be read.
	clearingBalanceOre *int64
}

func racedOutcome(code string) stripeClearOutcome {
	return stripeClearOutcome{state: stripePlanRaced, code: code}
}

// clearStripePayout books one payout against one deposit in a transaction
// that locks the bank row, then the payout (the order every clearing
// takes, so two sweeps queue instead of deadlocking), then its members FOR
// SHARE, and re-checks the pair and readiness under those locks. The entry
// is created, the bank row and the payout linked with guarded updates, the
// payout's own balance row marked booked with it, and the entry posted,
// all on the one transaction; a lost guard or a failed post rolls it all
// back. No statement in the transaction uses the pool.
func (s *Service) clearStripePayout(ctx context.Context, clubID, actorID, payoutRowID, bankRowID string, opts stripeClearOpts) (stripeClearOutcome, error) { //nolint:cyclop // lock, re-check and post one payout in a single transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return stripeClearOutcome{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, pair, err := lockStripeClearPairTx(ctx, tx, clubID, payoutRowID, bankRowID, opts)
	if err != nil || pair.state != 0 {
		return pair.stripeClearOutcome, err
	}
	p := pair.payout

	v, err := stripePayoutReadiness(ctx, tx, clubID, p, row.date, true, opts)
	if err != nil || v.state != stripePlanReady {
		return stripeClearOutcome{state: v.state, reason: v.reason, code: v.code, payoutID: p.payoutID, members: v.members,
			openBalance: v.openBalance}, err
	}
	periodID, _, err := existingPeriodIn(ctx, tx, clubID, row.date, "")
	if err != nil {
		return stripeClearOutcome{}, fmt.Errorf("resolving fiscal period: %w", err)
	}
	clearing, _, err := stripeAccountsIn(ctx, tx, clubID)
	if err != nil {
		return stripeClearOutcome{}, err
	}

	closed := stripeClearOutcome{state: stripePlanWaiting, reason: periodClosedReason(row.date), code: StripeClearPeriodClosed,
		payoutID: p.payoutID, members: v.members}
	referenceOutcome := stripeClearOutcome{state: stripePlanWaiting, reason: referencePeriodReason(row.date), code: StripeClearReferencePeriod,
		payoutID: p.payoutID, members: v.members}
	amount := oreToKr(p.amountOre)
	desc := "Stripe-utbetaling (ankomst " + p.arrival.Format("02.01.2006") + ")"
	sourceTable := "bank_import_rows"
	entry, err := createJournalEntryTx(ctx, tx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      row.date.Format("2006-01-02"),
		Description:    desc,
		Source:         "stripe",
		SourceID:       &bankRowID,
		SourceTable:    &sourceTable,
		CreatedBy:      actorID,
		ClubID:         clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: row.bankAccount, Debit: amount, Description: desc},
			{AccountCode: clearing, Credit: amount, Description: desc},
		},
	})
	if errors.Is(err, ErrPeriodClosed) {
		return closed, nil
	}
	if errors.Is(err, ErrReferencePeriod) {
		return referenceOutcome, nil
	}
	if err != nil {
		return stripeClearOutcome{}, fmt.Errorf("creating journal entry: %w", err)
	}
	if lost, err := linkStripePayoutTx(ctx, tx, clubID, entry.ID, bankRowID, payoutRowID, opts.autoMatched); err != nil || lost {
		return racedOutcome(stripeClearRaced), err
	}
	if err := postJournalEntryTx(ctx, tx, clubID, entry.ID, actorID); errors.Is(err, ErrPeriodClosed) {
		return closed, nil
	} else if errors.Is(err, ErrReferencePeriod) {
		return referenceOutcome, nil
	} else if err != nil {
		return stripeClearOutcome{}, fmt.Errorf("posting journal entry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return stripeClearOutcome{}, fmt.Errorf("commit: %w", err)
	}

	return stripeClearOutcome{
		state: stripePlanReady, entryID: entry.ID, payoutID: p.payoutID, members: v.members,
		clearingBalanceOre: s.checkStripeClearingBalance(ctx, clubID, payoutRowID, entry.ID, p.payoutID),
	}, nil
}

// lockedStripePair is the outcome of lockStripeClearPairTx: state 0 with the
// payout when the pair may clear, else the outcome to return.
type lockedStripePair struct {
	stripeClearOutcome
	payout stripePayoutCand
}

// lockStripeClearPairTx locks the bank row, then the payout, and checks the
// pair as opts asks.
func lockStripeClearPairTx(ctx context.Context, tx pgx.Tx, clubID, payoutRowID, bankRowID string, opts stripeClearOpts) (lockedStripeDeposit, lockedStripePair, error) {
	row, found, err := lockStripeDepositTx(ctx, tx, clubID, bankRowID)
	if err != nil || !found {
		return row, lockedStripePair{stripeClearOutcome: racedOutcome(stripeClearRowNotFound)}, err
	}
	p, found, open, err := lockStripePayoutTx(ctx, tx, clubID, payoutRowID)
	if err != nil || !found {
		return row, lockedStripePair{stripeClearOutcome: racedOutcome(stripeClearPayoutNotFound)}, err
	}
	// The payout first, so clearing a cleared payout again says so rather
	// than that its (now booked) deposit is not eligible.
	if !open {
		return row, lockedStripePair{stripeClearOutcome: racedOutcome(StripeClearPayoutNotClearable)}, nil
	}
	if opts.checkDescriptor {
		payouts, err := loadProviderPayouts(ctx, tx, clubID)
		if err != nil {
			return row, lockedStripePair{}, err
		}
		if !payouts.stripeOnly(row.description, row.counterpart, row.cents) {
			return row, lockedStripePair{stripeClearOutcome: racedOutcome(StripeClearNotStripeDeposit)}, nil
		}
	}
	switch {
	case !row.eligible:
		return row, lockedStripePair{stripeClearOutcome: racedOutcome(StripeClearBankRowNotEligible)}, nil
	case opts.requireAutomatic && (!p.automatic || !inStripePayoutWindow(row.date, p.arrival)):
		return row, lockedStripePair{stripeClearOutcome: racedOutcome(stripeClearNotMatched)}, nil
	case row.cents != p.amountOre:
		return row, lockedStripePair{stripeClearOutcome: racedOutcome(StripeClearAmountMismatch)}, nil
	}
	return row, lockedStripePair{payout: p}, nil
}

// lockedStripeDeposit is a deposit row as the clearing locked it.
type lockedStripeDeposit struct {
	date                     time.Time
	cents                    int64
	bankAccount              string
	description, counterpart string
	// eligible: incoming, unbooked, undismissed, not refund-paired, not
	// provisional and not a Vipps settlement.
	eligible bool
}

// lockStripeDepositTx locks the bank row; found is false for a row that is
// not the club's.
func lockStripeDepositTx(ctx context.Context, tx pgx.Tx, clubID, bankRowID string) (d lockedStripeDeposit, found bool, err error) {
	err = tx.QueryRow(ctx,
		`SELECT bir.row_date, round(bir.amount * 100)::bigint, bi.bank_account_code,
		        COALESCE(bir.description, ''), COALESCE(bir.counterpart, ''),
		        COALESCE((`+stripeDepositEligibleSQL()+`), false)
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.id = $1 AND bir.club_id = $2
		  FOR UPDATE OF bir`,
		bankRowID, clubID,
	).Scan(&d.date, &d.cents, &d.bankAccount, &d.description, &d.counterpart, &d.eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, fmt.Errorf("locking bank row: %w", err)
	}
	if d.bankAccount == "" {
		d.bankAccount = bankAccountCode
	}
	return d, true, nil
}

// lockStripePayoutTx locks the payout and reports whether it is still a
// paid NOK payout not yet cleared (open).
func lockStripePayoutTx(ctx context.Context, tx pgx.Tx, clubID, payoutRowID string) (p stripePayoutCand, found, open bool, err error) {
	p = stripePayoutCand{id: payoutRowID}
	err = tx.QueryRow(ctx,
		`SELECT payout_id, amount_ore, arrival_date, automatic, members_listed_at IS NOT NULL,
		        status = 'paid' AND currency = 'nok' AND journal_entry_id IS NULL AND bank_row_id IS NULL
		   FROM stripe_payouts
		  WHERE id = $1 AND club_id = $2
		  FOR UPDATE`,
		payoutRowID, clubID,
	).Scan(&p.payoutID, &p.amountOre, &p.arrival, &p.automatic, &p.listed, &open)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, false, nil
	}
	if err != nil {
		return p, false, false, fmt.Errorf("locking payout: %w", err)
	}
	return p, true, open, nil
}

// linkStripePayoutTx links the bank row and the payout to the clearing
// entry, each with a guarded update, and marks the payout's own balance
// row booked with it. raced is true when a guard matched nothing; the
// caller rolls back.
//
// The payout's own row (reporting category payout) is Stripe moving the
// money out of the balance; the clearing entry is its booking, so it is
// marked booked rather than left unbooked for ever. It is not a member and
// never blocks a clearing; a payout ingested without it is cleared the
// same.
func linkStripePayoutTx(ctx context.Context, tx pgx.Tx, clubID, entryID, bankRowID, payoutRowID string, autoMatched bool) (raced bool, err error) {
	tag, err := tx.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = $4
		  WHERE id = $2 AND club_id = $3 AND journal_entry_id IS NULL AND dismissed_at IS NULL`,
		entryID, bankRowID, clubID, autoMatched)
	if err != nil {
		return false, fmt.Errorf("linking bank row: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return true, nil
	}
	tag, err = tx.Exec(ctx,
		`UPDATE stripe_payouts SET bank_row_id = $1, journal_entry_id = $2, updated_at = now()
		  WHERE id = $3 AND club_id = $4 AND journal_entry_id IS NULL AND bank_row_id IS NULL`,
		bankRowID, entryID, payoutRowID, clubID)
	if err != nil {
		return false, fmt.Errorf("linking payout: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return true, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE stripe_balance_transactions SET journal_entry_id = $1, status = 'booked', updated_at = now()
		  WHERE club_id = $2 AND payout_id = $3 AND reporting_category = 'payout'
		    AND journal_entry_id IS NULL AND status = 'unbooked'`,
		entryID, clubID, payoutRowID); err != nil {
		return false, fmt.Errorf("marking payout transaction: %w", err)
	}
	return false, nil
}

// checkStripeClearingBalance returns the clearing account remainder, in
// øre, over the payout's members and its clearing entry, and logs a
// warning when it is not zero. After an automatic clearing every member is
// booked or dismissed and they add up to the payout, so a remainder means a
// member was dismissed rather than booked, or booked by hand to other
// amounts; after a manual one it is also what the members miss of the
// payout. The clearing stands and the treasurer corrects it. Nil when the
// balance could not be read; it runs after the commit, on the pool.
func (s *Service) checkStripeClearingBalance(ctx context.Context, clubID, payoutRowID, entryID, payoutID string) *int64 {
	var balance int64
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(round(SUM(jl.debit - jl.credit) * 100), 0)::bigint
		   FROM journal_lines jl
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE a.club_id = $1
		    AND a.code = (SELECT COALESCE(NULLIF(stripe_clearing_account, ''), $4) FROM clubs WHERE id = $1)
		    AND jl.journal_entry_id IN (
		      SELECT journal_entry_id FROM stripe_balance_transactions
		       WHERE club_id = $1 AND payout_id = $2 AND journal_entry_id IS NOT NULL
		         AND reporting_category NOT IN ('payout', 'payout_reversal')
		      UNION SELECT $3::uuid)`,
		clubID, payoutRowID, entryID, stripeClearingAccountCode,
	).Scan(&balance)
	if err != nil {
		s.log.Warn().Err(err).Str("payout_id", payoutID).Msg("stripe payout cleared; clearing balance check failed")
		return nil
	}
	if balance != 0 {
		s.log.Warn().Str("payout_id", payoutID).Int64("clearing_balance_ore", balance).
			Msg("stripe payout cleared but its clearing account movements do not net to zero")
	}
	return &balance
}
