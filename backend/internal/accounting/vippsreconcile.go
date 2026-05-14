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
	vippsFeeAccountCode      = "7700" // Bankgebyrer
	vippsClearingAccountCode = "2900" // Annen kortsiktig gjeld (holding for unresolved customers)
)

// VippsSettlementPattern extracts (settlement_number, msn) from a bank row description.
// Bank rows for Vipps payouts look like: "Utb. 2000591 Vippsnr 698382".
var VippsSettlementPattern = regexp.MustCompile(`(?i)Utb\.\s*(\d+)\s+Vippsnr\s+(\d+)`)

// VippsReconcileLine is one proposed line in the draft journal entry.
type VippsReconcileLine struct {
	VippsRowID       string  `json:"vipps_row_id,omitempty"`
	Kind             string  `json:"kind"` // bank_in | receivable | revenue | fee | clearing
	AccountCode      string  `json:"account_code"`
	Debit            float64 `json:"debit"`
	Credit           float64 `json:"credit"`
	Description      string  `json:"description"`
	CustomerName     string  `json:"customer_name,omitempty"`
	ResolvedMemberID string  `json:"resolved_member_id,omitempty"`
	Resolved         bool    `json:"resolved"`
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
	Lines            []VippsReconcileLine `json:"lines"`
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
		Lines:            []VippsReconcileLine{},
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
			memberID, _ := s.resolveCustomerToMember(ctx, clubID, v.CustomerName, v.Message)
			line := VippsReconcileLine{
				VippsRowID:   v.ID,
				Kind:         "receivable",
				AccountCode:  receivablesAccountCode,
				Credit:       v.Amount,
				Description:  fmt.Sprintf("Vipps innbetaling: %s", v.CustomerName),
				CustomerName: v.CustomerName,
			}
			if memberID != "" {
				line.ResolvedMemberID = memberID
				line.Resolved = true
			} else {
				preview.UnresolvedCount++
				line.Kind = "clearing"
				line.AccountCode = vippsClearingAccountCode
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

	return entry.ID, nil
}

// resolveCustomerToMember tries to map a Vipps customer to a member by (1) KID
// embedded in the message and (2) exact case-insensitive name match. Returns
// "" if no resolution.
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

	name := strings.TrimSpace(customerName)
	if name == "" {
		return "", nil
	}
	var memberID string
	err := s.db.QueryRow(ctx,
		`SELECT id FROM users
		 WHERE club_id = $1
		   AND lower(trim(coalesce(first_name, '') || ' ' || coalesce(last_name, ''))) = lower($2)
		 LIMIT 1`,
		clubID, name,
	).Scan(&memberID)
	if err == nil {
		return memberID, nil
	}
	return "", nil
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
