package accounting

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestLevenshteinWithin1(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"ole nordmann", "ole nordmann", true},  // equal
		{"ole nordmann", "ole nordman", true},   // one deletion
		{"ole nordman", "ole nordmann", true},   // one insertion (other order)
		{"jon", "jan", true},                    // one substitution
		{"kari berg", "kari borg", true},        // one substitution
		{"kari berg", "kari olsen", false},      // many diffs
		{"ole", "oleee", false},                 // length diff > 1
		{"abcd", "abef", false},                 // two substitutions
	}
	for _, c := range cases {
		if got := levenshteinWithin1(c.a, c.b); got != c.want {
			t.Errorf("levenshteinWithin1(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestResolveCustomerToMemberFuzzy covers DIL-367 cascade passes 4
// (Levenshtein-1) and 5 (email local-part).
func TestResolveCustomerToMemberFuzzy(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	u1, _ := testutil.SeedUser(t, pool, clubID, nil)
	u2, _ := testutil.SeedUser(t, pool, clubID, nil)
	// u1: name typo target; u2: name doesn't match but email local-part does.
	// full_name is a generated column (first + last), so set the parts.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET first_name='Ole', last_name='Nordmann', email='ole@example.no' WHERE id=$1`, u1); err != nil {
		t.Fatalf("update u1: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET first_name='Kari', last_name='Olsen', email='kari.berg@example.no' WHERE id=$1`, u2); err != nil {
		t.Fatalf("update u2: %v", err)
	}

	svc := NewService(pool, nil, zerolog.Nop())

	// Pass 4: "Ole Nordman" is Levenshtein-1 from "Ole Nordmann" and unique.
	if got, _ := svc.resolveCustomerToMember(ctx, clubID, "Ole Nordman", ""); got != u1 {
		t.Errorf("pass4 fuzzy name: got %q, want u1 %q", got, u1)
	}
	// Pass 5: "Kari Berg" doesn't match any name but matches u2's email local-part.
	if got, _ := svc.resolveCustomerToMember(ctx, clubID, "Kari Berg", ""); got != u2 {
		t.Errorf("pass5 email local-part: got %q, want u2 %q", got, u2)
	}
	// No match → empty.
	if got, _ := svc.resolveCustomerToMember(ctx, clubID, "Zxqv Wbbl", ""); got != "" {
		t.Errorf("no-match: got %q, want empty", got)
	}
}
