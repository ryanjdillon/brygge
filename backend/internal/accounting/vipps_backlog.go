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
//     per-charge path. A charge crediting 1500 without a recorded faktura is
//     always refused, never reclassified: it may have settled a faktura
//     before invoice_id was recorded (BRY-370);
//   - otherwise posts it as it stands.
//
// Reclassification only runs when the club has enabled auto-assign, so the
// club's opt-in and tie category govern the backlog exactly as they govern
// new charges. Preview changes nothing; apply is idempotent because posted
// entries are no longer drafts.

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

		reclass, parkedReason, err := s.backlogReclassification(ctx, clubID, cfg, clearing, d)
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
		if parkedReason != "" {
			res.Refused = append(res.Refused, SkippedItem{Ref: ref, Reason: parkedReason})
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

// backlogReclassification decides whether a charge draft parked on the 3900
// fallback should be rebooked by a rule. It returns the reclassification, or
// — for a receivable credit with no recorded faktura — the reason it must
// stay for a human.
func (s *Service) backlogReclassification(ctx context.Context, clubID string, cfg vippsAutoAssignConfig, clearing string, d backlogDraft) (*backlogReclass, string, error) {
	if d.sourceTable != "vipps_import_rows" {
		return nil, "", nil
	}
	var credits []AccountTotal
	for _, l := range d.lines {
		if l.AccountCode == clearing && l.Debit > 0 {
			continue
		}
		credits = append(credits, l)
	}
	if len(credits) != 1 || credits[0].Credit <= 0 {
		return nil, "", nil
	}
	from := credits[0].AccountCode
	if from != fallbackRevenueCode && from != receivablesAccountCode {
		return nil, "", nil
	}

	var (
		rowID, rowType, customer, message, orderRef string
		amount                                      float64
		paidOn                                      time.Time
		invoiceID                                   *string
		isOrder                                     bool
	)
	err := s.db.QueryRow(ctx,
		`SELECT r.id, r.row_type, r.amount, COALESCE(r.booking_date, r.tx_at::date),
		        COALESCE(r.customer_name, ''), COALESCE(r.message, ''),
		        COALESCE(r.order_id, ''), r.invoice_id::text,
		        EXISTS (SELECT 1 FROM orders o WHERE o.club_id = r.club_id AND r.order_id <> '' AND o.vipps_reference = r.order_id)
		   FROM vipps_import_rows r
		  WHERE r.club_id = $1 AND r.journal_entry_id = $2`,
		clubID, d.id,
	).Scan(&rowID, &rowType, &amount, &paidOn, &customer, &message, &orderRef, &invoiceID, &isOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("loading charge for bilag %d: %w", d.number, err)
	}
	if rowType != "belastning" || isOrder {
		return nil, "", nil
	}
	if from == receivablesAccountCode {
		if invoiceID != nil {
			return nil, "", nil // tied to a faktura: post as is
		}
		// A receivable credit with no recorded faktura may still have
		// settled one: charges assigned before invoice_id existed set only
		// invoices.payment_id (migration 000106 backfills the ones the audit
		// trail proves). Rebooking it as revenue would credit the money a
		// second time while the faktura stays paid, so a human decides.
		return nil, "receivable credit with no recorded faktura — open it in Tildel, use Angre, then assign it (check whether it paid a faktura)", nil
	}

	if cfg.enabled {
		dec, err := s.decideVippsCharge(ctx, clubID, cfg, customer, message, amount, paidOn)
		if err != nil {
			return nil, "", err
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
			}, "", nil
		}
	}
	return nil, "", nil
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
