package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/config"
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

// SetupTestDB connects to PostgreSQL via DATABASE_URL, creates an isolated
// schema with a random name, runs all migrations against it, and returns a
// pool whose search_path is set to that schema. The schema is dropped in
// t.Cleanup.
func SetupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	SkipIfNoDB(t)

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")

	schema := "test_" + RandomHex(8)

	adminPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connecting to database: %v", err)
	}

	if _, err := adminPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		adminPool.Close()
		t.Fatalf("creating schema %s: %v", schema, err)
	}

	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = adminPool.Exec(dropCtx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		adminPool.Close()
	})

	if err := runMigrations(ctx, adminPool, schema); err != nil {
		t.Fatalf("running migrations in schema %s: %v", schema, err)
	}

	poolCfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parsing database URL: %v", err)
	}
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schema

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("creating pool with schema search_path: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

// runMigrations reads all *.up.sql files from the migrations directory,
// sorts them by name, and executes each one inside the given schema.
func runMigrations(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	dir := migrationsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading migrations directory %s: %w", dir, err)
	}

	var upFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			upFiles = append(upFiles, e.Name())
		}
	}
	sort.Strings(upFiles)

	for _, name := range upFiles {
		sql, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- test-only, dir is hardcoded migrations path
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", name, err)
		}

		wrapped := fmt.Sprintf("SET search_path TO %s;\n%s", schema, string(sql))
		if _, err := pool.Exec(ctx, wrapped); err != nil {
			return fmt.Errorf("executing migration %s: %w", name, err)
		}
	}
	return nil
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
		Port:             8080,
		DatabaseURL:      dbURL,
		RedisURL:         redisURL,
		ClubSlug:         "test-club",
		JWTSecret:        "test-secret-do-not-use-in-production",
		JWTAccessExpiry:  15 * time.Minute,
		JWTRefreshExpiry: 7 * 24 * time.Hour,
		VippsTestMode:    true,
		S3Bucket:         "brygge-test",
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
		`INSERT INTO users (club_id, email, full_name, phone)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		clubID, email, "Test User", "+4700000000",
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
