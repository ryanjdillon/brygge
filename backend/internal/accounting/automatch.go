package accounting

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// MappingRule represents an auto-categorization rule from account_mapping_rules.
type MappingRule struct {
	ID              string         `json:"id"`
	ClubID          string         `json:"club_id"`
	Name            string         `json:"name"`
	Priority        int            `json:"priority"`
	MatchField      string         `json:"match_field"`
	MatchValue      string         `json:"match_value"`
	MatchOperator   string         `json:"match_operator"`
	DebitAccountID  *string        `json:"debit_account_id"`
	CreditAccountID *string        `json:"credit_account_id"`
	DebitCode       string         `json:"debit_code,omitempty"`
	CreditCode      string         `json:"credit_code,omitempty"`
	MVAEligible     MVAEligibility `json:"mva_eligible"`
	IsActive        bool           `json:"is_active"`
}

// ListRules returns all active mapping rules for a club, sorted by priority descending.
func (s *Service) ListRules(ctx context.Context, clubID string) ([]MappingRule, error) {
	rows, err := s.db.Query(ctx,
		`SELECT r.id, r.club_id, r.name, r.priority, r.match_field, r.match_value, r.match_operator,
		        r.debit_account_id, r.credit_account_id,
		        COALESCE(da.code, ''), COALESCE(ca.code, ''),
		        r.mva_eligible, r.is_active
		 FROM account_mapping_rules r
		 LEFT JOIN accounts da ON da.id = r.debit_account_id
		 LEFT JOIN accounts ca ON ca.id = r.credit_account_id
		 WHERE r.club_id = $1
		 ORDER BY r.priority DESC, r.name`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing rules: %w", err)
	}
	defer rows.Close()

	var rules []MappingRule
	for rows.Next() {
		var r MappingRule
		if err := rows.Scan(&r.ID, &r.ClubID, &r.Name, &r.Priority, &r.MatchField, &r.MatchValue,
			&r.MatchOperator, &r.DebitAccountID, &r.CreditAccountID, &r.DebitCode, &r.CreditCode,
			&r.MVAEligible, &r.IsActive); err != nil {
			return nil, fmt.Errorf("scanning rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// CreateRuleInput is the input for creating a mapping rule.
type CreateRuleInput struct {
	Name            string
	Priority        int
	MatchField      string
	MatchValue      string
	MatchOperator   string
	DebitAccountCode  string
	CreditAccountCode string
	MVAEligible     MVAEligibility
}

// CreateRule adds a new auto-categorization rule.
func (s *Service) CreateRule(ctx context.Context, clubID string, input CreateRuleInput) (string, error) {
	// Resolve account IDs from codes
	var debitID, creditID *string
	if input.DebitAccountCode != "" {
		var id string
		err := s.db.QueryRow(ctx,
			`SELECT id FROM accounts WHERE club_id = $1 AND code = $2 AND is_active = true`,
			clubID, input.DebitAccountCode,
		).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("debit account %s not found", input.DebitAccountCode)
		}
		debitID = &id
	}
	if input.CreditAccountCode != "" {
		var id string
		err := s.db.QueryRow(ctx,
			`SELECT id FROM accounts WHERE club_id = $1 AND code = $2 AND is_active = true`,
			clubID, input.CreditAccountCode,
		).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("credit account %s not found", input.CreditAccountCode)
		}
		creditID = &id
	}

	var ruleID string
	err := s.db.QueryRow(ctx,
		`INSERT INTO account_mapping_rules (club_id, name, priority, match_field, match_value, match_operator, debit_account_id, credit_account_id, mva_eligible)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id`,
		clubID, input.Name, input.Priority, input.MatchField, input.MatchValue, input.MatchOperator,
		debitID, creditID, input.MVAEligible,
	).Scan(&ruleID)
	if err != nil {
		return "", fmt.Errorf("creating rule: %w", err)
	}
	return ruleID, nil
}

// UpdateRule modifies an existing mapping rule.
func (s *Service) UpdateRule(ctx context.Context, ruleID string, input CreateRuleInput, clubID string) error {
	var debitID, creditID *string
	if input.DebitAccountCode != "" {
		var id string
		if err := s.db.QueryRow(ctx, `SELECT id FROM accounts WHERE club_id = $1 AND code = $2`, clubID, input.DebitAccountCode).Scan(&id); err != nil {
			return fmt.Errorf("debit account %s not found", input.DebitAccountCode)
		}
		debitID = &id
	}
	if input.CreditAccountCode != "" {
		var id string
		if err := s.db.QueryRow(ctx, `SELECT id FROM accounts WHERE club_id = $1 AND code = $2`, clubID, input.CreditAccountCode).Scan(&id); err != nil {
			return fmt.Errorf("credit account %s not found", input.CreditAccountCode)
		}
		creditID = &id
	}

	_, err := s.db.Exec(ctx,
		`UPDATE account_mapping_rules
		 SET name = $2, priority = $3, match_field = $4, match_value = $5, match_operator = $6,
		     debit_account_id = $7, credit_account_id = $8, mva_eligible = $9
		 WHERE id = $1`,
		ruleID, input.Name, input.Priority, input.MatchField, input.MatchValue, input.MatchOperator,
		debitID, creditID, input.MVAEligible,
	)
	return err
}

// DeleteRule removes a mapping rule.
func (s *Service) DeleteRule(ctx context.Context, ruleID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM account_mapping_rules WHERE id = $1`, ruleID)
	return err
}

// AutoMatchImport runs all active rules against unmatched bank import rows.
// Matched rows get draft journal entries (not auto-posted — treasurer reviews).
func (s *Service) AutoMatchImport(ctx context.Context, clubID, importID, periodID, matchedBy string) (int, error) {
	rules, err := s.ListRules(ctx, clubID)
	if err != nil {
		return 0, err
	}

	// Filter to active rules only
	var activeRules []MappingRule
	for _, r := range rules {
		if r.IsActive {
			activeRules = append(activeRules, r)
		}
	}

	// Get unmatched rows
	rows, err := s.db.Query(ctx,
		`SELECT bir.id, bir.row_date, bir.description, bir.amount, bir.counterpart, bir.kid_number
		 FROM bank_import_rows bir
		 JOIN bank_imports bi ON bi.id = bir.bank_import_id
		 WHERE bi.id = $1 AND bi.club_id = $2 AND bir.journal_entry_id IS NULL
		 ORDER BY bir.row_date`,
		importID, clubID,
	)
	if err != nil {
		return 0, fmt.Errorf("querying unmatched rows: %w", err)
	}
	defer rows.Close()

	type unmatchedRow struct {
		id          string
		date        string
		description string
		amount      float64
		counterpart string
		kid         string
	}

	var unmatched []unmatchedRow
	for rows.Next() {
		var r unmatchedRow
		if err := rows.Scan(&r.id, &r.date, &r.description, &r.amount, &r.counterpart, &r.kid); err != nil {
			return 0, fmt.Errorf("scanning row: %w", err)
		}
		r.date = r.date[:10] // trim time portion
		unmatched = append(unmatched, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	matched := 0
	for _, row := range unmatched {
		for _, rule := range activeRules {
			if !ruleMatches(rule, row.description, row.counterpart, row.kid) {
				continue
			}

			// First matching rule wins
			if rule.DebitCode == "" || rule.CreditCode == "" {
				break
			}

			debit := row.amount
			credit := row.amount
			if row.amount < 0 {
				debit = -row.amount
				credit = -row.amount
			}

			sourceID := row.id
			sourceTable := "bank_import_rows"
			entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
				FiscalPeriodID: periodID,
				EntryDate:      row.date,
				Description:    row.description,
				Source:         "bank_import",
				SourceID:       &sourceID,
				SourceTable:    &sourceTable,
				CreatedBy:      matchedBy,
				ClubID:         clubID,
				Lines: []CreateJournalLineInput{
					{AccountCode: rule.DebitCode, Debit: debit, Credit: 0},
					{AccountCode: rule.CreditCode, Debit: 0, Credit: credit},
				},
			})
			if err != nil {
				s.log.Error().Err(err).Str("row_id", row.id).Msg("auto-match failed to create entry")
				break
			}

			s.db.Exec(ctx,
				`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true WHERE id = $2`,
				entry.ID, row.id,
			)
			matched++
			break
		}
	}

	return matched, nil
}

// ruleMatches evaluates a single rule against a bank row's fields.
func ruleMatches(rule MappingRule, description, counterpart, kid string) bool {
	var fieldValue string
	switch rule.MatchField {
	case "description":
		fieldValue = description
	case "counterpart":
		fieldValue = counterpart
	case "kid_prefix":
		fieldValue = kid
	default:
		return false
	}

	fieldValue = strings.ToLower(fieldValue)
	matchValue := strings.ToLower(rule.MatchValue)

	switch rule.MatchOperator {
	case "eq":
		return fieldValue == matchValue
	case "like":
		return likeMatch(fieldValue, matchValue)
	case "regex":
		re, err := regexp.Compile("(?i)" + rule.MatchValue)
		if err != nil {
			return false
		}
		return re.MatchString(fieldValue)
	default:
		return false
	}
}

// likeMatch implements SQL LIKE semantics with % wildcards.
func likeMatch(value, pattern string) bool {
	if pattern == "%" {
		return true
	}
	if strings.HasPrefix(pattern, "%") && strings.HasSuffix(pattern, "%") {
		return strings.Contains(value, pattern[1:len(pattern)-1])
	}
	if strings.HasPrefix(pattern, "%") {
		return strings.HasSuffix(value, pattern[1:])
	}
	if strings.HasSuffix(pattern, "%") {
		return strings.HasPrefix(value, pattern[:len(pattern)-1])
	}
	return value == pattern
}
