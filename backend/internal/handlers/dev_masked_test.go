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
	"users", "clubs", "billing_recipients", "orders", "invoices",
	"vipps_import_rows", "bank_import_rows", "vipps_webhook_events",
	"slip_shares", "boats",
	"journal_entries", "journal_lines", "payments", "invoice_lines",
	"account_mapping_rules", "vipps_message_rules", "slips", "dock_fingers",
}

// rawTextColumns is every text/JSON column the role may read from a raw
// (non-view) public table, each reviewed as unable to name a person: GL
// account names, product/catalog text, the club's own bank account, enum-
// like codes. A new free-text column on an allowlisted table fails the
// "raw text columns" subtest until it is reviewed here or the table moves
// to a dev_masked view — free text copied from bank rows or typed by
// people is how payer names leaked before.
var rawTextColumns = map[string]bool{
	// chart of accounts
	"accounts.code": true, "accounts.description": true, "accounts.name": true, "accounts.parent_code": true,
	// the club's own statement files and account
	"bank_imports.bank_account_code": true, "bank_imports.bank_account_number": true, "bank_imports.filename": true, "bank_imports.format": true,
	// pier names
	"docks.name": true, "docks.slug": true,
	// enum-like codes
	"mva_compensation_reports.model": true, "mva_compensation_reports.status": true,
	// catalog item names (guest names live on orders, masked)
	"order_lines.name": true,
	// fee/price catalog
	"price_items.audience": true, "price_items.category": true, "price_items.currency": true, "price_items.description": true, "price_items.metadata": true, "price_items.name": true, "price_items.pricing_kind": true, "price_items.tier_dimension": true, "price_items.unit": true,
	// merch catalog
	"product_variants.color": true, "product_variants.image_url": true, "product_variants.size": true,
	// merch catalog
	"products.currency": true, "products.description": true, "products.image_url": true, "products.name": true,
	// settlement file names and merchant numbers
	"vipps_imports.filename": true, "vipps_imports.msn": true,
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

	t.Run("raw text columns", func(t *testing.T) {
		rows, err := pool.Query(ctx, `
			SELECT c.relname || '.' || a.attname
			FROM pg_class c
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
			WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r'
			  AND has_table_privilege('brygge_dev_ro', c.oid, 'SELECT')
			  AND a.atttypid IN ('text'::regtype, 'varchar'::regtype, 'bpchar'::regtype, 'jsonb'::regtype, 'json'::regtype)`)
		if err != nil {
			t.Skipf("brygge_dev_ro role not present: %v", err)
		}
		defer rows.Close()
		seen := 0
		for rows.Next() {
			var col string
			if err := rows.Scan(&col); err != nil {
				t.Fatalf("scan: %v", err)
			}
			seen++
			if !rawTextColumns[col] {
				t.Errorf("%s is free text readable raw by brygge_dev_ro — review it into rawTextColumns, or mask the table via a dev_masked view", col)
			}
		}
		if seen == 0 {
			t.Fatal("no raw-readable text columns found; the privilege query is broken, so this guard would pass vacuously")
		}
	})

	t.Run("role access: masking + default-deny", func(t *testing.T) {
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
		// search_path; identity columns come back NULL, the opaque id and
		// the has_email flag intact.
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

		// Default-deny: the raw base table is denied (reachable only via the
		// masked view), an allowlisted table is readable, and a brand-new
		// unclassified table is denied — the drift guard.
		if _, err := rolePool.Exec(ctx, "SELECT 1 FROM public.users"); err == nil {
			t.Error("role could read raw public.users — base PII not denied")
		}
		if _, err := rolePool.Exec(ctx, "SELECT 1 FROM public.clubs"); err == nil {
			t.Error("role could read raw public.clubs — base secrets not denied")
		}
		if _, err := rolePool.Exec(ctx, "SELECT 1 FROM public.journal_entries"); err == nil {
			t.Error("role could read raw public.journal_entries — bank-copied descriptions carry payer names")
		}
		if _, err := rolePool.Exec(ctx, "SELECT 1 FROM public.accounts"); err != nil {
			t.Errorf("role could not read allowlisted public.accounts: %v", err)
		}
		drift := "drifttest_" + testutil.RandomHex(6)
		mustExec(t, pool, ctx, "CREATE TABLE public."+drift+" (id int)")
		if _, err := rolePool.Exec(ctx, "SELECT 1 FROM public."+drift); err == nil {
			t.Errorf("role could read unclassified new table %s — default-deny not in effect", drift)
		}
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
