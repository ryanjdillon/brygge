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

	// seed inserts an order in the given state with one attempt, and returns
	// that attempt as settlement resolves it.
	seed := func(t *testing.T, user, status, guestName string) paymentAttempt {
		t.Helper()
		orderID, _ := seedCheckoutOrder(t, pool, clubID, user)
		if _, err := pool.Exec(ctx,
			`UPDATE orders SET status = $2::order_status, guest_name = $3 WHERE id = $1`,
			orderID, status, guestName,
		); err != nil {
			t.Fatal(err)
		}
		ref := "chk-settle-" + testutil.RandomHex(8)
		if _, err := pool.Exec(ctx,
			`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount)
			 VALUES ($1, $2, 'vipps', $3, 450)`,
			clubID, orderID, ref,
		); err != nil {
			t.Fatal(err)
		}
		a, ok, err := s.attemptByReference(ctx, clubID, "vipps", ref)
		if err != nil || !ok || a.OrderID != orderID || a.Guest != (user == "") {
			t.Fatalf("attemptByReference = %+v, %v, %v", a, ok, err)
		}
		return a
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
	marker := func(t *testing.T, invID string) (online, legacy *time.Time, provider *string) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT online_paid_at, vipps_paid_at, online_paid_provider FROM invoices WHERE id = $1`, invID,
		).Scan(&online, &legacy, &provider); err != nil {
			t.Fatal(err)
		}
		return online, legacy, provider
	}

	captured := func(t *testing.T, a paymentAttempt, at time.Time, name func(context.Context) string) {
		t.Helper()
		if err := s.markAttemptCaptured(ctx, a, at, name); err != nil {
			t.Fatalf("markAttemptCaptured: %v", err)
		}
	}
	failed := func(t *testing.T, a paymentAttempt) {
		t.Helper()
		if err := s.markAttemptFailed(ctx, a); err != nil {
			t.Fatalf("markAttemptFailed: %v", err)
		}
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
		{"failed is paid", "", "failed", "", "", "paid", ""},
		{"failed guest named", "", "failed", "", "Test Guest", "paid", "Test Guest"},
		{"refunded stays refunded", "", "refunded", "", "", "refunded", ""},
	}
	for i, tc := range paidTests {
		t.Run("paid/"+tc.name, func(t *testing.T) {
			a := seed(t, tc.user, tc.status, tc.guestName)
			invID := seedPayableInvoice(t, pool, clubID, userID, "open", 960+i)
			addOrderSource(t, pool, a.OrderID, invID)

			captured(t, a, capturedAt, namer(tc.payerName))

			status, guest, paidAt := load(t, a.OrderID)
			if status != tc.wantStatus || guest != tc.wantGuest {
				t.Errorf("status/guest = %q/%q, want %q/%q", status, guest, tc.wantStatus, tc.wantGuest)
			}
			if tc.wantStatus == "paid" {
				if paidAt == nil || !paidAt.Equal(capturedAt) {
					t.Errorf("paid_at = %v, want the capture time %v", paidAt, capturedAt)
				}
			} else if paidAt != nil {
				t.Errorf("paid_at = %v for a %s order", paidAt, tc.status)
			}
			online, legacy, provider := marker(t, invID)
			if tc.status == "refunded" {
				// A replayed capture never marks the fakturas of an order a
				// refund settled.
				if online != nil || legacy != nil || provider != nil {
					t.Errorf("invoice marker = %v/%v/%v on a refunded order, want none", online, legacy, provider)
				}
				return
			}
			if online == nil || !online.Equal(capturedAt) || legacy == nil || !legacy.Equal(capturedAt) ||
				provider == nil || *provider != "vipps" {
				t.Errorf("invoice marker online/legacy/provider = %v/%v/%v, want %v/%v/vipps", online, legacy, provider, capturedAt, capturedAt)
			}
		})
	}

	t.Run("paid/zero capture time uses now", func(t *testing.T) {
		a := seed(t, userID, "pending", "")
		invID := seedPayableInvoice(t, pool, clubID, userID, "open", 990)
		addOrderSource(t, pool, a.OrderID, invID)
		captured(t, a, time.Time{}, nil)
		online, _, _ := marker(t, invID)
		if online == nil || time.Since(*online) > time.Minute {
			t.Errorf("invoice marker = %v, want about now", online)
		}
		if _, _, paidAt := load(t, a.OrderID); paidAt == nil || !paidAt.Equal(*online) {
			t.Errorf("paid_at = %v, want the marker time %v", paidAt, online)
		}
	})

	t.Run("paid/name looked up after the settlement commits", func(t *testing.T) {
		a := seed(t, "", "pending", "")
		invID := seedPayableInvoice(t, pool, clubID, userID, "open", 991)
		addOrderSource(t, pool, a.OrderID, invID)
		called := false
		captured(t, a, capturedAt, func(ctx context.Context) string {
			called = true
			// The provider lookup must not hold up, or be needed for, marking
			// the order paid: everything is already committed.
			if status, _, _ := load(t, a.OrderID); status != "paid" {
				t.Errorf("status during name lookup = %q, want paid", status)
			}
			if online, _, _ := marker(t, invID); online == nil {
				t.Error("invoice marker not set before name lookup")
			}
			return "Test Guest"
		})
		if !called {
			t.Fatal("payer name source not called")
		}
		if _, guest, _ := load(t, a.OrderID); guest != "Test Guest" {
			t.Errorf("guest_name = %q", guest)
		}
	})

	for _, from := range []string{"pending", "paid", "refunded", "failed"} {
		t.Run("failed/from "+from, func(t *testing.T) {
			a := seed(t, "", from, "")
			failed(t, a)
			want := from
			if from == "pending" {
				want = "failed"
			}
			if status, _, _ := load(t, a.OrderID); status != want {
				t.Errorf("status = %q, want %q", status, want)
			}
		})
	}

	// A guarded write with nothing left to do (another delivery got there
	// first, or the order is past it) is the normal redelivery, not a failure
	// to return to the provider.
	t.Run("guarded writes with no row to change are not errors", func(t *testing.T) {
		a := seed(t, "", "pending", "")
		captured(t, a, capturedAt, nil)
		captured(t, a, capturedAt, nil) // replay: attempt and order already settled
		failed(t, a)                    // a late terminal event for a paid order
		if status, _, _ := load(t, a.OrderID); status != "paid" {
			t.Errorf("status after replay = %q, want paid", status)
		}

		refunded := seed(t, "", "refunded", "")
		captured(t, refunded, capturedAt, nil)
		if paid, err := s.payOrder(ctx, pool, refunded, capturedAt); err != nil || paid {
			t.Errorf("payOrder on a refunded order = %v, %v; want false, nil", paid, err)
		}

		// Releasing an attempt that is not authorized changes nothing.
		if err := s.releaseAuthorization(ctx, seed(t, "", "pending", "")); err != nil {
			t.Errorf("releaseAuthorization of an attempt that is not authorized: %v", err)
		}
	})
}

// namer adapts a fixed name to markAttemptCaptured's lazy name source; "" is
// none.
func namer(name string) func(context.Context) string {
	if name == "" {
		return nil
	}
	return func(context.Context) string { return name }
}
