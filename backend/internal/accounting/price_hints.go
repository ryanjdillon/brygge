package accounting

import (
	"context"
	"fmt"
	"strings"
)

// PriceItemCategories is the price_items.category vocabulary, in the order
// the pricing admin lists it. The payment-type keys that share
// paymentTypeAccountMap (dues, booking, merchandise) are not price-item
// categories and are left out.
var PriceItemCategories = []string{
	"membership",
	"harbor_membership",
	"slip_fee",
	"electricity",
	"seasonal_rental",
	"guest",
	"motorhome",
	"room_hire",
	"service",
	"other",
}

// PriceCategoryAccount returns the default revenue account a price-item
// category books to. Price items carry no account of their own: the category
// maps give the default and invoice_lines.account_id overrides it per line.
// The Vipps map is consulted first, then the invoice map; the two agree on
// every key they share (TestCategoryMapsAgree). A category neither map knows
// returns false rather than a guessed fallback.
func PriceCategoryAccount(category string) (string, bool) {
	if !isPriceItemCategory(category) {
		return "", false
	}
	if code, ok := vippsCategoryAccountMap[category]; ok {
		return code, true
	}
	code, ok := paymentTypeAccountMap[category]
	return code, ok
}

func isPriceItemCategory(category string) bool {
	for _, c := range PriceItemCategories {
		if c == category {
			return true
		}
	}
	return false
}

// AccountPriceItem names one active price item that books to an account by
// default. Names only: the accounts list is readable by every board role, so
// it carries no more than the public price list shows.
type AccountPriceItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// AccountWithPriceHints is an account plus what books to it by default, so
// a picker can show "3300 — Slipping, …" and find the account by item name.
type AccountWithPriceHints struct {
	Account
	PriceItems      []AccountPriceItem `json:"price_items"`
	PriceCategories []string           `json:"price_categories"`
}

// ListAccountsWithPriceHints returns the active accounts, each with the
// price-item categories that map to it and the club's active price items in
// those categories (ordered by sort_order then name, one entry per name: a
// member and a non-member price share a name).
func (s *Service) ListAccountsWithPriceHints(ctx context.Context, clubID string) ([]AccountWithPriceHints, error) {
	accounts, err := s.ListAccounts(ctx, clubID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx,
		`SELECT id, name, category
		   FROM price_items
		  WHERE club_id = $1 AND is_active = TRUE
		  ORDER BY sort_order, name`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing price items: %w", err)
	}
	defer rows.Close()

	items := map[string][]AccountPriceItem{}
	seen := map[string]bool{}
	for rows.Next() {
		var it AccountPriceItem
		if err := rows.Scan(&it.ID, &it.Name, &it.Category); err != nil {
			return nil, fmt.Errorf("scanning price item: %w", err)
		}
		code, ok := PriceCategoryAccount(it.Category)
		if !ok {
			continue
		}
		key := code + "\x00" + strings.ToLower(strings.TrimSpace(it.Name))
		if seen[key] {
			continue
		}
		seen[key] = true
		items[code] = append(items[code], it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating price items: %w", err)
	}

	categories := map[string][]string{}
	for _, c := range PriceItemCategories {
		if code, ok := PriceCategoryAccount(c); ok {
			categories[code] = append(categories[code], c)
		}
	}

	out := make([]AccountWithPriceHints, 0, len(accounts))
	for _, a := range accounts {
		h := AccountWithPriceHints{
			Account:         a,
			PriceItems:      items[a.Code],
			PriceCategories: categories[a.Code],
		}
		if h.PriceItems == nil {
			h.PriceItems = []AccountPriceItem{}
		}
		if h.PriceCategories == nil {
			h.PriceCategories = []string{}
		}
		out = append(out, h)
	}
	return out, nil
}

// suggestableAccount is an active account a suggestion may name.
type suggestableAccount struct {
	code, name, kind string
}

// Scores for the price_item pass: one account for every fitting price item
// is a likely match; a spread across accounts is only a hint.
const (
	priceItemScoreSingle = 60
	priceItemScoreSpread = 20
	priceItemMaxLabels   = 3

	suggestionReasonPriceItem = "price_item"
)

// priceItemAccountSuggestions turns the price items an amount fits
// (priceArticleCandidates) into account suggestions, one per default
// account, in the candidates' order. It follows the auto-assign audience
// rule: an account whose fitting items are all members-only is offered only
// when the payer is a known member. The suggestion is sannsynleg when every
// remaining item points at one account, else svak; the tooltip names the
// items ("3x Gjesteplass").
func priceItemAccountSuggestions(cands []priceArticleCandidate, knownMember bool, accounts map[string]suggestableAccount) []AccountSuggestion {
	var order []string
	byAccount := map[string][]priceArticleCandidate{}
	for _, c := range cands {
		code, ok := PriceCategoryAccount(c.category)
		if !ok {
			continue
		}
		if _, ok := accounts[code]; !ok {
			continue
		}
		if _, ok := byAccount[code]; !ok {
			order = append(order, code)
		}
		byAccount[code] = append(byAccount[code], c)
	}

	var kept []string
	for _, code := range order {
		if !knownMember && allMembersOnly(byAccount[code]) {
			continue
		}
		kept = append(kept, code)
	}

	score := priceItemScoreSpread
	if len(kept) == 1 {
		score = priceItemScoreSingle
	}
	out := make([]AccountSuggestion, 0, len(kept))
	for _, code := range kept {
		a := accounts[code]
		out = append(out, AccountSuggestion{
			Code:            a.code,
			Name:            a.name,
			AccountType:     a.kind,
			Score:           score,
			ConfidenceLabel: confidenceLabel(score),
			WhyTooltip:      priceItemLabels(byAccount[code]),
			Reason:          suggestionReasonPriceItem,
		})
	}
	return out
}

func allMembersOnly(cands []priceArticleCandidate) bool {
	for _, c := range cands {
		if c.audience != "member" {
			return false
		}
	}
	return len(cands) > 0
}

func priceItemLabels(cands []priceArticleCandidate) string {
	labels := make([]string, 0, priceItemMaxLabels)
	seen := map[string]bool{}
	for _, c := range cands {
		l := c.label()
		if seen[l] {
			continue
		}
		seen[l] = true
		labels = append(labels, l)
	}
	if len(labels) > priceItemMaxLabels {
		return strings.Join(labels[:priceItemMaxLabels], ", ") + ", …"
	}
	return strings.Join(labels, ", ")
}

// payerIsKnownMember reports whether a payer name resolves to a member on
// one of the strong passes, the bar auto-assign sets for members-only prices.
func (s *Service) payerIsKnownMember(ctx context.Context, clubID, payer, message string) bool {
	memberID, pass, err := s.resolveCustomerToMemberPass(ctx, clubID, payer, message)
	return err == nil && memberID != "" && pass <= memberPassFirstLast
}
