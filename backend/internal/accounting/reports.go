package accounting

import (
	"context"
	"fmt"
	"math"
)

// ── Report Types ────────────────────────────────────────────

// ReportLine represents one account's totals in a report.
type ReportLine struct {
	AccountCode string  `json:"account_code"`
	AccountName string  `json:"account_name"`
	Amount      float64 `json:"amount"`
}

// IncomeStatement is the resultatregnskap / aktivitetsregnskap.
type IncomeStatement struct {
	PeriodID string       `json:"period_id"`
	Year     int          `json:"year"`
	Revenue  []ReportLine `json:"revenue"`
	Expenses []ReportLine `json:"expenses"`
	TotalRevenue  float64 `json:"total_revenue"`
	TotalExpenses float64 `json:"total_expenses"`
	Result        float64 `json:"result"`
}

// BalanceSheet is the balanse.
type BalanceSheet struct {
	PeriodID    string       `json:"period_id"`
	Year        int          `json:"year"`
	Assets      []ReportLine `json:"assets"`
	Liabilities []ReportLine `json:"liabilities"`
	TotalAssets      float64 `json:"total_assets"`
	TotalLiabilities float64 `json:"total_liabilities"`
	IsBalanced       bool    `json:"is_balanced"`
}

// TrialBalanceLine includes both debit and credit totals.
type TrialBalanceLine struct {
	AccountCode string  `json:"account_code"`
	AccountName string  `json:"account_name"`
	Debit       float64 `json:"debit"`
	Credit      float64 `json:"credit"`
}

// TrialBalance is the saldobalanse.
type TrialBalance struct {
	PeriodID    string             `json:"period_id"`
	Year        int                `json:"year"`
	Lines       []TrialBalanceLine `json:"lines"`
	TotalDebit  float64            `json:"total_debit"`
	TotalCredit float64            `json:"total_credit"`
	IsBalanced  bool               `json:"is_balanced"`
}

// GeneralLedgerEntry is one journal entry's impact on an account.
type GeneralLedgerEntry struct {
	EntryDate   string  `json:"entry_date"`
	EntryNumber int     `json:"entry_number"`
	Description string  `json:"description"`
	Debit       float64 `json:"debit"`
	Credit      float64 `json:"credit"`
	Balance     float64 `json:"balance"`
}

// GeneralLedger is the hovedbok for a specific account.
type GeneralLedger struct {
	PeriodID    string               `json:"period_id"`
	AccountCode string               `json:"account_code"`
	AccountName string               `json:"account_name"`
	Entries     []GeneralLedgerEntry `json:"entries"`
	TotalDebit  float64              `json:"total_debit"`
	TotalCredit float64              `json:"total_credit"`
	Balance     float64              `json:"balance"`
}

// ── Queries ─────────────────────────────────────────────────

// IncomeStatement generates the resultatregnskap for a fiscal period.
// Revenue = sum of credits - debits for revenue accounts.
// Expenses = sum of debits - credits for expense accounts.
func (s *Service) IncomeStatement(ctx context.Context, clubID, periodID string) (*IncomeStatement, error) {
	var year int
	err := s.db.QueryRow(ctx, `SELECT year FROM fiscal_periods WHERE id = $1 AND club_id = $2`, periodID, clubID).Scan(&year)
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}

	rows, err := s.db.Query(ctx,
		`SELECT a.code, a.name, a.account_type,
		        COALESCE(SUM(jl.debit), 0) AS total_debit,
		        COALESCE(SUM(jl.credit), 0) AS total_credit
		 FROM journal_lines jl
		 JOIN journal_entries je ON je.id = jl.journal_entry_id
		 JOIN accounts a ON a.id = jl.account_id
		 WHERE je.club_id = $1
		   AND je.fiscal_period_id = $2
		   AND je.status = 'posted'
		   AND a.account_type IN ('revenue', 'expense')
		 GROUP BY a.code, a.name, a.account_type, a.sort_order
		 ORDER BY a.sort_order, a.code`,
		clubID, periodID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying income statement: %w", err)
	}
	defer rows.Close()

	stmt := &IncomeStatement{PeriodID: periodID, Year: year}

	for rows.Next() {
		var code, name string
		var accType AccountType
		var debit, credit float64
		if err := rows.Scan(&code, &name, &accType, &debit, &credit); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		switch accType {
		case AccountTypeRevenue:
			amount := credit - debit
			if amount != 0 {
				stmt.Revenue = append(stmt.Revenue, ReportLine{AccountCode: code, AccountName: name, Amount: amount})
				stmt.TotalRevenue += amount
			}
		case AccountTypeExpense:
			amount := debit - credit
			if amount != 0 {
				stmt.Expenses = append(stmt.Expenses, ReportLine{AccountCode: code, AccountName: name, Amount: amount})
				stmt.TotalExpenses += amount
			}
		}
	}

	if stmt.Revenue == nil {
		stmt.Revenue = []ReportLine{}
	}
	if stmt.Expenses == nil {
		stmt.Expenses = []ReportLine{}
	}
	stmt.Result = stmt.TotalRevenue - stmt.TotalExpenses
	return stmt, rows.Err()
}

// BalanceSheet generates the balanse — cumulative through the selected period.
func (s *Service) BalanceSheet(ctx context.Context, clubID, periodID string) (*BalanceSheet, error) {
	var year int
	var endDate string
	err := s.db.QueryRow(ctx,
		`SELECT year, end_date::text FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		periodID, clubID,
	).Scan(&year, &endDate)
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}

	// Sum all posted entries up to and including this period's end date
	rows, err := s.db.Query(ctx,
		`SELECT a.code, a.name, a.account_type,
		        COALESCE(SUM(jl.debit), 0) AS total_debit,
		        COALESCE(SUM(jl.credit), 0) AS total_credit
		 FROM journal_lines jl
		 JOIN journal_entries je ON je.id = jl.journal_entry_id
		 JOIN accounts a ON a.id = jl.account_id
		 JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		 WHERE je.club_id = $1
		   AND je.status = 'posted'
		   AND fp.end_date <= $2
		   AND a.account_type IN ('asset', 'liability')
		 GROUP BY a.code, a.name, a.account_type, a.sort_order
		 ORDER BY a.sort_order, a.code`,
		clubID, endDate,
	)
	if err != nil {
		return nil, fmt.Errorf("querying balance sheet: %w", err)
	}
	defer rows.Close()

	bs := &BalanceSheet{PeriodID: periodID, Year: year}

	for rows.Next() {
		var code, name string
		var accType AccountType
		var debit, credit float64
		if err := rows.Scan(&code, &name, &accType, &debit, &credit); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		switch accType {
		case AccountTypeAsset:
			amount := debit - credit // assets have debit balance
			if amount != 0 {
				bs.Assets = append(bs.Assets, ReportLine{AccountCode: code, AccountName: name, Amount: amount})
				bs.TotalAssets += amount
			}
		case AccountTypeLiability:
			amount := credit - debit // liabilities have credit balance
			if amount != 0 {
				bs.Liabilities = append(bs.Liabilities, ReportLine{AccountCode: code, AccountName: name, Amount: amount})
				bs.TotalLiabilities += amount
			}
		}
	}

	if bs.Assets == nil {
		bs.Assets = []ReportLine{}
	}
	if bs.Liabilities == nil {
		bs.Liabilities = []ReportLine{}
	}
	bs.IsBalanced = math.Abs(bs.TotalAssets-bs.TotalLiabilities) < 0.01
	return bs, rows.Err()
}

// TrialBalance generates the saldobalanse for a fiscal period.
func (s *Service) TrialBalance(ctx context.Context, clubID, periodID string) (*TrialBalance, error) {
	var year int
	err := s.db.QueryRow(ctx, `SELECT year FROM fiscal_periods WHERE id = $1 AND club_id = $2`, periodID, clubID).Scan(&year)
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}

	rows, err := s.db.Query(ctx,
		`SELECT a.code, a.name,
		        COALESCE(SUM(jl.debit), 0) AS total_debit,
		        COALESCE(SUM(jl.credit), 0) AS total_credit
		 FROM journal_lines jl
		 JOIN journal_entries je ON je.id = jl.journal_entry_id
		 JOIN accounts a ON a.id = jl.account_id
		 WHERE je.club_id = $1
		   AND je.fiscal_period_id = $2
		   AND je.status = 'posted'
		 GROUP BY a.code, a.name, a.sort_order
		 ORDER BY a.sort_order, a.code`,
		clubID, periodID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying trial balance: %w", err)
	}
	defer rows.Close()

	tb := &TrialBalance{PeriodID: periodID, Year: year}

	for rows.Next() {
		var line TrialBalanceLine
		if err := rows.Scan(&line.AccountCode, &line.AccountName, &line.Debit, &line.Credit); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		tb.Lines = append(tb.Lines, line)
		tb.TotalDebit += line.Debit
		tb.TotalCredit += line.Credit
	}

	if tb.Lines == nil {
		tb.Lines = []TrialBalanceLine{}
	}
	tb.IsBalanced = math.Abs(tb.TotalDebit-tb.TotalCredit) < 0.01
	return tb, rows.Err()
}

// GeneralLedger generates the hovedbok for a specific account in a period.
func (s *Service) GeneralLedger(ctx context.Context, clubID, periodID, accountID string) (*GeneralLedger, error) {
	var accountCode, accountName string
	err := s.db.QueryRow(ctx,
		`SELECT code, name FROM accounts WHERE id = $1 AND club_id = $2`,
		accountID, clubID,
	).Scan(&accountCode, &accountName)
	if err != nil {
		return nil, fmt.Errorf("getting account: %w", err)
	}

	rows, err := s.db.Query(ctx,
		`SELECT je.entry_date::text, je.entry_number, je.description, jl.debit, jl.credit
		 FROM journal_lines jl
		 JOIN journal_entries je ON je.id = jl.journal_entry_id
		 WHERE jl.account_id = $1
		   AND je.club_id = $2
		   AND je.fiscal_period_id = $3
		   AND je.status = 'posted'
		 ORDER BY je.entry_date, je.entry_number`,
		accountID, clubID, periodID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying general ledger: %w", err)
	}
	defer rows.Close()

	gl := &GeneralLedger{
		PeriodID:    periodID,
		AccountCode: accountCode,
		AccountName: accountName,
	}

	var runningBalance float64
	for rows.Next() {
		var e GeneralLedgerEntry
		if err := rows.Scan(&e.EntryDate, &e.EntryNumber, &e.Description, &e.Debit, &e.Credit); err != nil {
			return nil, fmt.Errorf("scanning entry: %w", err)
		}
		runningBalance += e.Debit - e.Credit
		e.Balance = runningBalance
		gl.Entries = append(gl.Entries, e)
		gl.TotalDebit += e.Debit
		gl.TotalCredit += e.Credit
	}

	if gl.Entries == nil {
		gl.Entries = []GeneralLedgerEntry{}
	}
	gl.Balance = runningBalance
	return gl, rows.Err()
}
