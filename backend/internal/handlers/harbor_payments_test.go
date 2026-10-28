package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

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

	// Not paid: never listed.
	pending := e.paidOrder(t, "stripe", 5, "guest_harbor")
	if _, err := e.pool.Exec(context.Background(), `UPDATE orders SET status = 'pending', paid_at = NULL WHERE id = $1`, pending); err != nil {
		t.Fatal(err)
	}

	type want struct{ kind, method, channel, brand string }
	wants := map[string]want{
		faktura:      {"faktura", "card", "stripe", ""},
		guestVipps:   {"guest", "vipps", "vipps", ""},
		merchVisa:    {"merch", "card", "stripe", "visa"},
		bobilApple:   {"motorhome", "apple_pay", "stripe", "mastercard"},
		otherGoogle:  {"other", "google_pay", "stripe", "visa"},
		guestFaktura: {"guest", "card", "stripe", "amex"},
		motorhome:    {"motorhome", "vipps", "vipps", ""},
	}

	t.Run("every payment says how it was paid", func(t *testing.T) {
		got := e.get(t, "")
		if len(got) != len(wants) {
			t.Fatalf("got %d payments %v, want %d", len(got), recentIDs(got), len(wants))
		}
		if got[0].ID != faktura || got[len(got)-1].ID != motorhome {
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

	t.Run("unknown kind matches nothing", func(t *testing.T) {
		if got := e.get(t, "kind=nonsense"); len(got) != 0 {
			t.Errorf("got %v, want none", recentIDs(got))
		}
	})
}
