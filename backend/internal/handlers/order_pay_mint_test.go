package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

const mintTestFrontend = "https://club.example.invalid"

var vippsReferencePattern = regexp.MustCompile(`^chk-[0-9a-f]{32}$`)

// newMintTestHandler builds an InvoiceHandler whose online payments go to
// client; a nil client is an instance without Vipps credentials.
func newMintTestHandler(pool *pgxpool.Pool, client *vipps.Client) *InvoiceHandler {
	var providers *payments.Registry
	if client != nil {
		providers = payments.NewRegistry(vipps.NewPaymentProvider(client))
	}
	return &InvoiceHandler{
		db:        pool,
		config:    &config.Config{FrontendURL: mintTestFrontend},
		providers: providers,
		log:       zerolog.Nop(),
	}
}

// mintTestRouter mounts the public pay routes that mint a payment, and the
// GETs that tell the page which providers it may offer.
func mintTestRouter(h *InvoiceHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/betal/{token}", h.HandleOrderPayRedirect)
	r.Get("/checkout/{token}", h.HandleGetCheckout)
	r.Get("/qr/{token}", h.HandleGetQRTarget)
	r.Post("/checkout/{token}/pay", h.HandlePayCheckout)
	r.Post("/qr/{token}/pay", h.HandlePayQRTarget)
	return r
}

func serve(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// seedCheckoutOrder inserts a pending checkout order; userID "" makes it a
// guest order (user_id NULL).
func seedCheckoutOrder(t *testing.T, pool *pgxpool.Pool, clubID, userID string) (orderID, token string) {
	t.Helper()
	var uid *string
	if userID != "" {
		uid = &userID
	}
	token = genCheckoutToken()
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO orders (club_id, user_id, total_amount, token) VALUES ($1, $2, 0, $3) RETURNING id`,
		clubID, uid, token,
	).Scan(&orderID); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	return orderID, token
}

func addOrderSource(t *testing.T, pool *pgxpool.Pool, orderID, invoiceID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO order_sources (order_id, invoice_id) VALUES ($1, $2)`, orderID, invoiceID,
	); err != nil {
		t.Fatalf("insert order source: %v", err)
	}
}

func addOrderLine(t *testing.T, pool *pgxpool.Pool, orderID, name string, price float64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO order_lines (order_id, name, quantity, unit_price, total_price) VALUES ($1, $2, 1, $3, $3)`,
		orderID, name, price,
	); err != nil {
		t.Fatalf("insert order line: %v", err)
	}
}

func orderReference(t *testing.T, pool *pgxpool.Pool, orderID string) (ref string, total float64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT vipps_reference, total_amount FROM orders WHERE id = $1`, orderID,
	).Scan(&ref, &total); err != nil {
		t.Fatalf("load order: %v", err)
	}
	return ref, total
}

func assertBody(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantBody string) {
	t.Helper()
	if rec.Code != wantStatus || rec.Body.String() != wantBody {
		t.Errorf("got %d %q, want %d %q", rec.Code, rec.Body.String(), wantStatus, wantBody)
	}
}

func TestMintOrderPayment_MemberFaktura(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	fake := newFakeVipps(t)
	h := newMintTestHandler(pool, fake.client())
	router := mintTestRouter(h)

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 900)

	token := h.ensureFakturaOrderToken(ctx, clubID, invID, true)
	if token == "" {
		t.Fatal("ensureFakturaOrderToken returned no token with a provider configured")
	}
	var orderID string
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE token = $1`, token).Scan(&orderID); err != nil {
		t.Fatalf("load order: %v", err)
	}
	longName := strings.Repeat("x", 150)
	addOrderLine(t, pool, orderID, longName, 50.25)

	rec := serve(router, http.MethodGet, "/betal/"+token, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("redirect: status %d body %q", rec.Code, rec.Body.String())
	}

	calls := fake.createCalls()
	if len(calls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(calls))
	}
	c := calls[0]
	ref := c.Body.Reference
	if !vippsReferencePattern.MatchString(ref) {
		t.Errorf("reference %q does not match chk-<32 hex>", ref)
	}
	if c.IdempotencyKey != ref {
		t.Errorf("idempotency key %q, want the reference %q", c.IdempotencyKey, ref)
	}
	if got := rec.Header().Get("Location"); got != "https://vipps.example.invalid/checkout/"+ref {
		t.Errorf("Location = %q", got)
	}
	if c.Body.Amount != (vipps.Amount{Currency: "NOK", Value: 50025}) {
		t.Errorf("amount = %+v, want NOK 50025 øre", c.Body.Amount)
	}
	if want := mintTestFrontend + "/betal/" + token + "/kvittering"; c.Body.ReturnURL != want {
		t.Errorf("returnUrl = %q, want the receipt page %q", c.Body.ReturnURL, want)
	}
	wantDesc := ("Faktura #900, " + longName)[:100]
	if c.Body.PaymentDescription != wantDesc {
		t.Errorf("description = %q (len %d), want %q", c.Body.PaymentDescription, len(c.Body.PaymentDescription), wantDesc)
	}
	if c.Body.UserFlow != "WEB_REDIRECT" || c.Body.PaymentMethod.Type != "WALLET" {
		t.Errorf("userFlow/method = %q/%q", c.Body.UserFlow, c.Body.PaymentMethod.Type)
	}
	if c.Body.Profile != nil {
		t.Errorf("member order asked for profile %+v; want none", c.Body.Profile)
	}

	gotRef, total := orderReference(t, pool, orderID)
	if gotRef != ref || total != 500.25 {
		t.Errorf("order reference/total = %q/%v, want %q/500.25", gotRef, total, ref)
	}

	// Every click mints a fresh reference and the order keeps only the latest.
	rec = serve(router, http.MethodGet, "/betal/"+token, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("second redirect: status %d", rec.Code)
	}
	calls = fake.createCalls()
	if len(calls) != 2 || calls[1].Body.Reference == ref {
		t.Fatalf("second mint did not use a fresh reference: %+v", calls)
	}
	if gotRef, _ := orderReference(t, pool, orderID); gotRef != calls[1].Body.Reference {
		t.Errorf("order reference = %q, want latest %q", gotRef, calls[1].Body.Reference)
	}
}

func TestMintOrderPayment_GuestCheckout(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	fake := newFakeVipps(t)
	router := mintTestRouter(newMintTestHandler(pool, fake.client()))
	clubID := testutil.SeedClub(t, pool)

	orderID, token := seedCheckoutOrder(t, pool, clubID, "")
	addOrderLine(t, pool, orderID, "Gjesteplass", 200)

	rec := serve(router, http.MethodPost, "/checkout/"+token+"/pay", "")
	calls := fake.createCalls()
	if len(calls) != 1 {
		t.Fatalf("create calls = %d, want 1 (status %d %q)", len(calls), rec.Code, rec.Body.String())
	}
	ref := calls[0].Body.Reference
	assertBody(t, rec, http.StatusOK, `{"redirect_url":"https://vipps.example.invalid/checkout/`+ref+`"}`+"\n")
	if p := calls[0].Body.Profile; p == nil || p.Scope != "name" {
		t.Errorf("guest order profile = %+v, want scope name", p)
	}
	if calls[0].Body.Amount.Value != 20000 || calls[0].Body.PaymentDescription != "Gjesteplass" {
		t.Errorf("amount/desc = %d/%q", calls[0].Body.Amount.Value, calls[0].Body.PaymentDescription)
	}
	if gotRef, _ := orderReference(t, pool, orderID); gotRef != ref {
		t.Errorf("order reference = %q, want %q", gotRef, ref)
	}

	// An unnamed line falls back to the generic description.
	order2, token2 := seedCheckoutOrder(t, pool, clubID, "")
	addOrderLine(t, pool, order2, "", 10)
	serve(router, http.MethodPost, "/checkout/"+token2+"/pay", "")
	calls = fake.createCalls()
	if len(calls) != 2 || calls[1].Body.PaymentDescription != "Betaling" {
		t.Errorf("unnamed line description: %+v", calls)
	}
}

func TestMintOrderPayment_HarborQR(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	fake := newFakeVipps(t)
	router := mintTestRouter(newMintTestHandler(pool, fake.client()))
	clubID := testutil.SeedClub(t, pool)

	var priceItemID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount) VALUES ($1, 'guest_slip', 'Test guest night', 150) RETURNING id`,
		clubID,
	).Scan(&priceItemID); err != nil {
		t.Fatalf("insert price item: %v", err)
	}
	qrToken := "qr-" + testutil.RandomHex(8)
	if _, err := pool.Exec(ctx,
		`INSERT INTO harbor_payment_targets (club_id, token, label, price_item_id, short_code)
		 VALUES ($1, $2, 'Test pier', $3, 'abcde')`,
		clubID, qrToken, priceItemID,
	); err != nil {
		t.Fatalf("insert target: %v", err)
	}

	rec := serve(router, http.MethodPost, "/qr/"+qrToken+"/pay", `{"units":2,"guest_name":""}`)
	calls := fake.createCalls()
	if len(calls) != 1 {
		t.Fatalf("create calls = %d, want 1 (status %d %q)", len(calls), rec.Code, rec.Body.String())
	}
	ref := calls[0].Body.Reference
	assertBody(t, rec, http.StatusOK, `{"redirect_url":"https://vipps.example.invalid/checkout/`+ref+`"}`+"\n")
	if calls[0].Body.Amount.Value != 30000 || calls[0].Body.PaymentDescription != "Test guest night" {
		t.Errorf("amount/desc = %d/%q", calls[0].Body.Amount.Value, calls[0].Body.PaymentDescription)
	}
	if p := calls[0].Body.Profile; p == nil || p.Scope != "name" {
		t.Errorf("QR guest profile = %+v, want scope name", p)
	}

	var (
		userID   *string
		provider string
		storedRf string
	)
	if err := pool.QueryRow(ctx,
		`SELECT user_id, provider, vipps_reference FROM orders WHERE club_id = $1`, clubID,
	).Scan(&userID, &provider, &storedRf); err != nil {
		t.Fatalf("load QR order: %v", err)
	}
	if userID != nil || provider != "vipps" || storedRf != ref {
		t.Errorf("QR order user/provider/ref = %v/%q/%q", userID, provider, storedRf)
	}
}

func TestMintOrderPayment_Blocked(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	fake := newFakeVipps(t)
	router := mintTestRouter(newMintTestHandler(pool, fake.client()))
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})

	tests := []struct {
		name  string
		setup func(invoiceID string)
	}{
		{"forced off", func(invoiceID string) {
			if _, err := pool.Exec(ctx, `UPDATE invoices SET online_payment_opt_in = false WHERE id = $1`, invoiceID); err != nil {
				t.Fatal(err)
			}
		}},
		{"over club cap", func(string) {
			if _, err := pool.Exec(ctx, `UPDATE clubs SET online_max_amount = 100 WHERE id = $1`, clubID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			invID := seedPayableInvoice(t, pool, clubID, userID, "open", 910+i)
			orderID, token := seedCheckoutOrder(t, pool, clubID, userID)
			addOrderSource(t, pool, orderID, invID)
			tc.setup(invID)

			assertBody(t, serve(router, http.MethodPost, "/checkout/"+token+"/pay", ""),
				http.StatusConflict, `{"error":"online_payment_over_limit"}`+"\n")

			rec := serve(router, http.MethodGet, "/betal/"+token, "")
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != mintTestFrontend+"/betal/"+token {
				t.Errorf("redirect = %d %q, want 302 to the checkout page", rec.Code, rec.Header().Get("Location"))
			}
			if n := len(fake.createCalls()); n != 0 {
				t.Errorf("blocked order reached Vipps %d times", n)
			}
		})
	}
}

func TestMintOrderPayment_NothingToPayAndSettled(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	fake := newFakeVipps(t)
	router := mintTestRouter(newMintTestHandler(pool, fake.client()))
	clubID := testutil.SeedClub(t, pool)

	_, empty := seedCheckoutOrder(t, pool, clubID, "")
	assertBody(t, serve(router, http.MethodPost, "/checkout/"+empty+"/pay", ""),
		http.StatusBadRequest, `{"error":"nothing to pay"}`+"\n")
	rec := serve(router, http.MethodGet, "/betal/"+empty, "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != mintTestFrontend {
		t.Errorf("empty order redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	paidID, paid := seedCheckoutOrder(t, pool, clubID, "")
	addOrderLine(t, pool, paidID, "Test item", 100)
	if _, err := pool.Exec(context.Background(), `UPDATE orders SET status = 'paid' WHERE id = $1`, paidID); err != nil {
		t.Fatal(err)
	}
	assertBody(t, serve(router, http.MethodPost, "/checkout/"+paid+"/pay", ""),
		http.StatusConflict, `{"error":"checkout is already paid"}`+"\n")
	rec = serve(router, http.MethodGet, "/betal/"+paid, "")
	if want := mintTestFrontend + "/betal/" + paid + "/kvittering"; rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
		t.Errorf("paid order redirect = %d %q, want the receipt page %q", rec.Code, rec.Header().Get("Location"), want)
	}

	if n := len(fake.createCalls()); n != 0 {
		t.Errorf("Vipps called %d times for nothing to pay", n)
	}
}

func TestMintOrderPayment_ProviderError(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	fake := newFakeVipps(t)
	fake.createStatus = http.StatusInternalServerError
	router := mintTestRouter(newMintTestHandler(pool, fake.client()))
	clubID := testutil.SeedClub(t, pool)

	orderID, token := seedCheckoutOrder(t, pool, clubID, "")
	addOrderLine(t, pool, orderID, "Test item", 100)

	assertBody(t, serve(router, http.MethodPost, "/checkout/"+token+"/pay", ""),
		http.StatusBadGateway, `{"error":"could not start online payment"}`+"\n")
	assertBody(t, serve(router, http.MethodGet, "/betal/"+token, ""),
		http.StatusBadGateway, "Kunne ikke starte betalingen. Prøv igjen.\n")
	if ref, _ := orderReference(t, pool, orderID); ref != "" {
		t.Errorf("failed mint stored reference %q", ref)
	}

	var priceItemID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount) VALUES ($1, 'guest_slip', 'Test guest night', 150) RETURNING id`,
		clubID,
	).Scan(&priceItemID); err != nil {
		t.Fatalf("insert price item: %v", err)
	}
	qrToken := "qr-" + testutil.RandomHex(8)
	if _, err := pool.Exec(ctx,
		`INSERT INTO harbor_payment_targets (club_id, token, label, price_item_id, short_code)
		 VALUES ($1, $2, 'Test pier', $3, 'abcde')`,
		clubID, qrToken, priceItemID,
	); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	assertBody(t, serve(router, http.MethodPost, "/qr/"+qrToken+"/pay", `{"units":1}`),
		http.StatusBadGateway, `{"error":"Kunne ikke starte betalingen. Prøv igjen."}`+"\n")
}

func TestPayment_NoProviderConfigured(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	h := newMintTestHandler(pool, nil)
	router := mintTestRouter(h)
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 920)

	orderID, token := seedCheckoutOrder(t, pool, clubID, "")
	addOrderLine(t, pool, orderID, "Test item", 100)

	assertBody(t, serve(router, http.MethodGet, "/betal/"+token, ""),
		http.StatusServiceUnavailable, "Nettbetaling er ikke tilgjengelig akkurat nå.\n")
	assertBody(t, serve(router, http.MethodPost, "/checkout/"+token+"/pay", ""),
		http.StatusServiceUnavailable, `{"error":"online payment is not available"}`+"\n")
	assertBody(t, serve(router, http.MethodPost, "/qr/any-token/pay", `{"units":1}`),
		http.StatusServiceUnavailable, `{"error":"Nettbetaling er ikke tilgjengelig akkurat nå."}`+"\n")

	rec := httptest.NewRecorder()
	h.HandleGetInvoicePayLink(rec, payLinkReq(invID, clubID))
	assertBody(t, rec, http.StatusConflict, `{"error":"online_payment_unavailable"}`+"\n")

	rec = httptest.NewRecorder()
	h.HandleGetUserPayLink(rec, userPayLinkReq(userID, clubID))
	assertBody(t, rec, http.StatusConflict, `{"error":"online_payment_unavailable"}`+"\n")

	if tok := h.ensureFakturaOrderToken(ctx, clubID, invID, true); tok != "" {
		t.Errorf("ensureFakturaOrderToken = %q with no provider, want empty", tok)
	}
}

// A mint charges in NOK only, so an order in another currency is refused
// before any provider is called or attempt recorded (BRY-452); a lower-case
// or blank currency reads as NOK.
func TestMintOrderPayment_UnsupportedCurrency(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	fake := newFakeVipps(t)
	router := mintTestRouter(newMintTestHandler(pool, fake.client()))
	clubID := testutil.SeedClub(t, pool)

	order := func(t *testing.T, currency string) (orderID, token string) {
		t.Helper()
		orderID, token = seedCheckoutOrder(t, pool, clubID, "")
		addOrderLine(t, pool, orderID, "Gjesteplass", 200)
		if _, err := pool.Exec(ctx, `UPDATE orders SET currency = $2 WHERE id = $1`, orderID, currency); err != nil {
			t.Fatal(err)
		}
		return orderID, token
	}
	attempts := func(t *testing.T, orderID string) (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_attempts WHERE order_id = $1`, orderID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("checkout pay", func(t *testing.T) {
		orderID, token := order(t, "EUR")
		assertBody(t, serve(router, http.MethodPost, "/checkout/"+token+"/pay", ""),
			http.StatusConflict, `{"error":"unsupported_currency"}`+"\n")
		if n, a := len(fake.createCalls()), attempts(t, orderID); n != 0 || a != 0 {
			t.Errorf("refused order reached Vipps %d times, left %d attempts", n, a)
		}
	})

	t.Run("pay link redirect", func(t *testing.T) {
		orderID, token := order(t, "SEK")
		rec := serve(router, http.MethodGet, "/betal/"+token, "")
		if rec.Code != http.StatusConflict || rec.Header().Get("Location") != "" {
			t.Errorf("redirect = %d %q, want a plain 409", rec.Code, rec.Header().Get("Location"))
		}
		if n, a := len(fake.createCalls()), attempts(t, orderID); n != 0 || a != 0 {
			t.Errorf("refused order reached Vipps %d times, left %d attempts", n, a)
		}
	})

	t.Run("QR target", func(t *testing.T) {
		var priceItemID string
		if err := pool.QueryRow(ctx,
			`INSERT INTO price_items (club_id, category, name, amount, currency) VALUES ($1, 'guest_slip', 'Test guest night', 15, 'EUR') RETURNING id`,
			clubID,
		).Scan(&priceItemID); err != nil {
			t.Fatalf("insert price item: %v", err)
		}
		qrToken := "qr-" + testutil.RandomHex(8)
		if _, err := pool.Exec(ctx,
			`INSERT INTO harbor_payment_targets (club_id, token, label, price_item_id, short_code)
			 VALUES ($1, $2, 'Test pier', $3, 'abcdf')`,
			clubID, qrToken, priceItemID,
		); err != nil {
			t.Fatalf("insert target: %v", err)
		}
		countOrders := func() (n int) {
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE club_id = $1`, clubID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		before := countOrders()
		assertBody(t, serve(router, http.MethodPost, "/qr/"+qrToken+"/pay", `{"units":1}`),
			http.StatusConflict, `{"error":"unsupported_currency"}`+"\n")
		if n, left := len(fake.createCalls()), countOrders()-before; n != 0 || left != 0 {
			t.Errorf("refused QR payment reached Vipps %d times, left %d orders", n, left)
		}
	})

	for _, currency := range []string{"NOK", "nok", ""} {
		t.Run("pays as NOK: "+currency, func(t *testing.T) {
			before := len(fake.createCalls())
			_, token := order(t, currency)
			rec := serve(router, http.MethodPost, "/checkout/"+token+"/pay", "")
			calls := fake.createCalls()
			if rec.Code != http.StatusOK || len(calls) != before+1 {
				t.Fatalf("status %d %q, create calls %d", rec.Code, rec.Body.String(), len(calls)-before)
			}
			if got := calls[before].Body.Amount; got != (vipps.Amount{Currency: "NOK", Value: 20000}) {
				t.Errorf("amount = %+v, want NOK 20000", got)
			}
		})
	}
}

// The description limit is in characters: an order line that crosses the
// 100-character mark inside a multi-byte character is cut before it, never
// through it.
func TestMintOrderPayment_DescriptionCutAtACharacter(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	fake := newFakeVipps(t)
	router := mintTestRouter(newMintTestHandler(pool, fake.client()))
	clubID := testutil.SeedClub(t, pool)

	orderID, token := seedCheckoutOrder(t, pool, clubID, "")
	name := strings.Repeat("æøå🚤", 40)
	addOrderLine(t, pool, orderID, name, 200)

	if rec := serve(router, http.MethodPost, "/checkout/"+token+"/pay", ""); rec.Code != http.StatusOK {
		t.Fatalf("status %d %q", rec.Code, rec.Body.String())
	}
	calls := fake.createCalls()
	if len(calls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(calls))
	}
	if got, want := calls[0].Body.PaymentDescription, string([]rune(name)[:100]); got != want {
		t.Errorf("description = %q, want the first 100 characters %q", got, want)
	}
}
