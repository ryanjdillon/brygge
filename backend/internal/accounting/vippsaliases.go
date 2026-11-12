package accounting

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
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

// UpsertCustomerAlias creates or replaces the alias for customerName. The
// user must be a person in the club: an alias pointing elsewhere would route
// the payer's payments to a stranger's fakturas, and one pointing at the
// system actor would route them to nobody.
func (s *Service) UpsertCustomerAlias(ctx context.Context, clubID, customerName, userID string) (*VippsCustomerAlias, error) {
	var inClub bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id::text = $1 AND club_id = $2 AND kind = 'person')`,
		userID, clubID,
	).Scan(&inClub); err != nil {
		return nil, err
	}
	if !inClub {
		return nil, ErrNotClubMember
	}
	return upsertCustomerAlias(ctx, s.db, clubID, customerName, userID)
}

func upsertCustomerAlias(ctx context.Context, q pgxQuerier, clubID, customerName, userID string) (*VippsCustomerAlias, error) {
	var a VippsCustomerAlias
	err := q.QueryRow(ctx,
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

// ErrAliasNotFound is returned for an alias id that doesn't exist or belongs
// to another club.
var ErrAliasNotFound = errors.New("alias not found")

// DeleteCustomerAlias removes an alias by id and returns the member it
// pointed at.
func (s *Service) DeleteCustomerAlias(ctx context.Context, clubID, id string) (string, error) {
	var userID string
	err := s.db.QueryRow(ctx,
		`DELETE FROM vipps_customer_aliases WHERE id::text = $1 AND club_id = $2 RETURNING user_id::text`,
		id, clubID,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrAliasNotFound
	}
	return userID, err
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

// ErrMessageRulePattern refuses a message-rule pattern that is too short to
// pick out a message, or that holds a LIKE wildcard or escape character
// (matchVippsMessageRule matches it with ILIKE, so `%` or `_` would match far
// more than the text the treasurer typed).
var ErrMessageRulePattern = errors.New("pattern must be at least 3 characters and must not contain %, _ or \\")

// ErrMessageRuleAccount refuses a message rule whose account code is not an
// active GL account of the club.
var ErrMessageRuleAccount = errors.New("account_code is not an active account of the club")

// minMessageRulePattern is the shortest pattern a message rule may have.
const minMessageRulePattern = 3

// validMessageRulePattern reports whether a trimmed pattern may be saved.
func validMessageRulePattern(pattern string) bool {
	return utf8.RuneCountInString(pattern) >= minMessageRulePattern &&
		!strings.ContainsAny(pattern, `%_\`)
}

// UpsertMessageRule creates or replaces the rule for pattern. The pattern is
// trimmed; it must pass validMessageRulePattern (ErrMessageRulePattern) and
// the account must be an active account of the club (ErrMessageRuleAccount).
func (s *Service) UpsertMessageRule(ctx context.Context, clubID, pattern, accountCode string, priority int) (*VippsMessageRule, error) {
	pattern = strings.TrimSpace(pattern)
	accountCode = strings.TrimSpace(accountCode)
	if !validMessageRulePattern(pattern) {
		return nil, ErrMessageRulePattern
	}
	var active bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM accounts WHERE club_id = $1 AND code = $2 AND is_active)`,
		clubID, accountCode,
	).Scan(&active); err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrMessageRuleAccount
	}
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

// DeleteMessageRule removes a rule by id and returns the removed rule, or
// nil when the club has no rule with that id (deleting stays idempotent). The
// id is compared as text, so a malformed id matches nothing instead of
// failing the UUID cast.
func (s *Service) DeleteMessageRule(ctx context.Context, clubID, id string) (*VippsMessageRule, error) {
	var r VippsMessageRule
	err := s.db.QueryRow(ctx,
		`DELETE FROM vipps_message_rules WHERE id::text = $1 AND club_id = $2
		 RETURNING id, pattern, account_code, priority, created_at`,
		id, clubID,
	).Scan(&r.ID, &r.Pattern, &r.AccountCode, &r.Priority, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
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
