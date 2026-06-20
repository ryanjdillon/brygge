package accounting

import (
	"context"
	"time"
)

// VippsCustomerAlias maps a Vipps display name to a member.
type VippsCustomerAlias struct {
	ID           string    `json:"id"`
	CustomerName string    `json:"customer_name"`
	UserID       string    `json:"user_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// VippsMessageRule maps a message substring pattern to a GL account code.
type VippsMessageRule struct {
	ID          string    `json:"id"`
	Pattern     string    `json:"pattern"`
	AccountCode string    `json:"account_code"`
	Priority    int       `json:"priority"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListCustomerAliases returns all aliases for the club, newest first.
func (s *Service) ListCustomerAliases(ctx context.Context, clubID string) ([]VippsCustomerAlias, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, customer_name, user_id, created_at
		   FROM vipps_customer_aliases
		  WHERE club_id = $1
		  ORDER BY created_at DESC`,
		clubID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VippsCustomerAlias
	for rows.Next() {
		var a VippsCustomerAlias
		if err := rows.Scan(&a.ID, &a.CustomerName, &a.UserID, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// UpsertCustomerAlias creates or replaces the alias for customerName.
func (s *Service) UpsertCustomerAlias(ctx context.Context, clubID, customerName, userID string) (*VippsCustomerAlias, error) {
	var a VippsCustomerAlias
	err := s.db.QueryRow(ctx,
		`INSERT INTO vipps_customer_aliases (club_id, customer_name, user_id)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (club_id, lower(customer_name))
		 DO UPDATE SET user_id = EXCLUDED.user_id, created_at = now()
		 RETURNING id, customer_name, user_id, created_at`,
		clubID, customerName, userID,
	).Scan(&a.ID, &a.CustomerName, &a.UserID, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// DeleteCustomerAlias removes an alias by id.
func (s *Service) DeleteCustomerAlias(ctx context.Context, clubID, id string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM vipps_customer_aliases WHERE id = $1 AND club_id = $2`,
		id, clubID,
	)
	return err
}

// ListMessageRules returns all message rules for the club, highest priority first.
func (s *Service) ListMessageRules(ctx context.Context, clubID string) ([]VippsMessageRule, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, pattern, account_code, priority, created_at
		   FROM vipps_message_rules
		  WHERE club_id = $1
		  ORDER BY priority DESC, created_at DESC`,
		clubID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VippsMessageRule
	for rows.Next() {
		var r VippsMessageRule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.AccountCode, &r.Priority, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// UpsertMessageRule creates or replaces the rule for pattern.
func (s *Service) UpsertMessageRule(ctx context.Context, clubID, pattern, accountCode string, priority int) (*VippsMessageRule, error) {
	var r VippsMessageRule
	err := s.db.QueryRow(ctx,
		`INSERT INTO vipps_message_rules (club_id, pattern, account_code, priority)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (club_id, lower(pattern))
		 DO UPDATE SET account_code = EXCLUDED.account_code, priority = EXCLUDED.priority, created_at = now()
		 RETURNING id, pattern, account_code, priority, created_at`,
		clubID, pattern, accountCode, priority,
	).Scan(&r.ID, &r.Pattern, &r.AccountCode, &r.Priority, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteMessageRule removes a rule by id.
func (s *Service) DeleteMessageRule(ctx context.Context, clubID, id string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM vipps_message_rules WHERE id = $1 AND club_id = $2`,
		id, clubID,
	)
	return err
}

// matchVippsMessageRule returns the GL account code for the first (highest
// priority) rule whose pattern appears in msg as a case-insensitive substring.
// Returns "" when no rule matches.
func (s *Service) matchVippsMessageRule(ctx context.Context, clubID, msg string) string {
	if msg == "" {
		return ""
	}
	var accountCode string
	err := s.db.QueryRow(ctx,
		`SELECT account_code
		   FROM vipps_message_rules
		  WHERE club_id = $1
		    AND $2 ILIKE '%' || pattern || '%'
		  ORDER BY priority DESC
		  LIMIT 1`,
		clubID, msg,
	).Scan(&accountCode)
	if err != nil {
		return ""
	}
	return accountCode
}
