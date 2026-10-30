package accounting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Posting the Vipps draft backlog (BRY-341). Before BRY-339 every Vipps
// posting was created as a draft and nothing posted it, so a fiscal year can
// hold hundreds of balanced drafts that the reports ignore. This walks the
// drafts of one fiscal period and, per draft:
//
//   - refuses it when it is unsafe to post: unbalanced, linked from no row
//     (an orphan), its source row already booked by another entry, or a
//     receivable credit that cannot be tied to exactly one unsettled faktura;
//   - reclassifies a charge parked on the 3900 fallback when the club's
//     auto-assign rules (BRY-340) give an unambiguous answer — the draft is
//     removed and the charge rebooked (and posted) through the normal
//     per-charge path;
//   - reclassifies a charge crediting 1500 without a recorded faktura the
//     same way, but only once receivableChargeSettledFaktura finds no
//     faktura it may already have paid (BRY-392). Such a credit was booked
//     as a faktura payment, and charges assigned before invoice_id existed
//     set only invoices.payment_id (BRY-370), so rebooking one that did pay
//     a faktura would count the money twice. One that fails the test is
//     refused naming the faktura; one that passes but no rule places is
//     refused and offered for release back to the Vipps queue
//     (ReleaseVippsBacklogCharge);
//   - otherwise posts it as it stands.
//
// A settlement's fee and payout drafts are the exception to "posts it as it
// stands": they post only when every charge of the settlement is posted, so
// the clearing account is never left holding their credits without the
// charge debits. The check and the posting of one settlement's drafts run
// in one transaction under the settlement's payout-row lock
// (lockSettlementPayoutsTx), after the period's charge drafts were posted or
// rebooked. A settlement with a charge that is still unbooked, on a draft the
// run did not post, or on a voided entry keeps its drafts and is listed in
// Refused with the count. A reclassification is one transaction too
// (applyBacklogReclass): the draft is replaced or the charge keeps it.
//
// An apply may name the reclassifications the treasurer ticked in the
// preview, each with the target the preview showed (reclassify). A
// reclassification not named is skipped entirely — neither rebooked nor
// posted as it stands, since its parked account is the one the treasurer
// has not accepted — and is listed again next time. Every named draft is
// re-decided under the same guards and rebooked only if the target is still
// the one shown; one that no longer reclassifies, or now would go elsewhere
// (a faktura issued since the preview), is left untouched and reported in
// StaleIDs. A nil list means every reclassification.
//
// Reclassification only runs when the club has enabled auto-assign, so the
// club's opt-in and tie category govern the backlog exactly as they govern
// new charges. With it off a 1500-parked charge with no faktura is refused,
// never posted as a receivable credit nothing clears. Preview changes
// nothing; apply is idempotent because posted entries are no longer drafts.

// AccountTotal is a debit/credit sum on one account.
type AccountTotal struct {
	AccountCode string  `json:"account_code"`
	Debit       float64 `json:"debit"`
	Credit      float64 `json:"credit"`
}

// VippsBacklogReclass is one charge whose draft is rebooked by a rule.
type VippsBacklogReclass struct {
	EntryID     string  `json:"entry_id"`
	RowID       string  `json:"row_id"`
	EntryNumber int     `json:"entry_number"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
	From        string  `json:"from"`
	To          string  `json:"to"`
	Rule        string  `json:"rule"`
}

// VippsBacklogRelease is a refused charge parked on 1500 with no faktura that
// passed the settled-faktura test, so the treasurer may release it back to
// the Vipps queue and assign it by hand.
type VippsBacklogRelease struct {
	Ref         string  `json:"ref"`
	RowID       string  `json:"row_id"`
	EntryNumber int     `json:"entry_number"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
}

// VippsBacklogResult reports a preview or an apply of PostVippsDraftBacklog.
// In a preview the counts are what apply would do.
type VippsBacklogResult struct {
	Year              int                   `json:"year"`
	Applied           bool                  `json:"applied"`
	AutoAssignEnabled bool                  `json:"auto_assign_enabled"`
	Drafts            int                   `json:"drafts"`
	Posted            int                   `json:"posted"`
	PostedTotals      []AccountTotal        `json:"posted_totals"`
	Reclassified      []VippsBacklogReclass `json:"reclassified"`
	Refused           []SkippedItem         `json:"refused"`
	// Releasable lists the refused charges that may be released to the
	// Vipps queue; each is also in Refused.
	Releasable []VippsBacklogRelease `json:"releasable"`
	// Unchecked counts the reclassifications left out of the apply's
	// include-list; their drafts are untouched.
	Unchecked int `json:"unchecked"`
	// StaleIDs are included entry ids that are not a reclassification now
	// (already handled, refused, or never one) or would now be rebooked to
	// another target than the preview showed; nothing is done with them.
	StaleIDs []string `json:"stale_ids"`
	// Payouts reports the settlements an apply completed and cleared; nil in
	// a preview.
	Payouts *VippsPayoutSweepResult `json:"payouts,omitempty"`
}

// VippsBacklogChoice is one reclassification the treasurer ticked: the
// draft's entry id and the target the preview showed for it.
type VippsBacklogChoice struct {
	EntryID string
	To      string
}

type backlogDraft struct {
	id          string
	number      int
	date        time.Time
	sourceTable string
	sourceID    string
	lines       []AccountTotal
}

// PostVippsDraftBacklog previews (apply=false) or posts (apply=true) the
// Vipps drafts of one fiscal period. actor posts the entries. reclassify is
// the include-list of ticked reclassifications; nil includes all.
func (s *Service) PostVippsDraftBacklog(ctx context.Context, clubID, periodID, actor string, apply bool, reclassify []VippsBacklogChoice) (*VippsBacklogResult, error) {
	res := &VippsBacklogResult{
		Applied:      apply,
		PostedTotals: []AccountTotal{},
		Reclassified: []VippsBacklogReclass{},
		Refused:      []SkippedItem{},
		Releasable:   []VippsBacklogRelease{},
	}
	choice := newBacklogChoice(reclassify)
	year, err := s.backlogPeriodYear(ctx, clubID, periodID)
	if err != nil {
		return nil, err
	}
	res.Year = year

	cfg, err := s.vippsAutoAssignConfig(ctx, clubID)
	if err != nil {
		return nil, err
	}
	res.AutoAssignEnabled = cfg.enabled
	clearing, _, err := s.vippsAccounts(ctx, clubID)
	if err != nil {
		return nil, err
	}

	drafts, err := s.loadBacklogDrafts(ctx, clubID, periodID)
	if err != nil {
		return nil, err
	}
	res.Drafts = len(drafts)

	totals := map[string]*AccountTotal{}
	// willBook holds the charge rows the run has posted (a preview: would
	// post), for the settlement check that follows the charge drafts.
	willBook := map[string]bool{}
	var settle []backlogSettleDraft
	for _, d := range drafts {
		ref := fmt.Sprintf("bilag %d", d.number)

		if reason, err := s.backlogRefusal(ctx, clubID, d); err != nil {
			return nil, err
		} else if reason != "" {
			res.Refused = append(res.Refused, SkippedItem{Ref: ref, Reason: reason})
			continue
		}

		key, err := s.backlogSettlementOf(ctx, clubID, d)
		if err != nil {
			return nil, err
		}
		if key != nil {
			settle = append(settle, backlogSettleDraft{key: *key, d: d})
			continue
		}

		reclass, hold, err := s.backlogReclassification(ctx, clubID, cfg, clearing, d)
		if err != nil {
			return nil, err
		}
		if reclass != nil {
			if s.backlogReclassify(ctx, clubID, actor, apply, choice, ref, reclass, res) {
				willBook[reclass.rowID] = true
			}
			continue
		}
		if hold != nil {
			hold.record(ref, res)
			continue
		}

		if apply {
			if err := s.PostJournalEntry(ctx, clubID, d.id, actor); err != nil {
				res.Refused = append(res.Refused, SkippedItem{Ref: ref, Reason: "posting: " + err.Error()})
				continue
			}
		}
		res.Posted++
		addAccountTotals(totals, d.lines)
		if d.sourceTable == "vipps_import_rows" {
			willBook[d.sourceID] = true
		}
	}
	runBacklogHook()
	if err := s.backlogPostSettlements(ctx, clubID, actor, apply, settle, willBook, totals, res); err != nil {
		return nil, err
	}
	res.StaleIDs = choice.stale()

	res.PostedTotals = sortedTotals(totals)

	// Posting the backlog books charges, which can complete settlements.
	if apply {
		res.Payouts = s.ClearReadyVippsPayoutsReported(ctx, clubID, actor)
	}
	return res, nil
}

// backlogPeriodYear returns the year of an open fiscal period; a backlog is
// only posted into an open one.
func (s *Service) backlogPeriodYear(ctx context.Context, clubID, periodID string) (int, error) {
	var year int
	var status string
	err := s.db.QueryRow(ctx,
		`SELECT year, status FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		periodID, clubID,
	).Scan(&year, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("fiscal period not found")
	}
	if err != nil {
		return 0, fmt.Errorf("loading period: %w", err)
	}
	if status != "open" {
		return 0, fmt.Errorf("fiscal period %d is %s — reopen it before posting drafts", year, status)
	}
	return year, nil
}

// backlogReclassify records one reclassification in res: skipped when the
// include-list leaves it out, else listed (and in an apply, rebooked, or
// refused when the rebooking fails). It reports whether the charge is (in a
// preview: would be) rebooked.
func (s *Service) backlogReclassify(ctx context.Context, clubID, actor string, apply bool, choice *backlogChoice, ref string, r *backlogReclass, res *VippsBacklogResult) bool {
	switch choice.take(r.EntryID, r.To) {
	case choiceUnchecked:
		res.Unchecked++
		return false
	case choiceMoved:
		return false // reported in StaleIDs
	}
	if apply {
		if err := s.applyBacklogReclass(ctx, clubID, actor, r); err != nil {
			res.Refused = append(res.Refused, SkippedItem{Ref: ref, Reason: err.Error()})
			return false
		}
	}
	res.Reclassified = append(res.Reclassified, r.VippsBacklogReclass)
	return true
}

// backlogBeforeSettlementsHook runs after a backlog run's charge drafts and
// before its settlement drafts, so a test can change a charge in between.
var backlogBeforeSettlementsHook func()

func runBacklogHook() {
	if backlogBeforeSettlementsHook != nil {
		backlogBeforeSettlementsHook()
	}
}

// backlogSettlementKey names the settlement a fee or payout draft belongs to.
type backlogSettlementKey struct {
	number, msn string
	booking     time.Time
}

type backlogSettleDraft struct {
	key backlogSettlementKey
	d   backlogDraft
}

// backlogSettlementOf returns the settlement a draft clears when it is a
// per-charge fee entry (sourced to the payout row) or payout entry (sourced
// to the deposit's bank row), or nil for any other draft. A legacy
// settlement bilag has its charges linked to the same entry, so it is not
// one: its charges and bank row stand or fall together.
func (s *Service) backlogSettlementOf(ctx context.Context, clubID string, d backlogDraft) (*backlogSettlementKey, error) {
	switch d.sourceTable {
	case "vipps_import_rows":
		var rowType, msn, number string
		var booking *time.Time
		err := s.db.QueryRow(ctx,
			`SELECT row_type, COALESCE(msn, ''), COALESCE(settlement_number, ''), booking_date
			   FROM vipps_import_rows WHERE id = $1 AND club_id = $2`,
			d.sourceID, clubID,
		).Scan(&rowType, &msn, &number, &booking)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("loading the source of bilag %d: %w", d.number, err)
		}
		if rowType != string(VippsRowPayout) || msn == "" || booking == nil {
			return nil, nil
		}
		return &backlogSettlementKey{number: number, msn: msn, booking: *booking}, nil
	case "bank_import_rows":
		var desc string
		var legacy bool
		err := s.db.QueryRow(ctx,
			`SELECT description,
			        EXISTS (SELECT 1 FROM vipps_import_rows WHERE club_id = $2 AND journal_entry_id = $3 AND row_type = 'belastning')
			   FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
			d.sourceID, clubID, d.id,
		).Scan(&desc, &legacy)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("loading the source of bilag %d: %w", d.number, err)
		}
		m := VippsSettlementPattern.FindStringSubmatch(desc)
		if legacy || len(m) != 3 {
			return nil, nil
		}
		var booking *time.Time
		err = s.db.QueryRow(ctx,
			`SELECT booking_date FROM vipps_import_rows
			  WHERE club_id = $1 AND msn = $2 AND row_type = 'payout' AND settlement_number = $3
			  ORDER BY booking_date, id LIMIT 1`,
			clubID, m[2], m[1],
		).Scan(&booking)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && booking == nil) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("loading the payout of bilag %d: %w", d.number, err)
		}
		return &backlogSettlementKey{number: m[1], msn: m[2], booking: *booking}, nil
	}
	return nil, nil
}

// backlogPostSettlements posts (apply) or previews the fee and payout drafts
// of each settlement whose charges are all posted, and refuses the rest.
func (s *Service) backlogPostSettlements(ctx context.Context, clubID, actor string, apply bool, settle []backlogSettleDraft, willBook map[string]bool, totals map[string]*AccountTotal, res *VippsBacklogResult) error {
	var order []backlogSettlementKey
	groups := map[backlogSettlementKey][]backlogDraft{}
	for _, sd := range settle {
		if _, ok := groups[sd.key]; !ok {
			order = append(order, sd.key)
		}
		groups[sd.key] = append(groups[sd.key], sd.d)
	}
	for _, key := range order {
		drafts := groups[key]
		reason, err := s.backlogPostSettlement(ctx, clubID, actor, apply, key, drafts, willBook)
		if err != nil {
			return err
		}
		for _, d := range drafts {
			if reason != "" {
				res.Refused = append(res.Refused, SkippedItem{Ref: fmt.Sprintf("bilag %d", d.number), Reason: reason})
				continue
			}
			res.Posted++
			addAccountTotals(totals, d.lines)
		}
	}
	return nil
}

// backlogPostSettlement checks that every charge of the settlement is posted
// (or, in a preview, would be) and posts its drafts, all in one transaction
// holding the settlement's payout rows. It returns why the drafts must stay,
// or "".
func (s *Service) backlogPostSettlement(ctx context.Context, clubID, actor string, apply bool, key backlogSettlementKey, drafts []backlogDraft, willBook map[string]bool) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockSettlementPayoutsTx(ctx, tx, clubID, key.msn, key.booking); err != nil {
		return "", err
	}
	rows, err := tx.Query(ctx,
		`SELECT r.id::text FROM vipps_import_rows r
		  WHERE r.club_id = $1 AND r.msn = $2 AND r.row_type = 'belastning' AND r.booking_date = $3::date
		    AND r.dismissed_at IS NULL AND NOT `+chargePostedSQL("r"),
		clubID, key.msn, key.booking)
	if err != nil {
		return "", fmt.Errorf("counting unposted charges: %w", err)
	}
	unposted, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", fmt.Errorf("counting unposted charges: %w", err)
	}
	// willBook is a preview's prediction. An apply trusts only the database:
	// a charge it posted may have been unassigned or voided since.
	waiting := 0
	for _, id := range unposted {
		if apply || !willBook[id] {
			waiting++
		}
	}
	if waiting > 0 {
		return fmt.Sprintf("settlement Utb. %s Vippsnr %s has %d charge(s) that are not posted — book them first; its fee and payout stay as drafts",
			key.number, key.msn, waiting), nil
	}
	if !apply {
		return "", nil
	}
	for _, d := range drafts {
		if err := postJournalEntryTx(ctx, tx, clubID, d.id, actor); err != nil {
			return "posting: " + err.Error(), nil
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit settlement drafts: %w", err)
	}
	return "", nil
}

// sortedTotals returns the totals rounded to øre, in account order.
func sortedTotals(totals map[string]*AccountTotal) []AccountTotal {
	out := make([]AccountTotal, 0, len(totals))
	for _, t := range totals {
		out = append(out, AccountTotal{AccountCode: t.AccountCode, Debit: roundCents(t.Debit), Credit: roundCents(t.Credit)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountCode < out[j].AccountCode })
	return out
}

func addAccountTotals(totals map[string]*AccountTotal, lines []AccountTotal) {
	for _, l := range lines {
		t := totals[l.AccountCode]
		if t == nil {
			t = &AccountTotal{AccountCode: l.AccountCode}
			totals[l.AccountCode] = t
		}
		t.Debit += l.Debit
		t.Credit += l.Credit
	}
}

// backlogChoice is an apply's include-list of reclassified drafts; nil
// includes every reclassification.
type backlogChoice struct {
	ids     []string
	targets map[string]string
	taken   map[string]bool
}

func newBacklogChoice(choices []VippsBacklogChoice) *backlogChoice {
	if choices == nil {
		return nil
	}
	c := &backlogChoice{targets: make(map[string]string, len(choices)), taken: map[string]bool{}}
	for _, ch := range choices {
		if _, dup := c.targets[ch.EntryID]; !dup {
			c.ids = append(c.ids, ch.EntryID)
		}
		c.targets[ch.EntryID] = ch.To
	}
	return c
}

const (
	choiceTaken = iota
	choiceUnchecked
	// choiceMoved: ticked, but the draft would now be rebooked somewhere
	// other than the preview showed.
	choiceMoved
)

// take classifies the reclassification of draft entryID to target to, and
// records a taken one.
func (c *backlogChoice) take(entryID, to string) int {
	if c == nil {
		return choiceTaken
	}
	want, ok := c.targets[entryID]
	switch {
	case !ok:
		return choiceUnchecked
	case want != to:
		return choiceMoved
	}
	c.taken[entryID] = true
	return choiceTaken
}

// stale returns the included ids that named no reclassification, each once.
func (c *backlogChoice) stale() []string {
	out := []string{}
	if c == nil {
		return out
	}
	for _, id := range c.ids {
		if !c.taken[id] {
			out = append(out, id)
		}
	}
	return out
}

func (s *Service) loadBacklogDrafts(ctx context.Context, clubID, periodID string) ([]backlogDraft, error) {
	rows, err := s.db.Query(ctx,
		`SELECT je.id, je.entry_number, je.entry_date, COALESCE(je.source_table, ''), COALESCE(je.source_id, ''),
		        a.code, jl.debit, jl.credit
		   FROM journal_entries je
		   JOIN journal_lines jl ON jl.journal_entry_id = je.id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1 AND je.fiscal_period_id = $2
		    AND je.source = 'vipps' AND je.status = 'draft'
		  ORDER BY je.entry_date, je.entry_number, a.code`,
		clubID, periodID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading vipps drafts: %w", err)
	}
	defer rows.Close()
	var out []backlogDraft
	idx := map[string]int{}
	for rows.Next() {
		var d backlogDraft
		var l AccountTotal
		if err := rows.Scan(&d.id, &d.number, &d.date, &d.sourceTable, &d.sourceID, &l.AccountCode, &l.Debit, &l.Credit); err != nil {
			return nil, fmt.Errorf("scanning vipps draft: %w", err)
		}
		i, ok := idx[d.id]
		if !ok {
			idx[d.id] = len(out)
			out = append(out, d)
			i = len(out) - 1
		}
		out[i].lines = append(out[i].lines, l)
	}
	return out, rows.Err()
}

// backlogRefusal returns why a draft must not be posted, or "".
func (s *Service) backlogRefusal(ctx context.Context, clubID string, d backlogDraft) (string, error) {
	var dr, cr float64
	for _, l := range d.lines {
		dr += l.Debit
		cr += l.Credit
	}
	if roundCents(dr) != roundCents(cr) || roundCents(dr) == 0 {
		return fmt.Sprintf("unbalanced (debit %.2f, credit %.2f)", dr, cr), nil
	}

	var linked int
	if err := s.db.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM vipps_import_rows WHERE club_id = $1 AND journal_entry_id = $2)
		      + (SELECT count(*) FROM bank_import_rows WHERE club_id = $1 AND journal_entry_id = $2)`,
		clubID, d.id,
	).Scan(&linked); err != nil {
		return "", fmt.Errorf("counting linked rows: %w", err)
	}
	if linked == 0 {
		return "orphan: no Vipps or bank row is linked to it — delete it from Bilag", nil
	}

	var other *int
	if err := s.db.QueryRow(ctx,
		`SELECT min(entry_number) FROM journal_entries
		  WHERE club_id = $1 AND id <> $2 AND source = 'vipps' AND status = 'posted'
		    AND source_table = $3 AND source_id = $4`,
		clubID, d.id, d.sourceTable, d.sourceID,
	).Scan(&other); err != nil {
		return "", fmt.Errorf("checking for an earlier booking: %w", err)
	}
	if other != nil {
		return fmt.Sprintf("its source row is already booked by bilag %d", *other), nil
	}

	// A receivable credit tied to a faktura must not settle a faktura that
	// another posted entry already settled.
	var invoiceNumber, otherEntry *int
	if err := s.db.QueryRow(ctx,
		`SELECT i.invoice_number,
		        (SELECT min(je.entry_number) FROM journal_entries je
		          WHERE je.club_id = $1 AND je.id <> $2 AND je.status = 'posted'
		            AND (je.id IN (SELECT journal_entry_id FROM bank_import_rows WHERE club_id = $1 AND invoice_id = i.id)
		              OR je.id IN (SELECT journal_entry_id FROM vipps_import_rows WHERE club_id = $1 AND invoice_id = i.id)))
		   FROM vipps_import_rows r JOIN invoices i ON i.id = r.invoice_id
		  WHERE r.club_id = $1 AND r.journal_entry_id = $2
		  LIMIT 1`,
		clubID, d.id,
	).Scan(&invoiceNumber, &otherEntry); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("checking faktura settlement: %w", err)
	}
	if otherEntry != nil {
		return fmt.Sprintf("faktura %d is already settled by bilag %d", *invoiceNumber, *otherEntry), nil
	}
	return "", nil
}

type backlogReclass struct {
	VippsBacklogReclass
	rowID    string
	decision *vippsChargeDecision
}

// backlogHold is why a charge draft must stay for a human; release is set
// when the charge may be released to the Vipps queue.
type backlogHold struct {
	reason  string
	release *VippsBacklogRelease
}

// record lists the held charge as refused, and as releasable when it may be
// released to the Vipps queue.
func (h *backlogHold) record(ref string, res *VippsBacklogResult) {
	res.Refused = append(res.Refused, SkippedItem{Ref: ref, Reason: h.reason})
	if h.release != nil {
		h.release.Ref = ref
		res.Releasable = append(res.Releasable, *h.release)
	}
}

// parkedCredit returns the account a charge draft credits — its one line
// besides the clearing debit — or "" when the draft has any other shape.
func parkedCredit(lines []AccountTotal, clearing string) string {
	var credits []AccountTotal
	for _, l := range lines {
		if l.AccountCode == clearing && l.Debit > 0 {
			continue
		}
		credits = append(credits, l)
	}
	if len(credits) != 1 || credits[0].Credit <= 0 {
		return ""
	}
	return credits[0].AccountCode
}

const (
	reasonParkedNoFaktura = "receivable credit with no recorded faktura"
	releaseHint           = " — release it to the Vipps queue and assign it there"
)

// backlogReclassification decides whether a charge draft parked on the 3900
// fallback, or on 1500 with no recorded faktura, should be rebooked by a
// rule. It returns the reclassification, or — for a 1500 credit no rule may
// place — why it must stay for a human.
func (s *Service) backlogReclassification(ctx context.Context, clubID string, cfg vippsAutoAssignConfig, clearing string, d backlogDraft) (*backlogReclass, *backlogHold, error) {
	if d.sourceTable != "vipps_import_rows" {
		return nil, nil, nil
	}
	from := parkedCredit(d.lines, clearing)
	if from != fallbackRevenueCode && from != receivablesAccountCode {
		return nil, nil, nil
	}

	var (
		rowID, rowType, customer, message string
		amount                            float64
		paidOn                            time.Time
		invoiceID                         *string
		isOrder                           bool
	)
	err := s.db.QueryRow(ctx,
		`SELECT r.id, r.row_type, r.amount, COALESCE(r.booking_date, r.tx_at::date),
		        COALESCE(r.customer_name, ''), COALESCE(r.message, ''), r.invoice_id::text,
		        EXISTS (SELECT 1 FROM orders o WHERE o.club_id = r.club_id AND r.order_id <> '' AND o.vipps_reference = r.order_id)
		   FROM vipps_import_rows r
		  WHERE r.club_id = $1 AND r.journal_entry_id = $2`,
		clubID, d.id,
	).Scan(&rowID, &rowType, &amount, &paidOn, &customer, &message, &invoiceID, &isOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("loading charge for bilag %d: %w", d.number, err)
	}
	if rowType != "belastning" || isOrder {
		return nil, nil, nil
	}

	parked := from == receivablesAccountCode
	if parked {
		if invoiceID != nil {
			return nil, nil, nil // tied to a faktura: post as is
		}
		settled, err := s.receivableChargeSettledFaktura(ctx, clubID, customer, message, amount, paidOn)
		if err != nil {
			return nil, nil, err
		}
		if settled != nil {
			return nil, &backlogHold{reason: settled.reason()}, nil
		}
	}
	release := &VippsBacklogRelease{RowID: rowID, EntryNumber: d.number, Date: d.date.Format("2006-01-02"), Amount: amount}

	if !cfg.enabled {
		if parked {
			return nil, &backlogHold{reason: reasonParkedNoFaktura + " and auto-assign is off" + releaseHint, release: release}, nil
		}
		return nil, nil, nil
	}
	dec, err := s.decideVippsCharge(ctx, clubID, cfg, customer, message, amount, paidOn)
	if err != nil {
		return nil, nil, err
	}
	if dec.Rule != "" && dec.Target() != from {
		return &backlogReclass{
			VippsBacklogReclass: VippsBacklogReclass{
				EntryID:     d.id,
				RowID:       rowID,
				EntryNumber: d.number,
				Date:        d.date.Format("2006-01-02"),
				Amount:      amount,
				From:        from,
				To:          dec.Target(),
				Rule:        dec.Rule,
			},
			rowID:    rowID,
			decision: dec,
		}, nil, nil
	}
	if parked {
		return nil, &backlogHold{reason: reasonParkedNoFaktura + ": " + dec.Reason + releaseHint, release: release}, nil
	}
	return nil, nil, nil
}

// settledFaktura is a faktura a parked charge may already have paid.
type settledFaktura struct {
	number int
	paid   float64
}

// There is no in-app way yet to fix the faktura side of such a charge
// (BRY-397), so the reason says the draft waits rather than what to click.
func (f *settledFaktura) reason() string {
	return fmt.Sprintf("%s, and faktura %d is marked paid by a %.2f kr payment that no other booking explains — it may be this charge. The draft stays until the faktura link is resolved (BRY-397); contact the developer meanwhile",
		reasonParkedNoFaktura, f.number, f.paid)
}

// settledWindowDays bounds how far a faktura payment's date may lie from a
// charge date for the payment to count as possibly that charge. Legacy
// KID-avstemming payments carry the payout date, a few days after the charge.
const settledWindowDays = 7

// receivableChargeSettledFaktura is the test that lets a Vipps charge
// credited to 1500 without a recorded faktura be rebooked, released or
// unassigned (BRY-392). payments has no column naming a Vipps charge, so a
// charge is taken to have settled a faktura when that faktura is marked paid
// for the charge's amount (±1 kr) and no non-voided bank or Vipps booking
// linked to the faktura explains the payment:
//
//   - a KID-avstemming payment (invoices.payment_id) on a faktura of the
//     member the payer resolves to, or the faktura whose KID is in the
//     message, at any date;
//   - a KID-avstemming payment on any faktura, paid within settledWindowDays
//     of the charge date — the payer may have paid a household member's
//     faktura, and name resolution can be wrong;
//   - a faktura marked paid by Vipps online (online_paid_at with provider
//     vipps) within the window, unless a booked charge of a checkout order
//     paying it explains it. Only Vipps markers count, because only a
//     booked Vipps charge can explain one.
//
// It returns the first such faktura, or nil when the charge passes.
func (s *Service) receivableChargeSettledFaktura(ctx context.Context, clubID, customer, message string, amount float64, paidOn time.Time) (*settledFaktura, error) {
	memberID, _, err := s.resolveCustomerToMemberPass(ctx, clubID, customer, message)
	if err != nil {
		return nil, fmt.Errorf("resolving payer: %w", err)
	}
	kid := extractKID(message)

	var f settledFaktura
	err = s.db.QueryRow(ctx,
		`SELECT i.invoice_number, COALESCE(p.amount, i.total_amount)
		   FROM invoices i
		   LEFT JOIN payments p ON p.id = i.payment_id AND p.club_id = i.club_id
		                       AND p.description = 'KID-avstemming'
		  WHERE i.club_id = $1
		    AND ((p.id IS NOT NULL AND abs(p.amount - $2) <= 1
		          AND (($3 <> '' AND i.user_id::text = $3)
		            OR ($4 <> '' AND i.kid_number = $4)
		            OR p.paid_at::date BETWEEN $5::date - $6::int AND $5::date + $6::int))
		      OR (i.online_paid_at IS NOT NULL AND i.online_paid_provider = 'vipps'
		          AND abs(i.total_amount - $2) <= 1
		          AND i.online_paid_at::date BETWEEN $5::date - $6::int AND $5::date + $6::int
		          AND NOT EXISTS (
		              SELECT 1 FROM order_sources os
		                JOIN orders o ON o.id = os.order_id
		                JOIN vipps_import_rows v ON v.club_id = o.club_id AND v.order_id = o.vipps_reference
		                JOIN journal_entries je ON je.id = v.journal_entry_id AND je.status <> 'voided'
		               WHERE os.invoice_id = i.id AND o.vipps_reference <> '')))
		    AND NOT EXISTS (
		        SELECT 1 FROM journal_entries je
		         WHERE je.club_id = i.club_id AND je.status <> 'voided'
		           AND (je.id IN (SELECT journal_entry_id FROM bank_import_rows WHERE club_id = i.club_id AND invoice_id = i.id)
		             OR je.id IN (SELECT journal_entry_id FROM vipps_import_rows WHERE club_id = i.club_id AND invoice_id = i.id)))
		  ORDER BY i.invoice_number
		  LIMIT 1`,
		clubID, amount, memberID, kid, paidOn, settledWindowDays,
	).Scan(&f.number, &f.paid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checking settled fakturas: %w", err)
	}
	return &f, nil
}

// parkedDraftCheck reports whether entryID, the draft a charge row is booked
// to, credits 1500 as its only line besides the clearing debit, and if so
// the faktura the charge may already have paid.
func (s *Service) parkedDraftCheck(ctx context.Context, clubID, rowID, entryID string) (bool, *settledFaktura, error) {
	clearing, _, err := s.vippsAccounts(ctx, clubID)
	if err != nil {
		return false, nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT a.code, jl.debit, jl.credit
		   FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1`,
		entryID,
	)
	if err != nil {
		return false, nil, fmt.Errorf("loading draft lines: %w", err)
	}
	var lines []AccountTotal
	for rows.Next() {
		var l AccountTotal
		if err := rows.Scan(&l.AccountCode, &l.Debit, &l.Credit); err != nil {
			rows.Close()
			return false, nil, fmt.Errorf("scanning draft line: %w", err)
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, nil, err
	}
	if parkedCredit(lines, clearing) != receivablesAccountCode {
		return false, nil, nil
	}

	var customer, message string
	var amount float64
	var paidOn time.Time
	if err := s.db.QueryRow(ctx,
		`SELECT r.amount, COALESCE(r.booking_date, r.tx_at::date),
		        COALESCE(r.customer_name, ''), COALESCE(r.message, '')
		   FROM vipps_import_rows r WHERE r.id = $1 AND r.club_id = $2`,
		rowID, clubID,
	).Scan(&amount, &paidOn, &customer, &message); err != nil {
		return false, nil, fmt.Errorf("loading vipps charge: %w", err)
	}
	settled, err := s.receivableChargeSettledFaktura(ctx, clubID, customer, message, amount, paidOn)
	return true, settled, err
}

// ReleaseVippsBacklogCharge removes the draft of a charge parked on 1500 with
// no recorded faktura and returns the charge to the Vipps queue, so the
// treasurer can assign by hand what the backlog run refused. On top of
// UnassignVippsCharge, which already refuses a parked draft that fails
// receivableChargeSettledFaktura, it requires exactly that shape of draft
// and an open period. It returns the deleted draft's id.
func (s *Service) ReleaseVippsBacklogCharge(ctx context.Context, clubID, actorID, rowID string) (string, error) {
	var (
		entryID, invoiceID, entryStatus, periodStatus *string
		rowType                                       string
	)
	err := s.db.QueryRow(ctx,
		`SELECT r.row_type, r.journal_entry_id::text, r.invoice_id::text, je.status::text, fp.status::text
		   FROM vipps_import_rows r
		   LEFT JOIN journal_entries je ON je.id = r.journal_entry_id AND je.club_id = r.club_id
		   LEFT JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		  WHERE r.id = $1 AND r.club_id = $2`,
		rowID, clubID,
	).Scan(&rowType, &entryID, &invoiceID, &entryStatus, &periodStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("vipps charge not found")
	}
	if err != nil {
		return "", fmt.Errorf("loading vipps charge: %w", err)
	}
	switch {
	case rowType != "belastning":
		return "", fmt.Errorf("only a Vipps charge can be released")
	case entryID == nil || entryStatus == nil:
		return "", fmt.Errorf("charge is not booked")
	case *entryStatus != "draft":
		return "", fmt.Errorf("only a draft can be released — this charge's bilag is %s", *entryStatus)
	case invoiceID != nil:
		return "", fmt.Errorf("the charge is recorded against a faktura — post it with Bokfør Vipps-utkast instead")
	case periodStatus == nil || *periodStatus != "open":
		return "", fmt.Errorf("the draft's fiscal period is not open: %w", ErrPeriodClosed)
	}

	parked, _, err := s.parkedDraftCheck(ctx, clubID, rowID, *entryID)
	if err != nil {
		return "", err
	}
	if !parked {
		return "", fmt.Errorf("only a charge draft crediting %s can be released", receivablesAccountCode)
	}
	prior, _, err := s.UnassignVippsCharge(ctx, clubID, actorID, rowID)
	return prior, err
}

// applyBacklogReclass replaces the parked draft with the rule's booking in
// one transaction (rebookVippsChargeTx): the draft is deleted and the charge
// posted to its new target together, or, when anything fails, the charge
// keeps the draft it had.
func (s *Service) applyBacklogReclass(ctx context.Context, clubID, actor string, r *backlogReclass) error {
	b, err := s.loadVippsBelastning(ctx, clubID, r.rowID)
	if err != nil {
		return err
	}
	d := r.decision
	desc := fmt.Sprintf("Vipps innbetaling: %s", b.customerName)
	var account, invoiceID string
	switch d.Rule {
	case autoAssignRuleMemberInvoice:
		account, invoiceID = receivablesAccountCode, d.InvoiceID
	case autoAssignRulePriceItem:
		account, desc = d.Account, desc+" — auto: "+d.Label
	default:
		return fmt.Errorf("no booking rule for this charge")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rb, err := rebookVippsChargeTx(ctx, tx, clubID, actor, r.rowID,
		[]CreateJournalLineInput{{AccountCode: account, Credit: b.amount, Description: desc}}, invoiceID)
	if err != nil {
		return fmt.Errorf("rebooking failed, the charge keeps its draft: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rebooking: %w", err)
	}
	if rb.InvoiceID != "" {
		// Best-effort, as in AssignVippsChargeToInvoice: the ledger is right.
		if err := s.linkInvoicePayment(ctx, clubID, rb.InvoiceID, rb.Date); err != nil {
			s.log.Warn().Err(err).Str("invoice_id", rb.InvoiceID).Str("vipps_row_id", r.rowID).
				Msg("vipps backlog rebook: invoice payment back-link failed")
		}
	}
	s.markAutoAssignedWith(ctx, clubID, actor, r.rowID, d.Rule, d.Target(), rb.EntryID, map[string]any{
		"prior_journal_entry_id": rb.PriorEntryID,
		"prior_invoice_id":       rb.PriorInvoiceID,
	})
	return nil
}
