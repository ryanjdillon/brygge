package openapi

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// The payment overview's filter groups (BRY-522). The icon enum below must list
// paymentfilters.IconAllowList in the same order (a test checks it), and the
// SPA's icon map is typed against the generated enum, so a missing icon is a
// compile error there.

// PaymentFilterGroup is one chip of the Payments page.
type PaymentFilterGroup struct {
	Slug         string   `json:"slug" doc:"Stable id used in ?kind= links; set at create and never changed by a rename."`
	Name         *string  `json:"name" doc:"The name the board typed, shown as typed in every locale. Null on a seeded row, which shows the translation of label_key."`
	LabelKey     *string  `json:"label_key" enum:"guest,motorhome,slipping,faktura,other" doc:"Translation key of a seeded row's default name (recentPayments.kind.<key>)."`
	Icon         string   `json:"icon" enum:"sailboat,van,forklift,file-text,store,circle-dollar-sign,anchor,zap,wrench,tent,key-round,shopping-bag,coffee,ticket"`
	SortOrder    int      `json:"sort_order"`
	Enabled      bool     `json:"enabled"`
	Builtin      *string  `json:"builtin" enum:"faktura,other" doc:"faktura matches orders that pay a faktura; other is the fallback and cannot be disabled or deleted. Null for an item group."`
	PriceItemIDs []string `json:"price_item_ids" format:"uuid" nullable:"false"`
	ItemCount    int      `json:"item_count"`
}

// PaymentFilterPriceItem is a price item as the group editor lists it.
type PaymentFilterPriceItem struct {
	ID             string   `json:"id" format:"uuid"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	IsActive       bool     `json:"is_active"`
	GroupSlug      *string  `json:"group_slug" doc:"The group the item is in; null when it is in none."`
	QRLabels       []string `json:"qr_labels" nullable:"false" doc:"The active QR posters that sell the item."`
	OrderReachable bool     `json:"order_reachable" doc:"False for an item that cannot be on an order line: a category the checkout never offers, on no active QR poster and on no order line. Only a faktura bills it, so assigning it changes nothing."`
	Sold           bool     `json:"sold" doc:"An active QR poster sells it or an order line has it. Only an active, sold item in no group counts as unassigned."`
}

type PaymentFilterGroupsListResponse struct {
	Groups     []PaymentFilterGroup     `json:"groups" nullable:"false" doc:"Display order, disabled groups included, the fallback last. Built-ins the club has no row for are synthesised."`
	PriceItems []PaymentFilterPriceItem `json:"price_items" nullable:"false" doc:"Every price item of the club, archived ones included."`
	Icons      []string                 `json:"icons" nullable:"false" enum:"sailboat,van,forklift,file-text,store,circle-dollar-sign,anchor,zap,wrench,tent,key-round,shopping-bag,coffee,ticket"`
}

type PaymentFilterGroupsResponse struct {
	Groups []PaymentFilterGroup `json:"groups" nullable:"false"`
}

type PaymentFilterMove struct {
	PriceItemID string `json:"price_item_id" format:"uuid"`
	FromGroup   string `json:"from_group" doc:"Slug of the group the item left."`
}

type PaymentFilterGroupResult struct {
	Group PaymentFilterGroup  `json:"group"`
	Moved []PaymentFilterMove `json:"moved" nullable:"false" doc:"Items that left another group for this one."`
}

type CreatePaymentFilterGroupRequest struct {
	Name         string   `json:"name" minLength:"1" maxLength:"40"`
	Icon         string   `json:"icon" enum:"sailboat,van,forklift,file-text,store,circle-dollar-sign,anchor,zap,wrench,tent,key-round,shopping-bag,coffee,ticket"`
	Enabled      *bool    `json:"enabled,omitempty" doc:"Defaults to true."`
	PriceItemIDs []string `json:"price_item_ids,omitempty" format:"uuid" doc:"Items to put in the group; one that is in another group moves."`
}

type UpdatePaymentFilterGroupRequest struct {
	Name      *string `json:"name,omitempty" minLength:"1" maxLength:"40"`
	ResetName bool    `json:"reset_name,omitempty" doc:"Back to the translated default name; only a group with a label_key has one (400 no_default_name)."`
	Icon      *string `json:"icon,omitempty" enum:"sailboat,van,forklift,file-text,store,circle-dollar-sign,anchor,zap,wrench,tent,key-round,shopping-bag,coffee,ticket"`
	Enabled   *bool   `json:"enabled,omitempty"`
}

type SetPaymentFilterGroupItemsRequest struct {
	PriceItemIDs []string `json:"price_item_ids" format:"uuid" doc:"The complete item set of the group."`
}

type ReorderPaymentFilterGroupsRequest struct {
	Slugs []string `json:"slugs" doc:"Every group except the fallback, once, in the new order."`
}

func registerPaymentFilterOps(api huma.API) {
	const errors = " Refusals carry a `code`: 400 `invalid_name`, `reserved_name`, `invalid_icon`, `no_default_name`, `unknown_price_item`, `price_item_ids_required` (PUT items without the key; send [] to empty a group), `order_mismatch`; 404 `not_found`; 409 `name_taken`, `too_many_groups`, `builtin_group`."
	tags := []string{"Admin", "Payments"}

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-payment-filter-groups",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/payment-filter-groups",
		Tags:        tags,
		Summary:     "List the payment overview's filter groups, the club's price items and the icon list (any admin viewer)",
		Description: "Never writes: a club without group rows gets the faktura and other built-ins synthesised.",
		Security:    BearerSecurity,
	}, stub[struct{}, struct {
		Body PaymentFilterGroupsListResponse
	}]())

	huma.Register(api, huma.Operation{
		OperationID:   "admin-create-payment-filter-group",
		Method:        http.MethodPost,
		Path:          "/api/v1/admin/payment-filter-groups",
		Tags:          tags,
		DefaultStatus: http.StatusCreated,
		Summary:       "Create a filter group (board, harbor master, treasurer, admin)",
		Description:   "The slug comes from the name and never changes. The group goes after the existing ones, before the fallback; at most 20 groups per club." + errors,
		Security:      BearerSecurity,
	}, stub[struct {
		Body CreatePaymentFilterGroupRequest
	}, struct{ Body PaymentFilterGroupResult }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-reorder-payment-filter-groups",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/payment-filter-groups/order",
		Tags:        tags,
		Summary:     "Set the display and priority order of the groups (board, harbor master, treasurer, admin)",
		Description: "An order in two groups takes the first of them in this order." + errors,
		Security:    BearerSecurity,
	}, stub[struct {
		Body ReorderPaymentFilterGroupsRequest
	}, struct{ Body PaymentFilterGroupsResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-payment-filter-group",
		Method:      http.MethodPatch,
		Path:        "/api/v1/admin/payment-filter-groups/{slug}",
		Tags:        tags,
		Summary:     "Rename, re-icon, enable or disable a filter group (board, harbor master, treasurer, admin)",
		Description: "The fallback group cannot be disabled." + errors,
		Security:    BearerSecurity,
	}, stub[struct {
		Slug string `path:"slug"`
		Body UpdatePaymentFilterGroupRequest
	}, struct{ Body PaymentFilterGroupResult }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-set-payment-filter-group-items",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/payment-filter-groups/{slug}/items",
		Tags:        tags,
		Summary:     "Replace the price items of a filter group (board, harbor master, treasurer, admin)",
		Description: "A price item is in at most one group: an item that is in another group moves, and `moved` lists them. A built-in group has no items." + errors,
		Security:    BearerSecurity,
	}, stub[struct {
		Slug string `path:"slug"`
		Body SetPaymentFilterGroupItemsRequest
	}, struct{ Body PaymentFilterGroupResult }]())

	huma.Register(api, huma.Operation{
		OperationID:   "admin-delete-payment-filter-group",
		Method:        http.MethodDelete,
		Path:          "/api/v1/admin/payment-filter-groups/{slug}",
		Tags:          tags,
		DefaultStatus: http.StatusNoContent,
		Summary:       "Delete a filter group (board, harbor master, treasurer, admin)",
		Description:   "Its items become unassigned. The built-ins cannot be deleted." + errors,
		Security:      BearerSecurity,
	}, stub[struct {
		Slug string `path:"slug"`
	}, struct{}]())
}
