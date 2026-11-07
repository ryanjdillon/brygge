package audit_test

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// A found club id is cached: renaming the slug afterwards does not change
// the answer. A miss is not cached: the slug resolves once the club exists.
func TestIntegration_ClubIDForSlugCachesHitsOnly(t *testing.T) {
	t.Parallel()
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := audit.NewService(pool, zerolog.Nop())

	clubID := testutil.SeedClub(t, pool)
	var slug string
	if err := pool.QueryRow(ctx, `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatalf("club slug: %v", err)
	}
	if got := svc.ClubIDForSlug(ctx, slug); got != clubID {
		t.Fatalf("ClubIDForSlug = %q, want %q", got, clubID)
	}
	if _, err := pool.Exec(ctx, `UPDATE clubs SET slug = slug || '-renamed' WHERE id = $1`, clubID); err != nil {
		t.Fatalf("rename slug: %v", err)
	}
	if got := svc.ClubIDForSlug(ctx, slug); got != clubID {
		t.Errorf("after rename ClubIDForSlug = %q, want the cached %q", got, clubID)
	}

	missing := "no-such-club-" + testutil.RandomHex(4)
	if got := svc.ClubIDForSlug(ctx, missing); got != "" {
		t.Fatalf("unknown slug = %q, want empty", got)
	}
	var created string
	if err := pool.QueryRow(ctx,
		`INSERT INTO clubs (slug, name, description) VALUES ($1, 'Later Club', 'x') RETURNING id`, missing,
	).Scan(&created); err != nil {
		t.Fatalf("create club: %v", err)
	}
	if got := svc.ClubIDForSlug(ctx, missing); got != created {
		t.Errorf("after create ClubIDForSlug = %q, want %q (a miss must not be cached)", got, created)
	}
}
