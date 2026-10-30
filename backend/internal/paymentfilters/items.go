package paymentfilters

import "context"

// A price item can put an order in a group only if it can be on an order line.
// That is decided from data, not from the category alone: the checkout offers
// every category except the ones the caller passes as excluded (billing-only
// categories), but a QR poster may use any price item, and an order line that
// already references the item proves it. An item that is none of these is
// billed on fakturas only, and assigning it changes nothing.
//
// "Sold" is narrower and drives the nudge: the item is on an active QR poster
// or an order line references it, so a payment for it has happened or can.

// reachableSQL and soldSQL are SQL conditions over a `price_items pi` alias.
// $2 is the checkout-excluded category list.
const (
	onActiveTargetSQL = `EXISTS (SELECT 1 FROM harbor_payment_targets t
	                      WHERE t.price_item_id = pi.id AND t.club_id = pi.club_id AND t.archived_at IS NULL)`
	onOrderLineSQL = `EXISTS (SELECT 1 FROM order_lines ol WHERE ol.price_item_id = pi.id)`
	soldSQL        = `(` + onActiveTargetSQL + ` OR ` + onOrderLineSQL + `)`
	reachableSQL   = `(pi.category <> ALL($2::text[]) OR ` + soldSQL + `)`
)

// PriceItem is a price item as the group editor lists it.
type PriceItem struct {
	ID       string
	Name     string
	Category string
	IsActive bool
	// GroupSlug is the item group the item is in; nil when it is in none.
	GroupSlug *string
	// QRLabels are the active QR posters that sell the item.
	QRLabels []string
	// Reachable: the item can be on an order line (its category is offered by
	// the checkout, it is on an active QR poster, or an order line has it).
	Reachable bool
	// Sold: it is on an active QR poster or an order line has it.
	Sold bool
}

// ListPriceItems returns the club's price items, archived ones included (past
// payments keep classifying through them), with the group each is in.
// checkoutExcluded are the categories the checkout never offers.
func ListPriceItems(ctx context.Context, q Querier, clubID string, checkoutExcluded []string) ([]PriceItem, error) {
	rows, err := q.Query(ctx,
		`SELECT pi.id::text, pi.name, pi.category, pi.is_active, g.slug,
		        COALESCE((SELECT array_agg(DISTINCT t.label ORDER BY t.label)
		                    FROM harbor_payment_targets t
		                   WHERE t.price_item_id = pi.id AND t.club_id = pi.club_id AND t.archived_at IS NULL),
		                 '{}'),
		        `+reachableSQL+`, `+soldSQL+`
		   FROM price_items pi
		   LEFT JOIN payment_filter_group_items gi ON gi.price_item_id = pi.id
		   LEFT JOIN payment_filter_groups g ON g.id = gi.group_id
		  WHERE pi.club_id = $1
		  ORDER BY pi.is_active DESC, pi.category, pi.sort_order, pi.name, pi.id`, clubID, checkoutExcluded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PriceItem{}
	for rows.Next() {
		var p PriceItem
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.IsActive, &p.GroupSlug, &p.QRLabels, &p.Reachable, &p.Sold); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountUnassigned counts the active price items that are sold (on an active QR
// poster, or on an order line) and in no group: their payments show under
// "other". It is the overview's nudge to a person who may assign them; an item
// nobody has sold does not count, so a club that leaves others ungrouped on
// purpose is not nagged.
func CountUnassigned(ctx context.Context, q Querier, clubID string) (int, error) {
	var n int
	err := q.QueryRow(ctx,
		`SELECT count(*) FROM price_items pi
		  WHERE pi.club_id = $1 AND pi.is_active AND `+soldSQL+`
		    AND NOT EXISTS (SELECT 1 FROM payment_filter_group_items gi WHERE gi.price_item_id = pi.id)`,
		clubID).Scan(&n)
	return n, err
}
