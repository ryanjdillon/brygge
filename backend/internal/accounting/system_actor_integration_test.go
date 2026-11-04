package accounting

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

const systemActorDisplayName = "Automatisk bokføring"

func systemActorFixture(t *testing.T) (*Service, *pgxpool.Pool, string) {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	return NewService(pool, nil, zerolog.Nop()), pool, testutil.SeedClub(t, pool)
}

func countSystemUsers(t *testing.T, pool *pgxpool.Pool, clubID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE club_id = $1 AND kind = 'system'`, clubID).Scan(&n); err != nil {
		t.Fatalf("count system users: %v", err)
	}
	return n
}

// SystemActor creates the row on first use, returns the same id on every later
// call, and the row is a non-person with the documented identity and no role.
func TestIntegration_SystemActorCreatesOnceAndIsIdempotent(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)
	ctx := context.Background()

	if n := countSystemUsers(t, pool, clubID); n != 0 {
		t.Fatalf("a club starts with %d system users, want 0 (the migration seeds none)", n)
	}
	first, err := svc.SystemActor(ctx, clubID)
	if err != nil || first == "" {
		t.Fatalf("SystemActor = %q, %v", first, err)
	}
	second, err := svc.SystemActor(ctx, clubID)
	if err != nil || second != first {
		t.Fatalf("second SystemActor = %q, %v; want %q", second, err, first)
	}
	if n := countSystemUsers(t, pool, clubID); n != 1 {
		t.Errorf("system users after two calls = %d, want 1", n)
	}

	var email, fullName, kind string
	var deactivated bool
	var roles int
	if err := pool.QueryRow(ctx,
		`SELECT email, full_name, kind, deactivated_at IS NOT NULL,
		        (SELECT count(*) FROM user_roles WHERE user_id = u.id)
		   FROM users u WHERE id = $1`, first,
	).Scan(&email, &fullName, &kind, &deactivated, &roles); err != nil {
		t.Fatalf("read system actor: %v", err)
	}
	if email != SystemActorEmail || fullName != systemActorDisplayName || kind != "system" || deactivated || roles != 0 {
		t.Errorf("system actor = email %q name %q kind %q deactivated %v roles %d", email, fullName, kind, deactivated, roles)
	}

	other := testutil.SeedClub(t, pool)
	otherID, err := svc.SystemActor(ctx, other)
	if err != nil || otherID == first {
		t.Errorf("a second club's system actor = %q, %v; want its own row", otherID, err)
	}
}

// Many first calls at once leave one row and every caller gets its id. The
// unique-violation fallback in SystemActor (a loser failing on the
// (club_id, email) index before the conflict clause sees the winner, then
// reading the winner's row) is not reached here: 70 runs of 20 callers never
// produced it, so TestIntegration_SystemActorLosingInsertReadsWinnersRow forces it.
func TestIntegration_SystemActorConcurrentFirstCalls(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)

	const callers = 20
	ids := make([]string, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ids[i], errs[i] = svc.SystemActor(context.Background(), clubID)
		}()
	}
	close(start)
	wg.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] || ids[i] == "" {
			t.Errorf("caller %d got %q, want %q", i, ids[i], ids[0])
		}
	}
	if n := countSystemUsers(t, pool, clubID); n != 1 {
		t.Errorf("system users after %d concurrent calls = %d, want 1", callers, n)
	}
}

// A loser whose INSERT fails on the (club_id, email) unique index, because the
// winner's row landed after its conflict check, returns the winner's id. The
// seam commits the winner's row and then runs the INSERT without the conflict
// clause, which fails with a real unique violation.
func TestIntegration_SystemActorLosingInsertReadsWinnersRow(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)
	ctx := context.Background()

	var winnerID string
	svc.systemActorInsert = func(ctx context.Context, clubID string) error {
		const insert = `INSERT INTO users (club_id, email, first_name, last_name, kind)
			VALUES ($1, $2, 'Automatisk', 'bokføring', 'system')`
		if err := pool.QueryRow(ctx, insert+" RETURNING id::text", clubID, SystemActorEmail).Scan(&winnerID); err != nil {
			t.Errorf("winner insert: %v", err)
		}
		_, err := pool.Exec(ctx, insert, clubID, SystemActorEmail)
		if !isUniqueViolation(err) {
			t.Errorf("loser insert = %v, want a unique violation", err)
		}
		return err
	}

	id, err := svc.SystemActor(ctx, clubID)
	if err != nil {
		t.Fatalf("SystemActor: %v", err)
	}
	if id == "" || id != winnerID {
		t.Errorf("SystemActor = %q, want the winner's %q", id, winnerID)
	}
	if n := countSystemUsers(t, pool, clubID); n != 1 {
		t.Errorf("system users = %d, want 1", n)
	}
}

// A person holding the system address (only possible for a row written outside
// the handlers, which refuse .invalid) makes SystemActor fail with an error. It
// never returns an empty id, which every actor guard would read as "no human".
func TestIntegration_SystemActorEmailCollisionFailsLoudly(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name) VALUES ($1, $2, 'Synthetic', 'Person')`,
		clubID, SystemActorEmail); err != nil {
		t.Fatalf("seed colliding person: %v", err)
	}

	id, err := svc.SystemActor(ctx, clubID)
	if err == nil || id != "" {
		t.Fatalf("SystemActor = %q, %v; want an error and no id", id, err)
	}
	if n := countSystemUsers(t, pool, clubID); n != 0 {
		t.Errorf("system users = %d, want 0", n)
	}
}

// The database refuses a second system user in a club and any role for one.
func TestIntegration_SystemActorDatabaseBackstops(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)
	ctx := context.Background()
	id, err := svc.SystemActor(ctx, clubID)
	if err != nil {
		t.Fatalf("SystemActor: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name, kind)
		 VALUES ($1, 'second@system.invalid', 'Second', 'Actor', 'system')`, clubID); err == nil {
		t.Error("a second system user in one club was accepted")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name, kind)
		 VALUES ($1, 'bogus@example.com', 'Bogus', 'Kind', 'robot')`, clubID); err == nil {
		t.Error("an unknown users.kind was accepted")
	}

	for _, role := range []string{"admin", "treasurer", "member", "board"} {
		_, err := pool.Exec(ctx,
			`INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, $3::user_role)`, id, clubID, role)
		if err == nil {
			t.Errorf("role %q was granted to the system user", role)
		}
	}

	// An UPDATE that moves an existing role row to the system user is refused too.
	personID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	if _, err := pool.Exec(ctx, `UPDATE user_roles SET user_id = $1 WHERE user_id = $2`, id, personID); err == nil {
		t.Error("a role row was reassigned to the system user")
	}
}

// Up, down, up: the down removes an unreferenced system actor and the schema
// it added; the second up works; a system actor that has authored a journal
// entry survives the down and is picked up again by the second up.
func TestIntegration_Migration000129UpDownUp(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)
	ctx := context.Background()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), name)) // #nosec G304 -- test-only, fixed migrations path
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	up, down := read("000129_automatic_booking.up.sql"), read("000129_automatic_booking.down.sql")
	exec := func(sql, what string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	hasColumn := func(table, column string) bool {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM information_schema.columns
			  WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`,
			table, column).Scan(&n); err != nil {
			t.Fatalf("column probe: %v", err)
		}
		return n == 1
	}

	if _, err := svc.SystemActor(ctx, clubID); err != nil {
		t.Fatalf("SystemActor: %v", err)
	}
	exec(up, "re-applying up on an already migrated schema (idempotent)")

	exec(down, "down with an unreferenced system actor")
	for _, c := range [][2]string{{"users", "kind"}, {"clubs", "automatic_booking"}, {"vipps_import_rows", "auto_declined_at"}, {"bank_import_rows", "auto_declined_at"}} {
		if hasColumn(c[0], c[1]) {
			t.Errorf("%s.%s survived the down", c[0], c[1])
		}
	}
	var systemEmails int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1`, SystemActorEmail).Scan(&systemEmails); err != nil {
		t.Fatalf("count: %v", err)
	}
	if systemEmails != 0 {
		t.Errorf("the unreferenced system actor survived the down")
	}

	exec(up, "second up")
	if !hasColumn("users", "kind") || !hasColumn("clubs", "automatic_booking") {
		t.Fatal("second up did not restore the columns")
	}
	if _, err := svc.SystemActor(ctx, clubID); err != nil {
		t.Fatalf("SystemActor after the second up: %v", err)
	}

	// Referenced by a journal entry: the down must keep the row and its kind.
	fc := fin.NewClub(t, pool)
	refID, err := svc.SystemActor(ctx, fc.ID)
	if err != nil {
		t.Fatalf("SystemActor for the second club: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, source, created_by)
		 VALUES ($1, $2, 1, make_date($3, 3, 1), 'Synthetic', 'manual', $4)`, fc.ID, fc.PeriodID, fc.Year, refID); err != nil {
		t.Fatalf("seed journal entry authored by the system actor: %v", err)
	}
	exec(down, "down with a referenced system actor")
	var kind string
	if err := pool.QueryRow(ctx, `SELECT kind FROM users WHERE id = $1`, refID).Scan(&kind); err != nil || kind != "system" {
		t.Fatalf("referenced system actor after the down: kind %q, %v", kind, err)
	}
	if !hasColumn("users", "kind") {
		t.Error("kind was dropped while a system actor remained")
	}
	if hasColumn("clubs", "automatic_booking") {
		t.Error("clubs.automatic_booking survived the down")
	}
	exec(up, "up after a down that kept the actor")
	again, err := svc.SystemActor(ctx, fc.ID)
	if err != nil || again != refID {
		t.Errorf("SystemActor after the kept-actor round trip = %q, %v; want %q", again, err, refID)
	}
}

// A member created with the system address cannot make SystemActor fail for
// another club, and the handlers' .invalid refusal is what keeps it from
// happening in one club (handlers tests). Here: the address is per club.
func TestIntegration_SystemActorAddressIsPerClub(t *testing.T) {
	svc, pool, clubID := systemActorFixture(t)
	ctx := context.Background()
	other := testutil.SeedClub(t, pool)
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name) VALUES ($1, $2, 'Synthetic', 'Person')`,
		other, SystemActorEmail); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.SystemActor(ctx, clubID); err != nil {
		t.Errorf("a person holding the address in another club broke SystemActor: %v", err)
	}
	if _, err := svc.SystemActor(ctx, other); err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("SystemActor in the colliding club = %v, want an error", err)
	}
}
