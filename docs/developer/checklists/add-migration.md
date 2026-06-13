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

## 4. Schema overview

Update `docs/developer/database.md` Schema overview to mention the new table or column. One-line bullet under the relevant subsystem is usually enough.

## 5. Enums

If the migration adds an enum value (`ALTER TYPE ... ADD VALUE 'xyz'`), update [`../enums.md`](../enums.md). New enum values can't be removed via down.sql — that's a Postgres limitation. Plan accordingly.

## 6. Config touch points

If the migration adds a column that affects how operators configure the system:

- A new env-var-driven column (e.g. a new feature flag): update [`configuration.md`](../configuration.md) Feature Flags section
- A new admin-editable column: update the relevant user doc, or open a tracking issue if user docs lag
- A new audit-relevant column: usually not required, but mention in [`audit-actions.md`](../audit-actions.md) if relevant

## 7. Invariants

If the new table or column introduces a rule (e.g. "this column must never be NULL once invoice is sent"), add it to [`invariants.md`](../invariants.md) with the failure mode.

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
- **Enum value mismatch with Go switch statement** — silent fallthrough to default. The [DIL-376 enums doc](../enums.md) is meant to surface these.
- **Forgot to update Schema overview** — the next person grepping for the table doesn't find it in docs and assumes it doesn't exist
