package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// newFakeStripeBalanceAPI serves a Stripe balance with one fee and one
// automatic payout, po_test_1, that paid the fee out.
func newFakeStripeBalanceAPI(t *testing.T) *stripe.Client {
	t.Helper()
	const fee = `{"id":"txn_test_fee","reporting_category":"fee","amount":-300,"fee":0,"net":-300,
		"currency":"nok","created":1790000000,"source":null}`
	const payoutTxn = `{"id":"txn_test_po","reporting_category":"payout","amount":-300,"fee":0,"net":-300,
		"currency":"nok","created":1790000100,"source":{"id":"po_test_1","object":"payout","status":"paid","automatic":true}}`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/balance_transactions", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("payout") == "po_test_1" {
			_, _ = w.Write([]byte(`{"has_more":false,"data":[` + payoutTxn + `,` + fee + `]}`))
			return
		}
		_, _ = w.Write([]byte(`{"has_more":false,"data":[` + fee + `]}`))
	})
	mux.HandleFunc("GET /v1/payouts/po_test_1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"po_test_1","amount":300,"currency":"nok","arrival_date":1790208000,
			"status":"paid","automatic":true,"statement_descriptor":"SYNTHETIC CLUB"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return stripe.New(srv.URL, stripeTestKey, zerolog.Nop())
}

// The Stripe sync needs the treasurer/admin role and a fresh TOTP, answers
// 503 without Stripe credentials, and is audited.
func TestStripeSyncGatedAndAudited(t *testing.T) {
	var h *AccountingHandler
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h = NewAccountingHandler(svc, as, nil, nil, nil, zerolog.Nop())
		r.Route("/gated", func(r chi.Router) {
			h.MountLedgerRoutes(r, middleware.RequireRole("treasurer", "admin"), middleware.RequireFreshTOTPDefault())
		})
		r.Route("/open", func(r chi.Router) { h.MountLedgerRoutes(r) })
	})

	for _, roles := range [][]string{{"board"}, {"member"}} {
		if rec := env.serve(roles, http.MethodPost, "/gated/stripe/sync", nil); rec.Code != http.StatusForbidden {
			t.Errorf("POST as %v: status %d, want 403", roles, rec.Code)
		}
	}
	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/gated/stripe/sync", nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "totp_fresh_required") {
		t.Errorf("POST as treasurer without step-up: %d %s, want 403 totp_fresh_required", rec.Code, rec.Body)
	}

	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open/stripe/sync", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("POST without Stripe: status %d, want 503", rec.Code)
	}
	if got := env.audit(audit.ActionAccountingStripeSynced); len(got) != 0 {
		t.Errorf("audited a sync that did not run: %v", got)
	}

	h.SetStripe(newFakeStripeBalanceAPI(t))
	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/open/stripe/sync", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	var res accounting.StripeSyncResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Fetched != 1 || res.Inserted != 1 || len(res.Errors) != 0 {
		t.Errorf("result = %+v, want one fee fetched and inserted", res)
	}
	got := env.audit(audit.ActionAccountingStripeSynced)
	if len(got) != 1 || got[0]["fetched"] != float64(1) || got[0]["inserted"] != float64(1) {
		t.Errorf("audit = %v, want one row with the counts", got)
	}
	var journal int
	if err := env.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, env.club.ID).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != 0 {
		t.Errorf("the sync posted %d journal entries; it must only ingest", journal)
	}
}

// blockingPayouts is a Stripe balance whose GetPayout waits until released,
// to hold a payout ingest open.
type blockingPayouts struct {
	accounting.StripeBalanceSource
	entered chan struct{}
	release chan struct{}
}

func (b *blockingPayouts) GetPayout(ctx context.Context, id string) (*stripe.Payout, error) {
	close(b.entered)
	<-b.release
	return b.StripeBalanceSource.GetPayout(ctx, id)
}

// While a payout webhook's ingest runs, the endpoint answers 409 instead of
// running a second ingest beside it.
func TestStripeSyncConflictsWithRunningIngest(t *testing.T) {
	var h *AccountingHandler
	var svc *accounting.Service
	env := newAliasTestEnv(t, func(r chi.Router, s *accounting.Service, as *audit.Service) {
		svc = s
		h = NewAccountingHandler(s, as, nil, nil, nil, zerolog.Nop())
		r.Route("/open", func(r chi.Router) { h.MountLedgerRoutes(r) })
	})
	src := newFakeStripeBalanceAPI(t)
	h.SetStripe(src)
	block := &blockingPayouts{StripeBalanceSource: src, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := svc.IngestStripePayout(context.Background(), block, env.club.ID, "po_test_1")
		done <- err
	}()
	<-block.entered

	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open/stripe/sync", nil); rec.Code != http.StatusConflict {
		t.Errorf("POST during a payout ingest: status %d, want 409", rec.Code)
	}
	close(block.release)
	if err := <-done; err != nil {
		t.Fatalf("payout ingest: %v", err)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open/stripe/sync", nil); rec.Code != http.StatusOK {
		t.Errorf("POST after the ingest: status %d (%s), want 200", rec.Code, rec.Body)
	}
}

// A payout.paid event refreshes the payout and links what it paid out.
func TestStripeWebhookPayoutPaidLinksMembers(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool)
	var slug string
	if err := pool.QueryRow(context.Background(), `SELECT slug FROM clubs WHERE id = $1`, club.ID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ClubSlug: slug, StripeWebhookSecret: stripeTestWebhookSecret}
	e := &stripeEnv{pool: pool, clubID: club.ID,
		h: NewStripeWebhookHandler(pool, cfg, newFakeStripeBalanceAPI(t), zerolog.Nop())}
	e.h.SetLedger(svc)

	rec := e.post(t, map[string]any{
		"id": "evt_test_payout_1", "type": stripe.EventPayoutPaid, "created": 1790208000,
		"data": map[string]any{"object": map[string]any{"id": "po_test_1", "object": "payout", "status": "paid"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body)
	}
	e.h.payouts.Wait()

	rows, err := pool.Query(context.Background(),
		`SELECT t.balance_transaction_id, COALESCE(p.payout_id, ''), COALESCE(p.status, '')
		   FROM stripe_balance_transactions t LEFT JOIN stripe_payouts p ON p.id = t.payout_id
		  WHERE t.club_id = $1 ORDER BY 1`, club.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, payout, status string
		if err := rows.Scan(&id, &payout, &status); err != nil {
			t.Fatal(err)
		}
		got = append(got, id+"->"+payout+"("+status+")")
	}
	want := "txn_test_fee->po_test_1(paid) txn_test_po->po_test_1(paid)"
	if strings.Join(got, " ") != want {
		t.Errorf("rows = %v, want %s", got, want)
	}
}

// Stripe settings: the payout descriptor must compile and the clearing
// account must be an active asset account; an omitted field keeps its
// value.
func TestFinancialSettingsStripeValidation(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	h := &ClubSettingsHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}
	ctx := context.Background()

	update := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
		req = req.WithContext(middleware.WithClaims(req.Context(),
			&auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
		rec := httptest.NewRecorder()
		h.HandleUpdateFinancialSettings(rec, req)
		return rec
	}
	stored := func() (account, descriptor string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(),
			&auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
		rec := httptest.NewRecorder()
		h.HandleGetFinancialSettings(rec, req)
		var s clubFinancialSettings
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatalf("get settings: %v (%s)", err, rec.Body)
		}
		return s.StripeClearingAccount, s.StripePayoutDescriptor
	}

	if acc, desc := stored(); acc != "1580" || desc != "(?i)stripe" {
		t.Errorf("defaults = %q %q, want 1580 and (?i)stripe", acc, desc)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO accounts (club_id, code, name, account_type, is_active)
		 VALUES ($1, '1581', 'Synthetic inactive', 'asset'::account_type, false)`, club.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, body string }{
		{"bad regexp", `{"stripe_payout_descriptor":"(stripe"}`},
		{"empty descriptor", `{"stripe_payout_descriptor":"  "}`},
		{"long descriptor", `{"stripe_payout_descriptor":"` + strings.Repeat("a", maxPayoutDescriptorLen+1) + `"}`},
		{"revenue account", `{"stripe_clearing_account":"3100"}`},
		{"unknown account", `{"stripe_clearing_account":"1599"}`},
		{"inactive account", `{"stripe_clearing_account":"1581"}`},
	} {
		if rec := update(tc.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.name, rec.Code)
		}
	}
	if acc, desc := stored(); acc != "1580" || desc != "(?i)stripe" {
		t.Errorf("rejected updates changed the settings: %q %q", acc, desc)
	}

	if rec := update(`{"stripe_clearing_account":" 1500 ","stripe_payout_descriptor":"(?i)stripe\\s+payout"}`); rec.Code != http.StatusOK {
		t.Fatalf("valid update: %d %s", rec.Code, rec.Body)
	}
	if acc, desc := stored(); acc != "1500" || desc != `(?i)stripe\s+payout` {
		t.Errorf("after update = %q %q", acc, desc)
	}
	if rec := update(`{"payment_fee_account":"7700"}`); rec.Code != http.StatusOK {
		t.Fatalf("unrelated update: %d %s", rec.Code, rec.Body)
	}
	if acc, desc := stored(); acc != "1500" || desc != `(?i)stripe\s+payout` {
		t.Errorf("an omitted field changed the settings: %q %q", acc, desc)
	}
}
