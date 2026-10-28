package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

const amountBelowMinimumBody = `{"error":"amount_below_minimum"}` + "\n"

// stripeNoClient is the Stripe provider for tests that only ask for its
// minimum; creating a payment with it would panic.
func stripeNoClient() payments.Provider { return stripe.NewPaymentProvider(nil, "testklubb") }

func TestEmailPayMethods_Amount(t *testing.T) {
	const link = "https://club.example.invalid/betal/x"
	tests := []struct {
		name      string
		providers []payments.Provider
		amount    float64
		want      string
	}{
		{"both, under the card minimum", []payments.Provider{&stubPayProvider{name: vipps.ProviderName}, stripeNoClient()}, 2.99, "Vipps"},
		{"both, at the card minimum", []payments.Provider{&stubPayProvider{name: vipps.ProviderName}, stripeNoClient()}, 3, "Vipps eller kort"},
		{"stripe only, under the minimum", []payments.Provider{stripeNoClient()}, 1, ""},
		{"stripe only, at the minimum", []payments.Provider{stripeNoClient()}, 3, "Kort"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newProvidersHandler(nil, tc.providers...)
			if got := h.emailPayMethods(link, tc.amount); got != tc.want {
				t.Errorf("emailPayMethods(%v) = %q, want %q", tc.amount, got, tc.want)
			}
		})
	}
}

func TestProvidersFor(t *testing.T) {
	h := newProvidersHandler(nil, stripeNoClient(), &stubPayProvider{name: vipps.ProviderName})
	if got := h.providersFor(299); !reflect.DeepEqual(got, []string{"vipps"}) {
		t.Errorf("providersFor(299) = %v, want [vipps]", got)
	}
	if got := h.providersFor(300); !reflect.DeepEqual(got, []string{"vipps", "stripe"}) {
		t.Errorf("providersFor(300) = %v, want [vipps stripe]", got)
	}
	if got := h.providerMinAmounts(); !reflect.DeepEqual(got, map[string]float64{"stripe": 3}) {
		t.Errorf("providerMinAmounts = %v", got)
	}
}

func TestCheckoutProviders_StripeMinimum(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE clubs SET bank_account = '1234.56.78903' WHERE id = $1`, clubID); err != nil {
		t.Fatal(err)
	}
	fake := newFakeStripe(t)
	both := newProvidersHandler(pool, &stubPayProvider{name: vipps.ProviderName}, stripe.NewPaymentProvider(fake.client(), "testklubb"))
	stripeOnly := newStripeMintHandler(pool, fake)

	orderAt := func(price float64) string {
		orderID, token := seedCheckoutOrder(t, pool, clubID, "")
		addOrderLine(t, pool, orderID, "Test item", price)
		return token
	}
	under, at := orderAt(2.99), orderAt(3)

	if st, _ := getCheckoutState(t, both, under); !reflect.DeepEqual(st.Providers, []string{"vipps"}) || st.BankAccount != "" {
		t.Errorf("both at 2.99: providers=%v bank=%q, want [vipps] and no bank box", st.Providers, st.BankAccount)
	}
	if st, _ := getCheckoutState(t, both, at); !reflect.DeepEqual(st.Providers, []string{"vipps", "stripe"}) {
		t.Errorf("both at 3.00: providers=%v, want [vipps stripe]", st.Providers)
	}
	st, raw := getCheckoutState(t, stripeOnly, under)
	if string(raw["providers"]) != "[]" || st.OnlineBlocked || st.BankAccount != "1234.56.78903" {
		t.Errorf("stripe only at 2.99: providers=%s blocked=%v bank=%q, want [] false and the account", raw["providers"], st.OnlineBlocked, st.BankAccount)
	}
	if st, _ := getCheckoutState(t, stripeOnly, at); !reflect.DeepEqual(st.Providers, []string{"stripe"}) {
		t.Errorf("stripe only at 3.00: providers=%v, want [stripe]", st.Providers)
	}
	if n := len(fake.createCalls()); n != 0 {
		t.Errorf("loading the page called Stripe %d times", n)
	}
}

// attemptCount is the number of payment attempts on the order behind token.
func attemptCount(t *testing.T, pool *pgxpool.Pool, token string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payment_attempts pa JOIN orders o ON o.id = pa.order_id WHERE o.token = $1`, token,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPayCheckout_StripeMinimum(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)

	newOrder := func(price float64) string {
		orderID, token := seedCheckoutOrder(t, pool, clubID, "")
		addOrderLine(t, pool, orderID, "Test item", price)
		return token
	}

	t.Run("under the minimum is refused without calling Stripe", func(t *testing.T) {
		fake := newFakeStripe(t)
		router := mintTestRouter(newStripeMintHandler(pool, fake))
		token := newOrder(1)
		assertBody(t, serve(router, http.MethodPost, "/checkout/"+token+"/pay", `{"provider":"stripe"}`),
			http.StatusConflict, amountBelowMinimumBody)
		if n := len(fake.createCalls()); n != 0 {
			t.Errorf("Stripe creates = %d, want 0", n)
		}
		if n := attemptCount(t, pool, token); n != 0 {
			t.Errorf("payment attempts = %d, want none for an amount never chargeable", n)
		}

		// The one-tap email link sends the payer to the page, which shows
		// the bank-transfer box.
		rec := serve(router, http.MethodGet, "/betal/"+token, "")
		if want := mintTestFrontend + "/betal/" + token; rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("redirect = %d %q, want 302 %q", rec.Code, rec.Header().Get("Location"), want)
		}
		if n := len(fake.createCalls()); n != 0 {
			t.Errorf("Stripe creates after redirect = %d, want 0", n)
		}
	})

	t.Run("Stripe's own amount_too_small is the same 409", func(t *testing.T) {
		fake := newFakeStripe(t)
		fake.createError = `{"error":{"type":"invalid_request_error","code":"amount_too_small","message":"The Checkout Session's total amount due must add up to at least kr3.00 nok."}}`
		router := mintTestRouter(newStripeMintHandler(pool, fake))
		token := newOrder(100)
		assertBody(t, serve(router, http.MethodPost, "/checkout/"+token+"/pay", `{"provider":"stripe"}`),
			http.StatusConflict, amountBelowMinimumBody)
		if n := len(fake.createCalls()); n != 1 {
			t.Errorf("Stripe creates = %d, want 1", n)
		}
		var status string
		if err := pool.QueryRow(context.Background(),
			`SELECT pa.status FROM payment_attempts pa JOIN orders o ON o.id = pa.order_id WHERE o.token = $1`, token,
		).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "failed" {
			t.Errorf("attempt status = %q, want failed", status)
		}
	})

	t.Run("vipps is unaffected by the card minimum", func(t *testing.T) {
		sv := &stubPayProvider{name: vipps.ProviderName}
		router := mintTestRouter(newProvidersHandler(pool, sv, stripeNoClient()))
		if rec := serve(router, http.MethodPost, "/checkout/"+newOrder(1)+"/pay", `{"provider":"vipps"}`); rec.Code != http.StatusOK || sv.createCalls() != 1 {
			t.Errorf("vipps at 1 kr = %d %q, creates %d", rec.Code, rec.Body.String(), sv.createCalls())
		}
	})
}

func TestQRPay_StripeMinimum(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	qrToken := seedQRTargetAt(t, pool, clubID, 1)
	fake := newFakeStripe(t)
	router := mintTestRouter(newStripeMintHandler(pool, fake))

	assertBody(t, serve(router, http.MethodPost, "/qr/"+qrToken+"/pay", `{"units":1,"provider":"stripe"}`),
		http.StatusConflict, amountBelowMinimumBody)
	var orders int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE club_id = $1`, clubID).Scan(&orders); err != nil {
		t.Fatal(err)
	}
	if orders != 0 {
		t.Errorf("orders = %d, want none for a refused amount", orders)
	}
	if n := len(fake.createCalls()); n != 0 {
		t.Errorf("Stripe creates = %d, want 0", n)
	}

	// Three nights at 1 kr reach the minimum.
	if rec := serve(router, http.MethodPost, "/qr/"+qrToken+"/pay", `{"units":3,"provider":"stripe"}`); rec.Code != http.StatusOK || len(fake.createCalls()) != 1 {
		t.Errorf("3 units = %d %q, creates %d", rec.Code, rec.Body.String(), len(fake.createCalls()))
	}
}

func TestQRTarget_ProviderMinAmount(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	qrToken := seedQRTargetAt(t, pool, clubID, 1)

	get := func(h *InvoiceHandler) map[string]json.RawMessage {
		t.Helper()
		rec := serve(mintTestRouter(h), http.MethodGet, "/qr/"+qrToken, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET qr = %d %q", rec.Code, rec.Body.String())
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}

	raw := get(newProvidersHandler(pool, &stubPayProvider{name: vipps.ProviderName}, stripeNoClient()))
	if string(raw["providers"]) != `["vipps","stripe"]` || string(raw["provider_min_amount"]) != `{"stripe":3}` {
		t.Errorf("both: providers=%s min=%s", raw["providers"], raw["provider_min_amount"])
	}
	raw = get(newProvidersHandler(pool, &stubPayProvider{name: vipps.ProviderName}))
	if string(raw["provider_min_amount"]) != `{}` {
		t.Errorf("vipps only: provider_min_amount = %s, want {}", raw["provider_min_amount"])
	}
}

func TestFakturaPayLink_StripeMinimum(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 901)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET total_amount = 2 WHERE id = $1`, invID); err != nil {
		t.Fatal(err)
	}

	stripeOnly := newProvidersHandler(pool, stripeNoClient())
	if token := stripeOnly.ensureFakturaOrderToken(ctx, clubID, invID, true); token != "" {
		t.Errorf("stripe only: pay link for 2 kr = %q, want none", token)
	}
	// The admin's copy-link says why, rather than "not set up".
	rec := httptest.NewRecorder()
	stripeOnly.HandleGetInvoicePayLink(rec, payLinkReq(invID, clubID))
	assertBody(t, rec, http.StatusConflict, amountBelowMinimumBody)
	both := newProvidersHandler(pool, &stubPayProvider{name: vipps.ProviderName}, stripeNoClient())
	if token := both.ensureFakturaOrderToken(ctx, clubID, invID, true); token == "" {
		t.Error("with Vipps: no pay link for 2 kr")
	}
	rec = httptest.NewRecorder()
	both.HandleGetInvoicePayLink(rec, payLinkReq(invID, clubID))
	if rec.Code != http.StatusOK {
		t.Errorf("with Vipps: pay link = %d %q, want 200", rec.Code, rec.Body.String())
	}
}
