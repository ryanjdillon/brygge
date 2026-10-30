package paymentfilters_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/paymentfilters"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func readMigration(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func addItem(t *testing.T, pool *pgxpool.Pool, clubID, category, name string, active bool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO price_items (club_id, category, name, amount, is_active) VALUES ($1, $2, $3, 100, $4) RETURNING id::text`,
		clubID, category, name, active).Scan(&id); err != nil {
		t.Fatalf("insert price item %q: %v", name, err)
	}
	return id
}

// groupItems is slug -> sorted item names, from the database.
func groupItems(t *testing.T, pool *pgxpool.Pool, clubID string) map[string][]string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT g.slug, COALESCE(pi.name, '') FROM payment_filter_groups g
		   LEFT JOIN payment_filter_group_items gi ON gi.group_id = g.id
		   LEFT JOIN price_items pi ON pi.id = gi.price_item_id
		  WHERE g.club_id = $1`, clubID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var slug, name string
		if err := rows.Scan(&slug, &name); err != nil {
			t.Fatal(err)
		}
		if name == "" {
			if _, ok := out[slug]; !ok {
				out[slug] = nil
			}
			continue
		}
		out[slug] = append(out[slug], name)
	}
	for _, v := range out {
		sort.Strings(v)
	}
	return out
}

func sameSet(got map[string][]string, want map[string][]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok || strings.Join(g, "|") != strings.Join(w, "|") {
			return false
		}
	}
	return true
}

// Migration 000123 ports the overview's old rules (guest, motorhome, and the
// interim "slipping" name rule) as groups derived from each club's own price
// items. The test re-executes the up file over fixtures, since the schema is
// migrated before any club exists.
func TestPaymentFilterGroupsMigrationBackfill(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	up := readMigration(t, "000123_payment_filter_groups.up.sql")

	clubA := testutil.SeedClub(t, pool)
	clubB := testutil.SeedClub(t, pool)
	addItem(t, pool, clubA, "guest", "Synthetic guest night", true)
	addItem(t, pool, clubA, "guest", "Synthetic old guest night", false) // archived: still assigned
	addItem(t, pool, clubA, "motorhome", "Synthetic motorhome night", true)
	addItem(t, pool, clubA, "service", "Synthetic Slipping small", true)
	addItem(t, pool, clubA, "guest", "Synthetic guest slipping", true) // matches two rules: lands in guest
	addItem(t, pool, clubA, "service", "Synthetic crane hire", true)
	addItem(t, pool, clubA, "legacy_bobil", "Synthetic legacy bobil", true) // old category spelling
	// Club B sells nothing the old rules recognised.
	addItem(t, pool, clubB, "service", "Synthetic workshop", true)

	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("apply 000123: %v", err)
	}

	wantA := map[string][]string{
		"guest":     {"Synthetic guest night", "Synthetic guest slipping", "Synthetic old guest night"},
		"motorhome": {"Synthetic legacy bobil", "Synthetic motorhome night"},
		"slipping":  {"Synthetic Slipping small"},
		"faktura":   nil,
		"other":     nil,
	}
	if got := groupItems(t, pool, clubA); !sameSet(got, wantA) {
		t.Errorf("club A groups = %v, want %v", got, wantA)
	}
	wantB := map[string][]string{"faktura": nil, "other": nil}
	if got := groupItems(t, pool, clubB); !sameSet(got, wantB) {
		t.Errorf("club B groups = %v, want %v", got, wantB)
	}

	// The seeded rows carry label keys, no text, and the old priority order.
	rows, err := pool.Query(ctx,
		`SELECT slug, name IS NULL, label_key, icon, sort_order, enabled, builtin
		   FROM payment_filter_groups WHERE club_id = $1 ORDER BY sort_order`, clubA)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for rows.Next() {
		var slug, key, icon string
		var noName, enabled bool
		var sortOrder int
		var builtin *string
		if err := rows.Scan(&slug, &noName, &key, &icon, &sortOrder, &enabled, &builtin); err != nil {
			t.Fatal(err)
		}
		order = append(order, slug)
		if !noName || key != slug || !enabled || !paymentfilters.ValidIcon(icon) {
			t.Errorf("%s: name null %v, label_key %q, enabled %v, icon %q", slug, noName, key, enabled, icon)
		}
	}
	rows.Close()
	if strings.Join(order, ",") != "guest,motorhome,slipping,faktura,other" {
		t.Errorf("sort order = %v, want guest, motorhome, slipping, faktura, other", order)
	}

	// A second run adds nothing: retries and the dry-run are harmless.
	var groupsBefore, itemsBefore int
	count := func() (int, int) {
		var g, i int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_filter_groups), (SELECT count(*) FROM payment_filter_group_items)`).Scan(&g, &i); err != nil {
			t.Fatal(err)
		}
		return g, i
	}
	groupsBefore, itemsBefore = count()
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("second run of 000123: %v", err)
	}
	if g, i := count(); g != groupsBefore || i != itemsBefore {
		t.Errorf("second run changed rows: groups %d -> %d, items %d -> %d", groupsBefore, g, itemsBefore, i)
	}

	// Deleting a price item takes its assignment with it.
	if _, err := pool.Exec(ctx, `DELETE FROM price_items WHERE name = 'Synthetic crane hire'`); err != nil {
		t.Fatal(err)
	}

	// The down migration drops both tables, and the up applies again.
	if _, err := pool.Exec(ctx, readMigration(t, "000123_payment_filter_groups.down.sql")); err != nil {
		t.Fatalf("down: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name LIKE 'payment_filter_group%'`).Scan(&left); err != nil || left != 0 {
		t.Errorf("tables left after down: %d, %v", left, err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if got := groupItems(t, pool, clubA); !sameSet(got, wantA) {
		t.Errorf("club A groups after down and up = %v", got)
	}
}

// A club created after the migration has no group rows: reads synthesise the
// two built-ins, and the first write creates them (PLAT-1: no seeded groups).
func TestFreshClubHasOnlySynthesisedBuiltins(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := testutil.SeedClub(t, pool)

	groups, err := paymentfilters.ListGroups(ctx, pool, club, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Slug != "faktura" || groups[1].Slug != "other" {
		t.Fatalf("groups = %+v, want faktura then other", groups)
	}
	for _, g := range groups {
		if g.ID != "" || g.Name != nil || g.LabelKey == nil || !g.Enabled || g.Builtin == nil {
			t.Errorf("%s is not a synthesised default built-in: %+v", g.Slug, g)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_filter_groups WHERE club_id = $1`, club).Scan(&n); err != nil || n != 0 {
		t.Errorf("a read wrote %d rows (%v)", n, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := paymentfilters.EnsureBuiltins(ctx, tx, club); err != nil {
		t.Fatal(err)
	}
	if err := paymentfilters.EnsureBuiltins(ctx, tx, club); err != nil {
		t.Fatalf("second EnsureBuiltins: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_filter_groups WHERE club_id = $1`, club).Scan(&n); err != nil || n != 2 {
		t.Errorf("EnsureBuiltins left %d rows, want 2 (%v)", n, err)
	}
}
