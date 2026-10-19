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
	// Payouts reports the settlements an apply completed and cleared; nil in
	// a preview.
	Payouts *VippsPayoutSweepResult `json:"payouts,omitempty"`
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
// Vipps drafts of one fiscal period. actor posts the entries.
func (s *Service) PostVippsDraftBacklog(ctx context.Context, clubID, periodID, actor string, apply bool) (*VippsBacklogResult, error) {
	res := &VippsBacklogResult{
		Applied:      apply,
		PostedTotals: []AccountTotal{},
		Reclassified: []VippsBacklogReclass{},
		Refused:      []SkippedItem{},
		Releasable:   []VippsBacklogRelease{},
	}
	var status string
	err := s.db.QueryRow(ctx,
		`SELECT year, status FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		periodID, clubID,
	).Scan(&res.Year, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("fiscal period not found")
	}
	if err != nil {
		return nil, fmt.Errorf("loading period: %w", err)
	}
	if status != "open" {
		return nil, fmt.Errorf("fiscal period %d is %s — reopen it before posting drafts", res.Year, status)
	}

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
	for _, d := range drafts {
		ref := fmt.Sprintf("bilag %d", d.number)

		if reason, err := s.backlogRefusal(ctx, clubID, d); err != nil {
			return nil, err
		} else if reason != "" {
			res.Refused = append(res.Refused, SkippedItem{Ref: ref, Reason: reason})
			continue
		}

		reclass, hold, err := s.backlogReclassification(ctx, clubID, cfg, clearing, d)
		if err != nil {
			return nil, err
		}
		if reclass != nil {
			if apply {
				if err := s.applyBacklogReclass(ctx, clubID, actor, reclass); err != nil {
					res.Refused = append(res.Refused, SkippedItem{Ref: ref, Reason: err.Error()})
					continue
				}
			}
			res.Reclassified = append(res.Reclassified, reclass.VippsBacklogReclass)
			continue
		}
		if hold != nil {
			res.Refused = append(res.Refused, SkippedItem{Ref: ref, Reason: hold.reason})
			if hold.release != nil {
				hold.release.Ref = ref
				res.Releasable = append(res.Releasable, *hold.release)
			}
			continue
		}

		if apply {
			if err := s.PostJournalEntry(ctx, clubID, d.id, actor); err != nil {
				res.Refused = append(res.Refused, SkippedItem{Ref: ref, Reason: "posting: " + err.Error()})
				continue
			}
		}
		res.Posted++
		for _, l := range d.lines {
			t := totals[l.AccountCode]
			if t == nil {
				t = &AccountTotal{AccountCode: l.AccountCode}
				totals[l.AccountCode] = t
			}
			t.Debit += l.Debit
			t.Credit += l.Credit
		}
	}

	for _, t := range totals {
		t.Debit, t.Credit = roundCents(t.Debit), roundCents(t.Credit)
		res.PostedTotals = append(res.PostedTotals, *t)
	}
	sort.Slice(res.PostedTotals, func(i, j int) bool {
		return res.PostedTotals[i].AccountCode < res.PostedTotals[j].AccountCode
	})

	// Posting the backlog books charges, which can complete settlements.
	if apply {
		res.Payouts = s.ClearReadyVippsPayoutsReported(ctx, clubID, actor)
	}
	return res, nil
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

func (f *settledFaktura) reason() string {
	return fmt.Sprintf("%s, and faktura %d is marked paid by a %.2f kr KID payment that no other booking explains — it may be this charge: check faktura %d and correct it by hand",
		reasonParkedNoFaktura, f.number, f.paid, f.number)
}

// receivableChargeSettledFaktura is the test that lets a Vipps charge
// credited to 1500 without a recorded faktura be rebooked or released
// (BRY-392). payments has no column naming a Vipps charge, so a charge is
// taken to have settled a faktura when one that could be its payer's is
// marked paid by a KID-avstemming payment of the charge's amount (±1 kr) and
// no posted bank or Vipps booking linked to that faktura explains the
// payment. The candidates are the fakturas of the member the payer resolves
// to and the faktura whose KID is in the message. A payer with no strong
// member match and no known KID could have paid anyone's faktura, so then
// any such payment within 7 days of the charge counts. It returns the first
// such faktura, or nil when the charge passes.
func (s *Service) receivableChargeSettledFaktura(ctx context.Context, clubID, customer, message string, amount float64, paidOn time.Time) (*settledFaktura, error) {
	memberID, pass, err := s.resolveCustomerToMemberPass(ctx, clubID, customer, message)
	if err != nil {
		return nil, fmt.Errorf("resolving payer: %w", err)
	}
	kid := extractKID(message)
	kidKnown := false
	if kid != "" {
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM invoices WHERE club_id = $1 AND kid_number = $2)`,
			clubID, kid,
		).Scan(&kidKnown); err != nil {
			return nil, fmt.Errorf("checking KID: %w", err)
		}
	}
	strongMember := memberID != "" && pass <= memberPassFirstLast
	anyMember := !strongMember && !kidKnown

	var f settledFaktura
	err = s.db.QueryRow(ctx,
		`SELECT i.invoice_number, p.amount
		   FROM invoices i
		   JOIN payments p ON p.id = i.payment_id AND p.club_id = i.club_id
		  WHERE i.club_id = $1
		    AND p.description = 'KID-avstemming'
		    AND abs(p.amount - $2) <= 1
		    AND (($3 <> '' AND i.user_id::text = $3)
		      OR ($4 <> '' AND i.kid_number = $4)
		      OR ($5 AND p.paid_at::date BETWEEN $6::date - 7 AND $6::date + 7))
		    AND NOT EXISTS (
		        SELECT 1 FROM journal_entries je
		         WHERE je.club_id = i.club_id AND je.status = 'posted'
		           AND (je.id IN (SELECT journal_entry_id FROM bank_import_rows WHERE club_id = i.club_id AND invoice_id = i.id)
		             OR je.id IN (SELECT journal_entry_id FROM vipps_import_rows WHERE club_id = i.club_id AND invoice_id = i.id)))
		  ORDER BY i.invoice_number
		  LIMIT 1`,
		clubID, amount, memberID, kid, anyMember, paidOn,
	).Scan(&f.number, &f.paid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checking settled fakturas: %w", err)
	}
	return &f, nil
}

// ReleaseVippsBacklogCharge removes the draft of a charge parked on 1500 with
// no recorded faktura and returns the charge to the Vipps queue, so the
// treasurer can assign by hand what the backlog run refused. Only a charge
// that passes receivableChargeSettledFaktura is released: one that may have
// paid a faktura stays put until that faktura is corrected. The draft's
// period must be open. It returns the deleted draft's id.
func (s *Service) ReleaseVippsBacklogCharge(ctx context.Context, clubID, actorID, rowID string) (string, error) {
	var (
		entryID, invoiceID, entryStatus, periodStatus *string
		rowType, customer, message                    string
		amount                                        float64
		paidOn                                        time.Time
	)
	err := s.db.QueryRow(ctx,
		`SELECT r.row_type, r.journal_entry_id::text, r.invoice_id::text, je.status::text, fp.status::text,
		        r.amount, COALESCE(r.booking_date, r.tx_at::date),
		        COALESCE(r.customer_name, ''), COALESCE(r.message, '')
		   FROM vipps_import_rows r
		   LEFT JOIN journal_entries je ON je.id = r.journal_entry_id AND je.club_id = r.club_id
		   LEFT JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		  WHERE r.id = $1 AND r.club_id = $2`,
		rowID, clubID,
	).Scan(&rowType, &entryID, &invoiceID, &entryStatus, &periodStatus, &amount, &paidOn, &customer, &message)
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

	clearing, _, err := s.vippsAccounts(ctx, clubID)
	if err != nil {
		return "", err
	}
	rows, err := s.db.Query(ctx,
		`SELECT a.code, jl.debit, jl.credit
		   FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1`,
		*entryID,
	)
	if err != nil {
		return "", fmt.Errorf("loading draft lines: %w", err)
	}
	var lines []AccountTotal
	for rows.Next() {
		var l AccountTotal
		if err := rows.Scan(&l.AccountCode, &l.Debit, &l.Credit); err != nil {
			rows.Close()
			return "", fmt.Errorf("scanning draft line: %w", err)
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if parkedCredit(lines, clearing) != receivablesAccountCode {
		return "", fmt.Errorf("only a charge draft crediting %s can be released", receivablesAccountCode)
	}

	settled, err := s.receivableChargeSettledFaktura(ctx, clubID, customer, message, amount, paidOn)
	if err != nil {
		return "", err
	}
	if settled != nil {
		return "", errors.New(settled.reason())
	}
	prior, _, err := s.UnassignVippsCharge(ctx, clubID, actorID, rowID)
	return prior, err
}

// applyBacklogReclass removes the parked draft and rebooks the charge per
// the rule. If the rebooking fails the charge is back in the Tildel queue.
func (s *Service) applyBacklogReclass(ctx context.Context, clubID, actor string, r *backlogReclass) error {
	if _, _, err := s.UnassignVippsCharge(ctx, clubID, actor, r.rowID); err != nil {
		return fmt.Errorf("removing the parked draft: %w", err)
	}
	if _, err := s.bookVippsChargeDecision(ctx, clubID, actor, r.rowID, r.decision); err != nil {
		return fmt.Errorf("rebooking failed, the charge is back in Tildel: %w", err)
	}
	return nil
}
