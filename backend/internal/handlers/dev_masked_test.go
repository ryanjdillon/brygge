package handlers

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// maskedTables is the set of base tables fronted by a dev_masked view.
// It mirrors migrations/repeatable/dev_masked.sql; the parity subtest
// fails if a base table here grows a column the view doesn't account for.
var maskedTables = []string{
	"users", "billing_recipients", "orders", "invoices",
	"vipps_import_rows", "bank_import_rows", "vipps_webhook_events",
	"slip_shares", "boats",
}

// TestDevMaskedViews migrates a throwaway database, applies the dev_masked
// repeatable file, and checks (1) every base column of every masked table
// is represented in its view — the drift guard — and (2) the role actually
// reads masked NULLs through the views via its login search_path.
func TestDevMaskedViews(t *testing.T) {
	testutil.SkipIfNoDB(t)
	ctx := context.Background()
	baseURL := os.Getenv("DATABASE_URL")

	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer admin.Close()

	dbName := "devmasked_" + testutil.RandomHex(6)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Skipf("cannot CREATE DATABASE (need superuser): %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
	})

	dbURL := withDBName(t, baseURL, dbName)
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("throwaway pool: %v", err)
	}
	defer pool.Close()

	if err := testutil.MigrateSchema(ctx, pool, "public"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	masked, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), "repeatable", "dev_masked.sql"))
	if err != nil {
		t.Fatalf("read dev_masked.sql: %v", err)
	}
	if _, err := pool.Exec(ctx, string(masked)); err != nil {
		t.Fatalf("apply dev_masked.sql: %v", err)
	}

	t.Run("column parity", func(t *testing.T) {
		for _, tbl := range maskedTables {
			var missing []string
			rows, err := pool.Query(ctx, `
				SELECT b.column_name
				FROM information_schema.columns b
				LEFT JOIN information_schema.columns v
				  ON v.table_schema = 'dev_masked' AND v.table_name = $1
				 AND v.column_name = b.column_name
				WHERE b.table_schema = 'public' AND b.table_name = $1
				  AND v.column_name IS NULL
				ORDER BY b.column_name`, tbl)
			if err != nil {
				t.Fatalf("%s parity query: %v", tbl, err)
			}
			for rows.Next() {
				var c string
				if err := rows.Scan(&c); err != nil {
					t.Fatalf("scan: %v", err)
				}
				missing = append(missing, c)
			}
			rows.Close()
			if len(missing) > 0 {
				t.Errorf("dev_masked.%s is missing base column(s) %v — classify them in dev_masked.sql (NULL-mask, keep, or derive)", tbl, missing)
			}
		}
	})

	t.Run("role reads masked NULLs", func(t *testing.T) {
		// Synthetic fixtures only.
		club := "22222222-2222-2222-2222-222222222222"
		user := "11111111-1111-1111-1111-111111111111"
		mustExec(t, pool, ctx, "INSERT INTO clubs (id, slug, name) VALUES ($1,'synthetic','Synthetic Club')", club)
		mustExec(t, pool, ctx,
			"INSERT INTO users (id, club_id, email, first_name, last_name, phone) VALUES ($1,$2,'ola@example.test','Ola','Nordmann','99999999')",
			user, club)

		roleURL := devRoleURL(t, dbURL)
		rolePool, err := pgxpool.New(ctx, roleURL)
		if err != nil {
			t.Skipf("brygge_dev_ro pool unavailable: %v", err)
		}
		defer rolePool.Close()
		if err := rolePool.Ping(ctx); err != nil {
			t.Skipf("brygge_dev_ro not reachable (run `just dev-role-bootstrap`): %v", err)
		}

		// Unqualified name resolves to dev_masked via the role's login
		// search_path; identity columns must come back NULL, the opaque id
		// and the has_email flag intact.
		var email *string
		var gotID string
		var hasEmail bool
		if err := rolePool.QueryRow(ctx,
			"SELECT id, email, has_email FROM users WHERE id = $1", user,
		).Scan(&gotID, &email, &hasEmail); err != nil {
			t.Fatalf("role select from masked users: %v", err)
		}
		if email != nil {
			t.Errorf("email leaked through dev_masked.users: %q", *email)
		}
		if gotID != user {
			t.Errorf("id not preserved: got %q", gotID)
		}
		if !hasEmail {
			t.Errorf("has_email should be true for a user with an email on file")
		}

		// public.users must NOT resolve for this role once A2 lands; at A3
		// it still can (default-allow), so we don't assert denial here.
	})
}

func withDBName(t *testing.T, base, name string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}
