package handlers

import (
	"context"
	"slices"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// A magic link is deleted once it expired more than a day ago, used or not;
// one inside that day, or not yet expired, stays.
func TestIntegration_MagicLinkPurgeKeepsTheGracePeriod(t *testing.T) {
	t.Parallel()
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, db)

	for _, l := range []struct {
		token, expires string
		used           bool
	}{
		{"expired-25h", "now() - interval '25 hours'", false},
		{"used-expired-25h", "now() - interval '25 hours'", true},
		{"expired-23h", "now() - interval '23 hours'", false},
		{"used-expired-1h", "now() - interval '1 hour'", true},
		{"fresh", "now() + interval '15 minutes'", false},
		{"used-fresh", "now() + interval '10 minutes'", true},
	} {
		if _, err := db.Exec(ctx,
			`INSERT INTO magic_links (token, email, club_id, expires_at, used)
			 VALUES ($1, 'link@example.test', $2, `+l.expires+`, $3)`,
			l.token, clubID, l.used); err != nil {
			t.Fatalf("seed %s: %v", l.token, err)
		}
	}

	h := NewMagicLinkHandler(db, testConfig(), nil, nil, zerolog.Nop())
	n, err := h.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if n != 2 {
		t.Errorf("purged %d rows, want 2", n)
	}

	rows, err := db.Query(ctx, `SELECT token FROM magic_links ORDER BY token`)
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	var left []string
	for rows.Next() {
		var tok string
		if err := rows.Scan(&tok); err != nil {
			t.Fatalf("scan: %v", err)
		}
		left = append(left, tok)
	}
	rows.Close()
	want := []string{"expired-23h", "fresh", "used-expired-1h", "used-fresh"}
	if !slices.Equal(left, want) {
		t.Errorf("links left = %v, want %v", left, want)
	}
}
