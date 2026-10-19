#!/usr/bin/env bash
# Tests for scripts/deploy-migration-dryrun.sh against the local compose DB.
#
# A scratch database (brygge_dryrun_test) stands in for prod: PROD_PSQL points
# at a wrapper that logs every invocation and runs psql in the compose db
# container. Fixture migrations live in a tmpdir; the snapshot fallback is a
# stub that records its arguments. The dev `brygge` DB is never touched.
#
# Usage (from the repo root, with `just up` running):
#   bash scripts/tests/deploy-migration-dryrun.test.sh
#   just test-deploy-dryrun
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT/scripts/deploy-migration-dryrun.sh"
DB="brygge_dryrun_test"
COMPOSE=(docker compose -f "$ROOT/deploy/docker-compose.yml" -f "$ROOT/deploy/docker-compose.dev.yml")

tmp="$(mktemp -d)"
cleanup() {
    [[ -n "${DB_CID:-}" ]] && docker exec "$DB_CID" psql -U brygge -d postgres -q \
        -c "DROP DATABASE IF EXISTS ${DB} WITH (FORCE);" >/dev/null 2>&1 || true
    rm -rf "$tmp"
}
trap cleanup EXIT

DB_CID="$("${COMPOSE[@]}" ps -q db 2>/dev/null || true)"
if [[ -z "$DB_CID" ]] || ! docker exec "$DB_CID" pg_isready -q >/dev/null 2>&1; then
    echo "compose db is not running — start it with 'just up'" >&2
    exit 1
fi
PSQL=(docker exec -i "$DB_CID" psql -U brygge)

sql() { "${PSQL[@]}" -X -q -t -A -v ON_ERROR_STOP=1 -U brygge -d "$DB" -c "$1"; }

# ── Stand-ins for prod and the snapshot check ─────────────────
cat > "$tmp/fake-psql" <<EOF
#!/usr/bin/env bash
# Logs each call, runs psql in the compose db; optionally simulates a
# concurrent migration right after a dry-run stream.
set -uo pipefail
input="\$(cat)"
{ echo "=== call: \$*"; printf '%s\n' "\$input"; } >> "$tmp/psql.log"
printf '%s\n' "\$input" | ${PSQL[*]} -d $DB "\$@"
rc=\$?
if [[ -f "$tmp/bump" && "\$input" == *"ROLLBACK;"* ]]; then
    ${PSQL[*]} -X -q -d $DB \
        -c "UPDATE schema_migrations SET version = version + 1" >/dev/null
fi
exit \$rc
EOF
chmod +x "$tmp/fake-psql"

cat > "$tmp/fake-snapshot" <<EOF
#!/usr/bin/env bash
echo "snapshot-called \$*" >> "$tmp/snapshot.log"
exit \${FAKE_SNAPSHOT_RC:-0}
EOF
chmod +x "$tmp/fake-snapshot"

export PROD_PSQL="$tmp/fake-psql"
export SNAPSHOT_CHECK_CMD="$tmp/fake-snapshot"
export SNAPSHOT_DUMP="$tmp/predeploy.dump"
unset PROD_MIGRATION_STATE SKIP_PROD_CHECK PROD_CHECK

# ── Fixture "prod" ────────────────────────────────────────────
reset_db() {
    "${PSQL[@]}" -d postgres -q -v ON_ERROR_STOP=1 \
        -c "DROP DATABASE IF EXISTS ${DB} WITH (FORCE);" \
        -c "CREATE DATABASE ${DB} OWNER brygge;" >/dev/null
    sql "
        CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL);
        INSERT INTO schema_migrations VALUES (1, false);
        CREATE TYPE mood AS ENUM ('sad');
        CREATE TABLE boats (id int PRIMARY KEY, length numeric NOT NULL);
        INSERT INTO boats VALUES (1, 5.5), (2, -1);
        CREATE SCHEMA dev_masked;
        CREATE VIEW dev_masked.boats AS SELECT id FROM public.boats;"
}

# Schema + data fingerprint: anything a leaked commit would change.
fingerprint() {
    sql "
        SELECT string_agg(x, ' ' ORDER BY x) FROM (
            SELECT 'rel:' || n.nspname || '.' || c.relname || ':' || c.relkind::text
              FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE n.nspname IN ('public', 'dev_masked')
            UNION ALL
            SELECT 'col:' || attrelid::regclass || '.' || attname FROM pg_attribute
             WHERE attrelid = 'public.boats'::regclass AND attnum > 0 AND NOT attisdropped
            UNION ALL
            SELECT 'con:' || conname FROM pg_constraint WHERE connamespace = 'public'::regnamespace
            UNION ALL
            SELECT 'enum:' || enumlabel FROM pg_enum WHERE enumtypid = 'mood'::regtype
            UNION ALL
            SELECT 'rows:' || count(*) FROM boats
            UNION ALL
            SELECT 'sm:' || version || dirty FROM schema_migrations
        ) t(x);"
}

new_migrations() {
    rm -rf "$tmp/m"
    mkdir -p "$tmp/m/repeatable"
    echo "-- already applied" > "$tmp/m/000001_base.up.sql"
    echo "-- already applied" > "$tmp/m/000001_base.down.sql"
    cat > "$tmp/m/repeatable/dev_masked.sql" <<'EOF'
DROP SCHEMA IF EXISTS dev_masked CASCADE;
CREATE SCHEMA dev_masked;
CREATE VIEW dev_masked.boats AS SELECT id FROM public.boats;
EOF
    export MIGRATIONS_DIR="$tmp/m"
    : > "$tmp/psql.log"
    rm -f "$tmp/snapshot.log" "$tmp/bump"
}

pass=0
fail=0
out=""
rc=0
run() { rc=0; out="$(cd "$ROOT" && bash "$SCRIPT" 2>&1)" || rc=$?; }
check() {
    local name="$1"; shift
    if "$@"; then
        pass=$((pass + 1)); echo "ok   - $name"
    else
        fail=$((fail + 1)); echo "FAIL - $name"
        printf '       rc=%s\n' "$rc"
        printf '%s\n' "$out" | sed 's/^/       | /'
    fi
}
has() { grep -qF -- "$1" <<< "$out"; }
logged() { grep -qF -- "$1" "$tmp/psql.log"; }
snapshot_called_with_dump() { grep -qF "snapshot-called $SNAPSHOT_DUMP" "$tmp/snapshot.log" 2>/dev/null; }

# ── Cases ─────────────────────────────────────────────────────
reset_db
before="$(fingerprint)"

# 1. Nothing pending → only dev_masked.sql is dry-run.
new_migrations
run
check "nothing pending: exits 0" test "$rc" -eq 0
check "nothing pending: dry-runs dev_masked only" \
    bash -c "grep -qF \"'>>> dev_masked.sql'\" '$tmp/psql.log' && ! grep -qF '>>> 0000' '$tmp/psql.log'"
check "nothing pending: no trace" test "$(fingerprint)" == "$before"

# 2. Good migration (incl. a plpgsql block) → passes and leaves no trace.
new_migrations
cat > "$tmp/m/000002_good.up.sql" <<'EOF'
ALTER TABLE boats ADD COLUMN IF NOT EXISTS name text;
CREATE TABLE IF NOT EXISTS harbours (id int PRIMARY KEY);
INSERT INTO harbours VALUES (1);
UPDATE boats SET length = 1 WHERE length < 0;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'boats_length_positive') THEN
        ALTER TABLE boats ADD CONSTRAINT boats_length_positive CHECK (length > 0);
    END IF;
END;
$$;
ALTER TYPE mood ADD VALUE IF NOT EXISTS 'calm';
EOF
run
check "good migration: exits 0" test "$rc" -eq 0
check "good migration: was sent" logged "'>>> 000002_good.up.sql'"
check "good migration: stream ends in ROLLBACK" logged "ROLLBACK;"
check "good migration: no trace" test "$(fingerprint)" == "$before"
check "good migration: no snapshot fallback" test ! -f "$tmp/snapshot.log"

# 3. Failing CHECK on existing rows → aborts naming the file.
new_migrations
cat > "$tmp/m/000002_ok.up.sql" <<'EOF'
ALTER TABLE boats ADD COLUMN IF NOT EXISTS name text;
EOF
cat > "$tmp/m/000003_bad_check.up.sql" <<'EOF'
-- negative lengths exist in prod
ALTER TABLE boats
    ADD CONSTRAINT boats_length_positive CHECK (length > 0);
EOF
run
check "failing CHECK: exits 1" test "$rc" -eq 1
check "failing CHECK: names the file and line" has "000003_bad_check.up.sql line 3"
check "failing CHECK: reports SQLSTATE 23514" has "SQLSTATE 23514"
check "failing CHECK: shows the statement" has "ADD CONSTRAINT boats_length_positive"
check "failing CHECK: no trace" test "$(fingerprint)" == "$before"

# 4. A file with its own COMMIT → snapshot fallback, nothing sent.
new_migrations
cat > "$tmp/m/000002_own_tx.up.sql" <<'EOF'
BEGIN;
ALTER TABLE boats ADD COLUMN name text;
COMMIT;
EOF
run
check "COMMIT file: exits 0 via fallback" test "$rc" -eq 0
check "COMMIT file: reports the line" has "000002_own_tx.up.sql:3: transaction control"
check "COMMIT file: snapshot check gets the pre-deploy dump" snapshot_called_with_dump
check "COMMIT file: no dry-run sent" bash -c "! grep -qF 'own_tx' '$tmp/psql.log' && ! grep -qF 'ROLLBACK;' '$tmp/psql.log'"
check "COMMIT file: no trace" test "$(fingerprint)" == "$before"

# 4b. A psql meta-command → fallback, nothing sent.
new_migrations
printf 'SELECT 1;\n\\! touch /tmp/x\n' > "$tmp/m/000002_meta.up.sql"
run
check "meta-command: falls back" snapshot_called_with_dump
check "meta-command: no dry-run sent" bash -c "! grep -qF 'ROLLBACK;' '$tmp/psql.log'"

# 5. New enum value used in a later file (55P04) → snapshot fallback.
new_migrations
echo "ALTER TYPE mood ADD VALUE 'happy';" > "$tmp/m/000002_enum.up.sql"
echo "UPDATE boats SET length = length WHERE 'happy'::mood IS NOT NULL;" > "$tmp/m/000003_use_enum.up.sql"
run
check "55P04: exits 0 via fallback" test "$rc" -eq 0
check "55P04: classified" has "SQLSTATE 55P04"
check "55P04: snapshot check gets the pre-deploy dump" snapshot_called_with_dump
check "55P04: no trace" test "$(fingerprint)" == "$before"

# 5b. A fallback whose snapshot check fails aborts the deploy.
new_migrations
echo "ALTER TYPE mood ADD VALUE 'happy';" > "$tmp/m/000002_enum.up.sql"
echo "SELECT 'happy'::mood;" > "$tmp/m/000003_use_enum.up.sql"
rc=0; out="$(cd "$ROOT" && FAKE_SNAPSHOT_RC=1 bash "$SCRIPT" 2>&1)" || rc=$?
check "failed snapshot check: exits 1" test "$rc" -eq 1

# 6. Lock contention (55P03) → "prod busy", distinct exit code.
new_migrations
echo "ALTER TABLE boats ADD COLUMN name text;" > "$tmp/m/000002_needs_lock.up.sql"
"${PSQL[@]}" -X -q -d "$DB" \
    -c "BEGIN; LOCK TABLE boats IN ACCESS SHARE MODE; SELECT pg_sleep(8); COMMIT;" >/dev/null 2>&1 &
locker=$!
for _ in $(seq 1 50); do
    [[ "$(sql "SELECT count(*) FROM pg_locks WHERE relation = 'boats'::regclass AND granted")" -gt 0 ]] && break
    sleep 0.2
done
run
wait "$locker" || true
check "lock timeout: exits 75" test "$rc" -eq 75
check "lock timeout: says prod busy, retry" has "PROD BUSY"
check "lock timeout: does not suggest skipping" bash -c "! grep -qF SKIP_PROD_CHECK <<< \"\$1\"" _ "$out"
check "lock timeout: no trace" test "$(fingerprint)" == "$before"

# 7. schema_migrations changes while the check runs → abort.
new_migrations
echo "ALTER TABLE boats ADD COLUMN IF NOT EXISTS name text;" > "$tmp/m/000002_good.up.sql"
touch "$tmp/bump"
run
check "tripwire: exits 2" test "$rc" -eq 2
check "tripwire: reports the change" has "changed during the check"
sql "UPDATE schema_migrations SET version = 1" >/dev/null

# 8. Dirty → refuse, nothing sent.
new_migrations
sql "UPDATE schema_migrations SET dirty = true" >/dev/null
run
check "dirty: exits 2" test "$rc" -eq 2
check "dirty: says DIRTY" has "DIRTY"
check "dirty: no dry-run sent" bash -c "! grep -qF 'ROLLBACK;' '$tmp/psql.log'"
sql "UPDATE schema_migrations SET dirty = false" >/dev/null

# 9. Prod ahead of this checkout → refuse.
new_migrations
sql "UPDATE schema_migrations SET version = 7" >/dev/null
run
check "prod ahead: exits 2" test "$rc" -eq 2
check "prod ahead: says so" has "does not have"
sql "UPDATE schema_migrations SET version = 1" >/dev/null

# 10. State handed in by the caller → no extra state read before the run.
new_migrations
rc=0; out="$(cd "$ROOT" && PROD_MIGRATION_STATE="1|false" bash "$SCRIPT" 2>&1)" || rc=$?
check "folded state: exits 0" test "$rc" -eq 0
check "folded state: one state read (the tripwire) + one dry-run" \
    test "$(grep -c '^=== call' "$tmp/psql.log")" -eq 2

# 11. Switches.
new_migrations
rc=0; out="$(cd "$ROOT" && SKIP_PROD_CHECK=1 bash "$SCRIPT" 2>&1)" || rc=$?
check "SKIP_PROD_CHECK=1: exits 0 with a warning" bash -c "[[ $rc -eq 0 ]] && grep -qF WARNING <<< \"\$1\"" _ "$out"
check "SKIP_PROD_CHECK=1: nothing sent" test ! -s "$tmp/psql.log"
new_migrations
echo "ALTER TABLE boats ADD COLUMN name text;" > "$tmp/m/000002_good.up.sql"
rc=0; out="$(cd "$ROOT" && PROD_CHECK=snapshot bash "$SCRIPT" 2>&1)" || rc=$?
check "PROD_CHECK=snapshot: runs the snapshot check" snapshot_called_with_dump
check "PROD_CHECK=snapshot: no dry-run sent" bash -c "! grep -qF 'ROLLBACK;' '$tmp/psql.log'"

# 12. Fresh host (no schema_migrations) → nothing to check.
new_migrations
sql "DROP TABLE schema_migrations" >/dev/null
run
check "fresh host: exits 0" test "$rc" -eq 0
check "fresh host: says nothing to check" has "nothing to check"

# 13. The real migrations: only files that manage their own transaction are flagged.
rc=0; out="$(cd "$ROOT" && bash "$SCRIPT" --scan backend/migrations/*.up.sql backend/migrations/repeatable/dev_masked.sql 2>&1)" || rc=$?
check "scan of real migrations: only 000001 and 000006" \
    test "$(grep -oE '^[^:]+' <<< "$out" | sort -u | xargs -n1 basename | tr '\n' ' ')" == "000001_init.up.sql 000006_accounting.up.sql "

echo
echo "${pass} passed, ${fail} failed"
(( fail == 0 ))
