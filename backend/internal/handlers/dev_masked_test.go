package handlers

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	"payment_refunds", "payment_webhook_events", "stripe_balance_transactions",
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
	// exemption config: board-role keys and price categories
	"price_exemptions.category": true, "price_exemptions.role": true,
	// provider name, opaque provider reference, status and currency codes
	"payment_attempts.currency": true, "payment_attempts.provider": true, "payment_attempts.provider_reference": true, "payment_attempts.status": true,
	// Stripe payout ids, status and currency codes, and the payout's
	// statement descriptor, which is the club's own text
	"stripe_payouts.currency": true, "stripe_payouts.payout_id": true, "stripe_payouts.statement_descriptor": true, "stripe_payouts.status": true,
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

	// The migrations grant to brygge_dev_ro only when the role already exists,
	// as on a deployed host, where it is created before them. Create it here
	// too, so the grant drift guards below see what production sees.
	if _, err := admin.Exec(ctx, `DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
				CREATE ROLE brygge_dev_ro NOLOGIN;
			END IF;
		EXCEPTION WHEN duplicate_object THEN NULL;
		END $$`); err != nil {
		t.Skipf("cannot create brygge_dev_ro (need CREATEROLE): %v", err)
	}

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
		if err := rows.Err(); err != nil {
			t.Fatalf("listing raw text columns: %v", err)
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

	t.Run("keyed name tokens", func(t *testing.T) {
		// Synthetic fixtures only.
		const (
			club   = "22222222-2222-2222-2222-000000000001"
			kari   = "11111111-0000-0000-0000-000000000001"
			ola    = "11111111-0000-0000-0000-000000000002"
			anne   = "11111111-0000-0000-0000-000000000003"
			bankIm = "33333333-0000-0000-0000-000000000001"
			vipIm  = "55555555-0000-0000-0000-000000000001"
		)
		mustExec(t, pool, ctx, "INSERT INTO clubs (id, slug, name) VALUES ($1, 'tok', 'Synthetic Tok Club')", club)
		mustExec(t, pool, ctx, `INSERT INTO users (id, club_id, email, first_name, last_name, address_line, postal_code) VALUES
			($1, $4, 'k@example.test', 'Kari', 'Nordmann', 'Strandvegen 5', '5000'),
			($2, $4, 'o@example.test', 'Ola', 'Nordmann', 'Strandvegen 5', '5000'),
			($3, $4, 'a@example.test', 'Anne-Lise', 'Bø', 'Fjellvegen 2', '5001')`, kari, ola, anne, club)
		mustExec(t, pool, ctx, "INSERT INTO bank_imports (id, club_id, filename, imported_by) VALUES ($1, $2, 'synthetic.csv', $3)", bankIm, club, kari)
		bankRows := []struct{ id, desc, counterpart string }{
			{"44444444-0000-0000-0000-000000000001", "Fra: Kari Nordmann Betalt: 01.09.26", ""},
			{"44444444-0000-0000-0000-000000000002", "Overføring", "NORDMANN KARI"},
			{"44444444-0000-0000-0000-000000000003", "Fra: Kari Anne Nordmann Betalt: 03.09.26", ""},
			{"44444444-0000-0000-0000-000000000004", "Overføring", "ANNE LISE BO"},
			{"44444444-0000-0000-0000-000000000005", "Intern", "Synthetic Tok Club"},
		}
		for i, r := range bankRows {
			mustExec(t, pool, ctx, `INSERT INTO bank_import_rows (id, club_id, bank_import_id, row_date, description, counterpart, amount, row_hash)
				VALUES ($1, $2, $3, '2026-09-01', $4, $5, 450, $6)`, r.id, club, bankIm, r.desc, r.counterpart, fmt.Sprintf("tokh%d", i))
		}
		mustExec(t, pool, ctx, "INSERT INTO vipps_imports (id, club_id, filename) VALUES ($1, $2, 'v.csv')", vipIm, club)
		mustExec(t, pool, ctx, `INSERT INTO vipps_import_rows (id, club_id, vipps_import_id, row_type, row_hash, customer_name, amount)
			VALUES ('66666666-0000-0000-0000-000000000001', $1, $2, 'belastning', 'tokv1', 'Kari Nordmann', 450)`, club, vipIm)
		mustExec(t, pool, ctx, `INSERT INTO invoices (id, club_id, user_id, total_amount, due_date, kid_number, member_name)
			VALUES ('77777777-0000-0000-0000-000000000001', $1, $2, 450, '2026-09-30', '000000990017', 'Kari Nordmann')`, club, kari)

		rolePool, err := pgxpool.New(ctx, devRoleURL(t, dbURL))
		if err != nil {
			t.Skipf("brygge_dev_ro pool unavailable: %v", err)
		}
		defer rolePool.Close()
		if err := rolePool.Ping(ctx); err != nil {
			t.Skipf("brygge_dev_ro not reachable (run `just dev-role-bootstrap`): %v", err)
		}

		// The key is unreachable: no schema USAGE, no table SELECT.
		var usage bool
		if err := rolePool.QueryRow(ctx, "SELECT has_schema_privilege('dev_private', 'USAGE')").Scan(&usage); err != nil || usage {
			t.Errorf("role has USAGE on dev_private (usage=%v err=%v)", usage, err)
		}
		if _, err := rolePool.Exec(ctx, "SELECT key FROM dev_private.token_key"); err == nil {
			t.Error("role could read the token key")
		}
		// No callable function may reach the key — it would be an oracle.
		var oracles int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc
			WHERE prosrc ILIKE '%dev_private%' AND has_function_privilege('brygge_dev_ro', oid, 'EXECUTE')`).Scan(&oracles); err != nil || oracles != 0 {
			t.Errorf("functions reaching dev_private executable by the role: %d (err %v)", oracles, err)
		}

		// Payer ↔ member matching across sources, as the role.
		matched := func(bankRow string) []string {
			var ids []string
			rows, err := rolePool.Query(ctx, `SELECT u.user_id::text FROM bank_row_payer_tokens b
				JOIN user_name_tokens u ON b.payer_pair_toks && u.name_pair_toks
				WHERE b.bank_import_row_id = $1 ORDER BY 1`, bankRow)
			if err != nil {
				t.Fatalf("match query: %v", err)
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				_ = rows.Scan(&id)
				ids = append(ids, id)
			}
			return ids
		}
		want := map[string][]string{
			bankRows[0].id: {kari}, // "Fra: Kari Nordmann"
			bankRows[1].id: {kari}, // reversed word order, upper case
			bankRows[2].id: {kari}, // extra middle name
			bankRows[3].id: {anne}, // hyphen and ø folded
			bankRows[4].id: nil,    // the club's own name is never a payer
		}
		for row, w := range want {
			if got := matched(row); fmt.Sprint(got) != fmt.Sprint(w) {
				t.Errorf("bank row %s matched %v, want %v", row, got, w)
			}
		}
		var vippsOK, invoiceOK, household, olaLinked bool
		if err := rolePool.QueryRow(ctx, `SELECT
				(SELECT v.customer_pair_toks && u.name_pair_toks FROM vipps_row_customer_tokens v),
				(SELECT i.member_pair_toks && u.name_pair_toks FROM invoice_name_tokens i),
				(SELECT o.household_tok = u.household_tok FROM user_name_tokens o WHERE o.user_id = $2),
				(SELECT o.name_pair_toks && u.name_pair_toks FROM user_name_tokens o WHERE o.user_id = $2)
			FROM user_name_tokens u WHERE u.user_id = $1`, kari, ola).Scan(&vippsOK, &invoiceOK, &household, &olaLinked); err != nil {
			t.Fatalf("cross-source query: %v", err)
		}
		if !vippsOK || !invoiceOK {
			t.Errorf("Vipps match %v, invoice snapshot match %v; want both true", vippsOK, invoiceOK)
		}
		if !household {
			t.Error("members at the same address should share household_tok")
		}
		if olaLinked {
			t.Error("a shared surname alone must not link two people")
		}

		// EXPLAIN must never print key bytes (an IMMUTABLE accessor would).
		var keyHex string
		if err := pool.QueryRow(ctx, "SELECT encode(key, 'hex') FROM dev_private.token_key").Scan(&keyHex); err != nil {
			t.Fatalf("read key as owner: %v", err)
		}
		for _, q := range []string{
			"EXPLAIN (VERBOSE, COSTS OFF) SELECT * FROM user_name_tokens",
			"EXPLAIN (ANALYZE, VERBOSE) SELECT * FROM bank_row_payer_tokens",
		} {
			rows, err := rolePool.Query(ctx, q)
			if err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			for rows.Next() {
				var line string
				_ = rows.Scan(&line)
				if strings.Contains(line, keyHex[:16]) {
					t.Fatalf("%s printed key bytes", q)
				}
			}
			rows.Close()
		}

		// Tokens are stable across re-applying the views, and change on rotation.
		tokens := func() string {
			var s string
			if err := pool.QueryRow(ctx, "SELECT name_pair_toks::text FROM dev_masked.user_name_tokens WHERE user_id = $1", kari).Scan(&s); err != nil {
				t.Fatalf("read tokens: %v", err)
			}
			return s
		}
		before := tokens()
		if _, err := pool.Exec(ctx, string(masked)); err != nil {
			t.Fatalf("re-apply dev_masked.sql: %v", err)
		}
		if after := tokens(); after != before {
			t.Errorf("tokens changed on re-applying the views: %s → %s", before, after)
		}
		mustExec(t, pool, ctx, "UPDATE dev_private.token_key SET key = public.gen_random_bytes(32), rotated_at = now()")
		if rotated := tokens(); rotated == before {
			t.Error("tokens did not change after key rotation")
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
