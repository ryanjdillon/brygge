package audit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// Migration 000124 scrubs rows about members who are already erased (an
// anonymised address, or no users row), leaves rows about active and merely
// deactivated members, and is safe to run twice.
func TestIntegration_Migration000124ScrubsOnlyErasedMembers(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)

	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000124_scrub_erased_member_audit_pii.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	member := func(email, setSQL string) string {
		id, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
		if _, err := pool.Exec(ctx, `UPDATE users SET email = $2 `+setSQL+` WHERE id = $1`, id, email); err != nil {
			t.Fatalf("seed member: %v", err)
		}
		return id
	}
	erased := member("00000000-0000-4000-8000-000000000001@deleted.local", "")
	legacy := member("deleted@deleted", "")
	active := member("active-subject@example.com", "")
	deactivated := member("deactivated-subject@example.com", ", deactivated_at = now()")
	const hardDeleted = "00000000-0000-4000-8000-0000000000ff"

	for _, id := range []string{erased, legacy, active, deactivated, hardDeleted} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (club_id, action, resource, resource_id, details)
			 VALUES ($1, 'user.deactivated', 'user', $2, '{"email":"audit-subject@example.com","full_name":"Audit Subject"}'),
			        ($1, 'delete_user', 'user', $2, '{"old_email":"audit-subject@example.com","erasure":false,"sessions_revoked":1}'),
			        ($1, 'invoice.created', 'invoice', 'inv-' || $2::text, jsonb_build_object('user_id', $2::text, 'email', 'audit-subject@example.com', 'total', 450))`,
			clubID, id); err != nil {
			t.Fatalf("seed audit rows: %v", err)
		}
	}

	// A user_id that is not a user id (JSON null, a number, free text) names
	// nobody, so those rows must stay as they are.
	for _, details := range []string{
		`{"user_id":null,"email":"audit-subject@example.com"}`,
		`{"user_id":12345,"email":"audit-subject@example.com"}`,
		`{"user_id":"not-a-user","email":"audit-subject@example.com"}`,
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (club_id, action, resource, resource_id, details)
			 VALUES ($1, 'x.recorded', 'thing', 'thing-1', $2::jsonb)`, clubID, details); err != nil {
			t.Fatalf("seed odd user_id row: %v", err)
		}
	}

	for run := 1; run <= 2; run++ {
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}

	named := func(id string) int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log
			  WHERE (resource_id = $1 OR details->>'user_id' = $1)
			    AND details::text LIKE '%audit-subject%'`, id,
		).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	for name, id := range map[string]string{"anonymised": erased, "legacy sentinel": legacy, "hard-deleted": hardDeleted} {
		if n := named(id); n != 0 {
			t.Errorf("%s member: %d rows still carry the identity", name, n)
		}
	}
	for name, id := range map[string]string{"active": active, "deactivated": deactivated} {
		if n := named(id); n != 3 {
			t.Errorf("%s member: %d rows carry the identity, want 3 (not erased yet)", name, n)
		}
	}

	var odd int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'x.recorded' AND details::text LIKE '%audit-subject%'`,
	).Scan(&odd); err != nil {
		t.Fatalf("count odd rows: %v", err)
	}
	if odd != 3 {
		t.Errorf("rows whose user_id names no user were scrubbed: %d of 3 kept their email", odd)
	}

	var kept int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		  WHERE (action = 'delete_user' AND details->>'sessions_revoked' = '1' AND resource_id = $1)
		     OR (action = 'invoice.created' AND details->>'total' = '450' AND details->>'user_id' = $1)`, erased,
	).Scan(&kept); err != nil {
		t.Fatalf("count kept: %v", err)
	}
	if kept != 2 {
		t.Errorf("non-identity keys kept on %d of the erased member's rows, want 2", kept)
	}
}
