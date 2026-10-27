package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestOrderSettlement(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := orderSettlement{db: pool, log: zerolog.Nop()}
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	capturedAt := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)

	seed := func(t *testing.T, user, status, guestName string) string {
		t.Helper()
		orderID, _ := seedCheckoutOrder(t, pool, clubID, user)
		if _, err := pool.Exec(ctx,
			`UPDATE orders SET status = $2::order_status, guest_name = $3 WHERE id = $1`,
			orderID, status, guestName,
		); err != nil {
			t.Fatal(err)
		}
		return orderID
	}
	load := func(t *testing.T, orderID string) (status, guestName string, paidAt *time.Time) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT status::text, guest_name, paid_at FROM orders WHERE id = $1`, orderID,
		).Scan(&status, &guestName, &paidAt); err != nil {
			t.Fatal(err)
		}
		return status, guestName, paidAt
	}

	paidTests := []struct {
		name       string
		user       string
		status     string
		guestName  string
		payerName  string
		wantStatus string
		wantGuest  string
	}{
		{"guest gets payer name", "", "pending", "", "Test Guest", "paid", "Test Guest"},
		{"typed guest name kept", "", "pending", "Typed Name", "Test Guest", "paid", "Typed Name"},
		{"member not named", userID, "pending", "", "Test Guest", "paid", ""},
		{"no payer name", "", "pending", "", "", "paid", ""},
		{"failed stays failed", "", "failed", "", "", "failed", ""},
		{"failed guest still named", "", "failed", "", "Test Guest", "failed", "Test Guest"},
		{"refunded stays refunded", "", "refunded", "", "", "refunded", ""},
	}
	for i, tc := range paidTests {
		t.Run("paid/"+tc.name, func(t *testing.T) {
			orderID := seed(t, tc.user, tc.status, tc.guestName)
			invID := seedPayableInvoice(t, pool, clubID, userID, "open", 960+i)
			addOrderSource(t, pool, orderID, invID)

			s.markOrderPaid(ctx, orderID, capturedAt, namer(tc.payerName))

			status, guest, paidAt := load(t, orderID)
			if status != tc.wantStatus || guest != tc.wantGuest {
				t.Errorf("status/guest = %q/%q, want %q/%q", status, guest, tc.wantStatus, tc.wantGuest)
			}
			if (paidAt != nil) != (tc.status == "pending") {
				t.Errorf("paid_at = %v for a %s order", paidAt, tc.status)
			}
			var marker *time.Time
			if err := pool.QueryRow(ctx, `SELECT vipps_paid_at FROM invoices WHERE id = $1`, invID).Scan(&marker); err != nil {
				t.Fatal(err)
			}
			if marker == nil || !marker.Equal(capturedAt) {
				t.Errorf("invoice marker = %v, want %v", marker, capturedAt)
			}
		})
	}

	t.Run("paid/zero capture time uses now", func(t *testing.T) {
		orderID := seed(t, userID, "pending", "")
		invID := seedPayableInvoice(t, pool, clubID, userID, "open", 990)
		addOrderSource(t, pool, orderID, invID)
		s.markOrderPaid(ctx, orderID, time.Time{}, nil)
		var marker *time.Time
		if err := pool.QueryRow(ctx, `SELECT vipps_paid_at FROM invoices WHERE id = $1`, invID).Scan(&marker); err != nil {
			t.Fatal(err)
		}
		if marker == nil || time.Since(*marker) > time.Minute {
			t.Errorf("invoice marker = %v, want about now", marker)
		}
	})

	t.Run("paid/name looked up after status, before marker", func(t *testing.T) {
		orderID := seed(t, "", "pending", "")
		invID := seedPayableInvoice(t, pool, clubID, userID, "open", 991)
		addOrderSource(t, pool, orderID, invID)
		called := false
		s.markOrderPaid(ctx, orderID, capturedAt, func(ctx context.Context) string {
			called = true
			if status, _, _ := load(t, orderID); status != "paid" {
				t.Errorf("status during name lookup = %q, want paid", status)
			}
			var marker *time.Time
			if err := pool.QueryRow(ctx, `SELECT vipps_paid_at FROM invoices WHERE id = $1`, invID).Scan(&marker); err != nil {
				t.Fatal(err)
			}
			if marker != nil {
				t.Errorf("invoice marker set before name lookup: %v", marker)
			}
			return "Test Guest"
		})
		if !called {
			t.Fatal("payer name source not called")
		}
		if _, guest, _ := load(t, orderID); guest != "Test Guest" {
			t.Errorf("guest_name = %q", guest)
		}
	})

	for _, from := range []string{"pending", "paid", "refunded", "failed"} {
		t.Run("failed/from "+from, func(t *testing.T) {
			orderID := seed(t, "", from, "")
			s.markOrderFailed(ctx, orderID)
			want := from
			if from == "pending" {
				want = "failed"
			}
			if status, _, _ := load(t, orderID); status != want {
				t.Errorf("status = %q, want %q", status, want)
			}
		})
	}
}

// namer adapts a fixed name to markOrderPaid's lazy name source; "" is none.
func namer(name string) func(context.Context) string {
	if name == "" {
		return nil
	}
	return func(context.Context) string { return name }
}
