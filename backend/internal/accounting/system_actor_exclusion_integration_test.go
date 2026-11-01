package accounting

import (
	"context"
	"errors"
	"testing"

	"github.com/brygge-klubb/brygge/internal/audit"
)

// The system actor is not a member. Its name must never resolve a payer, an
// alias must never point at it, and the PII purge must never anonymise it.
// Each case fails without the kind = 'person' filter at its site.

func TestIntegration_SystemActorNeverResolvesAPayer(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)
	ctx := context.Background()
	if _, err := svc.SystemActor(ctx, clubID); err != nil {
		t.Fatalf("SystemActor: %v", err)
	}

	// The payer's name is exactly the system actor's display name.
	id, pass, err := svc.resolveCustomerToMemberPass(ctx, clubID, systemActorDisplayName, "")
	if err != nil {
		t.Fatalf("resolveCustomerToMemberPass: %v", err)
	}
	if id != "" || pass != memberPassNone {
		t.Errorf("payer %q resolved to %q (pass %d), want nobody", systemActorDisplayName, id, pass)
	}

	// A one-letter slip of the name must not reach it through the fuzzy passes either.
	if id, _, err := svc.resolveCustomerToMemberPass(ctx, clubID, "Automatisk bokforing", ""); err != nil || id != "" {
		t.Errorf("accent-stripped payer name resolved to %q, %v; want nobody", id, err)
	}

	cands, err := loadMemberCands(ctx, pool, clubID)
	if err != nil {
		t.Fatalf("loadMemberCands: %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("loadMemberCands returned %d candidates in a club with only the system actor", len(cands))
	}
}

func TestIntegration_SystemActorRefusedAsAliasTarget(t *testing.T) {
	svc, _, clubID := systemActorFixture(t)
	ctx := context.Background()
	id, err := svc.SystemActor(ctx, clubID)
	if err != nil {
		t.Fatalf("SystemActor: %v", err)
	}
	if _, err := svc.UpsertCustomerAlias(ctx, clubID, "SYN PAYER", id); !errors.Is(err, ErrNotClubMember) {
		t.Errorf("UpsertCustomerAlias to the system actor = %v, want ErrNotClubMember", err)
	}
}

func TestIntegration_PurgeNeverAnonymisesTheSystemActor(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)
	ctx := context.Background()
	id, err := svc.SystemActor(ctx, clubID)
	if err != nil {
		t.Fatalf("SystemActor: %v", err)
	}
	// A system actor is never deactivated; force the state the sweep selects on
	// so the filter, not the missing flag, is what keeps it out.
	if _, err := pool.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("force deactivation: %v", err)
	}

	purged, err := svc.PurgeExpiredMemberPII(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredMemberPII: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0", purged)
	}
	if done, err := svc.purgeOneMember(ctx, id, clubID); err != nil || done {
		t.Errorf("purgeOneMember(system actor) = %v, %v; want no change", done, err)
	}
	var email, first string
	if err := pool.QueryRow(ctx, `SELECT email, first_name FROM users WHERE id = $1`, id).Scan(&email, &first); err != nil {
		t.Fatalf("read: %v", err)
	}
	if email != SystemActorEmail || first != "Automatisk" {
		t.Errorf("system actor after the purge = %q / %q, want unchanged", email, first)
	}
}

// The audit scrub is aimed at a member's identity; it must leave rows about
// the system actor exactly as written.
func TestIntegration_ScrubMemberPIIIgnoresTheSystemActor(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)
	ctx := context.Background()
	id, err := svc.SystemActor(ctx, clubID)
	if err != nil {
		t.Fatalf("SystemActor: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_log (club_id, action, resource, resource_id, details)
		 VALUES ($1, 'user.deactivated', 'user', $2, '{"email":"kept@example.com","full_name":"Kept Name"}')`,
		clubID, id); err != nil {
		t.Fatalf("seed audit row: %v", err)
	}
	n, err := audit.ScrubMemberPII(ctx, pool, id)
	if err != nil || n != 0 {
		t.Fatalf("ScrubMemberPII(system actor) = %d, %v; want 0 rows", n, err)
	}
	var kept int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE resource_id = $1 AND details ? 'email'`, id).Scan(&kept); err != nil || kept != 1 {
		t.Errorf("audit row about the system actor changed: %d, %v", kept, err)
	}
}
