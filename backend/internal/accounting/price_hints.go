package accounting

import (
	"context"
	"fmt"
	"strings"
)

// PriceItemCategories is the price_items.category vocabulary, in the order
// the pricing admin lists it. The payment-type keys that share
// revenueAccountMap (dues, booking, merchandise) are not price-item
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
// map gives the default and invoice_lines.account_id overrides it per line.
// A category outside the price-item vocabulary, or one the map does not know,
// returns false rather than the fallback, so no hint names a guessed account.
func PriceCategoryAccount(category string) (string, bool) {
	if !isPriceItemCategory(category) {
		return "", false
	}
	return RevenueAccount(category)
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
// rule: members-only items count only when the payer is a known member, so
// for anyone else they neither make an account nor appear in its tooltip.
// The suggestion is sannsynleg when every remaining item points at one
// account and one of them fits as a single unit, else svak: a multiple of a
// per-night price is the same guess auto-assign refuses to book. The tooltip
// names the items ("3x Gjesteplass").
func priceItemAccountSuggestions(cands []priceArticleCandidate, knownMember bool, accounts map[string]suggestableAccount) []AccountSuggestion {
	var order []string
	byAccount := map[string][]priceArticleCandidate{}
	for _, c := range cands {
		if !knownMember && c.audience == "member" {
			continue
		}
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

	out := make([]AccountSuggestion, 0, len(order))
	for _, code := range order {
		a := accounts[code]
		score := priceItemScoreSpread
		if len(order) == 1 && hasSingleUnit(byAccount[code]) {
			score = priceItemScoreSingle
		}
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

func hasSingleUnit(cands []priceArticleCandidate) bool {
	for _, c := range cands {
		if c.multiplier == 1 {
			return true
		}
	}
	return false
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
