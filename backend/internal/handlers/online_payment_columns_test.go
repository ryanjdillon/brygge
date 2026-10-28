package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// rerunMigration applies an up migration again. The provider-neutral payment
// migrations only add and backfill, guarded so a second run changes nothing
// the first did not, which lets a test seed legacy-shaped rows and replay
// their backfills.
func rerunMigration(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	sql, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), name)) // #nosec G304 -- test-only, fixed migrations path
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

func TestOnlinePaymentMigrationBackfill(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	withChart := testutil.SeedClub(t, pool)
	ownChart := testutil.SeedClub(t, pool)
	noChart := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, withChart, []string{"member"})
	exec(`UPDATE clubs SET vipps_max_amount = 500, vipps_fee_account = '7790' WHERE id = $1`, withChart)
	for _, club := range []string{withChart, ownChart} {
		exec(`INSERT INTO accounts (club_id, code, name, account_type) VALUES ($1, '1500', 'Kundefordringer', 'asset')`, club)
	}
	exec(`INSERT INTO accounts (club_id, code, name, account_type) VALUES ($1, '1580', 'Club-named 1580', 'asset')`, ownChart)

	paidAt := time.Date(2026, 5, 4, 9, 30, 0, 0, time.UTC)
	marked := seedPayableInvoice(t, pool, withChart, userID, "open", 801)
	forcedOff := seedPayableInvoice(t, pool, withChart, userID, "open", 802)
	plain := seedPayableInvoice(t, pool, withChart, userID, "open", 803)
	exec(`UPDATE invoices SET vipps_paid_at = $2, vipps_payment_link_opt_in = true WHERE id = $1`, marked, paidAt)
	exec(`UPDATE invoices SET vipps_payment_link_opt_in = false WHERE id = $1`, forcedOff)

	orders := map[string]string{} // reference → order status
	for ref, status := range map[string]string{
		"chk-bf-paid": "paid", "chk-bf-failed": "failed", "chk-bf-pending": "pending", "chk-bf-refunded": "refunded", "": "pending",
	} {
		orders[ref] = status
		exec(`INSERT INTO orders (club_id, total_amount, vipps_reference, status, token)
		      VALUES ($1, 321.5, $2, $3::order_status, $4)`, withChart, ref, status, genCheckoutToken())
	}

	// Two orders carrying the same reference: the attempt goes to the one that
	// stored it last.
	var sharedOld, sharedNew string
	for _, o := range []struct {
		id      *string
		updated time.Time
	}{{&sharedOld, paidAt.Add(-time.Hour)}, {&sharedNew, paidAt}} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO orders (club_id, total_amount, vipps_reference, token, updated_at)
			 VALUES ($1, 321.5, 'chk-bf-shared', $2, $3) RETURNING id`,
			withChart, genCheckoutToken(), o.updated,
		).Scan(o.id); err != nil {
			t.Fatal(err)
		}
	}

	// Rewind to the state before the migrations: legacy columns only.
	exec(`DELETE FROM payment_attempts`)
	exec(`UPDATE invoices SET online_paid_at = NULL, online_paid_provider = NULL, online_payment_opt_in = NULL`)
	exec(`UPDATE clubs SET online_max_amount = NULL, payment_fee_account = '7700'`)
	exec(`DELETE FROM accounts WHERE code = '1580' AND club_id <> $1`, ownChart)

	for range 2 {
		rerunMigration(t, pool, "000114_online_payment_columns.up.sql")
		rerunMigration(t, pool, "000115_payment_attempts.up.sql")
	}

	t.Run("attempts from vipps_reference", func(t *testing.T) {
		want := map[string]string{
			"chk-bf-paid": "captured", "chk-bf-refunded": "captured",
			"chk-bf-failed": "failed", "chk-bf-pending": "created",
			"chk-bf-shared": "created",
		}
		rows, err := pool.Query(ctx,
			`SELECT pa.provider_reference, pa.status, pa.provider, pa.amount::float8, pa.club_id = o.club_id
			   FROM payment_attempts pa JOIN orders o ON o.id = pa.order_id
			  WHERE o.vipps_reference = pa.provider_reference`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[string]string{}
		for rows.Next() {
			var (
				ref, status, provider string
				amount                float64
				sameClub              bool
			)
			if err := rows.Scan(&ref, &status, &provider, &amount, &sameClub); err != nil {
				t.Fatal(err)
			}
			if provider != "vipps" || amount != 321.5 || !sameClub {
				t.Errorf("attempt %s = %s/%v/club match %v", ref, provider, amount, sameClub)
			}
			got[ref] = status
		}
		if len(got) != len(want) {
			t.Errorf("attempts = %v, want %v", got, want)
		}
		for ref, status := range want {
			if got[ref] != status {
				t.Errorf("attempt %s status = %q, want %q (order %s)", ref, got[ref], status, orders[ref])
			}
		}
		var total int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_attempts`).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if total != len(want) {
			t.Errorf("payment_attempts rows = %d, want %d (none for an order without a reference, none duplicated)", total, len(want))
		}
	})

	t.Run("shared reference goes to the order updated last", func(t *testing.T) {
		var orderID string
		var createdAt time.Time
		if err := pool.QueryRow(ctx,
			`SELECT order_id, created_at FROM payment_attempts WHERE provider_reference = 'chk-bf-shared'`,
		).Scan(&orderID, &createdAt); err != nil {
			t.Fatal(err)
		}
		if orderID != sharedNew || orderID == sharedOld {
			t.Errorf("shared reference attempt on order %s, want the later-updated %s", orderID, sharedNew)
		}
		if !createdAt.Equal(paidAt) {
			t.Errorf("attempt created_at = %v, want the order's updated_at %v", createdAt, paidAt)
		}
	})

	t.Run("invoice marker and opt-in", func(t *testing.T) {
		for _, tc := range []struct {
			id       string
			wantAt   *time.Time
			wantProv string
			wantOpt  *bool
		}{
			{marked, &paidAt, "vipps", ptr(true)},
			{forcedOff, nil, "", ptr(false)},
			{plain, nil, "", nil},
		} {
			var (
				at   *time.Time
				prov *string
				opt  *bool
			)
			if err := pool.QueryRow(ctx,
				`SELECT online_paid_at, online_paid_provider, online_payment_opt_in FROM invoices WHERE id = $1`, tc.id,
			).Scan(&at, &prov, &opt); err != nil {
				t.Fatal(err)
			}
			if (at == nil) != (tc.wantAt == nil) || (at != nil && !at.Equal(*tc.wantAt)) {
				t.Errorf("invoice %s online_paid_at = %v, want %v", tc.id, at, tc.wantAt)
			}
			if (prov == nil && tc.wantProv != "") || (prov != nil && *prov != tc.wantProv) {
				t.Errorf("invoice %s online_paid_provider = %v, want %q", tc.id, prov, tc.wantProv)
			}
			if (opt == nil) != (tc.wantOpt == nil) || (opt != nil && *opt != *tc.wantOpt) {
				t.Errorf("invoice %s online_payment_opt_in = %v, want %v", tc.id, opt, tc.wantOpt)
			}
		}
	})

	t.Run("club cap and fee account", func(t *testing.T) {
		var (
			maxAmount *float64
			fee       string
			clearing  string
		)
		if err := pool.QueryRow(ctx,
			`SELECT online_max_amount::float8, payment_fee_account, stripe_clearing_account FROM clubs WHERE id = $1`, withChart,
		).Scan(&maxAmount, &fee, &clearing); err != nil {
			t.Fatal(err)
		}
		if maxAmount == nil || *maxAmount != 500 || fee != "7790" || clearing != "1580" {
			t.Errorf("club = %v/%q/%q, want 500/7790/1580", maxAmount, fee, clearing)
		}
	})

	t.Run("1580 seeded only where missing", func(t *testing.T) {
		for _, tc := range []struct {
			club, wantName string
			wantRows       int
		}{
			{withChart, "Til gode betalingsformidler (Stripe)", 1},
			{ownChart, "Club-named 1580", 1},
			{noChart, "", 0},
		} {
			var (
				n    int
				name string
			)
			if err := pool.QueryRow(ctx,
				`SELECT count(*), COALESCE(max(name), '') FROM accounts WHERE club_id = $1 AND code = '1580'`, tc.club,
			).Scan(&n, &name); err != nil {
				t.Fatal(err)
			}
			if n != tc.wantRows || name != tc.wantName {
				t.Errorf("club %s 1580 = %d rows %q, want %d %q", tc.club, n, name, tc.wantRows, tc.wantName)
			}
		}
	})
}

func TestFinancialSettingsDualWrite(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	h := &ClubSettingsHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}

	withClaims := func(r *http.Request) *http.Request {
		return r.WithContext(middleware.WithClaims(r.Context(),
			&auth.Claims{UserID: "00000000-0000-0000-0000-000000000001", ClubID: clubID, Roles: []string{"treasurer"}}))
	}
	update := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.HandleUpdateFinancialSettings(rec, withClaims(httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(body))))
		if rec.Code >= 300 {
			t.Fatalf("update %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	stored := func() (online, legacy *float64, fee, legacyFee string) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT online_max_amount::float8, vipps_max_amount::float8, payment_fee_account, vipps_fee_account
			   FROM clubs WHERE id = $1`, clubID,
		).Scan(&online, &legacy, &fee, &legacyFee); err != nil {
			t.Fatal(err)
		}
		return online, legacy, fee, legacyFee
	}

	update(`{"vipps_fee_account":"7790","vipps_max_amount":250}`)
	if online, legacy, fee, legacyFee := stored(); online == nil || *online != 250 || legacy == nil || *legacy != 250 ||
		fee != "7790" || legacyFee != "7790" {
		t.Errorf("after set: cap %v/%v fee %q/%q, want 250 and 7790 in both", online, legacy, fee, legacyFee)
	}

	update(`{"vipps_per_charge_enabled":false}`)
	if online, legacy, fee, legacyFee := stored(); online == nil || *online != 250 || legacy == nil || *legacy != 250 ||
		fee != "7790" || legacyFee != "7790" {
		t.Errorf("after omit: cap %v/%v fee %q/%q, want both kept", online, legacy, fee, legacyFee)
	}

	update(`{"vipps_max_amount":0}`)
	if online, legacy, _, _ := stored(); online != nil || legacy != nil {
		t.Errorf("after clear: cap %v/%v, want both NULL", online, legacy)
	}

	// The API keeps its field names and reads the provider-neutral columns.
	if _, err := pool.Exec(ctx, `UPDATE clubs SET online_max_amount = 123, payment_fee_account = '7701' WHERE id = $1`, clubID); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.HandleGetFinancialSettings(rec, withClaims(httptest.NewRequest(http.MethodGet, "/x", nil)))
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if got["vipps_max_amount"] != 123.0 || got["vipps_fee_account"] != "7701" {
		t.Errorf("GET vipps_max_amount/vipps_fee_account = %v/%v, want 123/7701", got["vipps_max_amount"], got["vipps_fee_account"])
	}
}

func TestReminderOverrideDualWrite(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 811)
	h := &InvoiceHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}

	if err := h.applyReminderVippsOverride(ctx, clubID, userID, "", ptr(false),
		[]bcast.InvoiceDelivery{{InvoiceID: invID}}); err != nil {
		t.Fatal(err)
	}
	var online, legacy *bool
	if err := pool.QueryRow(ctx,
		`SELECT online_payment_opt_in, vipps_payment_link_opt_in FROM invoices WHERE id = $1`, invID,
	).Scan(&online, &legacy); err != nil {
		t.Fatal(err)
	}
	if online == nil || *online || legacy == nil || *legacy {
		t.Errorf("opt-in online/legacy = %v/%v, want false in both", online, legacy)
	}
}
