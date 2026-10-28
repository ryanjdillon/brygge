package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// newProvidersHandler builds an InvoiceHandler with the given providers.
func newProvidersHandler(pool *pgxpool.Pool, providers ...payments.Provider) *InvoiceHandler {
	return &InvoiceHandler{
		db:        pool,
		config:    &config.Config{FrontendURL: mintTestFrontend},
		providers: payments.NewRegistry(providers...),
		log:       zerolog.Nop(),
	}
}

// newStripeMintHandler is an instance with Stripe as its only provider.
func newStripeMintHandler(pool *pgxpool.Pool, f *fakeStripe) *InvoiceHandler {
	return newProvidersHandler(pool, stripe.NewPaymentProvider(f.client(), "testklubb"))
}

// seedQRTarget adds a guest-night QR target at 150 kr a unit.
func seedQRTarget(t *testing.T, pool *pgxpool.Pool, clubID string) string {
	t.Helper()
	ctx := context.Background()
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
	return qrToken
}

func TestStripeOnly_PayLinkAndRedirect(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	fake := newFakeStripe(t)
	h := newStripeMintHandler(pool, fake)
	router := mintTestRouter(h)
	clubID := testutil.SeedClub(t, pool)
	userID, email := testutil.SeedUser(t, pool, clubID, []string{"member"})
	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 801)

	token := h.ensureFakturaOrderToken(ctx, clubID, invID, true)
	if token == "" {
		t.Fatal("no pay link with only Stripe configured")
	}
	rec := serve(router, http.MethodGet, "/betal/"+token, "")
	calls := fake.createCalls()
	if rec.Code != http.StatusFound || len(calls) != 1 {
		t.Fatalf("redirect = %d %q, creates = %d", rec.Code, rec.Body.String(), len(calls))
	}
	if got := rec.Header().Get("Location"); got != "https://checkout.stripe.example.invalid/cs_test_1" {
		t.Errorf("Location = %q", got)
	}

	var attemptID, provider, ref, vippsRef string
	if err := pool.QueryRow(ctx,
		`SELECT pa.id, pa.provider, pa.provider_reference, o.vipps_reference
		   FROM payment_attempts pa JOIN orders o ON o.id = pa.order_id
		  WHERE o.token = $1`, token,
	).Scan(&attemptID, &provider, &ref, &vippsRef); err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if provider != "stripe" || ref != "cs_test_1" || vippsRef != "" {
		t.Errorf("attempt provider/ref/vipps_reference = %q/%q/%q", provider, ref, vippsRef)
	}
	form := calls[0].Form
	want := map[string]string{
		"success_url":                            mintTestFrontend + "/betal/" + token + "/kvittering",
		"cancel_url":                             mintTestFrontend + "/betal/" + token,
		"customer_email":                         email,
		"metadata[attempt_id]":                   attemptID,
		"line_items[0][price_data][unit_amount]": "45000",
		"line_items[0][price_data][currency]":    "nok",
	}
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Errorf("form %s = %q, want %q", k, got, v)
		}
	}
	if calls[0].IdempotencyKey != attemptID {
		t.Errorf("idempotency key = %q, want the attempt id", calls[0].IdempotencyKey)
	}
	if _, ok := form["metadata[token]"]; ok {
		t.Error("order token leaked into metadata")
	}

	// The checkout page's pay button uses the same default.
	assertBody(t, serve(router, http.MethodPost, "/checkout/"+token+"/pay", ""),
		http.StatusOK, `{"redirect_url":"https://checkout.stripe.example.invalid/cs_test_2"}`+"\n")
}

func TestStripeOnly_QRCancelsBackToPoster(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	fake := newFakeStripe(t)
	router := mintTestRouter(newStripeMintHandler(pool, fake))
	clubID := testutil.SeedClub(t, pool)
	qrToken := seedQRTarget(t, pool, clubID)

	rec := serve(router, http.MethodPost, "/qr/"+qrToken+"/pay", `{"units":2}`)
	calls := fake.createCalls()
	if rec.Code != http.StatusOK || len(calls) != 1 {
		t.Fatalf("qr pay = %d %q, creates = %d", rec.Code, rec.Body.String(), len(calls))
	}
	if got := calls[0].Form.Get("cancel_url"); got != mintTestFrontend+"/qr/"+qrToken {
		t.Errorf("cancel_url = %q", got)
	}
	if _, ok := calls[0].Form["customer_email"]; ok {
		t.Error("guest order carries customer_email")
	}
	var provider, token string
	if err := pool.QueryRow(ctx, `SELECT provider, token FROM orders WHERE club_id = $1`, clubID).Scan(&provider, &token); err != nil {
		t.Fatal(err)
	}
	if provider != "stripe" {
		t.Errorf("order provider = %q", provider)
	}
	if got := calls[0].Form.Get("success_url"); got != mintTestFrontend+"/betal/"+token+"/kvittering" {
		t.Errorf("success_url = %q", got)
	}
}

func TestPayProviderSelection(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	fv := newFakeVipps(t)
	fs := newFakeStripe(t)
	both := mintTestRouter(newProvidersHandler(pool,
		vipps.NewPaymentProvider(fv.client()), stripe.NewPaymentProvider(fs.client(), "testklubb")))
	vippsOnly := mintTestRouter(newMintTestHandler(pool, fv.client()))

	newOrder := func() string {
		orderID, token := seedCheckoutOrder(t, pool, clubID, "")
		addOrderLine(t, pool, orderID, "Test item", 100)
		return token
	}

	// No choice: Vipps is the default while it is configured.
	if rec := serve(both, http.MethodPost, "/checkout/"+newOrder()+"/pay", ""); rec.Code != http.StatusOK || len(fv.createCalls()) != 1 {
		t.Errorf("default = %d, vipps creates %d", rec.Code, len(fv.createCalls()))
	}
	if rec := serve(both, http.MethodPost, "/checkout/"+newOrder()+"/pay", `{"provider":"stripe"}`); rec.Code != http.StatusOK || len(fs.createCalls()) != 1 {
		t.Errorf("stripe chosen = %d, stripe creates %d", rec.Code, len(fs.createCalls()))
	}

	unknown := `{"error":"unknown payment provider"}` + "\n"
	assertBody(t, serve(both, http.MethodPost, "/checkout/"+newOrder()+"/pay", `{"provider":"klarna"}`), http.StatusBadRequest, unknown)
	assertBody(t, serve(vippsOnly, http.MethodPost, "/checkout/"+newOrder()+"/pay", `{"provider":"stripe"}`), http.StatusBadRequest, unknown)
	assertBody(t, serve(both, http.MethodPost, "/checkout/"+newOrder()+"/pay", `{"provider":`), http.StatusBadRequest, `{"error":"invalid request body"}`+"\n")

	qrToken := seedQRTarget(t, pool, clubID)
	assertBody(t, serve(vippsOnly, http.MethodPost, "/qr/"+qrToken+"/pay", `{"units":1,"provider":"stripe"}`), http.StatusBadRequest, unknown)
	if rec := serve(both, http.MethodPost, "/qr/"+qrToken+"/pay", `{"units":1,"provider":"stripe"}`); rec.Code != http.StatusOK || len(fs.createCalls()) != 2 {
		t.Errorf("qr stripe = %d %q, stripe creates %d", rec.Code, rec.Body.String(), len(fs.createCalls()))
	}
	if n := len(fv.createCalls()); n != 1 {
		t.Errorf("vipps creates = %d, want only the default one", n)
	}
}
