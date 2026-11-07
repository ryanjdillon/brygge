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

// The sweep clears the board's notes and the member's boats: a boat on a
// faktura line stays with its identifying columns blanked, any other is
// deleted, and the faktura line is not rewritten.
func TestIntegration_PurgeOneMemberErasesNotesAndBoats(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	svc := NewService(pool, nil, zerolog.Nop())

	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	var billed, free, invoiceID string
	for _, q := range []struct {
		dst *string
		sql string
	}{
		{&billed, `INSERT INTO boats (user_id, club_id, name, registration_number, model, length_m)
		           VALUES ($1, $2, 'Billed', 'REG-1', 'T-20', 6.5) RETURNING id`},
		{&free, `INSERT INTO boats (user_id, club_id, name, registration_number)
		         VALUES ($1, $2, 'Free', 'REG-2') RETURNING id`},
		{&invoiceID, `INSERT INTO invoices (club_id, user_id, kid_number, total_amount, status, issue_date, due_date)
		              VALUES ($2, $1, '8800001', 300, 'open', '2015-06-01', '2015-06-15') RETURNING id`},
	} {
		if err := pool.QueryRow(ctx, q.sql, userID, clubID).Scan(q.dst); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	for _, stmt := range []string{
		`UPDATE users SET deactivated_at = now(), admin_notes = 'Synthetic board note' WHERE id = $1`,
		`INSERT INTO user_consents (user_id, club_id, consent_type, version)
		 SELECT $1, club_id, t, '1' FROM users, unnest(ARRAY['privacy', 'newsletter']) t WHERE id = $1`,
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, boat_id)
		 SELECT id, 'Slip fee Billed REG-1', 1, 300, 300, '` + billed + `' FROM invoices WHERE user_id = $1`,
	} {
		if _, err := pool.Exec(ctx, stmt, userID); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if done, err := svc.purgeOneMember(ctx, userID, clubID); err != nil || !done {
		t.Fatalf("purgeOneMember = (%v, %v), want (true, nil)", done, err)
	}

	var notes string
	if err := pool.QueryRow(ctx, `SELECT admin_notes FROM users WHERE id = $1`, userID).Scan(&notes); err != nil {
		t.Fatalf("read member: %v", err)
	}
	if notes != "" {
		t.Errorf("admin_notes = %q, want empty", notes)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM boats WHERE id = $1`, free).Scan(&n); err != nil || n != 0 {
		t.Errorf("unreferenced boat left (%d, %v)", n, err)
	}
	// The sweep runs past retention, so every consent goes with the identity.
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_consents WHERE user_id = $1`, userID).Scan(&n); err != nil || n != 0 {
		t.Errorf("consents left after the sweep (%d, %v)", n, err)
	}
	var name, reg, model string
	if err := pool.QueryRow(ctx, `SELECT name, registration_number, model FROM boats WHERE id = $1`, billed).Scan(&name, &reg, &model); err != nil {
		t.Fatalf("billed boat should be kept: %v", err)
	}
	if name != "" || reg != "" || model != "T-20" {
		t.Errorf("billed boat = (%q, %q, %q), want identity blanked and model kept", name, reg, model)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM invoice_lines WHERE invoice_id = $1 AND boat_id = $2 AND description = 'Slip fee Billed REG-1'`,
		invoiceID, billed).Scan(&n); err != nil || n != 1 {
		t.Errorf("faktura line changed (%d, %v)", n, err)
	}
}

// Legal consents kept through erasure are deleted once the erased member's
// retention ends, and not before; a member who is only deactivated keeps
// theirs.
func TestIntegration_PurgeRetentionClearedConsents(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	svc := NewService(pool, nil, zerolog.Nop())

	seed := func(erased bool, issueDate string) string {
		t.Helper()
		userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
		stmts := []string{
			`UPDATE users SET deactivated_at = now() WHERE id = $1`,
			`INSERT INTO user_consents (user_id, club_id, consent_type, version)
			 SELECT id, club_id, 'privacy', '1' FROM users WHERE id = $1`,
		}
		if erased {
			stmts = append(stmts, `UPDATE users SET email = gen_random_uuid()::text || '`+ErasedEmailSuffix+`' WHERE id = $1`)
		}
		if issueDate != "" {
			stmts = append(stmts, `INSERT INTO invoices (club_id, user_id, kid_number, total_amount, status, issue_date, due_date)
			 SELECT club_id, id, '99' || left(md5(id::text), 6), 100, 'open', '`+issueDate+`'::date, '`+issueDate+`'::date
			   FROM users WHERE id = $1`)
		}
		for _, stmt := range stmts {
			if _, err := pool.Exec(ctx, stmt, userID); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		return userID
	}
	pastRetention := seed(true, "2015-06-01")
	noInvoices := seed(true, "")
	inRetention := seed(true, "now")
	deactivatedOnly := seed(false, "2015-06-01")

	if _, err := svc.PurgeRetentionClearedConsents(ctx); err != nil {
		t.Fatalf("PurgeRetentionClearedConsents: %v", err)
	}
	for _, tc := range []struct {
		name, userID string
		want         int
	}{
		{"erased, retention ended", pastRetention, 0},
		{"erased, no fakturas", noInvoices, 0},
		{"erased, inside retention", inRetention, 1},
		{"deactivated, not erased", deactivatedOnly, 1},
	} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_consents WHERE user_id = $1`, tc.userID).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != tc.want {
			t.Errorf("%s: consents = %d, want %d", tc.name, n, tc.want)
		}
	}
}
