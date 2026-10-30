package audit_test

import (
	"context"
	"testing"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// The scrub removes the identity keys from the rows about one member, leaves
// every other key and every other member's rows alone, and can run twice.
func TestIntegration_ScrubMemberPII(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	target, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	other, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})

	insert := func(action, resource, resourceID, details string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (club_id, action, resource, resource_id, details)
			 VALUES ($1, $2, $3, $4, $5::jsonb)`,
			clubID, action, resource, resourceID, details,
		); err != nil {
			t.Fatalf("seed %s: %v", action, err)
		}
	}
	insert("delete_user", "user", target, `{"old_email":"a@example.com","old_full_name":"Synthetic Person","sessions_revoked":2}`)
	insert("user.deactivated", "user", target, `{"email":"a@example.com","full_name":"Synthetic Person"}`)
	insert("update_user", "user", target, `{"first_name":"Synthetic","phone":"+4700000000","admin_notes":"note","fields":["phone"]}`)
	insert("invoice.created", "invoice", "inv-1", `{"user_id":"`+target+`","email":"a@example.com","total":450}`)
	insert("user.deactivated", "user", other, `{"email":"b@example.com","full_name":"Other Person"}`)
	insert("invoice.created", "invoice", "inv-2", `{"user_id":"`+other+`","email":"b@example.com"}`)
	insert("user.pii_purged", "user", target, `null`)
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_log (club_id, action, resource, resource_id) VALUES ($1, 'x', 'user', $2)`, clubID, target); err != nil {
		t.Fatalf("seed null details: %v", err)
	}

	for run := 1; run <= 2; run++ {
		n, err := audit.ScrubMemberPII(ctx, pool, target)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		want := int64(4)
		if run == 2 {
			want = 0
		}
		if n != want {
			t.Errorf("run %d scrubbed %d rows, want %d", run, n, want)
		}
	}

	var leaked int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		  WHERE (resource_id = $1 OR details->>'user_id' = $1)
		    AND details::text ~ '(a@example|Synthetic|4700000000|"note")'`, target,
	).Scan(&leaked); err != nil {
		t.Fatalf("count leaks: %v", err)
	}
	if leaked != 0 {
		t.Errorf("%d rows about the member still carry their identity", leaked)
	}

	var kept int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		  WHERE (action = 'delete_user' AND details->>'sessions_revoked' = '2')
		     OR (action = 'update_user' AND details->'fields' = '["phone"]')
		     OR (action = 'invoice.created' AND details->>'total' = '450')`,
	).Scan(&kept); err != nil {
		t.Fatalf("count kept: %v", err)
	}
	if kept != 3 {
		t.Errorf("non-identity keys kept on %d rows, want 3", kept)
	}

	var untouched int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE details::text LIKE '%b@example.com%'`).Scan(&untouched); err != nil {
		t.Fatalf("count other member's rows: %v", err)
	}
	if untouched != 2 {
		t.Errorf("another member's rows changed: %d still carry their identity, want 2", untouched)
	}
}
