package accounting

import (
	"context"
	"fmt"
	"math"
)

// MomskompReport holds the momskompensasjon calculation results.
type MomskompReport struct {
	PeriodID            string             `json:"period_id"`
	Year                int                `json:"year"`
	Model               string             `json:"model"` // "simplified" or "documented"
	TotalOperatingCosts float64            `json:"total_operating_costs"`
	EligibleCosts       float64            `json:"eligible_costs"`
	IneligibleCosts     float64            `json:"ineligible_costs"`
	PartialCosts        float64            `json:"partial_costs"`
	CompensationAmount  float64            `json:"compensation_amount"`
	BreakdownByAccount  []AccountBreakdown `json:"breakdown_by_account"`
	HasDraftEntries     bool               `json:"has_draft_entries"`
}

// AccountBreakdown shows per-account detail for the momskomp report.
type AccountBreakdown struct {
	AccountCode  string  `json:"account_code"`
	AccountName  string  `json:"account_name"`
	TotalAmount  float64 `json:"total_amount"`
	MVAAmount    float64 `json:"mva_amount"`
	Eligibility  string  `json:"eligibility"`
	PartialRatio float64 `json:"partial_ratio,omitempty"`
	EligiblePart float64 `json:"eligible_part"`
}

// Momskompensasjon calculates the VAT compensation report for a fiscal period.
func (s *Service) Momskompensasjon(ctx context.Context, clubID, periodID, model string) (*MomskompReport, error) {
	var year int
	err := s.db.QueryRow(ctx,
		`SELECT year FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		periodID, clubID,
	).Scan(&year)
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}

	// Check for draft entries (incomplete data warning)
	var hasDrafts bool
	err = s.db.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM journal_entries
			WHERE club_id = $1 AND fiscal_period_id = $2 AND status = 'draft'
		)`,
		clubID, periodID,
	).Scan(&hasDrafts)
	if err != nil {
		return nil, fmt.Errorf("checking drafts: %w", err)
	}

	// Query expense accounts with their totals and MVA amounts
	rows, err := s.db.Query(ctx,
		`SELECT a.code, a.name, a.mva_eligible, a.mva_partial_ratio,
		        COALESCE(SUM(jl.debit) - SUM(jl.credit), 0) AS total_amount,
		        COALESCE(SUM(jl.mva_amount), 0) AS total_mva
		 FROM accounts a
		 LEFT JOIN journal_lines jl ON jl.account_id = a.id
		 LEFT JOIN journal_entries je ON je.id = jl.journal_entry_id
		   AND je.fiscal_period_id = $2 AND je.status = 'posted'
		 WHERE a.club_id = $1
		   AND a.account_type = 'expense'
		   AND a.is_active = true
		 GROUP BY a.code, a.name, a.mva_eligible, a.mva_partial_ratio, a.sort_order
		 HAVING COALESCE(SUM(jl.debit) - SUM(jl.credit), 0) != 0
		 ORDER BY a.sort_order, a.code`,
		clubID, periodID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying expense accounts: %w", err)
	}
	defer rows.Close()

	report := &MomskompReport{
		PeriodID:        periodID,
		Year:            year,
		Model:           model,
		HasDraftEntries: hasDrafts,
	}

	for rows.Next() {
		var ab AccountBreakdown
		var eligibility MVAEligibility
		var partialRatio float64
		if err := rows.Scan(&ab.AccountCode, &ab.AccountName, &eligibility, &partialRatio,
			&ab.TotalAmount, &ab.MVAAmount); err != nil {
			return nil, fmt.Errorf("scanning account: %w", err)
		}

		ab.Eligibility = string(eligibility)
		ab.PartialRatio = partialRatio
		report.TotalOperatingCosts += ab.TotalAmount

		switch eligibility {
		case MVAEligible:
			ab.EligiblePart = ab.TotalAmount
			report.EligibleCosts += ab.TotalAmount
		case MVAIneligible:
			ab.EligiblePart = 0
			report.IneligibleCosts += ab.TotalAmount
		case MVAPartial:
			ab.EligiblePart = ab.TotalAmount * partialRatio
			report.EligibleCosts += ab.EligiblePart
			report.IneligibleCosts += ab.TotalAmount - ab.EligiblePart
			report.PartialCosts += ab.TotalAmount
		default:
			report.IneligibleCosts += ab.TotalAmount
		}

		report.BreakdownByAccount = append(report.BreakdownByAccount, ab)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if report.BreakdownByAccount == nil {
		report.BreakdownByAccount = []AccountBreakdown{}
	}

	// Calculate compensation amount based on model
	switch model {
	case "simplified":
		report.CompensationAmount = SimplifiedCompensation(report.EligibleCosts)
	case "documented":
		report.CompensationAmount = s.documentedCompensation(ctx, clubID, periodID)
	default:
		report.CompensationAmount = SimplifiedCompensation(report.EligibleCosts)
	}

	return report, nil
}

// SimplifiedCompensation calculates using the tiered percentage model:
// 8% of first 7M NOK, 6% above 7M.
func SimplifiedCompensation(eligibleCosts float64) float64 {
	const tier1Limit = 7_000_000.0
	const tier1Rate = 0.08
	const tier2Rate = 0.06

	if eligibleCosts <= 0 {
		return 0
	}

	if eligibleCosts <= tier1Limit {
		return math.Round(eligibleCosts*tier1Rate*100) / 100
	}

	tier1 := tier1Limit * tier1Rate
	tier2 := (eligibleCosts - tier1Limit) * tier2Rate
	return math.Round((tier1+tier2)*100) / 100
}

// documentedCompensation sums actual MVA amounts from eligible journal lines.
func (s *Service) documentedCompensation(ctx context.Context, clubID, periodID string) float64 {
	var total float64
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(jl.mva_amount), 0)
		 FROM journal_lines jl
		 JOIN journal_entries je ON je.id = jl.journal_entry_id
		 JOIN accounts a ON a.id = jl.account_id
		 WHERE je.club_id = $1
		   AND je.fiscal_period_id = $2
		   AND je.status = 'posted'
		   AND a.account_type = 'expense'
		   AND a.mva_eligible IN ('eligible', 'partial')`,
		clubID, periodID,
	).Scan(&total)
	if err != nil {
		s.log.Error().Err(err).Msg("failed to calculate documented compensation")
		return 0
	}
	return math.Round(total*100) / 100
}

// SaveMomskompReport stores a report snapshot in the database.
func (s *Service) SaveMomskompReport(ctx context.Context, clubID, periodID, model, generatedBy string, report *MomskompReport) (string, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO mva_compensation_reports (club_id, fiscal_period_id, model, total_operating_costs, eligible_costs, ineligible_costs, compensation_amount, generated_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (club_id, fiscal_period_id) DO UPDATE
		 SET model = $3, total_operating_costs = $4, eligible_costs = $5, ineligible_costs = $6, compensation_amount = $7, generated_by = $8, updated_at = now()
		 RETURNING id`,
		clubID, periodID, model, report.TotalOperatingCosts, report.EligibleCosts, report.IneligibleCosts, report.CompensationAmount, generatedBy,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("saving momskomp report: %w", err)
	}
	return id, nil
}

// UpdateMomskompStatus updates the report status (e.g., draft → submitted).
func (s *Service) UpdateMomskompStatus(ctx context.Context, reportID, status string) error {
	query := `UPDATE mva_compensation_reports SET status = $2, updated_at = now()`
	if status == "submitted" {
		query += `, submitted_at = now()`
	}
	query += ` WHERE id = $1`
	_, err := s.db.Exec(ctx, query, reportID, status)
	return err
}
