package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/roles"
)

// Role-based price exemptions (BRY-351). A club marks a price item, or a
// whole price category, as exempt for a board role. When a faktura is
// generated for a member who holds that role, the item is billed at full
// price and an offsetting "Fritak – <role>" line brings it to zero. The
// exemption line carries the configured expense account, so GL sync books
// the waived revenue gross (CR the item's revenue, DR the expense account)
// and a fully exempt faktura never becomes a receivable. Nothing is exempt
// until a club configures it.

// exemptionRule is one configured (role, target) pair. Exactly one of
// Category and PriceItemID is set.
type exemptionRule struct {
	Role        string  `json:"role"`
	Category    *string `json:"category,omitempty"`
	PriceItemID *string `json:"price_item_id,omitempty"`
}

// exemptionConfig is a club's exemption setup as the billing paths use it.
type exemptionConfig struct {
	rules              []exemptionRule
	expenseAccountCode string
	// expenseAccountID is empty when no account is configured or the code
	// no longer resolves to an active account; exemptions are then not
	// applied and callers surface a warning instead.
	expenseAccountID string
}

// active reports whether exemptions can be applied at all.
func (c *exemptionConfig) active() bool {
	return c != nil && len(c.rules) > 0 && c.expenseAccountID != ""
}

// misconfigured reports rules that exist but cannot be applied for want of
// an expense account, so the billing paths can say so rather than silently
// billing members who should have been exempt.
func (c *exemptionConfig) misconfigured() bool {
	return c != nil && len(c.rules) > 0 && c.expenseAccountID == ""
}

// match returns the role that exempts a line for a member holding
// userRoles, checking roles in the canonical board order so the result is
// deterministic when a member holds several exempting roles.
func (c *exemptionConfig) match(userRoles map[string]bool, category, priceItemID string) (string, bool) {
	if !c.active() {
		return "", false
	}
	for _, role := range roles.Board {
		if !userRoles[role] {
			continue
		}
		for _, r := range c.rules {
			if r.Role != role {
				continue
			}
			if r.Category != nil && category != "" && *r.Category == category {
				return role, true
			}
			if r.PriceItemID != nil && priceItemID != "" && *r.PriceItemID == priceItemID {
				return role, true
			}
		}
	}
	return "", false
}

// exemptionLineDescription is what the offsetting line says on the faktura.
func exemptionLineDescription(role string) string {
	return "Fritak – " + roles.BoardLabelNB(role)
}

// loadExemptionConfig reads a club's rules and resolves the expense account.
func loadExemptionConfig(ctx context.Context, db dbQuerier, clubID string) (*exemptionConfig, error) {
	cfg := &exemptionConfig{}
	var code *string
	if err := db.QueryRow(ctx,
		`SELECT exemption_expense_account FROM clubs WHERE id = $1`, clubID,
	).Scan(&code); err != nil {
		return nil, fmt.Errorf("loading exemption account: %w", err)
	}
	if code != nil {
		cfg.expenseAccountCode = *code
		var id string
		err := db.QueryRow(ctx,
			`SELECT id FROM accounts
			  WHERE club_id = $1 AND code = $2 AND is_active AND account_type = 'expense'`,
			clubID, *code,
		).Scan(&id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("resolving exemption account: %w", err)
		}
		cfg.expenseAccountID = id
	}

	rows, err := db.Query(ctx,
		`SELECT role, category, price_item_id::text
		   FROM price_exemptions
		  WHERE club_id = $1
		  ORDER BY role, category NULLS LAST, price_item_id`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading exemptions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r exemptionRule
		if err := rows.Scan(&r.Role, &r.Category, &r.PriceItemID); err != nil {
			return nil, fmt.Errorf("scanning exemption: %w", err)
		}
		cfg.rules = append(cfg.rules, r)
	}
	return cfg, rows.Err()
}

// memberRoles returns the set of roles a member holds in the club.
func memberRoles(ctx context.Context, db dbQuerier, clubID, userID string) (map[string]bool, error) {
	out := map[string]bool{}
	if userID == "" {
		return out, nil
	}
	rows, err := db.Query(ctx,
		`SELECT role::text FROM user_roles WHERE club_id = $1 AND user_id = $2`,
		clubID, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading member roles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out[r] = true
	}
	return out, rows.Err()
}

// dbQuerier is the subset of pgx used here, so the helpers work on a pool
// or a transaction.
type dbQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// exemptionsResponse is the settings payload.
type exemptionsResponse struct {
	ExpenseAccount string          `json:"expense_account"`
	Rules          []exemptionRule `json:"rules"`
	// BoardRoles is the canonical set the rules may use, in display order.
	BoardRoles []string `json:"board_roles"`
}

// HandleGetExemptions returns the club's exemption configuration.
func (h *InvoiceHandler) HandleGetExemptions(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	cfg, err := loadExemptionConfig(r.Context(), h.db, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("load exemptions")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	rules := cfg.rules
	if rules == nil {
		rules = []exemptionRule{}
	}
	JSON(w, http.StatusOK, exemptionsResponse{
		ExpenseAccount: cfg.expenseAccountCode,
		Rules:          rules,
		BoardRoles:     roles.Board,
	})
}

type putExemptionsRequest struct {
	ExpenseAccount string          `json:"expense_account"`
	Rules          []exemptionRule `json:"rules"`
}

// HandlePutExemptions replaces the club's exemption configuration. It
// changes what members are billed, so the route sits behind the money gate
// (treasurer/admin + fresh TOTP) and every change is audited.
func (h *InvoiceHandler) HandlePutExemptions(w http.ResponseWriter, r *http.Request) { //nolint:gocognit,cyclop // validates and replaces the exemption set in one transaction; split tracked in BRY-424
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req putExemptionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ExpenseAccount = strings.TrimSpace(req.ExpenseAccount)

	seen := map[string]bool{}
	for _, rule := range req.Rules {
		if !roles.IsBoard(rule.Role) {
			Error(w, http.StatusBadRequest, fmt.Sprintf("%q is not a board role", rule.Role))
			return
		}
		hasCat := rule.Category != nil && *rule.Category != ""
		hasItem := rule.PriceItemID != nil && *rule.PriceItemID != ""
		if hasCat == hasItem {
			Error(w, http.StatusBadRequest, "each exemption needs exactly one of category or price_item_id")
			return
		}
		key := rule.Role + "|"
		if hasCat {
			if !validCategories[*rule.Category] {
				Error(w, http.StatusBadRequest, fmt.Sprintf("%q is not a price category", *rule.Category))
				return
			}
			key += "c:" + *rule.Category
		} else {
			var exists bool
			if err := h.db.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM price_items WHERE id = $1 AND club_id = $2)`,
				*rule.PriceItemID, claims.ClubID,
			).Scan(&exists); err != nil || !exists {
				Error(w, http.StatusBadRequest, "price item not found")
				return
			}
			key += "i:" + *rule.PriceItemID
		}
		if seen[key] {
			Error(w, http.StatusBadRequest, "duplicate exemption")
			return
		}
		seen[key] = true
	}
	if len(req.Rules) > 0 && req.ExpenseAccount == "" {
		Error(w, http.StatusBadRequest, "choose the expense account exemptions are booked to")
		return
	}
	// Any account that is stored must be valid, even with no rules yet, so
	// a later rule never inherits a bogus account.
	if req.ExpenseAccount != "" {
		var ok bool
		if err := h.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM accounts
			                 WHERE club_id = $1 AND code = $2 AND is_active AND account_type = 'expense')`,
			claims.ClubID, req.ExpenseAccount,
		).Scan(&ok); err != nil || !ok {
			Error(w, http.StatusBadRequest, "expense_account must be an active expense account in the chart of accounts")
			return
		}
	}

	before, err := loadExemptionConfig(ctx, h.db, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("load exemptions before update")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM price_exemptions WHERE club_id = $1`, claims.ClubID); err != nil {
		h.log.Error().Err(err).Msg("clear exemptions")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, rule := range req.Rules {
		var cat, item any
		if rule.Category != nil && *rule.Category != "" {
			cat = *rule.Category
		} else {
			item = *rule.PriceItemID
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO price_exemptions (club_id, role, category, price_item_id, created_by)
			 VALUES ($1, $2, $3, $4, $5)`,
			claims.ClubID, rule.Role, cat, item, claims.UserID,
		); err != nil {
			h.log.Error().Err(err).Msg("insert exemption")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	var account any
	if req.ExpenseAccount != "" {
		account = req.ExpenseAccount
	}
	if _, err := tx.Exec(ctx,
		`UPDATE clubs SET exemption_expense_account = $2 WHERE id = $1`,
		claims.ClubID, account,
	); err != nil {
		h.log.Error().Err(err).Msg("update exemption account")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionPricingExemptionsUpdated, "price_exemptions", claims.ClubID,
			map[string]any{
				"rules_before":           before.rules,
				"rules_after":            req.Rules,
				"expense_account_before": before.expenseAccountCode,
				"expense_account_after":  req.ExpenseAccount,
			})
	}

	rules := req.Rules
	if rules == nil {
		rules = []exemptionRule{}
	}
	JSON(w, http.StatusOK, exemptionsResponse{
		ExpenseAccount: req.ExpenseAccount,
		Rules:          rules,
		BoardRoles:     roles.Board,
	})
}
