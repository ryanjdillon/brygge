package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/paymentfilters"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// pfGroup is a group as the admin API returns it.
type pfGroup struct {
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

type pfResult struct {
	Group pfGroup `json:"group"`
	Moved []struct {
		PriceItemID string `json:"price_item_id"`
		FromGroup   string `json:"from_group"`
	} `json:"moved"`
}

type pfEnv struct {
	*recentPaymentsEnv
	router http.Handler
	userID string
}

func mustClaims(clubID string, roles ...string) *auth.Claims {
	return &auth.Claims{UserID: "00000000-0000-0000-0000-00000000000a", ClubID: clubID, Roles: roles}
}

// newPFEnv is a club created after the migrations, so it has no group rows:
// the path a fresh deployment takes.
func newPFEnv(t *testing.T) *pfEnv {
	t.Helper()
	e := newRecentPaymentsEnv(t)
	h := NewPaymentFilterHandler(e.pool, audit.NewService(e.pool, zerolog.Nop()), zerolog.Nop())
	r := chi.NewRouter()
	r.Route("/admin", func(r chi.Router) {
		r.Use(testAuthMiddleware)
		h.MountAdminRoutes(r, middleware.RequireRoleOrBoardRead(PaymentFilterWriteRoles...))
	})
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"admin"})
	return &pfEnv{recentPaymentsEnv: e, router: r, userID: userID}
}

func (e *pfEnv) call(t *testing.T, method, path string, body any, roles ...string) *httptest.ResponseRecorder {
	t.Helper()
	if len(roles) == 0 {
		roles = []string{"admin"}
	}
	var rdr *bytes.Reader
	switch b := body.(type) {
	case nil:
		rdr = bytes.NewReader(nil)
	case string:
		rdr = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/admin/payment-filter-groups"+path, rdr)
	req.Header.Set("Authorization", authHeader(generateTestToken(e.userID, e.clubID, roles)))
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// ok runs a call that must succeed with want and decodes the result.
func (e *pfEnv) ok(t *testing.T, want int, method, path string, body any) pfResult {
	t.Helper()
	rec := e.call(t, method, path, body)
	if rec.Code != want {
		t.Fatalf("%s %s: status %d (%s), want %d", method, path, rec.Code, rec.Body.String(), want)
	}
	var res pfResult
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
	}
	return res
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Code
}

type pfList struct {
	Groups     []pfGroup `json:"groups"`
	PriceItems []struct {
		ID             string   `json:"id"`
		Category       string   `json:"category"`
		IsActive       bool     `json:"is_active"`
		GroupSlug      *string  `json:"group_slug"`
		QRLabels       []string `json:"qr_labels"`
		OrderReachable bool     `json:"order_reachable"`
		Sold           bool     `json:"sold"`
	} `json:"price_items"`
	Icons []string `json:"icons"`
}

func (e *pfEnv) list(t *testing.T) pfList {
	t.Helper()
	rec := e.call(t, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d (%s)", rec.Code, rec.Body.String())
	}
	var l pfList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l pfList) slugs() []string {
	out := make([]string, len(l.Groups))
	for i, g := range l.Groups {
		out[i] = g.Slug
	}
	return out
}

func (e *pfEnv) item(t *testing.T, category, name string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO price_items (club_id, category, name, amount) VALUES ($1, $2, $3, 100) RETURNING id::text`,
		e.clubID, category, name).Scan(&id); err != nil {
		t.Fatalf("insert price item: %v", err)
	}
	return id
}

// order is a paid order with one line per item.
func (e *pfEnv) order(t *testing.T, ageMin int, items ...string) string {
	t.Helper()
	id := e.paidOrder(t, "vipps", ageMin)
	for _, it := range items {
		if _, err := e.pool.Exec(context.Background(),
			`INSERT INTO order_lines (order_id, price_item_id, name, unit_price, total_price) VALUES ($1, $2, 'Synthetic line', 100, 100)`,
			id, it); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// ids lists the orders the overview returns for a kind filter, newest first.
func (e *pfEnv) ids(t *testing.T, kind string) []string {
	t.Helper()
	return recentIDs(e.get(t, "kind="+kind))
}

func (e *pfEnv) kindOf(t *testing.T, orderID string) string {
	t.Helper()
	for _, p := range e.get(t, "") {
		if p.ID == orderID {
			return p.Kind
		}
	}
	t.Fatalf("order %s not in the overview", orderID)
	return ""
}

func (e *pfEnv) auditCount(t *testing.T, action string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE club_id = $1 AND action = $2`, e.clubID, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sameIDs(got []string, want ...string) bool {
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	return slices.Equal(g, w)
}

// An admin creates a Slipping group, assigns the slipping item, and the
// overview filters by it; no code change. Priority is by sort order, and
// disabling or deleting a group falls through to the next match.
func TestPaymentFilterGroupsAcceptance(t *testing.T) {
	e := newPFEnv(t)
	slipItem := e.item(t, "service", "Synthetic haul-out small")
	guestItem := e.item(t, "guest", "Synthetic guest night")
	craneItem := e.item(t, "service", "Synthetic crane hire")
	slipOrder := e.order(t, 10, slipItem)
	guestSlip := e.order(t, 20, guestItem, slipItem)
	plain := e.order(t, 30, craneItem)

	if k := e.kindOf(t, slipOrder); k != "other" {
		t.Fatalf("before any group, kind = %q, want other", k)
	}

	created := e.ok(t, http.StatusCreated, http.MethodPost, "/",
		map[string]any{"name": "Slipping", "icon": "forklift", "price_item_ids": []string{slipItem}})
	if created.Group.Slug != "slipping" || created.Group.ItemCount != 1 || len(created.Moved) != 0 {
		t.Fatalf("created = %+v", created)
	}
	if got := e.ids(t, "slipping"); !sameIDs(got, slipOrder, guestSlip) {
		t.Errorf("slipping = %v, want the slipping order and the guest order that also holds the item", got)
	}
	if k := e.kindOf(t, plain); k != "other" {
		t.Errorf("an unassigned item's order = %q, want other", k)
	}

	// A guest group after it: Slipping is earlier, so it wins the shared order.
	e.ok(t, http.StatusCreated, http.MethodPost, "/",
		map[string]any{"name": "Gjest", "icon": "sailboat", "price_item_ids": []string{guestItem}})
	if k := e.kindOf(t, guestSlip); k != "slipping" {
		t.Errorf("guest + slipping with slipping first = %q, want slipping", k)
	}
	rec := e.call(t, http.MethodPut, "/order", map[string]any{"slugs": []string{"gjest", "slipping", "faktura"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}
	if k := e.kindOf(t, guestSlip); k != "gjest" {
		t.Errorf("guest + slipping with guest first = %q, want gjest", k)
	}
	if got := e.ids(t, "slipping"); !sameIDs(got, slipOrder) {
		t.Errorf("slipping after reorder = %v, want only %s", got, slipOrder)
	}

	// Disabled: not a chip, not matched; the order falls to the next match.
	e.ok(t, http.StatusOK, http.MethodPatch, "/slipping", map[string]any{"enabled": false})
	if k := e.kindOf(t, slipOrder); k != "other" {
		t.Errorf("order of a disabled group = %q, want other", k)
	}
	if k := e.kindOf(t, guestSlip); k != "gjest" {
		t.Errorf("shared order with slipping disabled = %q, want gjest", k)
	}
	body := e.call(t, http.MethodGet, "/", nil).Body.String()
	if !strings.Contains(body, `"slug":"slipping"`) {
		t.Error("the admin list must still show a disabled group")
	}
	e.ok(t, http.StatusOK, http.MethodPatch, "/slipping", map[string]any{"enabled": true})

	// Moving the slipping item into Gjest takes it out of Slipping.
	moved := e.ok(t, http.StatusOK, http.MethodPut, "/gjest/items", map[string]any{"price_item_ids": []string{guestItem, slipItem}})
	if len(moved.Moved) != 1 || moved.Moved[0].PriceItemID != slipItem || moved.Moved[0].FromGroup != "slipping" {
		t.Errorf("moved = %+v, want the slipping item from slipping", moved.Moved)
	}
	if k := e.kindOf(t, slipOrder); k != "gjest" {
		t.Errorf("slipping order after the move = %q, want gjest", k)
	}
	items := e.auditCount(t, audit.ActionPaymentFilterGroupItemsUpdated)
	e.ok(t, http.StatusOK, http.MethodPut, "/gjest/items", map[string]any{"price_item_ids": []string{guestItem, slipItem}})
	if got := e.auditCount(t, audit.ActionPaymentFilterGroupItemsUpdated); got != items {
		t.Errorf("a no-op item write was audited (%d -> %d)", items, got)
	}

	// Deleting a group unassigns its items.
	if rec := e.call(t, http.MethodDelete, "/gjest", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	l := e.list(t)
	for _, it := range l.PriceItems {
		if it.ID == slipItem || it.ID == guestItem {
			if it.GroupSlug != nil {
				t.Errorf("item %s still in group %s after the delete", it.ID, *it.GroupSlug)
			}
		}
	}
	for _, id := range []string{slipOrder, guestSlip, plain} {
		if k := e.kindOf(t, id); k != "other" {
			t.Errorf("order %s after the delete = %q, want other", id, k)
		}
	}

	for action, want := range map[string]int{
		audit.ActionPaymentFilterGroupCreated:      2,
		audit.ActionPaymentFilterGroupUpdated:      2,
		audit.ActionPaymentFilterGroupItemsUpdated: 2, // the move: the group it went to and the one it left
		audit.ActionPaymentFilterGroupsReordered:   1,
		audit.ActionPaymentFilterGroupDeleted:      1,
	} {
		if got := e.auditCount(t, action); got != want {
			t.Errorf("%s audited %d times, want %d", action, got, want)
		}
	}
}

// A club created after the migrations has no group rows. It still classifies
// faktura and other, reads synthesise the two built-ins without writing, and
// the first write creates them.
func TestPaymentFilterGroupsFreshClub(t *testing.T) {
	e := newPFEnv(t)
	ctx := context.Background()
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	fakturaOrder := e.order(t, 10)
	addOrderSource(t, e.pool, fakturaOrder, seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 5101))
	plain := e.order(t, 20, e.item(t, "service", "Synthetic crane hire"))

	if e.kindOf(t, fakturaOrder) != "faktura" || e.kindOf(t, plain) != "other" {
		t.Fatalf("a club without rows must classify faktura and other")
	}
	l := e.list(t)
	if strings.Join(l.slugs(), ",") != "faktura,other" {
		t.Fatalf("groups = %v, want faktura and other", l.slugs())
	}
	if len(l.Icons) == 0 || !slices.Equal(l.Icons, paymentfilters.IconAllowList()) {
		t.Errorf("icons = %v, want the allow-list", l.Icons)
	}
	count := func() int {
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM payment_filter_groups WHERE club_id = $1`, e.clubID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 0 {
		t.Fatal("a read created rows")
	}

	// The built-ins are addressable by slug on the first write.
	e.ok(t, http.StatusOK, http.MethodPatch, "/faktura", map[string]any{"name": "Fakturabetaling"})
	if count() != 2 {
		t.Errorf("first write left %d rows, want the two built-ins", count())
	}
	e.ok(t, http.StatusOK, http.MethodPatch, "/faktura", map[string]any{"reset_name": true})
	if g := e.list(t).Groups[0]; g.Name != nil || g.LabelKey == nil {
		t.Errorf("faktura after reset = %+v, want the default name back", g)
	}

	// Disabled, a faktura built-in is skipped: its orders fall to other.
	e.ok(t, http.StatusOK, http.MethodPatch, "/faktura", map[string]any{"enabled": false})
	if k := e.kindOf(t, fakturaOrder); k != "other" {
		t.Errorf("faktura order with the built-in disabled = %q, want other", k)
	}
	rec := httptest.NewRecorder()
	e.h.HandleRecentPayments(rec, httptest.NewRequest(http.MethodGet, "/api/v1/harbor/recent-payments", nil))
	var resp struct {
		Groups []struct{ Slug string } `json:"groups"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Groups) != 1 || resp.Groups[0].Slug != "other" {
		t.Errorf("chips with faktura disabled = %+v, want only other", resp.Groups)
	}

	// ClassifyOrder is the same rule for single orders (the SMS alerts).
	g, err := paymentfilters.ClassifyOrder(ctx, e.pool, fakturaOrder)
	if err != nil || g.Slug != "other" || g.ID == "" {
		t.Errorf("ClassifyOrder = %+v, %v, want the other built-in", g, err)
	}
	e.ok(t, http.StatusOK, http.MethodPatch, "/faktura", map[string]any{"enabled": true})
	if g, err := paymentfilters.ClassifyOrder(ctx, e.pool, fakturaOrder); err != nil || g.Slug != "faktura" {
		t.Errorf("ClassifyOrder = %+v, %v, want faktura", g, err)
	}
}

// An archived price item keeps its group so past payments keep classifying;
// assigning never blocks a hard delete of an unreferenced item.
func TestPaymentFilterGroupsArchivedAndDeletedItems(t *testing.T) {
	e := newPFEnv(t)
	ctx := context.Background()
	archived := e.item(t, "guest", "Synthetic old guest night")
	if _, err := e.pool.Exec(ctx, `UPDATE price_items SET is_active = false WHERE id = $1`, archived); err != nil {
		t.Fatal(err)
	}
	unused := e.item(t, "service", "Synthetic unused service")
	e.ok(t, http.StatusCreated, http.MethodPost, "/",
		map[string]any{"name": "Gjest", "icon": "sailboat", "price_item_ids": []string{archived, unused}})
	order := e.order(t, 10, archived)
	if k := e.kindOf(t, order); k != "gjest" {
		t.Errorf("order of an archived item = %q, want gjest", k)
	}

	h := NewPriceItemsHandler(e.pool, testConfig(), nil, zerolog.Nop())
	r := chi.NewRouter()
	r.Delete("/admin/pricing/{itemID}", h.HandleDelete)
	req := httptest.NewRequest(http.MethodDelete, "/admin/pricing/"+unused, nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), mustClaims(e.clubID, "treasurer")))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete of an assigned, unreferenced item: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM payment_filter_group_items WHERE price_item_id = $1`, unused).Scan(&n); err != nil || n != 0 {
		t.Errorf("assignment of the deleted item: %d rows (%v)", n, err)
	}
}

// target puts the item on a QR poster; an archived poster does not count.
func (e *pfEnv) target(t *testing.T, itemID, label string, archived bool) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO harbor_payment_targets (club_id, token, short_code, label, price_item_id, archived_at)
		 VALUES ($1, $2, $3, $4, $5, CASE WHEN $6 THEN now() END)`,
		e.clubID, "tok-"+testutil.RandomHex(6), testutil.RandomHex(4), label, itemID, archived); err != nil {
		t.Fatal(err)
	}
}

// An item can reach an order line when the checkout offers its category, an
// active QR poster sells it, or an order line already has it. Only an item that
// is none of these is billed on fakturas only.
func TestPaymentFilterPriceItemReachability(t *testing.T) {
	e := newPFEnv(t)
	service := e.item(t, "service", "Synthetic offered by checkout")
	// membership is a checkout add-on category (only harbor_membership, slip_fee
	// and broadcast are excluded), so it can be an add-on line.
	membership := e.item(t, "membership", "Synthetic dues as add-on")
	billingOnly := e.item(t, "harbor_membership", "Synthetic billing only")
	onPoster := e.item(t, "harbor_membership", "Synthetic billing item on a poster")
	e.target(t, onPoster, "Synthetic poster", false)
	archivedPoster := e.item(t, "slip_fee", "Synthetic billing item, archived poster")
	e.target(t, archivedPoster, "Synthetic old poster", true)
	onLine := e.item(t, "slip_fee", "Synthetic billing item on an order line")
	e.order(t, 10, onLine)

	got := map[string][2]bool{}
	labels := map[string][]string{}
	for _, it := range e.list(t).PriceItems {
		got[it.ID] = [2]bool{it.OrderReachable, it.Sold}
		labels[it.ID] = it.QRLabels
	}
	want := map[string][2]bool{ // reachable, sold
		service:        {true, false},
		membership:     {true, false},
		billingOnly:    {false, false},
		onPoster:       {true, true},
		archivedPoster: {false, false},
		onLine:         {true, true},
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("item %s: reachable/sold = %v, want %v", id, got[id], w)
		}
	}
	if len(labels[onPoster]) != 1 || labels[onPoster][0] != "Synthetic poster" {
		t.Errorf("poster labels = %v", labels[onPoster])
	}
}

// The unassigned count is for those who can assign, and counts only active
// items that are sold and in no group, so leaving other items ungrouped on
// purpose does not nag.
func TestRecentPaymentsUnassignedItemsNudge(t *testing.T) {
	e := newPFEnv(t)
	e.item(t, "service", "Synthetic crane hire, never sold")
	onPoster := e.item(t, "guest", "Synthetic guest night")
	e.target(t, onPoster, "Synthetic poster", false)
	onLine := e.item(t, "service", "Synthetic sold service")
	e.order(t, 10, onLine)
	// Billing-only and unsold: not counted.
	e.item(t, "membership", "Synthetic dues")
	// Archived poster and archived item: not counted.
	oldPoster := e.item(t, "service", "Synthetic retired poster item")
	e.target(t, oldPoster, "Synthetic old poster", true)
	archived := e.item(t, "service", "Synthetic archived sold item")
	e.order(t, 20, archived)
	if _, err := e.pool.Exec(context.Background(), `UPDATE price_items SET is_active = false WHERE id = $1`, archived); err != nil {
		t.Fatal(err)
	}
	// Sold but already in a group: not counted.
	grouped := e.item(t, "service", "Synthetic grouped item")
	e.order(t, 30, grouped)
	e.ok(t, http.StatusCreated, http.MethodPost, "/",
		map[string]any{"name": "Kran", "icon": "wrench", "price_item_ids": []string{grouped}})

	get := func(roles ...string) map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/harbor/recent-payments", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), mustClaims(e.clubID, roles...)))
		rec := httptest.NewRecorder()
		e.h.HandleRecentPayments(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}
	if n, ok := get("harbor_master")["unassigned_items"].(float64); !ok || n != 2 {
		t.Errorf("harbor master sees unassigned_items = %v, want 2 (the poster item and the sold service)", get("harbor_master")["unassigned_items"])
	}
	if _, ok := get("chair")["unassigned_items"]; ok {
		t.Error("a read-only viewer must not get the nudge")
	}
}

func TestPaymentFilterGroupsValidation(t *testing.T) {
	e := newPFEnv(t)
	ctx := context.Background()
	other := testutil.SeedClub(t, e.pool)
	foreignItem := ""
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount) VALUES ($1, 'service', 'Synthetic foreign item', 1) RETURNING id::text`, other).Scan(&foreignItem); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO payment_filter_groups (club_id, slug, name, icon, sort_order) VALUES ($1, 'foreign', 'Foreign', 'zap', 10)`, other); err != nil {
		t.Fatal(err)
	}
	own := e.item(t, "service", "Synthetic own item")

	create := func(body map[string]any) *httptest.ResponseRecorder {
		return e.call(t, http.MethodPost, "/", body)
	}
	expect := func(what string, rec *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if rec.Code != status || errCode(t, rec) != code {
			t.Errorf("%s: %d %s, want %d %s", what, rec.Code, rec.Body.String(), status, code)
		}
	}

	expect("empty name", create(map[string]any{"name": " ", "icon": "zap"}), 400, "invalid_name")
	expect("long name", create(map[string]any{"name": strings.Repeat("x", 41), "icon": "zap"}), 400, "invalid_name")
	expect("unknown icon", create(map[string]any{"name": "Kiosk", "icon": "rocket"}), 400, "invalid_icon")
	expect("reserved: faktura", create(map[string]any{"name": "Faktura", "icon": "zap"}), 400, "reserved_name")
	expect("reserved: all", create(map[string]any{"name": "All", "icon": "zap"}), 400, "reserved_name")
	expect("reserved: harbor", create(map[string]any{"name": "Harbor", "icon": "zap"}), 400, "reserved_name")
	expect("rendered label of a seeded row", create(map[string]any{"name": "Annet", "icon": "zap"}), 409, "name_taken")
	expect("rendered label, other case", create(map[string]any{"name": "invoice", "icon": "zap"}), 409, "name_taken")
	expect("bad json", e.call(t, http.MethodPost, "/", "{"), 400, "")
	expect("foreign item", create(map[string]any{"name": "Kiosk", "icon": "zap", "price_item_ids": []string{foreignItem}}), 400, "unknown_price_item")
	expect("malformed item id", create(map[string]any{"name": "Kiosk", "icon": "zap", "price_item_ids": []string{"x'; --"}}), 400, "unknown_price_item")
	expect("unknown item id", create(map[string]any{"name": "Kiosk", "icon": "zap", "price_item_ids": []string{"00000000-0000-0000-0000-000000000001"}}), 400, "unknown_price_item")
	if g := e.list(t).Groups; len(g) != 2 {
		t.Fatalf("a refused create left groups: %v", g)
	}

	// Duplicate names, and slugs that de-duplicate.
	e.ok(t, http.StatusCreated, http.MethodPost, "/", map[string]any{"name": "Slipping", "icon": "forklift"})
	expect("duplicate name", create(map[string]any{"name": "slipping", "icon": "zap"}), 409, "name_taken")
	if res := e.ok(t, http.StatusCreated, http.MethodPost, "/", map[string]any{"name": "Slipping!", "icon": "forklift"}); res.Group.Slug != "slipping-2" {
		t.Errorf("slug of a clashing name = %q, want slipping-2", res.Group.Slug)
	}
	if res := e.ok(t, http.StatusCreated, http.MethodPost, "/", map[string]any{"name": "Båt & Bryggeøl", "icon": "anchor"}); res.Group.Slug != "bat-bryggeol" {
		t.Errorf("slug = %q, want bat-bryggeol", res.Group.Slug)
	}

	// Another club's group does not exist here.
	expect("patch foreign group", e.call(t, http.MethodPatch, "/foreign", map[string]any{"enabled": false}), 404, "not_found")
	expect("delete foreign group", e.call(t, http.MethodDelete, "/foreign", nil), 404, "not_found")
	expect("items of foreign group", e.call(t, http.MethodPut, "/foreign/items", map[string]any{"price_item_ids": []string{}}), 404, "not_found")
	expect("foreign item into own group", e.call(t, http.MethodPut, "/slipping/items", map[string]any{"price_item_ids": []string{foreignItem}}), 400, "unknown_price_item")

	// Built-ins.
	expect("disable other", e.call(t, http.MethodPatch, "/other", map[string]any{"enabled": false}), 409, "builtin_group")
	expect("delete faktura", e.call(t, http.MethodDelete, "/faktura", nil), 409, "builtin_group")
	expect("delete other", e.call(t, http.MethodDelete, "/other", nil), 409, "builtin_group")
	expect("items on faktura", e.call(t, http.MethodPut, "/faktura/items", map[string]any{"price_item_ids": []string{own}}), 409, "builtin_group")
	expect("reset name of an admin group", e.call(t, http.MethodPatch, "/slipping", map[string]any{"reset_name": true}), 400, "no_default_name")
	expect("name and reset", e.call(t, http.MethodPatch, "/faktura", map[string]any{"name": "X", "reset_name": true}), 400, "invalid_name")
	expect("rename to a taken name", e.call(t, http.MethodPatch, "/slipping-2", map[string]any{"name": "SLIPPING"}), 409, "name_taken")
	expect("icon outside the list", e.call(t, http.MethodPatch, "/slipping", map[string]any{"icon": "rocket"}), 400, "invalid_icon")
	e.ok(t, http.StatusOK, http.MethodPatch, "/other", map[string]any{"name": "Diverse", "icon": "ticket"})

	// Order: exactly the non-other groups.
	order := func(slugs ...string) *httptest.ResponseRecorder {
		return e.call(t, http.MethodPut, "/order", map[string]any{"slugs": slugs})
	}
	expect("order with other", order("slipping", "slipping-2", "bat-bryggeol", "faktura", "other"), 400, "order_mismatch")
	expect("order missing a group", order("slipping", "faktura"), 400, "order_mismatch")
	expect("order with a repeat", order("slipping", "slipping", "bat-bryggeol", "faktura"), 400, "order_mismatch")
	expect("order with an unknown slug", order("slipping", "slipping-2", "bat-bryggeol", "nope"), 400, "order_mismatch")
	expect("empty order", order(), 400, "order_mismatch")

	// A no-op write leaves no audit row; a real one does.
	before := e.auditCount(t, audit.ActionPaymentFilterGroupUpdated)
	e.ok(t, http.StatusOK, http.MethodPatch, "/slipping", map[string]any{"name": "Slipping", "icon": "forklift", "enabled": true})
	if got := e.auditCount(t, audit.ActionPaymentFilterGroupUpdated); got != before {
		t.Errorf("a no-op PATCH was audited")
	}
	e.ok(t, http.StatusOK, http.MethodPatch, "/slipping", map[string]any{"icon": "anchor"})
	if got := e.auditCount(t, audit.ActionPaymentFilterGroupUpdated); got != before+1 {
		t.Errorf("a PATCH was not audited exactly once (%d -> %d)", before, got)
	}
	reorders := e.auditCount(t, audit.ActionPaymentFilterGroupsReordered)
	same := e.list(t).slugs()
	same = slices.DeleteFunc(same, func(s string) bool { return s == "other" })
	if rec := order(same...); rec.Code != http.StatusOK {
		t.Fatalf("same order: %d %s", rec.Code, rec.Body.String())
	}
	if e.auditCount(t, audit.ActionPaymentFilterGroupsReordered) != reorders {
		t.Error("an unchanged order was audited")
	}

	// The cap: built-ins count, so 18 more fill it.
	have := len(e.list(t).Groups)
	for i := have; i < paymentfilters.MaxGroups; i++ {
		e.ok(t, http.StatusCreated, http.MethodPost, "/", map[string]any{"name": fmt.Sprintf("Group %d", i), "icon": "zap"})
	}
	expect("21st group", create(map[string]any{"name": "One too many", "icon": "zap"}), 409, "too_many_groups")
}

// Writes by two admins at once apply one after the other: groups get distinct
// slugs and sort positions, built-ins are made once, and an item moved by two
// requests ends in exactly one group.
func TestPaymentFilterGroupsConcurrentWrites(t *testing.T) {
	e := newPFEnv(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	statuses := make([]int, 8)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = e.call(t, http.MethodPost, "/", map[string]any{"name": fmt.Sprintf("Group %d", i), "icon": "zap"}).Code
		}()
	}
	wg.Wait()
	for i, s := range statuses {
		if s != http.StatusCreated {
			t.Errorf("concurrent create %d: status %d", i, s)
		}
	}
	var builtins, distinctSorts, total int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE builtin IS NOT NULL), count(DISTINCT sort_order), count(*)
		   FROM payment_filter_groups WHERE club_id = $1`, e.clubID).Scan(&builtins, &distinctSorts, &total); err != nil {
		t.Fatal(err)
	}
	if builtins != 2 || total != 10 || distinctSorts != 10 {
		t.Errorf("after 8 concurrent creates: %d built-ins, %d groups, %d distinct sort positions", builtins, total, distinctSorts)
	}

	item := e.item(t, "service", "Synthetic contested item")
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slug := fmt.Sprintf("group-%d", i%2)
			statuses[i] = e.call(t, http.MethodPut, "/"+slug+"/items", map[string]any{"price_item_ids": []string{item}}).Code
		}()
	}
	wg.Wait()
	for i, s := range statuses {
		if s != http.StatusOK {
			t.Errorf("concurrent move %d: status %d", i, s)
		}
	}
	var holders int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM payment_filter_group_items WHERE price_item_id = $1`, item).Scan(&holders); err != nil || holders != 1 {
		t.Errorf("contested item is in %d groups (%v), want 1", holders, err)
	}
}

// The writes sit behind the /admin step-up and a role gate, with no fresh
// TOTP: the chair reads, the roles that run the page and own the price list
// write, and a member gets nothing.
func TestPaymentFilterAdminRouteGates(t *testing.T) {
	h := NewPaymentFilterHandler(nil, nil, zerolog.Nop())
	r := chi.NewRouter()
	r.Route("/admin", func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Use(testSessionState)
		r.Use(middleware.RequireAdminTOTP(nil))
		r.Use(panicAsTeapot)
		h.MountAdminRoutes(r, middleware.RequireRoleOrBoardRead(PaymentFilterWriteRoles...))
	})
	calls := []routeCall{
		{http.MethodGet, "/admin/payment-filter-groups/", false},
		{http.MethodPost, "/admin/payment-filter-groups/", false},
		{http.MethodPut, "/admin/payment-filter-groups/order", false},
		{http.MethodPatch, "/admin/payment-filter-groups/slipping", false},
		{http.MethodPut, "/admin/payment-filter-groups/slipping/items", false},
		{http.MethodDelete, "/admin/payment-filter-groups/slipping", false},
	}

	walked := 0
	if err := chi.Walk(r, func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/admin/payment-filter-groups") {
			walked++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if walked != len(calls) {
		t.Fatalf("mounted %d routes, the test covers %d: choose a gate for the new route", walked, len(calls))
	}

	reached := func(code int) bool { return code != http.StatusUnauthorized && code != http.StatusForbidden }
	for _, c := range calls {
		name := c.method + " " + c.path
		isRead := c.method == http.MethodGet

		if rec := serveWaitingList(r, c, nil, sessFresh); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without auth: %d, want 401", name, rec.Code)
		}
		for _, s := range []string{sessNone, sessLapsed} {
			rec := serveWaitingList(r, c, []string{"admin"}, s)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"totp_required"`) {
				t.Errorf("%s as admin, session %s: %d %s, want 403 totp_required", name, s, rec.Code, rec.Body.String())
			}
		}
		if rec := serveWaitingList(r, c, []string{"member"}, sessStepUp); rec.Code != http.StatusForbidden {
			t.Errorf("%s as member: %d, want 403", name, rec.Code)
		}
		// Inside the step-up window, no fresh TOTP is asked of a writer.
		for _, role := range PaymentFilterWriteRoles {
			if rec := serveWaitingList(r, c, []string{role}, sessStepUp); !reached(rec.Code) {
				t.Errorf("%s as %s with step-up: %d, want the handler reached", name, role, rec.Code)
			}
		}
		for _, role := range []string{"chair", "vice_chair", "secretary", "deputy"} {
			rec := serveWaitingList(r, c, []string{role}, sessStepUp)
			if isRead && !reached(rec.Code) {
				t.Errorf("GET as %s: %d, want the handler reached", role, rec.Code)
			}
			if !isRead && (rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient permissions")) {
				t.Errorf("%s as %s: %d %s, want 403", name, role, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestMayEditPaymentFilters(t *testing.T) {
	for _, role := range []string{"board", "harbor_master", "treasurer", "admin"} {
		if !mayEditPaymentFilters([]string{"member", role}) {
			t.Errorf("%s may edit", role)
		}
	}
	for _, role := range []string{"chair", "vice_chair", "secretary", "deputy", "member"} {
		if mayEditPaymentFilters([]string{role}) {
			t.Errorf("%s may only read", role)
		}
	}
}

// A new group ranks above Faktura, like the groups the migration seeds, so on a
// fresh club a faktura checkout with that group's add-on shows as that group.
func TestPaymentFilterNewGroupRanksAboveFaktura(t *testing.T) {
	e := newPFEnv(t)
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	slipItem := e.item(t, "service", "Synthetic haul-out")
	checkout := e.order(t, 10, slipItem)
	addOrderSource(t, e.pool, checkout, seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 5201))
	plainFaktura := e.order(t, 20)
	addOrderSource(t, e.pool, plainFaktura, seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 5202))

	if k := e.kindOf(t, checkout); k != "faktura" {
		t.Fatalf("before the group, the faktura checkout = %q, want faktura", k)
	}
	e.ok(t, http.StatusCreated, http.MethodPost, "/",
		map[string]any{"name": "Slipping", "icon": "forklift", "price_item_ids": []string{slipItem}})
	if k := e.kindOf(t, checkout); k != "slipping" {
		t.Errorf("faktura checkout with the new group's add-on = %q, want slipping", k)
	}
	if k := e.kindOf(t, plainFaktura); k != "faktura" {
		t.Errorf("a faktura with no add-on = %q, want faktura", k)
	}

	// Each new group goes just before Faktura, after the earlier ones; the
	// positions are renumbered 10, 20, ... and the fallback stays last.
	e.ok(t, http.StatusCreated, http.MethodPost, "/", map[string]any{"name": "Kiosk", "icon": "coffee"})
	l := e.list(t)
	if got := strings.Join(l.slugs(), ","); got != "slipping,kiosk,faktura,other" {
		t.Fatalf("order = %s, want slipping, kiosk, faktura, other", got)
	}
	for i, g := range l.Groups[:3] {
		if g.SortOrder != (i+1)*10 {
			t.Errorf("%s sort_order = %d, want %d", g.Slug, g.SortOrder, (i+1)*10)
		}
	}

	// With faktura moved above a group, a new group still goes before faktura.
	if rec := e.call(t, http.MethodPut, "/order", map[string]any{"slugs": []string{"faktura", "slipping", "kiosk"}}); rec.Code != http.StatusOK {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}
	e.ok(t, http.StatusCreated, http.MethodPost, "/", map[string]any{"name": "Camping", "icon": "tent"})
	if got := strings.Join(e.list(t).slugs(), ","); got != "camping,faktura,slipping,kiosk,other" {
		t.Errorf("order after faktura was moved up = %s", got)
	}
}

// Creating a group with prices that are in another group moves them: the audit
// says where from, and the group they left gets a row of its own.
func TestPaymentFilterCreateMoveIsAudited(t *testing.T) {
	e := newPFEnv(t)
	ctx := context.Background()
	a := e.item(t, "service", "Synthetic a")
	b := e.item(t, "service", "Synthetic b")
	e.ok(t, http.StatusCreated, http.MethodPost, "/",
		map[string]any{"name": "Old", "icon": "anchor", "price_item_ids": []string{a, b}})

	res := e.ok(t, http.StatusCreated, http.MethodPost, "/",
		map[string]any{"name": "New", "icon": "zap", "price_item_ids": []string{a}})
	if len(res.Moved) != 1 || res.Moved[0].PriceItemID != a || res.Moved[0].FromGroup != "old" {
		t.Fatalf("moved = %+v", res.Moved)
	}

	var created string
	if err := e.pool.QueryRow(ctx,
		`SELECT details::text FROM audit_log WHERE club_id = $1 AND action = $2 AND details->>'slug' = 'new'`,
		e.clubID, audit.ActionPaymentFilterGroupCreated).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created, `"moved_from"`) || !strings.Contains(created, a) || !strings.Contains(created, "old") {
		t.Errorf("created audit lacks moved_from: %s", created)
	}
	var removed string
	if err := e.pool.QueryRow(ctx,
		`SELECT details::text FROM audit_log WHERE club_id = $1 AND action = $2 AND details->>'slug' = 'old'`,
		e.clubID, audit.ActionPaymentFilterGroupItemsUpdated).Scan(&removed); err != nil {
		t.Fatalf("the group the item left has no items_updated row: %v", err)
	}
	if !strings.Contains(removed, a) || !strings.Contains(removed, `"moved_to"`) {
		t.Errorf("source audit = %s", removed)
	}

	// Moving by PUT audits the group it left too.
	before := e.auditCount(t, audit.ActionPaymentFilterGroupItemsUpdated)
	e.ok(t, http.StatusOK, http.MethodPut, "/new/items", map[string]any{"price_item_ids": []string{a, b}})
	if got := e.auditCount(t, audit.ActionPaymentFilterGroupItemsUpdated); got != before+2 {
		t.Errorf("a PUT that moved an item wrote %d rows, want 2 (target and source)", got-before)
	}
}

// Only an explicit [] empties a group; a missing or null list is refused.
func TestPaymentFilterSetItemsNeedsTheList(t *testing.T) {
	e := newPFEnv(t)
	a := e.item(t, "service", "Synthetic a")
	e.ok(t, http.StatusCreated, http.MethodPost, "/",
		map[string]any{"name": "Kran", "icon": "wrench", "price_item_ids": []string{a}})

	for _, body := range []string{`{}`, `{"price_item_ids":null}`, `{"items":["x"]}`} {
		rec := e.call(t, http.MethodPut, "/kran/items", body)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "price_item_ids_required" {
			t.Errorf("PUT %s: %d %s, want 400 price_item_ids_required", body, rec.Code, rec.Body.String())
		}
	}
	if got := e.list(t).Groups[0]; got.ItemCount != 1 {
		t.Fatalf("a refused PUT changed the group: %+v", got)
	}
	res := e.ok(t, http.StatusOK, http.MethodPut, "/kran/items", `{"price_item_ids":[]}`)
	if res.Group.ItemCount != 0 {
		t.Errorf("an explicit empty list left %d items", res.Group.ItemCount)
	}
}

// Names are stored composed, refuse invisible characters, and cannot take the
// name of the no-filter chip in any locale.
func TestPaymentFilterNameRules(t *testing.T) {
	e := newPFEnv(t)
	expect := func(what, name string, status int, code string) {
		t.Helper()
		rec := e.call(t, http.MethodPost, "/", map[string]any{"name": name, "icon": "zap"})
		if rec.Code != status || errCode(t, rec) != code {
			t.Errorf("%s: %d %s, want %d %s", what, rec.Code, rec.Body.String(), status, code)
		}
	}
	for _, n := range []string{"all", "ALL", "Alle", "Tous", "Tutti", "Wszystkie"} {
		expect("the no-filter label "+n, n, 400, "reserved_name")
	}
	expect("zero-width space", "Sli\u200bpping", 400, "invalid_name")
	expect("zero-width joiner", "Sli\u200dpping", 400, "invalid_name")

	res := e.ok(t, http.StatusCreated, http.MethodPost, "/", map[string]any{"name": "Cafe\u0301", "icon": "coffee"})
	if res.Group.Name == nil || *res.Group.Name != "Caf\u00e9" {
		t.Errorf("stored name = %v, want the composed form", res.Group.Name)
	}
	expect("the same name typed composed", "Caf\u00e9", 409, "name_taken")

	// Renaming cannot take the label either, built-ins included.
	rec := e.call(t, http.MethodPatch, "/other", map[string]any{"name": "Alle"})
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "reserved_name" {
		t.Errorf("rename other to Alle: %d %s", rec.Code, rec.Body.String())
	}
}
