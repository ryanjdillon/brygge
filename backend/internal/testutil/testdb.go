package testutil

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// migrationsDir returns the absolute path to the migrations directory,
// resolved relative to this source file.
func migrationsDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

// RandomHex returns n random bytes encoded as a hex string.
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// SkipIfNoDB skips the test if DATABASE_URL is not set.
func SkipIfNoDB(tb testing.TB) {
	tb.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		tb.Skip("DATABASE_URL not set, skipping integration test")
	}
}

// SkipIfNoRedis skips the test if Redis is not reachable.
// It checks REDIS_URL or falls back to localhost:6379.
func SkipIfNoRedis(tb testing.TB) {
	tb.Helper()
	addr := redisAddr()
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		tb.Skipf("Redis not available at %s: %v", addr, err)
	}
}

// redisAddr extracts the host:port from REDIS_URL or returns a default.
func redisAddr() string {
	u := os.Getenv("REDIS_URL")
	if u == "" {
		return "localhost:6379"
	}
	opt, err := redis.ParseURL(u)
	if err != nil {
		return "localhost:6379"
	}
	return opt.Addr
}

// DBOption tunes the pool SetupTestDB returns.
type DBOption func(*pgxpool.Config)

// WithMaxConns caps the returned pool at n connections. Concurrency tests use
// a small cap so that a code path waiting for a second connection while it
// holds a row lock hangs in the test instead of only under production load.
func WithMaxConns(n int32) DBOption {
	return func(c *pgxpool.Config) { c.MaxConns = n }
}

// SetupTestDB returns a pool on a fresh, fully migrated database of its own:
// a clone of a template database that holds every migration (see
// ensureTemplate). The clone is dropped in t.Cleanup. The role behind
// DATABASE_URL needs CREATEDB.
//
// Cloning costs a file copy; applying every migration per test cost seconds
// and, being serialized cluster-wide, dominated the integration suite.
func SetupTestDB(t *testing.T, opts ...DBOption) *pgxpool.Pool {
	t.Helper()
	SkipIfNoDB(t)

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")

	admin, err := adminPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connecting to database: %v", err)
	}

	name := "test_" + RandomHex(8)
	if err := cloneTemplate(ctx, admin, dbURL, name); err != nil {
		t.Fatalf("creating test database %s: %v", name, err)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(dropCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
	})

	poolCfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parsing database URL: %v", err)
	}
	poolCfg.ConnConfig.Database = name
	for _, opt := range opts {
		opt(poolCfg)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("creating pool on %s: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

// templatePrefix names the migrated template databases; anything else with
// this prefix is a stale template (older migrations) or an aborted build.
const templatePrefix = "brygge_tmpl_"

// templateLockKey serializes template builds across test processes. Advisory
// locks are per database, so it is always taken on the DATABASE_URL database.
const templateLockKey int64 = 0x42525954 // "BRYT"

var (
	templateMu  sync.Mutex
	sharedAdmin *pgxpool.Pool
	cachedTmpl  string
)

// adminPool returns the process-wide pool on the DATABASE_URL database used
// to create and drop test databases. It lives until the test binary exits.
func adminPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	templateMu.Lock()
	defer templateMu.Unlock()
	if sharedAdmin != nil {
		return sharedAdmin, nil
	}
	p, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	sharedAdmin = p
	return p, nil
}

// cloneTemplate creates database name from the current migration template,
// rebuilding the template once if another process dropped it as stale.
func cloneTemplate(ctx context.Context, admin *pgxpool.Pool, dbURL, name string) error {
	for attempt := 0; ; attempt++ {
		tmpl, err := ensureTemplate(ctx, admin, dbURL, attempt > 0)
		if err != nil {
			return err
		}
		_, err = admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, tmpl))
		var pgErr *pgconn.PgError
		if attempt == 0 && errors.As(err, &pgErr) && pgErr.Code == "3D000" { // invalid_catalog_name
			continue
		}
		return err
	}
}

// ensureTemplate returns the name of a template database holding every
// migration, building it if no process has yet. The name is keyed by the
// migration files and by whether the brygge_dev_ro role exists (migrations
// grant to it only when it does), so a changed migration gets a new template
// and a stale one is never reused.
func ensureTemplate(ctx context.Context, admin *pgxpool.Pool, dbURL string, recheck bool) (string, error) {
	templateMu.Lock()
	defer templateMu.Unlock()
	if cachedTmpl != "" && !recheck {
		return cachedTmpl, nil
	}

	conn, err := admin.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("acquiring admin connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", templateLockKey); err != nil {
		return "", fmt.Errorf("acquiring template advisory lock: %w", err)
	}
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", templateLockKey) //nolint:errcheck // best-effort; conn release drops it anyway

	var devRO bool
	if err := conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro')").Scan(&devRO); err != nil {
		return "", fmt.Errorf("checking brygge_dev_ro: %w", err)
	}
	key, err := migrationsKey(devRO)
	if err != nil {
		return "", err
	}
	tmpl := templatePrefix + key

	var exists bool
	if err := conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", tmpl).Scan(&exists); err != nil {
		return "", fmt.Errorf("looking up template %s: %w", tmpl, err)
	}
	if !exists {
		if err := buildTemplate(ctx, conn.Conn(), dbURL, tmpl); err != nil {
			return "", err
		}
		dropStaleTemplates(ctx, conn.Conn(), tmpl)
	}

	cachedTmpl = tmpl
	return tmpl, nil
}

// migrationsKey hashes every *.up.sql file name and body.
func migrationsKey(devRO bool) (string, error) {
	files, err := upMigrations()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "dev_ro=%t\n", devRO)
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(migrationsDir(), name)) // #nosec G304 -- test-only, dir is hardcoded migrations path
		if err != nil {
			return "", fmt.Errorf("reading migration %s: %w", name, err)
		}
		fmt.Fprintf(h, "%s %d\n", name, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// buildTemplate migrates a scratch database and only then renames it to tmpl,
// so a template name that exists always holds a complete migration run.
func buildTemplate(ctx context.Context, admin *pgx.Conn, dbURL, tmpl string) error {
	build := tmpl + "_build"
	if _, err := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", build)); err != nil {
		return fmt.Errorf("dropping aborted template build: %w", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", build)); err != nil {
		return fmt.Errorf("creating template build database (role needs CREATEDB): %w", err)
	}

	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return fmt.Errorf("parsing database URL: %w", err)
	}
	cfg.ConnConfig.Database = build
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connecting to template build: %w", err)
	}
	err = runMigrations(ctx, pool, "public")
	pool.Close()
	if err != nil {
		return fmt.Errorf("migrating template: %w", err)
	}

	// Closing the pool does not wait for the server backends to exit, and
	// RENAME refuses while any session is still connected.
	rename := fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", build, tmpl)
	for i := 0; ; i++ {
		_, err = admin.Exec(ctx, rename)
		var pgErr *pgconn.PgError
		if err == nil || i >= 50 || !errors.As(err, &pgErr) || pgErr.Code != "55006" { // object_in_use
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("publishing template %s: %w", tmpl, err)
	}
	// No connections: CREATE DATABASE ... TEMPLATE fails while anyone is
	// connected to the source.
	if _, err := admin.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s WITH IS_TEMPLATE true ALLOW_CONNECTIONS false", tmpl)); err != nil {
		return fmt.Errorf("marking %s as a template: %w", tmpl, err)
	}
	return nil
}

// dropStaleTemplates removes templates built from older migrations. Best
// effort: one still being cloned by another run stays, and that run rebuilds
// its template if it disappears (see cloneTemplate).
func dropStaleTemplates(ctx context.Context, admin *pgx.Conn, keep string) {
	rows, err := admin.Query(ctx,
		"SELECT datname FROM pg_database WHERE starts_with(datname, $1) AND datname <> $2", templatePrefix, keep)
	if err != nil {
		return
	}
	stale, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return
	}
	for _, name := range stale {
		_, _ = admin.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s WITH IS_TEMPLATE false", name))
		_, _ = admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", name))
	}
}

// migrationLockKey serializes concurrent test-schema migration runs (BRY-184).
const migrationLockKey int64 = 0x42525947 // "BRYG"

// runMigrations reads all *.up.sql files from the migrations directory,
// sorts them by name, and executes each one inside the given schema.
//
// The whole run is guarded by a session-level Postgres advisory lock on a
// single connection. Parallel tests (`t.Parallel()`) each migrate their own
// schema concurrently, but some migrations touch cluster-global objects —
// e.g. 000051 GRANTs on the shared `brygge_dev_ro` role — and concurrent
// role-privilege DDL races with "tuple concurrently updated" (XX000). The
// lock makes migration runs mutually exclusive without changing any
// migration SQL.
func runMigrations(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	dir := migrationsDir()
	upFiles, err := upMigrations()
	if err != nil {
		return err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("acquiring migration advisory lock: %w", err)
	}
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey) //nolint:errcheck // best-effort; conn release drops it anyway

	// pgcrypto provides public.digest(), called by migrations 000037/000038/
	// 000042. Install it in public ONCE here, before the loop sets the
	// per-test schema search_path — otherwise `CREATE EXTENSION IF NOT EXISTS
	// pgcrypto` inside a migration lands the extension (and digest()) in the
	// throwaway test schema, and the migrations' `public.digest()` calls fail
	// with "function public.digest(...) does not exist". Serialized by the
	// advisory lock above so parallel test schemas don't race.
	if _, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public"); err != nil {
		return fmt.Errorf("creating pgcrypto extension: %w", err)
	}

	for _, name := range upFiles {
		sql, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- test-only, dir is hardcoded migrations path
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", name, err)
		}

		wrapped := fmt.Sprintf("SET search_path TO %s;\n%s", schema, string(sql))
		if _, err := conn.Exec(ctx, wrapped); err != nil {
			return fmt.Errorf("executing migration %s: %w", name, err)
		}
	}
	return nil
}

// upMigrations lists the *.up.sql file names in apply order.
func upMigrations() ([]string, error) {
	dir := migrationsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading migrations directory %s: %w", dir, err)
	}
	var upFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			upFiles = append(upFiles, e.Name())
		}
	}
	sort.Strings(upFiles)
	return upFiles, nil
}

// MigrationsDir exposes the absolute migrations directory path so callers
// outside this package (e.g. the dev_masked integration test, which also
// needs migrations/repeatable/dev_masked.sql) can resolve sibling files.
func MigrationsDir() string { return migrationsDir() }

// MigrateSchema runs every *.up.sql migration into the given schema of the
// supplied pool's database. Unlike SetupTestDB (which isolates into a random
// schema), passing "public" migrates the database proper — needed by tests
// that must then connect as a different role, since per-role search_path and
// object privileges apply per database, not per schema search_path.
func MigrateSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	return runMigrations(ctx, pool, schema)
}

// SetupTestRedis connects to Redis, selects a random DB number (1-15), and
// flushes it. The DB is flushed again in t.Cleanup.
func SetupTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	SkipIfNoRedis(t)

	b := make([]byte, 1)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("generating random DB index: %v", err)
	}
	db := int(b[0])%15 + 1

	addr := redisAddr()
	client := redis.NewClient(&redis.Options{
		Addr: addr,
		DB:   db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.FlushDB(ctx).Err(); err != nil {
		client.Close()
		t.Fatalf("flushing redis DB %d: %v", db, err)
	}

	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanCancel()
		_ = client.FlushDB(cleanCtx).Err()
		client.Close()
	})

	return client
}

// TestConfig returns a Config suitable for integration tests.
func TestConfig() config.Config {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://brygge:brygge@localhost:5432/brygge?sslmode=disable" // #nosec G101 -- test-only default credentials
	}
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	return config.Config{
		Port:          8080,
		DatabaseURL:   dbURL,
		RedisURL:      redisURL,
		ClubSlug:      "test-club",
		VippsTestMode: true,
		S3BucketDocs:  "brygge-test",
	}
}

// SeedClub inserts a test club and returns its ID.
func SeedClub(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()

	slug := "test-club-" + RandomHex(4)
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO clubs (slug, name, description)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
		slug, "Test Club "+slug, "A club created for testing",
	).Scan(&id)
	if err != nil {
		t.Fatalf("seeding club: %v", err)
	}
	return id
}

// SeedUser inserts a test user associated with the given club and grants the
// specified roles. It returns the user ID and email.
func SeedUser(t *testing.T, pool *pgxpool.Pool, clubID string, roles []string) (userID string, email string) {
	t.Helper()
	ctx := context.Background()

	email = fmt.Sprintf("testuser-%s@example.com", RandomHex(4))
	err := pool.QueryRow(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name, phone)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		clubID, email, "Test", "User", "+4700000000",
	).Scan(&userID)
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	for _, role := range roles {
		if _, err := pool.Exec(ctx,
			`INSERT INTO user_roles (user_id, club_id, role)
			 VALUES ($1, $2, $3)`,
			userID, clubID, role,
		); err != nil {
			t.Fatalf("granting role %q to user %s: %v", role, userID, err)
		}
	}

	return userID, email
}
