package accounting

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// The sweep erases the member's identity from the audit log too: after it, no
// row about them carries the old name or address, while a member who is only
// deactivated (still in the retention window) keeps theirs.
func TestIntegration_PurgeExpiredMemberPIIScrubsAuditLog(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	svc := NewService(pool, nil, zerolog.Nop())

	seed := func(withInvoice bool) string {
		id, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
		if _, err := pool.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (club_id, action, resource, resource_id, details)
			 VALUES ($1, 'user.deactivated', 'user', $2, '{"email":"audit-subject@example.com","full_name":"Audit Subject"}'),
			        ($1, 'delete_user', 'user', $2, '{"old_email":"audit-subject@example.com","old_full_name":"Audit Subject"}')`,
			clubID, id); err != nil {
			t.Fatalf("seed audit rows: %v", err)
		}
		if withInvoice {
			if _, err := pool.Exec(ctx,
				`INSERT INTO invoices (club_id, user_id, kid_number, total_amount, status, issue_date, due_date)
				 VALUES ($1, $2, $3, 450, 'open', CURRENT_DATE, CURRENT_DATE)`,
				clubID, id, "99"+testutil.RandomHex(5)); err != nil {
				t.Fatalf("seed invoice: %v", err)
			}
		}
		return id
	}
	erasable, retained := seed(false), seed(true)

	if purged, err := svc.PurgeExpiredMemberPII(ctx); err != nil || purged != 1 {
		t.Fatalf("sweep = (%d, %v), want (1, nil)", purged, err)
	}

	count := func(userID string) int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log
			  WHERE resource_id = $1 AND details::text ~ '(audit-subject|Audit Subject)'`, userID,
		).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	if n := count(erasable); n != 0 {
		t.Errorf("erased member still named in %d audit rows", n)
	}
	if n := count(retained); n != 2 {
		t.Errorf("a member inside retention must keep their audit rows, %d carry the name, want 2", n)
	}
	var purgeRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = 'user.pii_purged'`, erasable,
	).Scan(&purgeRows); err != nil || purgeRows != 1 {
		t.Errorf("purge audit rows = %d (%v), want 1", purgeRows, err)
	}
}
