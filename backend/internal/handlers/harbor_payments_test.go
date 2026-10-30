package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/paymentfilters"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestPaymentMethod(t *testing.T) {
	s := func(v string) *string { return &v }
	tests := []struct {
		provider string
		wallet   *string
		want     string
	}{
		{"vipps", nil, "vipps"},
		{"vipps", s("apple_pay"), "vipps"},
		{"stripe", nil, "card"},
		{"stripe", s("apple_pay"), "apple_pay"},
		{"stripe", s("google_pay"), "google_pay"},
		{"stripe", s("link"), "card"},
		{"stripe", s("samsung_pay"), "card"},
		{"stripe", s(""), "card"},
	}
	for _, tc := range tests {
		if got := paymentMethod(tc.provider, tc.wallet); got != tc.want {
			t.Errorf("paymentMethod(%q, %v) = %q, want %q", tc.provider, tc.wallet, got, tc.want)
		}
	}
}

func TestSplitList(t *testing.T) {
	if got := splitList(" guest, ,motorhome,"); len(got) != 2 || got[0] != "guest" || got[1] != "motorhome" {
		t.Errorf("splitList = %q", got)
	}
	if got := splitList(""); got != nil {
		t.Errorf("splitList(\"\") = %q, want none", got)
	}
}

func TestProviderLinksFor(t *testing.T) {
	const portal = "https://portal.vippsmobilepay.com/100200/home"
	tests := []struct {
		name   string
		cfg    config.Config
		portal string
		want   providerLinks
	}{
		{"nothing configured", config.Config{}, "", providerLinks{}},
		{
			"live Stripe and Vipps",
			config.Config{StripeSecretKey: "sk_live_x", StripeWebhookSecret: "whsec_x", VippsMSN: "123456"},
			portal,
			providerLinks{Stripe: "https://dashboard.stripe.com/payments", Vipps: portal},
		},
		{
			"live Vipps without a stored address",
			config.Config{VippsMSN: "123456"},
			"",
			providerLinks{},
		},
		{
			"test-mode Stripe key",
			config.Config{StripeSecretKey: "sk_test_x", StripeWebhookSecret: "whsec_x"},
			"",
			providerLinks{Stripe: "https://dashboard.stripe.com/test/payments"},
		},
		{
			"Stripe key without its webhook secret is not enabled",
			config.Config{StripeSecretKey: "sk_live_x"},
			"",
			providerLinks{},
		},
		{
			"Vipps mock",
			config.Config{VippsMSN: "mock-msn", VippsTestMode: true},
			portal,
			providerLinks{},
		},
		{"Vipps test API", config.Config{VippsMSN: "123456", VippsTestMode: true}, portal, providerLinks{}},
		{"Vipps without a serial number", config.Config{}, portal, providerLinks{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerLinksFor(&tc.cfg, tc.portal); got != tc.want {
				t.Errorf("providerLinksFor = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestInclusiveUntil(t *testing.T) {
	for in, want := range map[string]string{
		"2026-06-30": "2026-07-01",
		"2026-12-31": "2027-01-01",
		"2028-02-28": "2028-02-29",
		"not-a-date": "not-a-date",
	} {
		if got := inclusiveUntil(in); got != want {
			t.Errorf("inclusiveUntil(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecentPaymentsQueryLimit(t *testing.T) {
	for query, want := range map[string]int{
		"":           50,
		"limit=7":    7,
		"limit=200":  200,
		"limit=201":  50,
		"limit=0":    50,
		"limit=nope": 50,
	} {
		q, _ := url.ParseQuery(query)
		if _, _, got := recentPaymentsQuery("club", q); got != want {
			t.Errorf("limit for %q = %d, want %d", query, got, want)
		}
	}
}

// The kind filter is only ever a bound argument: a value that looks like SQL
// reaches the database as data, and the classifier is the shared constant.
func TestRecentPaymentsQueryBindsKinds(t *testing.T) {
	evil := "x'; DROP TABLE orders; --"
	q, _ := url.ParseQuery(url.Values{"kind": {"guest," + evil}}.Encode())
	sql, args, _ := recentPaymentsQuery("club", q)
	if strings.Contains(sql, "DROP TABLE") || strings.Contains(sql, evil) {
		t.Fatalf("kind value reached the SQL text:\n%s", sql)
	}
	if !strings.Contains(sql, "COALESCE(pfg.slug, 'other') = ANY($2::text[])") {
		t.Errorf("kind filter is not a bound text[] on the group slug:\n%s", sql)
	}
	kinds, ok := args[1].([]string)
	if !ok || len(kinds) != 2 || kinds[0] != "guest" || kinds[1] != evil {
		t.Errorf("args = %#v, want the kinds bound as a []string", args)
	}
	if !strings.Contains(sql, paymentfilters.OrderGroupLateralSQL) {
		t.Error("the overview must classify through paymentfilters.OrderGroupLateralSQL")
	}
}

type recentPaymentsEnv struct {
	pool   *pgxpool.Pool
	h      *HarborHandler
	clubID string
	now    time.Time
}

func newRecentPaymentsEnv(t *testing.T) *recentPaymentsEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	var slug string
	if err := pool.QueryRow(context.Background(), `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.ClubSlug = slug
	return &recentPaymentsEnv{
		pool: pool, h: NewHarborHandler(pool, cfg, zerolog.Nop()), clubID: clubID,
		now: time.Now().Truncate(time.Second),
	}
}

// backfill runs migration 000123 over the club's price items: the schema is
// migrated before any club exists, so the groups an existing club got at
// deploy are made by re-executing the file (it is idempotent).
func (e *recentPaymentsEnv) backfill(t *testing.T) {
	t.Helper()
	sql, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), "000123_payment_filter_groups.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("apply 000123: %v", err)
	}
}

// paidOrder inserts an order paid ageMin minutes ago through provider, with
// one line per price-item category given.
func (e *recentPaymentsEnv) paidOrder(t *testing.T, provider string, ageMin int, categories ...string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, token, status, paid_at, provider, guest_name)
		 VALUES ($1, 450, $2, 'paid', $3, $4, 'Synthetic Payer') RETURNING id`,
		e.clubID, genCheckoutToken(), e.now.Add(-time.Duration(ageMin)*time.Minute), provider,
	).Scan(&id); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	for _, c := range categories {
		var itemID string
		if err := e.pool.QueryRow(ctx,
			`INSERT INTO price_items (club_id, category, name, amount) VALUES ($1, $2, 'Synthetic item', 450) RETURNING id`,
			e.clubID, c,
		).Scan(&itemID); err != nil {
			t.Fatalf("insert price item: %v", err)
		}
		if _, err := e.pool.Exec(ctx,
			`INSERT INTO order_lines (order_id, price_item_id, name, unit_price, total_price) VALUES ($1, $2, 'Synthetic line', 450, 450)`,
			id, itemID,
		); err != nil {
			t.Fatalf("insert order line: %v", err)
		}
	}
	return id
}

// attempt adds a payment attempt with the card details a capture recorded
// (brand "" leaves them unrecorded).
func (e *recentPaymentsEnv) attempt(t *testing.T, orderID, provider, status, brand, wallet string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, card_brand, card_wallet)
		 VALUES ($1, $2, $3, $4, $5, 450, NULLIF($6, ''), NULLIF($7, ''))`,
		e.clubID, orderID, provider, "ref-"+testutil.RandomHex(6), status, brand, wallet,
	); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
}

func (e *recentPaymentsEnv) get(t *testing.T, query string) []recentPayment {
	t.Helper()
	rec := httptest.NewRecorder()
	e.h.HandleRecentPayments(rec, httptest.NewRequest(http.MethodGet, "/api/v1/harbor/recent-payments?"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Payments []recentPayment `json:"payments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Payments
}

func recentIDs(ps []recentPayment) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func TestHandleRecentPayments(t *testing.T) {
	e := newRecentPaymentsEnv(t)
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})

	// Newest first: a faktura paid by card whose charge is not read yet.
	faktura := e.paidOrder(t, "stripe", 10)
	addOrderSource(t, e.pool, faktura, seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 4691))
	e.attempt(t, faktura, "stripe", "captured", "", "")

	guestVipps := e.paidOrder(t, "vipps", 20, "guest_harbor")
	e.attempt(t, guestVipps, "vipps", "captured", "", "")

	merchVisa := e.paidOrder(t, "stripe", 30, "merch")
	e.attempt(t, merchVisa, "stripe", "failed", "", "")
	e.attempt(t, merchVisa, "stripe", "captured", "visa", "")

	bobilApple := e.paidOrder(t, "stripe", 40, "bobil")
	e.attempt(t, bobilApple, "stripe", "captured", "mastercard", "apple_pay")

	otherGoogle := e.paidOrder(t, "stripe", 50)
	e.attempt(t, otherGoogle, "stripe", "captured", "visa", "google_pay")

	// A guest stay settled together with a faktura is a guest payment.
	guestFaktura := e.paidOrder(t, "stripe", 60, "guest_slip")
	addOrderSource(t, e.pool, guestFaktura, seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 4692))
	e.attempt(t, guestFaktura, "stripe", "captured", "amex", "link")

	motorhome := e.paidOrder(t, "vipps", 70, "motorhome")
	e.attempt(t, motorhome, "vipps", "captured", "", "")

	// A line with no price item (the item was deleted) and a category the
	// groups never matched fall to the fallback, as before.
	noItem := e.paidOrder(t, "vipps", 80)
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO order_lines (order_id, price_item_id, name, unit_price, total_price) VALUES ($1, NULL, 'Synthetic line', 450, 450)`, noItem); err != nil {
		t.Fatal(err)
	}
	e.attempt(t, noItem, "vipps", "captured", "", "")
	unknownCategory := e.paidOrder(t, "vipps", 90, "synthetic_unknown")
	e.attempt(t, unknownCategory, "vipps", "captured", "", "")

	// Not paid: never listed.
	pending := e.paidOrder(t, "stripe", 5, "guest_harbor")
	if _, err := e.pool.Exec(context.Background(), `UPDATE orders SET status = 'pending', paid_at = NULL WHERE id = $1`, pending); err != nil {
		t.Fatal(err)
	}

	// The groups an existing club got when the migration ran over its price
	// items. Every order below must classify as the old CASE did, except
	// merchandise: no price-item category ever contained "merch", so that
	// chip was always empty and is not seeded.
	e.backfill(t)

	type want struct{ kind, method, channel, brand string }
	wants := map[string]want{
		faktura:         {"faktura", "card", "stripe", ""},
		guestVipps:      {"guest", "vipps", "vipps", ""},
		merchVisa:       {"other", "card", "stripe", "visa"},
		bobilApple:      {"motorhome", "apple_pay", "stripe", "mastercard"},
		otherGoogle:     {"other", "google_pay", "stripe", "visa"},
		guestFaktura:    {"guest", "card", "stripe", "amex"},
		motorhome:       {"motorhome", "vipps", "vipps", ""},
		noItem:          {"other", "vipps", "vipps", ""},
		unknownCategory: {"other", "vipps", "vipps", ""},
	}

	t.Run("every payment says how it was paid", func(t *testing.T) {
		got := e.get(t, "")
		if len(got) != len(wants) {
			t.Fatalf("got %d payments %v, want %d", len(got), recentIDs(got), len(wants))
		}
		if got[0].ID != faktura || got[len(got)-1].ID != unknownCategory {
			t.Errorf("order = %v, want newest first", recentIDs(got))
		}
		for _, p := range got {
			w, ok := wants[p.ID]
			if !ok {
				t.Errorf("unexpected payment %s", p.ID)
				continue
			}
			brand := ""
			if p.CardBrand != nil {
				brand = *p.CardBrand
			}
			if p.Kind != w.kind || p.Method != w.method || p.Channel != w.channel || brand != w.brand {
				t.Errorf("%s = kind %q method %q channel %q brand %q, want %+v", p.ID, p.Kind, p.Method, p.Channel, brand, w)
			}
		}
	})

	t.Run("harbor default filter", func(t *testing.T) {
		got := recentIDs(e.get(t, "kind=guest,motorhome"))
		want := []string{guestVipps, bobilApple, guestFaktura, motorhome}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("got %v, want %v", got, want)
				break
			}
		}
	})

	t.Run("kind filters before the limit", func(t *testing.T) {
		// The newer faktura must not take the only slot and hide the guest.
		got := e.get(t, "kind=guest&limit=1")
		if len(got) != 1 || got[0].ID != guestVipps {
			t.Errorf("got %v, want only %s", recentIDs(got), guestVipps)
		}
		got = e.get(t, "kind=faktura")
		if len(got) != 1 || got[0].ID != faktura {
			t.Errorf("faktura = %v, want only %s", recentIDs(got), faktura)
		}
	})

	t.Run("has_more says the list was cut short", func(t *testing.T) {
		hasMore := func(query string) (bool, int) {
			t.Helper()
			rec := httptest.NewRecorder()
			e.h.HandleRecentPayments(rec, httptest.NewRequest(http.MethodGet, "/api/v1/harbor/recent-payments?"+query, nil))
			var body struct {
				Payments []recentPayment `json:"payments"`
				HasMore  *bool           `json:"has_more"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.HasMore == nil {
				t.Fatalf("body %s: %v", rec.Body.String(), err)
			}
			return *body.HasMore, len(body.Payments)
		}
		// Nine payments: asking for fewer cuts the list, asking for exactly as
		// many or more does not.
		if more, n := hasMore("limit=3"); !more || n != 3 {
			t.Errorf("limit=3: has_more %v with %d payments, want true with 3", more, n)
		}
		if more, n := hasMore("limit=8"); !more || n != 8 {
			t.Errorf("limit=8: has_more %v with %d payments, want true with 8", more, n)
		}
		if more, n := hasMore("limit=9"); more || n != 9 {
			t.Errorf("limit=9: has_more %v with %d payments, want false with 9", more, n)
		}
		if more, n := hasMore(""); more || n != 9 {
			t.Errorf("default: has_more %v with %d payments, want false with 9", more, n)
		}
	})

	t.Run("until includes its whole day", func(t *testing.T) {
		// Fra = Til is one day. Dates are read in the database's time zone, so
		// take the day from the database rather than from the test clock.
		var newest, quiet string
		if err := e.pool.QueryRow(context.Background(),
			`SELECT paid_at::date::text, (paid_at::date - 10)::text FROM orders WHERE paid_at IS NOT NULL ORDER BY paid_at DESC LIMIT 1`,
		).Scan(&newest, &quiet); err != nil {
			t.Fatal(err)
		}
		if got := e.get(t, "since="+newest+"&until="+newest); len(got) == 0 {
			t.Error("since = until = the newest payment's day returned nothing")
		}
		if got := e.get(t, "since="+quiet+"&until="+quiet); len(got) != 0 {
			t.Errorf("a day with no payments returned %v", recentIDs(got))
		}
	})

	t.Run("the response names the groups for the chips", func(t *testing.T) {
		rec := httptest.NewRecorder()
		e.h.HandleRecentPayments(rec, httptest.NewRequest(http.MethodGet, "/api/v1/harbor/recent-payments", nil))
		var body struct {
			Groups []struct {
				Slug     string  `json:"slug"`
				Name     *string `json:"name"`
				LabelKey *string `json:"label_key"`
				Icon     string  `json:"icon"`
			} `json:"groups"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var slugs []string
		for _, g := range body.Groups {
			slugs = append(slugs, g.Slug)
			if g.Name != nil || g.LabelKey == nil || g.Icon == "" {
				t.Errorf("seeded group %s = %+v, want a label key and no name", g.Slug, g)
			}
		}
		if strings.Join(slugs, ",") != "guest,motorhome,faktura,other" {
			t.Errorf("groups = %v, want guest, motorhome, faktura, other (no slipping item, no slipping chip)", slugs)
		}
	})

	t.Run("unknown kind matches nothing", func(t *testing.T) {
		if got := e.get(t, "kind=nonsense"); len(got) != 0 {
			t.Errorf("got %v, want none", recentIDs(got))
		}
	})
}
