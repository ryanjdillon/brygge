package accounting

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// users carries UNIQUE (club_id, email), so the sweep must give every purged
// member its own address. It must also leave rows it already erased alone and
// tolerate a row that holds the constant address earlier versions wrote.
func TestIntegration_PurgeExpiredMemberPIIUniqueAddresses(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	svc := NewService(pool, nil, zerolog.Nop())

	deactivated := func() string {
		id, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
		if _, err := pool.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
		return id
	}
	legacy := deactivated()
	if _, err := pool.Exec(ctx, `UPDATE users SET email = $2 WHERE id = $1`, legacy, LegacyAnonymizedEmail); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	first, second, third := deactivated(), deactivated(), deactivated()

	purged, err := svc.PurgeExpiredMemberPII(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredMemberPII: %v", err)
	}
	if purged != 3 {
		t.Fatalf("purged = %d, want 3 (legacy row skipped)", purged)
	}

	var distinct int
	if err := pool.QueryRow(ctx,
		`SELECT count(DISTINCT email) FROM users
		  WHERE id = ANY($1) AND email LIKE '%' || $2 AND first_name = 'Deleted' AND phone = ''`,
		[]string{first, second, third}, ErasedEmailSuffix,
	).Scan(&distinct); err != nil {
		t.Fatalf("read purged rows: %v", err)
	}
	if distinct != 3 {
		t.Errorf("distinct erased addresses = %d, want 3", distinct)
	}

	var legacyEmail string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, legacy).Scan(&legacyEmail); err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if legacyEmail != LegacyAnonymizedEmail {
		t.Errorf("legacy row email = %q, want it untouched", legacyEmail)
	}

	var audits int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE club_id = $1 AND action = 'user.pii_purged'`, clubID,
	).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 3 {
		t.Errorf("audit rows = %d, want 3", audits)
	}

	// Erased rows are not picked up again, so a re-run changes nothing.
	again, err := svc.PurgeExpiredMemberPII(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if again != 0 {
		t.Errorf("second sweep purged = %d, want 0", again)
	}
}

// A member reactivated between the sweep's SELECT and its UPDATE must not be
// erased: the per-member step re-checks the filters and does nothing.
func TestIntegration_PurgeOneMemberSkipsReactivatedMember(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	svc := NewService(pool, nil, zerolog.Nop())

	userID, email := testutil.SeedUser(t, pool, clubID, []string{"member"})
	done, err := svc.purgeOneMember(ctx, userID, clubID)
	if err != nil || done {
		t.Fatalf("purgeOneMember on an active member = (%v, %v), want (false, nil)", done, err)
	}

	var gotEmail, first string
	if err := pool.QueryRow(ctx, `SELECT email, first_name FROM users WHERE id = $1`, userID).Scan(&gotEmail, &first); err != nil {
		t.Fatalf("read member: %v", err)
	}
	if gotEmail != email || first != "Test" {
		t.Errorf("active member was erased: email=%q first_name=%q", gotEmail, first)
	}
	var audits int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = 'user.pii_purged'`, userID,
	).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 0 {
		t.Errorf("audit rows = %d, want 0", audits)
	}

	// Deactivated, the same call erases once and then skips the erased row.
	if _, err := pool.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, userID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if done, err := svc.purgeOneMember(ctx, userID, clubID); err != nil || !done {
		t.Fatalf("purgeOneMember on a deactivated member = (%v, %v), want (true, nil)", done, err)
	}
	if done, err := svc.purgeOneMember(ctx, userID, clubID); err != nil || done {
		t.Fatalf("second purgeOneMember = (%v, %v), want (false, nil)", done, err)
	}
}

// The sweep clears the member's own cancellation reason and deletes the join
// requests made with their address in their club, and nothing else's.
func TestIntegration_PurgeOneMemberErasesMembershipData(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	otherClub := testutil.SeedClub(t, pool)
	svc := NewService(pool, nil, zerolog.Nop())

	userID, email := testutil.SeedUser(t, pool, clubID, []string{"member"})
	if _, err := pool.Exec(ctx,
		`UPDATE users SET deactivated_at = now(), membership_ends_on = '2025-12-31',
		        membership_cancelled_at = now(), membership_cancel_reason = 'Synthetic reason'
		  WHERE id = $1`, userID); err != nil {
		t.Fatalf("seed cancelled member: %v", err)
	}
	insertApp := func(club, addr, hash string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO membership_applications (club_id, first_name, last_name, email, token_hash, expires_at)
			 VALUES ($1, 'Test', 'Applicant', $2, $3, now() + interval '1 day')`,
			club, addr, hash); err != nil {
			t.Fatalf("seed application: %v", err)
		}
	}
	insertApp(clubID, strings.ToUpper(email), "hash-own")
	insertApp(clubID, "someone-else@example.test", "hash-other-address")
	insertApp(otherClub, email, "hash-other-club")

	if done, err := svc.purgeOneMember(ctx, userID, clubID); err != nil || !done {
		t.Fatalf("purgeOneMember = (%v, %v), want (true, nil)", done, err)
	}

	var reason *string
	var endsOn *string
	if err := pool.QueryRow(ctx,
		`SELECT membership_cancel_reason, membership_ends_on::text FROM users WHERE id = $1`, userID,
	).Scan(&reason, &endsOn); err != nil {
		t.Fatalf("read member: %v", err)
	}
	if reason != nil {
		t.Errorf("membership_cancel_reason = %q, want NULL", *reason)
	}
	if endsOn == nil || *endsOn != "2025-12-31" {
		t.Errorf("membership_ends_on = %v, want it kept", endsOn)
	}

	rows, err := pool.Query(ctx, `SELECT token_hash FROM membership_applications ORDER BY token_hash`)
	if err != nil {
		t.Fatalf("list applications: %v", err)
	}
	var left []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("scan: %v", err)
		}
		left = append(left, h)
	}
	rows.Close()
	if strings.Join(left, ",") != "hash-other-address,hash-other-club" {
		t.Errorf("applications left = %v, want only the other address and the other club", left)
	}
}
