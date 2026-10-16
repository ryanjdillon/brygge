package accounting

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// ResplitMove is one account's correction on one invoice: a positive
// amount is credited to the account (it was under-credited), a negative
// one debited (it was over-credited).
type ResplitMove struct {
	AccountCode string  `json:"account_code"`
	Amount      float64 `json:"amount"`
}

// ResplitInvoice is the correction planned or posted for one invoice.
type ResplitInvoice struct {
	InvoiceNumber int           `json:"invoice_number"`
	Moves         []ResplitMove `json:"moves"`
}

// ResplitResult reports a resplit run. In a preview nothing is posted and
// Posted is 0; Invoices and AccountTotals describe what apply would do.
type ResplitResult struct {
	Invoices       []ResplitInvoice `json:"invoices"`
	AccountTotals  []ResplitMove    `json:"account_totals"`
	Posted         int              `json:"posted"`
	SkippedDetails []SkippedItem    `json:"skipped_details"`
}

// ResplitInvoiceBilags corrects the revenue split of already-booked
// invoices without touching their entries. For each invoice in the period
// with a posted invoice_sync entry it recomputes the per-account revenue
// from the invoice's lines (invoiceRevenueCredits, the same mapping a fresh
// sync uses), subtracts what is already booked on non-receivable accounts
// by the sync entry plus any earlier resplits, and — when apply is true —
// posts one balanced invoice_resplit entry moving the difference. Booked
// entries stay immutable and a second run finds nothing to move.
func (s *Service) ResplitInvoiceBilags(ctx context.Context, clubID, periodID, actor string, apply bool) (*ResplitResult, error) {
	var status string
	if err := s.db.QueryRow(ctx,
		`SELECT status FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		periodID, clubID,
	).Scan(&status); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("fiscal period not found")
		}
		return nil, fmt.Errorf("loading period: %w", err)
	}
	if status != "open" {
		return nil, fmt.Errorf("fiscal period is %s — reopen it before correcting bilags", status)
	}

	type target struct {
		invoiceID     string
		invoiceNumber int
		total         float64
		entryDate     time.Time
	}
	rows, err := s.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.total_amount, je.entry_date
		 FROM journal_entries je
		 JOIN invoices i ON i.id::text = je.source_id AND i.club_id = je.club_id
		 WHERE je.club_id = $1 AND je.fiscal_period_id = $2
		   AND je.source = 'invoice_sync' AND je.status = 'posted'
		   AND i.status <> 'voided'
		 ORDER BY i.invoice_number`,
		clubID, periodID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying booked invoices: %w", err)
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.invoiceID, &t.invoiceNumber, &t.total, &t.entryDate); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning booked invoice: %w", err)
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := &ResplitResult{Invoices: []ResplitInvoice{}, AccountTotals: []ResplitMove{}}
	totals := map[string]float64{}

	for _, t := range targets {
		ref := fmt.Sprintf("faktura %d", t.invoiceNumber)

		expected, err := s.invoiceRevenueCredits(ctx, t.invoiceID, roundCents(t.total))
		if err != nil {
			result.SkippedDetails = append(result.SkippedDetails, SkippedItem{Ref: ref, Reason: "reading lines: " + err.Error()})
			continue
		}
		booked, err := s.bookedInvoiceRevenue(ctx, clubID, t.invoiceID)
		if err != nil {
			result.SkippedDetails = append(result.SkippedDetails, SkippedItem{Ref: ref, Reason: "reading booked revenue: " + err.Error()})
			continue
		}

		diff := map[string]float64{}
		for code, v := range expected {
			diff[code] += v
		}
		for code, v := range booked {
			diff[code] -= v
		}
		var moves []ResplitMove
		var net float64
		for code, v := range diff {
			if v = roundCents(v); v != 0 {
				moves = append(moves, ResplitMove{AccountCode: code, Amount: v})
				net += v
			}
		}
		if len(moves) == 0 {
			continue
		}
		// A reclassification only moves revenue between accounts. A
		// non-zero net means the invoice total changed after booking,
		// which needs a human, not a resplit.
		if roundCents(net) != 0 {
			result.SkippedDetails = append(result.SkippedDetails, SkippedItem{
				Ref:    ref,
				Reason: fmt.Sprintf("booked revenue differs from the faktura total by %.2f; correct it manually", net),
			})
			continue
		}
		sort.Slice(moves, func(i, j int) bool { return moves[i].AccountCode < moves[j].AccountCode })

		if apply {
			lines := make([]CreateJournalLineInput, 0, len(moves))
			for _, m := range moves {
				// Revenue credited to an account is a credit line; taking
				// it away is a debit.
				lines = append(lines, signedLine(m.AccountCode, -m.Amount))
			}
			sourceID := t.invoiceID
			sourceTable := "invoices"
			entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
				FiscalPeriodID: periodID,
				EntryDate:      t.entryDate.Format("2006-01-02"),
				Description:    fmt.Sprintf("Omklassifisering faktura #%d: inntektsfordeling per kategori", t.invoiceNumber),
				Source:         "invoice_resplit",
				SourceID:       &sourceID,
				SourceTable:    &sourceTable,
				CreatedBy:      actor,
				ClubID:         clubID,
				Lines:          lines,
			})
			if err != nil {
				result.SkippedDetails = append(result.SkippedDetails, SkippedItem{Ref: ref, Reason: err.Error()})
				continue
			}
			if err := s.PostJournalEntry(ctx, entry.ID, actor); err != nil {
				s.discardDraft(ctx, clubID, entry.ID)
				result.SkippedDetails = append(result.SkippedDetails, SkippedItem{Ref: ref, Reason: "post: " + err.Error()})
				continue
			}
			result.Posted++
		}

		result.Invoices = append(result.Invoices, ResplitInvoice{InvoiceNumber: t.invoiceNumber, Moves: moves})
		for _, m := range moves {
			totals[m.AccountCode] += m.Amount
		}
	}

	for code, v := range totals {
		if v = roundCents(v); v != 0 {
			result.AccountTotals = append(result.AccountTotals, ResplitMove{AccountCode: code, Amount: v})
		}
	}
	sort.Slice(result.AccountTotals, func(i, j int) bool {
		return result.AccountTotals[i].AccountCode < result.AccountTotals[j].AccountCode
	})
	return result, nil
}

// bookedInvoiceRevenue sums credit minus debit per non-receivable account
// over an invoice's posted invoice_sync and invoice_resplit entries.
func (s *Service) bookedInvoiceRevenue(ctx context.Context, clubID, invoiceID string) (map[string]float64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT a.code, SUM(jl.credit) - SUM(jl.debit)
		 FROM journal_entries je
		 JOIN journal_lines jl ON jl.journal_entry_id = je.id
		 JOIN accounts a ON a.id = jl.account_id
		 WHERE je.club_id = $1 AND je.source_id = $2
		   AND je.source IN ('invoice_sync', 'invoice_resplit')
		   AND je.status = 'posted'
		   AND a.code <> $3
		 GROUP BY a.code`,
		clubID, invoiceID, receivablesAccountCode,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var code string
		var net float64
		if err := rows.Scan(&code, &net); err != nil {
			return nil, err
		}
		out[code] = net
	}
	return out, rows.Err()
}
