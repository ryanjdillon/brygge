// Package paymentfilters owns the payment overview's filter groups (BRY-522):
// the per-club groups of price items behind the chips of the Payments page, and
// the one rule that says which group an order is in. The overview query and the
// SMS alerts (BRY-521) both classify through here and never re-implement the
// rule. See README.md.
package paymentfilters

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
)

// The two built-in groups every club has. "faktura" holds the orders that pay a
// faktura; "other" is the fallback for everything no enabled group claims.
const (
	BuiltinFaktura = "faktura"
	BuiltinOther   = "other"
)

// Default sort positions. A seeded item group sits before faktura, new groups
// are appended before "other", which always shows last whatever its number.
const (
	sortFaktura = 40
	sortOther   = 1000
	sortStep    = 10
)

// Label keys of seeded rows: the SPA renders them with recentPayments.kind.<key>
// so the migration writes no text into club data.
const (
	LabelFaktura = "faktura"
	LabelOther   = "other"
)

// Querier is what reads need: a pool, a connection or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Group is one payment filter group. ID is empty for a built-in that the club
// has no row for yet: reads synthesise it, the first write creates it.
type Group struct {
	ID        string
	Slug      string
	Name      *string
	LabelKey  *string
	Icon      string
	SortOrder int
	Enabled   bool
	Builtin   *string
}

// IsBuiltin reports whether the group is the faktura or the other built-in.
func (g Group) IsBuiltin() bool { return g.Builtin != nil }

// isOther reports whether the group is the fallback.
func (g Group) isOther() bool { return g.Builtin != nil && *g.Builtin == BuiltinOther }

func ptr(s string) *string { return &s }

// defaultBuiltins are the rows a club without any gets, in the shape EnsureBuiltins
// inserts and ListGroups synthesises.
func defaultBuiltins() []Group {
	return []Group{
		{Slug: BuiltinFaktura, LabelKey: ptr(LabelFaktura), Icon: iconFaktura, SortOrder: sortFaktura, Enabled: true, Builtin: ptr(BuiltinFaktura)},
		{Slug: BuiltinOther, LabelKey: ptr(LabelOther), Icon: iconOther, SortOrder: sortOther, Enabled: true, Builtin: ptr(BuiltinOther)},
	}
}

const groupColumns = `id::text, slug, name, label_key, icon, sort_order, enabled, builtin`

func scanGroup(row pgx.Row) (Group, error) {
	var g Group
	err := row.Scan(&g.ID, &g.Slug, &g.Name, &g.LabelKey, &g.Icon, &g.SortOrder, &g.Enabled, &g.Builtin)
	return g, err
}

// sortForDisplay orders groups as the chips show them: by sort_order, the
// fallback last whatever its number.
func sortForDisplay(groups []Group) {
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.isOther() != b.isOther() {
			return b.isOther()
		}
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		return a.Slug < b.Slug
	})
}

// ListGroups returns the club's groups in display order. A built-in the club
// has no row for is synthesised (empty ID, default label and icon, enabled), so
// a fresh club shows the same two chips; a read never writes. Disabled groups
// are left out unless includeDisabled.
func ListGroups(ctx context.Context, q Querier, clubID string, includeDisabled bool) ([]Group, error) {
	rows, err := q.Query(ctx,
		`SELECT `+groupColumns+` FROM payment_filter_groups WHERE club_id = $1`, clubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []Group
	have := map[string]bool{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		have[g.Slug] = true
		if g.Enabled || includeDisabled {
			groups = append(groups, g)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, b := range defaultBuiltins() {
		if !have[b.Slug] {
			groups = append(groups, b)
		}
	}
	sortForDisplay(groups)
	return groups, nil
}

// ListGroupItems returns the price item ids assigned to each group, by group
// slug.
func ListGroupItems(ctx context.Context, q Querier, clubID string) (map[string][]string, error) {
	rows, err := q.Query(ctx,
		`SELECT g.slug, gi.price_item_id::text
		   FROM payment_filter_group_items gi
		   JOIN payment_filter_groups g ON g.id = gi.group_id
		  WHERE g.club_id = $1
		  ORDER BY g.slug, gi.created_at, gi.price_item_id`, clubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var slug, item string
		if err := rows.Scan(&slug, &item); err != nil {
			return nil, err
		}
		out[slug] = append(out[slug], item)
	}
	return out, rows.Err()
}

// OrderGroupLateralSQL joins the group an order is in. It expects an `orders o`
// alias in scope and yields pfg.slug and pfg.group_id (NULL group_id for a
// faktura built-in the club has no row for); callers select
// COALESCE(pfg.slug, 'other'), because "other" is only the fallback and is never
// matched.
//
// The rule: the enabled group with the lowest sort order among the item groups
// that hold any of the order's line items and the faktura built-in when the
// order has an order_sources row. A club with no faktura row is read as
// faktura enabled at the top, which cannot matter: a club with item groups has
// both built-ins (the first write creates them). It is a constant, with no
// group data in it, so a caller binds only its own arguments.
const OrderGroupLateralSQL = `
	LEFT JOIN LATERAL (
	    SELECT m.slug, m.group_id
	      FROM (
	            SELECT g.sort_order, g.id AS group_id, g.slug
	              FROM order_lines ol
	              JOIN payment_filter_group_items gi ON gi.price_item_id = ol.price_item_id
	              JOIN payment_filter_groups g ON g.id = gi.group_id AND g.enabled AND g.builtin IS NULL AND g.club_id = o.club_id
	             WHERE ol.order_id = o.id
	            UNION ALL
	            SELECT COALESCE(fg.sort_order, 0), fg.id, 'faktura'
	              FROM (SELECT 1) one
	              LEFT JOIN payment_filter_groups fg ON fg.club_id = o.club_id AND fg.builtin = 'faktura'
	             WHERE COALESCE(fg.enabled, true)
	               AND EXISTS (SELECT 1 FROM order_sources os WHERE os.order_id = o.id)
	           ) m
	     ORDER BY m.sort_order, m.slug
	     LIMIT 1) pfg ON true`

// ClassifyOrder returns the group an order is in, for callers that act on one
// order (the SMS alert on payment, BRY-521). The overview query uses
// OrderGroupLateralSQL directly; both are the same rule. A disabled or missing
// "other" row still yields the built-in.
func ClassifyOrder(ctx context.Context, q Querier, orderID string) (Group, error) {
	var clubID, slug string
	if err := q.QueryRow(ctx,
		`SELECT o.club_id::text, COALESCE(pfg.slug, 'other') FROM orders o `+OrderGroupLateralSQL+` WHERE o.id = $1`,
		orderID,
	).Scan(&clubID, &slug); err != nil {
		return Group{}, err
	}
	groups, err := ListGroups(ctx, q, clubID, false)
	if err != nil {
		return Group{}, err
	}
	for _, g := range groups {
		if g.Slug == slug {
			return g, nil
		}
	}
	return defaultBuiltins()[1], nil
}
