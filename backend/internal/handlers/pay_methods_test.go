package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// stubPayProvider is a payments.Provider that answers at once, so a handler
// can be given several providers without fake HTTP servers behind them.
type stubPayProvider struct {
	name  string
	mu    sync.Mutex
	calls int
}

func (p *stubPayProvider) Name() string { return p.name }

func (p *stubPayProvider) CreatePayment(_ context.Context, req payments.Request) (payments.Result, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return payments.Result{
		RedirectURL: "https://pay.example.invalid/" + p.name + "/" + req.AttemptID,
		Reference:   p.name + "-" + req.AttemptID,
	}, nil
}

func (p *stubPayProvider) createCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestAvailableProviders(t *testing.T) {
	tests := []struct {
		name      string
		providers []payments.Provider
		want      []string
		wantEmail string
	}{
		{"none", nil, nil, ""},
		{"vipps only", []payments.Provider{&stubPayProvider{name: vipps.ProviderName}}, []string{"vipps"}, "Vipps"},
		{"stripe only", []payments.Provider{&stubPayProvider{name: stripe.ProviderName}}, []string{"stripe"}, "Kort"},
		// Names() sorts stripe before vipps; the default leads regardless.
		{"both", []payments.Provider{&stubPayProvider{name: stripe.ProviderName}, &stubPayProvider{name: vipps.ProviderName}},
			[]string{"vipps", "stripe"}, "Vipps eller kort"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newProvidersHandler(nil, tc.providers...)
			if got := h.availableProviders(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("availableProviders = %v, want %v", got, tc.want)
			}
			if got := h.emailPayMethods("https://club.example.invalid/betal/x", 450); got != tc.wantEmail {
				t.Errorf("emailPayMethods = %q, want %q", got, tc.wantEmail)
			}
			if got := h.emailPayMethods("", 450); got != "" {
				t.Errorf("emailPayMethods without a link = %q, want empty", got)
			}
		})
	}
}

// checkoutState is the part of GET /checkout/{token} the payer's choice needs.
type checkoutState struct {
	Providers     []string `json:"providers"`
	OnlineBlocked bool     `json:"online_blocked"`
	BankAccount   string   `json:"bank_account"`
	KID           string   `json:"kid_number"`
}

func getCheckoutState(t *testing.T, h *InvoiceHandler, token string) (checkoutState, map[string]json.RawMessage) {
	t.Helper()
	router := mintTestRouter(h)
	rec := serve(router, http.MethodGet, "/checkout/"+token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET checkout = %d %q", rec.Code, rec.Body.String())
	}
	var st checkoutState
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	return st, raw
}

func TestCheckoutProviders(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	if _, err := pool.Exec(ctx, `UPDATE clubs SET bank_account = '1234.56.78903' WHERE id = $1`, clubID); err != nil {
		t.Fatal(err)
	}

	both := newProvidersHandler(pool, &stubPayProvider{name: stripe.ProviderName}, &stubPayProvider{name: vipps.ProviderName})
	none := newProvidersHandler(pool)

	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 1101)
	orderID, token := seedCheckoutOrder(t, pool, clubID, userID)
	addOrderSource(t, pool, orderID, invID)

	st, raw := getCheckoutState(t, both, token)
	if !reflect.DeepEqual(st.Providers, []string{"vipps", "stripe"}) || st.OnlineBlocked {
		t.Errorf("both: providers=%v blocked=%v, want [vipps stripe] false", st.Providers, st.OnlineBlocked)
	}
	if _, ok := raw["vipps_blocked"]; ok {
		t.Error("response still carries vipps_blocked")
	}

	if st.BankAccount != "" {
		t.Errorf("both: bank_account = %q, want empty while online payment is offered", st.BankAccount)
	}

	// No provider: bank transfer is the only way, so its details are shown.
	st, raw = getCheckoutState(t, none, token)
	if string(raw["providers"]) != "[]" || st.OnlineBlocked || st.BankAccount != "1234.56.78903" {
		t.Errorf("none: providers=%s blocked=%v bank=%q, want [] false and the account", raw["providers"], st.OnlineBlocked, st.BankAccount)
	}

	// Blocked: no provider is offered, and the bank details are.
	if _, err := pool.Exec(ctx, `UPDATE invoices SET online_payment_opt_in = false WHERE id = $1`, invID); err != nil {
		t.Fatal(err)
	}
	st, raw = getCheckoutState(t, both, token)
	if string(raw["providers"]) != "[]" || !st.OnlineBlocked {
		t.Errorf("blocked: providers=%s blocked=%v, want [] true", raw["providers"], st.OnlineBlocked)
	}
	if st.BankAccount != "1234.56.78903" || st.KID == "" {
		t.Errorf("blocked: bank=%q kid=%q, want both set", st.BankAccount, st.KID)
	}
}

func TestOrderPayRedirect_ProviderCount(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)

	newOrder := func() (string, string) {
		orderID, token := seedCheckoutOrder(t, pool, clubID, "")
		addOrderLine(t, pool, orderID, "Test item", 100)
		return orderID, token
	}

	t.Run("two providers go to the choice page", func(t *testing.T) {
		sv, ss := &stubPayProvider{name: vipps.ProviderName}, &stubPayProvider{name: stripe.ProviderName}
		router := mintTestRouter(newProvidersHandler(pool, sv, ss))
		_, token := newOrder()
		rec := serve(router, http.MethodGet, "/betal/"+token, "")
		if want := mintTestFrontend + "/betal/" + token; rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("redirect = %d %q, want 302 %q", rec.Code, rec.Header().Get("Location"), want)
		}
		if sv.createCalls()+ss.createCalls() != 0 {
			t.Error("a payment was minted before the payer chose")
		}

		// A paid order still goes to its receipt.
		paidID, paid := newOrder()
		if _, err := pool.Exec(ctx, `UPDATE orders SET status = 'paid' WHERE id = $1`, paidID); err != nil {
			t.Fatal(err)
		}
		rec = serve(router, http.MethodGet, "/betal/"+paid, "")
		if want := mintTestFrontend + "/betal/" + paid + "/kvittering"; rec.Header().Get("Location") != want {
			t.Errorf("paid redirect = %q, want %q", rec.Header().Get("Location"), want)
		}
	})

	t.Run("one provider mints at once", func(t *testing.T) {
		ss := &stubPayProvider{name: stripe.ProviderName}
		router := mintTestRouter(newProvidersHandler(pool, ss))
		_, token := newOrder()
		rec := serve(router, http.MethodGet, "/betal/"+token, "")
		if rec.Code != http.StatusFound || ss.createCalls() != 1 {
			t.Fatalf("redirect = %d %q, creates = %d", rec.Code, rec.Body.String(), ss.createCalls())
		}
		var attemptID string
		if err := pool.QueryRow(ctx,
			`SELECT pa.id FROM payment_attempts pa JOIN orders o ON o.id = pa.order_id WHERE o.token = $1`, token,
		).Scan(&attemptID); err != nil {
			t.Fatal(err)
		}
		if want := "https://pay.example.invalid/stripe/" + attemptID; rec.Header().Get("Location") != want {
			t.Errorf("Location = %q, want %q", rec.Header().Get("Location"), want)
		}
	})

	t.Run("no provider is unavailable", func(t *testing.T) {
		_, token := newOrder()
		assertBody(t, serve(mintTestRouter(newProvidersHandler(pool)), http.MethodGet, "/betal/"+token, ""),
			http.StatusServiceUnavailable, "Nettbetaling er ikke tilgjengelig akkurat nå.\n")
	})
}

func TestQRTargetProviders(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	qrToken := seedQRTarget(t, pool, clubID)

	get := func(h *InvoiceHandler) string {
		t.Helper()
		rec := serve(mintTestRouter(h), http.MethodGet, "/qr/"+qrToken, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET qr = %d %q", rec.Code, rec.Body.String())
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		return string(raw["providers"])
	}
	if got := get(newProvidersHandler(pool, &stubPayProvider{name: stripe.ProviderName}, &stubPayProvider{name: vipps.ProviderName})); got != `["vipps","stripe"]` {
		t.Errorf("both: providers = %s", got)
	}
	if got := get(newProvidersHandler(pool, &stubPayProvider{name: stripe.ProviderName})); got != `["stripe"]` {
		t.Errorf("stripe only: providers = %s", got)
	}
	if got := get(newProvidersHandler(pool)); got != `[]` {
		t.Errorf("none: providers = %s", got)
	}
}
