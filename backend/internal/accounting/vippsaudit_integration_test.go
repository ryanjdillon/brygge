package accounting

import (
	"context"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// AuditUnlinkedVippsPayments surfaces belastning charges whose payer resolves
// to a member with a matching open, unpaid faktura, flags the already-booked
// ones as confirmed misses (returned first), and excludes charges that don't
// resolve to a member, have no matching open invoice, or were dismissed.
func TestAuditUnlinkedVippsPayments(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	// SeedUser creates a member named "Test User" (first/last), which the
	// reconciler's resolveCustomerToMember matches on customer_name.
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	svc := NewService(pool, nil, zerolog.Nop())

	var periodID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		clubID,
	).Scan(&periodID); err != nil {
		t.Fatalf("insert period: %v", err)
	}

	// Two open, unpaid invoices for the member: 450 and 300. issue_date
	// defaults to today so both fall inside findOpenInvoiceForMember's window.
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date, total_amount,
		    status, fiscal_period_id, recipient_kind, recipient_email, sent_at)
		 VALUES
		   ($1, $2, 701, '000007010019', '2026-12-31', 450.00,
		    'open', $3, 'private', 'm@example.com', now()),
		   ($1, $2, 702, '000007020016', '2026-12-31', 300.00,
		    'open', $3, 'private', 'm@example.com', now())`,
		clubID, userID, periodID,
	); err != nil {
		t.Fatalf("insert invoices: %v", err)
	}

	// A journal entry to mark one charge as already reconciled (to the wrong
	// account) — the confirmed-miss case.
	var jeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, created_by)
		 VALUES ($1, $2, 1, '2026-06-04', 'test', $3) RETURNING id`,
		clubID, periodID, userID,
	).Scan(&jeID); err != nil {
		t.Fatalf("insert journal entry: %v", err)
	}

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by)
		 VALUES ($1, 'settlement.csv', '548005', $2) RETURNING id`,
		clubID, userID,
	).Scan(&importID); err != nil {
		t.Fatalf("insert vipps import: %v", err)
	}

	// A: 450 charge, unreconciled     → matches invoice 701, "in Tildel"
	// B: 300 charge, already booked    → matches invoice 702, CONFIRMED MISS
	// C: 450 charge, name unresolvable → excluded (no member)
	// D: 999 charge                    → excluded (no matching open invoice)
	// E: 450 charge, dismissed         → excluded (dismissed_at set)
	if _, err := pool.Exec(ctx,
		`INSERT INTO vipps_import_rows
		   (vipps_import_id, club_id, row_hash, row_type, booking_date, amount,
		    customer_name, message, settlement_number, msn, journal_entry_id, dismissed_at)
		 VALUES
		   ($1, $2, repeat('a',64), 'belastning', '2026-06-04', 450.00,
		    'Test User', 'Sesong 2026', '2000167', '548005', NULL, NULL),
		   ($1, $2, repeat('b',64), 'belastning', '2026-06-03', 300.00,
		    'Test User', 'Slippleige', '2000167', '548005', $3, NULL),
		   ($1, $2, repeat('c',64), 'belastning', '2026-06-04', 450.00,
		    'Ukjent Betaler', '', '2000167', '548005', NULL, NULL),
		   ($1, $2, repeat('d',64), 'belastning', '2026-06-04', 999.00,
		    'Test User', '', '2000167', '548005', NULL, NULL),
		   ($1, $2, repeat('e',64), 'belastning', '2026-06-04', 450.00,
		    'Test User', '', '2000167', '548005', NULL, now())`,
		importID, clubID, jeID,
	); err != nil {
		t.Fatalf("insert vipps rows: %v", err)
	}

	got, err := svc.AuditUnlinkedVippsPayments(ctx, clubID)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (A + B); got %+v", len(got), got)
	}

	// Confirmed miss (B) sorts first.
	first, second := got[0], got[1]
	if !first.AlreadyReconciled {
		t.Errorf("first candidate should be the confirmed miss (AlreadyReconciled=true); got %+v", first)
	}
	if first.Amount != 300 || first.InvoiceNumber != 702 {
		t.Errorf("confirmed miss = amount %.2f invoice #%d, want 300 / #702", first.Amount, first.InvoiceNumber)
	}
	if second.AlreadyReconciled {
		t.Errorf("second candidate should be the in-Tildel charge (AlreadyReconciled=false); got %+v", second)
	}
	if second.Amount != 450 || second.InvoiceNumber != 701 {
		t.Errorf("in-Tildel charge = amount %.2f invoice #%d, want 450 / #701", second.Amount, second.InvoiceNumber)
	}
	for _, c := range got {
		if c.MemberName != "Test User" {
			t.Errorf("member name = %q, want %q", c.MemberName, "Test User")
		}
	}
}
