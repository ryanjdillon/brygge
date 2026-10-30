# Adding a Postgres migration

Numbered, up + down, tested both directions. Docs synced in the same commit so drift is visible in PR review.

## 1. Pick the next number

```bash
ls backend/migrations/*.up.sql | tail -3
```

Use the next zero-padded number. Migration filenames are `NNNNNN_short_slug.{up,down}.sql`.

## 2. Write the up + down

Idempotency is preferred (`IF NOT EXISTS`, `IF EXISTS`) where it doesn't lose information. For schema additions:

```sql
ALTER TABLE clubs
  ADD COLUMN feature_xyz BOOLEAN NOT NULL DEFAULT TRUE;
```

For data backfills, wrap in a CTE or transaction-internal block. For new tables, add the index in the same up.sql:

```sql
CREATE TABLE invoice_pdf_archive (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id  UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    -- …
);
CREATE INDEX idx_invoice_pdf_archive_invoice ON invoice_pdf_archive (invoice_id, archived_at DESC);
```

The matching down.sql is usually trivial:

```sql
DROP TABLE IF EXISTS invoice_pdf_archive;
```

But if the up.sql added a column to an existing table, the down.sql must `DROP COLUMN` rather than dropping the table.

## 3. Test the up + down locally

```bash
# Up
nix develop -c bash -c 'cd backend && go test ./internal/testutil -run TestMigrations'

# Or manually against a scratch DB:
psql -h localhost -p 5433 -U brygge_dev -d brygge_test -f backend/migrations/NNNNNN_x.up.sql
psql -h localhost -p 5433 -U brygge_dev -d brygge_test -f backend/migrations/NNNNNN_x.down.sql
```

A down that errors when run after the up is a real bug — it means the up isn't atomically reversible.

Checking against prod data needs no manual step for an ordinary migration. `just deploy` dry-runs the pending files on prod in a rolled-back transaction (see [deploy.md](../deploy.md#pending-migrations-checked-against-prod-data)). Run `just migrate-check-prod` by hand only to check early, before you deploy.

## 4. Schema overview

Update `docs/developer/database.md` Schema overview to mention the new table or column. One-line bullet under the relevant subsystem is usually enough.

## 5. Enums

If the migration adds an enum value (`ALTER TYPE ... ADD VALUE 'xyz'`), update [`../reference/enums.md`](../reference/enums.md). New enum values can't be removed via down.sql — that's a Postgres limitation. Plan accordingly.

## 6. Config touch points

If the migration adds a column that affects how operators configure the system:

- A new env-var-driven column (e.g. a new feature flag): update [`configuration.md`](../configuration.md) Feature Flags section
- A new admin-editable column: update the relevant user doc, or open a tracking issue if user docs lag
- A new audit-relevant column: usually not required, but mention in [`audit-actions.md`](../reference/audit-actions.md) if relevant

## 7. Invariants

If the new table or column introduces a rule (e.g. "this column must never be NULL once invoice is sent"), add it to [`invariants.md`](../reference/invariants.md) with the failure mode.

### Dev-query read-only role (default-deny)

The `brygge_dev_ro` role (`/admin/dev/query`, `just prod-query`) is default-deny:

- **New table** → invisible to the role until classified. If it holds no PII, add it to the allowlist (a migration following `000095`'s pattern); if it holds PII/secrets, add a `dev_masked` view instead. Record the choice in [`../security/dev-query.md`](../security/dev-query.md).
- **New text/JSON column on an allowlisted (raw) table** → the `raw text columns` subtest in `dev_masked_test.go` fails until you either add it to the reviewed `rawTextColumns` list (only if it can never name a person) or move the table to a `dev_masked` view.
- **New column on a table that already has a `dev_masked` view** → the parity test in `backend/internal/handlers/dev_masked_test.go` fails until you classify it in `backend/migrations/repeatable/dev_masked.sql` (keep it, `NULL`-mask it, or expose a derived hint) and update the matrix doc. Its **down** migration must `DROP VIEW IF EXISTS dev_masked.<table>` before dropping the column, because the view depends on it; the repeatable file rebuilds the view on the next migrate run (see `000105`).

## 8. Code wiring

Most migrations need matching Go changes:

- New enum value → switch statements in handlers, scan code, validation
- New table → query helpers, JSON serialization shape, frontend types
- New column → existing INSERT/UPDATE/SELECT statements, struct field, JSON tag

`grep -rn "tablename" backend/` after adding a table is the easy check.

## Common misses

- **Forgot the down.sql** — migration is irreversible in dev, painful to test
- **Down deletes data the up created** — fine for new tables, dangerous for backfills. If the up backfilled an existing column, the down should restore to nil/zero, not silently re-derive
- **Forgot the index** — sequential scans appear in slow query logs on first heavy use
- **Index the planner can't use** — a partial index is used only when the query's `WHERE` implies its predicate, and an index serves `ORDER BY ... DESC` only if its `NULLS FIRST/LAST` matches (`DESC` defaults to `NULLS FIRST`, so a query saying `DESC NULLS LAST` needs the index to say so). Check with `EXPLAIN` on loaded data and drop an index the plan doesn't pick
- **Enum value mismatch with Go switch statement** — silent fallthrough to default. The [DIL-376 enums doc](../reference/enums.md) is meant to surface these.
- **Forgot to update Schema overview** — the next person grepping for the table doesn't find it in docs and assumes it doesn't exist
- **Top-level `BEGIN;` / `COMMIT;`, savepoints, timeout changes, `COPY … FROM STDIN` or a psql `\` meta-command in the file.** golang-migrate already runs each file in one implicit transaction, so you don't need transaction control. With any of these, the deploy can't dry-run the file on prod and falls back to the slower snapshot restore. plpgsql `BEGIN … END` inside `$$` is fine. Run `scripts/deploy-migration-dryrun.sh --scan <file>` to see what the scan flags.
- **Side effects that survive the dry-run's `ROLLBACK`.** The deploy runs pending migrations on prod and rolls them back. Some effects stay anyway. Sequences advance and don't roll back: `nextval`, `setval`, or inserting into a `SERIAL` column. `invoices.invoice_number` is a `SERIAL`, so a migration that inserts invoices leaves a gap in prod's numbering. Work done through `dblink`, and anything written outside the DB, also stays. Deploy such a migration with `PROD_CHECK=snapshot`. These are safe: `NOTIFY`, which is only delivered on commit; advisory locks, which are released when the dry-run's session ends; and `CREATE DATABASE` / `ALTER SYSTEM`, which can't run in a transaction (25001) and so go to the snapshot fallback. Nothing like this is pending today.
