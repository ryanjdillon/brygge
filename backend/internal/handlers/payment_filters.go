package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/paymentfilters"
)

// PaymentFilterWriteRoles may change the payment overview's filter groups. The
// SPA mirrors the list as WRITE_ROLES.paymentFilters; keep the two in step.
// Chair, vice chair and secretary read the page like any admin viewer.
var PaymentFilterWriteRoles = []string{"board", "harbor_master", "treasurer", "admin"}

// mayEditPaymentFilters reports whether the roles may change filter groups.
func mayEditPaymentFilters(roles []string) bool {
	for _, r := range PaymentFilterWriteRoles {
		if slices.Contains(roles, r) {
			return true
		}
	}
	return false
}

// PaymentFilterHandler is the admin API for the payment overview's filter
// groups (BRY-522). The rule that classifies an order lives in
// internal/paymentfilters; this handler only validates, runs each write in one
// transaction and audits it.
type PaymentFilterHandler struct {
	db    *pgxpool.Pool
	audit *audit.Service
	log   zerolog.Logger
}

func NewPaymentFilterHandler(db *pgxpool.Pool, auditSvc *audit.Service, log zerolog.Logger) *PaymentFilterHandler {
	return &PaymentFilterHandler{db: db, audit: auditSvc, log: log.With().Str("handler", "payment_filters").Logger()}
}

// MountAdminRoutes registers the routes under /admin/payment-filter-groups. The
// caller mounts it inside /admin, so the 12-hour step-up applies. Reads take
// any admin viewer, writes the roles in the gate. There is no fresh-TOTP gate:
// the writes change display configuration only, like /admin/pricing (PAY-15).
func (h *PaymentFilterHandler) MountAdminRoutes(r chi.Router, roleGate func(http.Handler) http.Handler) {
	r.Route("/payment-filter-groups", func(r chi.Router) {
		r.Use(roleGate)
		r.Get("/", h.HandleList)
		r.Post("/", h.HandleCreate)
		r.Put("/order", h.HandleReorder)
		r.Patch("/{slug}", h.HandleUpdate)
		r.Put("/{slug}/items", h.HandleSetItems)
		r.Delete("/{slug}", h.HandleDelete)
	})
}

const paymentFilterGroupResource = "payment_filter_group"

type paymentFilterGroupJSON struct {
	Slug         string   `json:"slug"`
	Name         *string  `json:"name"`
	LabelKey     *string  `json:"label_key"`
	Icon         string   `json:"icon"`
	SortOrder    int      `json:"sort_order"`
	Enabled      bool     `json:"enabled"`
	Builtin      *string  `json:"builtin"`
	PriceItemIDs []string `json:"price_item_ids"`
	ItemCount    int      `json:"item_count"`
}

func groupJSON(g paymentfilters.Group, items []string) paymentFilterGroupJSON {
	if items == nil {
		items = []string{}
	}
	return paymentFilterGroupJSON{
		Slug: g.Slug, Name: g.Name, LabelKey: g.LabelKey, Icon: g.Icon, SortOrder: g.SortOrder,
		Enabled: g.Enabled, Builtin: g.Builtin, PriceItemIDs: items, ItemCount: len(items),
	}
}

type paymentFilterPriceItemJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	IsActive bool   `json:"is_active"`
	// GroupSlug is the group the item is in, null when it is in none.
	GroupSlug *string `json:"group_slug"`
	// QRLabels are the active QR posters that sell the item.
	QRLabels []string `json:"qr_labels"`
	// OrderReachable is false for an item that cannot be on an order line: its
	// category is one the checkout never offers, no active QR poster sells it and
	// no order line has it. Only a faktura bills it, so assigning it changes
	// nothing.
	OrderReachable bool `json:"order_reachable"`
	// Sold: an active QR poster sells it or an order line has it. Only an
	// active, sold, ungrouped item counts as unassigned.
	Sold bool `json:"sold"`
}

type paymentFilterMoveJSON struct {
	PriceItemID string `json:"price_item_id"`
	FromGroup   string `json:"from_group" doc:"Slug of the group the item left."`
}

type paymentFilterResultJSON struct {
	Group paymentFilterGroupJSON  `json:"group"`
	Moved []paymentFilterMoveJSON `json:"moved"`
}

func movesJSON(change paymentfilters.ItemChange) []paymentFilterMoveJSON {
	out := make([]paymentFilterMoveJSON, 0, len(change.Moved))
	for _, m := range change.Moved {
		out = append(out, paymentFilterMoveJSON{PriceItemID: m.PriceItemID, FromGroup: m.FromSlug})
	}
	return out
}

func displayName(g paymentfilters.Group) string {
	if g.Name != nil {
		return *g.Name
	}
	if g.LabelKey != nil {
		return *g.LabelKey
	}
	return g.Slug
}

// HandleList returns the club's groups (disabled ones too), its price items
// with the group each is in, and the icon allow-list. Built-ins the club has no
// row for are synthesised: a GET never writes.
func (h *PaymentFilterHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	groups, err := paymentfilters.ListGroups(ctx, h.db, claims.ClubID, true)
	var assigned map[string][]string
	if err == nil {
		assigned, err = paymentfilters.ListGroupItems(ctx, h.db, claims.ClubID)
	}
	var items []paymentfilters.PriceItem
	if err == nil {
		items, err = paymentfilters.ListPriceItems(ctx, h.db, claims.ClubID, checkoutCatalogExcludedCategories)
	}
	if err != nil {
		h.log.Error().Err(err).Msg("payment filter groups: list failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	outGroups := make([]paymentFilterGroupJSON, 0, len(groups))
	for _, g := range groups {
		outGroups = append(outGroups, groupJSON(g, assigned[g.Slug]))
	}
	outItems := make([]paymentFilterPriceItemJSON, 0, len(items))
	for _, p := range items {
		if p.QRLabels == nil {
			p.QRLabels = []string{}
		}
		outItems = append(outItems, paymentFilterPriceItemJSON{
			ID: p.ID, Name: p.Name, Category: p.Category, IsActive: p.IsActive,
			GroupSlug: p.GroupSlug, QRLabels: p.QRLabels, OrderReachable: p.Reachable, Sold: p.Sold,
		})
	}
	JSON(w, http.StatusOK, map[string]any{
		"groups":      outGroups,
		"price_items": outItems,
		"icons":       paymentfilters.IconAllowList(),
	})
}

type createPaymentFilterRequest struct {
	Name         string   `json:"name"`
	Icon         string   `json:"icon"`
	Enabled      *bool    `json:"enabled"`
	PriceItemIDs []string `json:"price_item_ids"`
}

func (h *PaymentFilterHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.claims(w, r)
	if !ok {
		return
	}
	var req createPaymentFilterRequest
	if !decodeBody(w, r, &req) {
		return
	}
	enabled := req.Enabled == nil || *req.Enabled

	var (
		g      paymentfilters.Group
		change paymentfilters.ItemChange
		items  []string
	)
	err := h.inTx(r.Context(), func(ctx context.Context, tx pgx.Tx) (err error) {
		if g, change, err = paymentfilters.Create(ctx, tx, claims.ClubID, paymentfilters.CreateInput{
			Name: req.Name, Icon: req.Icon, Enabled: enabled, PriceItemIDs: req.PriceItemIDs,
		}); err != nil {
			return err
		}
		items, err = paymentfilters.ListGroupItemsOf(ctx, tx, g.ID)
		return err
	})
	if h.fail(w, err, "create") {
		return
	}
	h.logAudit(r, claims, audit.ActionPaymentFilterGroupCreated, g.ID, map[string]any{
		"slug": g.Slug, "name": displayName(g), "icon": g.Icon, "price_item_ids": items,
		"moved_from": movedFrom(change),
	})
	h.logMovedAway(r, claims, g, change)
	JSON(w, http.StatusCreated, paymentFilterResultJSON{Group: groupJSON(g, items), Moved: movesJSON(change)})
}

type updatePaymentFilterRequest struct {
	Name      *string `json:"name"`
	ResetName bool    `json:"reset_name"`
	Icon      *string `json:"icon"`
	Enabled   *bool   `json:"enabled"`
}

func (h *PaymentFilterHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.claims(w, r)
	if !ok {
		return
	}
	var req updatePaymentFilterRequest
	if !decodeBody(w, r, &req) {
		return
	}
	slug := chi.URLParam(r, "slug")

	var (
		before, after paymentfilters.Group
		items         []string
	)
	err := h.inTx(r.Context(), func(ctx context.Context, tx pgx.Tx) (err error) {
		if before, after, err = paymentfilters.Update(ctx, tx, claims.ClubID, slug, paymentfilters.UpdateInput{
			Name: req.Name, ResetName: req.ResetName, Icon: req.Icon, Enabled: req.Enabled,
		}); err != nil {
			return err
		}
		items, err = paymentfilters.ListGroupItemsOf(ctx, tx, after.ID)
		return err
	})
	if h.fail(w, err, "update") {
		return
	}
	if details := updateDetails(before, after); len(details) > 0 {
		details["slug"] = slug
		h.logAudit(r, claims, audit.ActionPaymentFilterGroupUpdated, after.ID, details)
	}
	JSON(w, http.StatusOK, paymentFilterResultJSON{Group: groupJSON(after, items), Moved: []paymentFilterMoveJSON{}})
}

// updateDetails is the audit extra of a PATCH: the before and after of what
// changed. Empty when nothing did, which is then not audited.
func updateDetails(before, after paymentfilters.Group) map[string]any {
	d := map[string]any{}
	if displayName(before) != displayName(after) || (before.Name == nil) != (after.Name == nil) {
		d["name_before"], d["name_after"] = displayName(before), displayName(after)
	}
	if before.Icon != after.Icon {
		d["icon_before"], d["icon_after"] = before.Icon, after.Icon
	}
	if before.Enabled != after.Enabled {
		d["enabled_before"], d["enabled_after"] = before.Enabled, after.Enabled
	}
	return d
}

type setPaymentFilterItemsRequest struct {
	PriceItemIDs []string `json:"price_item_ids"`
}

func (h *PaymentFilterHandler) HandleSetItems(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.claims(w, r)
	if !ok {
		return
	}
	var req setPaymentFilterItemsRequest
	if !decodeBody(w, r, &req) {
		return
	}
	// A missing or null list is not an empty one: only an explicit [] clears
	// the group, so a client that forgot the key cannot wipe it.
	if req.PriceItemIDs == nil {
		JSON(w, http.StatusBadRequest, map[string]any{
			"error": "price_item_ids is required; send [] to empty the group",
			"code":  "price_item_ids_required",
		})
		return
	}
	var (
		g      paymentfilters.Group
		change paymentfilters.ItemChange
		items  []string
	)
	err := h.inTx(r.Context(), func(ctx context.Context, tx pgx.Tx) (err error) {
		if g, change, err = paymentfilters.SetItems(ctx, tx, claims.ClubID, chi.URLParam(r, "slug"), req.PriceItemIDs); err != nil {
			return err
		}
		items, err = paymentfilters.ListGroupItemsOf(ctx, tx, g.ID)
		return err
	})
	if h.fail(w, err, "set items") {
		return
	}
	if change.Changed() {
		h.logAudit(r, claims, audit.ActionPaymentFilterGroupItemsUpdated, g.ID, map[string]any{
			"slug": g.Slug, "added": nonNil(change.Added), "removed": nonNil(change.Removed), "moved_from": movedFrom(change),
		})
		h.logMovedAway(r, claims, g, change)
	}
	JSON(w, http.StatusOK, paymentFilterResultJSON{Group: groupJSON(g, items), Moved: movesJSON(change)})
}

// movedFrom maps each item that left another group to that group's slug.
func movedFrom(change paymentfilters.ItemChange) map[string]string {
	out := map[string]string{}
	for _, m := range change.Moved {
		out[m.PriceItemID] = m.FromSlug
	}
	return out
}

// logMovedAway gives each group an item was moved out of its own
// items_updated row, since its membership changed too.
func (h *PaymentFilterHandler) logMovedAway(r *http.Request, claims *auth.Claims, to paymentfilters.Group, change paymentfilters.ItemChange) {
	type source struct {
		slug  string
		items []string
	}
	sources := map[string]*source{}
	var order []string
	for _, m := range change.Moved {
		if sources[m.FromID] == nil {
			sources[m.FromID] = &source{slug: m.FromSlug}
			order = append(order, m.FromID)
		}
		sources[m.FromID].items = append(sources[m.FromID].items, m.PriceItemID)
	}
	for _, id := range order {
		src := sources[id]
		h.logAudit(r, claims, audit.ActionPaymentFilterGroupItemsUpdated, id, map[string]any{
			"slug": src.slug, "added": []string{}, "removed": src.items, "moved_to": to.Slug,
		})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type reorderPaymentFilterRequest struct {
	Slugs []string `json:"slugs"`
}

func (h *PaymentFilterHandler) HandleReorder(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.claims(w, r)
	if !ok {
		return
	}
	var req reorderPaymentFilterRequest
	if !decodeBody(w, r, &req) {
		return
	}
	var before, after []string
	err := h.inTx(r.Context(), func(ctx context.Context, tx pgx.Tx) (err error) {
		before, after, err = paymentfilters.Reorder(ctx, tx, claims.ClubID, req.Slugs)
		return err
	})
	if h.fail(w, err, "reorder") {
		return
	}
	if !slices.Equal(before, after) {
		h.logAudit(r, claims, audit.ActionPaymentFilterGroupsReordered, claims.ClubID, map[string]any{
			"order_before": before, "order_after": after,
		})
	}
	h.respondGroups(w, r, claims.ClubID)
}

// respondGroups answers with the club's groups in display order.
func (h *PaymentFilterHandler) respondGroups(w http.ResponseWriter, r *http.Request, clubID string) {
	ctx := r.Context()
	groups, err := paymentfilters.ListGroups(ctx, h.db, clubID, true)
	var assigned map[string][]string
	if err == nil {
		assigned, err = paymentfilters.ListGroupItems(ctx, h.db, clubID)
	}
	if err != nil {
		h.log.Error().Err(err).Msg("payment filter groups: list failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]paymentFilterGroupJSON, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupJSON(g, assigned[g.Slug]))
	}
	JSON(w, http.StatusOK, map[string]any{"groups": out})
}

func (h *PaymentFilterHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.claims(w, r)
	if !ok {
		return
	}
	var (
		g     paymentfilters.Group
		items []string
	)
	err := h.inTx(r.Context(), func(ctx context.Context, tx pgx.Tx) (err error) {
		g, items, err = paymentfilters.Delete(ctx, tx, claims.ClubID, chi.URLParam(r, "slug"))
		return err
	})
	if h.fail(w, err, "delete") {
		return
	}
	h.logAudit(r, claims, audit.ActionPaymentFilterGroupDeleted, g.ID, map[string]any{
		"slug": g.Slug, "name": displayName(g), "price_item_ids": nonNil(items),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *PaymentFilterHandler) claims(w http.ResponseWriter, r *http.Request) (*auth.Claims, bool) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	return claims, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// inTx runs fn in one transaction, rolled back when it fails.
func (h *PaymentFilterHandler) inTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// logAudit writes the audit row after the commit, so a rolled-back write
// leaves none.
func (h *PaymentFilterHandler) logAudit(r *http.Request, claims *auth.Claims, action, resourceID string, details map[string]any) {
	if h.audit == nil {
		return
	}
	h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr, action, paymentFilterGroupResource, resourceID, details)
}

// paymentFilterStatus maps a refused write's code to its HTTP status.
var paymentFilterStatus = map[string]int{
	paymentfilters.CodeNotFound:      http.StatusNotFound,
	paymentfilters.CodeBuiltinGroup:  http.StatusConflict,
	paymentfilters.CodeNameTaken:     http.StatusConflict,
	paymentfilters.CodeTooManyGroups: http.StatusConflict,
}

// fail answers a failed write and reports whether it did: a refusal with its
// code (400 unless mapped above), a duplicate name that raced past the check
// as 409, anything else as a logged 500.
func (h *PaymentFilterHandler) fail(w http.ResponseWriter, err error, what string) bool {
	if err == nil {
		return false
	}
	var refused *paymentfilters.Error
	if errors.As(err, &refused) {
		status, ok := paymentFilterStatus[refused.Code]
		if !ok {
			status = http.StatusBadRequest
		}
		JSON(w, status, map[string]any{"error": refused.Message, "code": refused.Code})
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		JSON(w, http.StatusConflict, map[string]any{"error": "another group has that name", "code": paymentfilters.CodeNameTaken})
		return true
	}
	h.log.Error().Err(err).Str("op", what).Msg("payment filter groups: write failed")
	Error(w, http.StatusInternalServerError, "internal error")
	return true
}
