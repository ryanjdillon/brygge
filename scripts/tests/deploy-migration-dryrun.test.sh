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
if [[ -n "\${FAKE_SSH_FAIL:-}" ]]; then
    echo "ssh: connect to host prod.example.invalid port 22: Connection refused" >&2
    exit 255
fi
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
        CREATE TABLE contacts (email text, ref text);
        INSERT INTO contacts VALUES
            ('synthetic.dup@example.invalid', 'not-a-uuid-SYNTHETIC-SECRET'),
            ('synthetic.dup@example.invalid', NULL);
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

# 11b. The stream opens and closes the transaction and guards each file.
new_migrations
echo "SELECT 1;" > "$tmp/m/000002_good.up.sql"
rc=0; out="$(cd "$ROOT" && DRYRUN_STREAM_COPY="$tmp/stream.sql" bash "$SCRIPT" 2>&1)" || rc=$?
check "stream: exits 0" test "$rc" -eq 0
check "stream: header is BEGIN, SET LOCAL timeouts, SAVEPOINT" test "$(head -5 "$tmp/stream.sql")" == \
"BEGIN;
SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';
SET LOCAL idle_in_transaction_session_timeout = '30s';
SAVEPOINT dryrun;"
check "stream: each file is wrapped in SAVEPOINT/RELEASE" test "$(sed -n 6,10p "$tmp/stream.sql")" == \
"\\echo '>>> 000002_good.up.sql'
SAVEPOINT dryrun_file;
SELECT 1;
RELEASE SAVEPOINT dryrun_file;
\\echo '>>> dev_masked.sql'"
check "stream: ends ROLLBACK TO SAVEPOINT, ROLLBACK" test "$(tail -2 "$tmp/stream.sql")" == \
"ROLLBACK TO SAVEPOINT dryrun;
ROLLBACK;"

# 11c. A COMMIT the scan missed (scan disabled) is caught by the guards:
# hard abort, exit 2, and the warning is true — the stand-in prod changed.
new_migrations
echo "ALTER TABLE boats ADD COLUMN leaked text; COMMIT;" > "$tmp/m/000002_sneaky.up.sql"
echo "ALTER TABLE boats ADD COLUMN after_leak text;" > "$tmp/m/000003_next.up.sql"
rc=0; out="$(cd "$ROOT" && DRYRUN_TEST_SKIP_SCAN=1 bash "$SCRIPT" 2>&1)" || rc=$?
check "inline COMMIT: exits 2" test "$rc" -eq 2
check "inline COMMIT: says prod may have been modified" has "PROD MAY HAVE BEEN MODIFIED"
check "inline COMMIT: names the file" has "(${tmp}/m/000002_sneaky.up.sql)"
check "inline COMMIT: halts before the next file" \
    test "$(sql "SELECT count(*) FROM pg_attribute WHERE attrelid = 'boats'::regclass AND attname = 'after_leak'")" -eq 0
check "inline COMMIT: no snapshot fallback" test ! -f "$tmp/snapshot.log"
check "inline COMMIT: the leak is real (stand-in prod changed)" test "$(fingerprint)" != "$before"
reset_db

new_migrations
echo "SELECT 1; COMMIT; BEGIN;" > "$tmp/m/000002_reopen.up.sql"
rc=0; out="$(cd "$ROOT" && DRYRUN_TEST_SKIP_SCAN=1 bash "$SCRIPT" 2>&1)" || rc=$?
check "COMMIT; BEGIN: exits 2 (3B001)" test "$rc" -eq 2
check "COMMIT; BEGIN: says prod may have been modified" has "PROD MAY HAVE BEEN MODIFIED"

# 11d. Error output never shows row values (synthetic data).
new_migrations
echo "CREATE UNIQUE INDEX contacts_email ON contacts (email);" > "$tmp/m/000002_unique.up.sql"
run
check "redaction, unique violation: exits 1" test "$rc" -eq 1
check "redaction, unique violation: reports 23505" has "SQLSTATE 23505"
check "redaction, unique violation: value not shown" bash -c "! grep -qF 'synthetic.dup' <<< \"\$1\"" _ "$out"
new_migrations
echo "ALTER TABLE contacts ALTER COLUMN ref TYPE uuid USING ref::uuid;" > "$tmp/m/000002_uuid.up.sql"
run
check "redaction, uuid cast: exits 1" test "$rc" -eq 1
check "redaction, uuid cast: value redacted" has 'invalid input syntax for type uuid: "[redacted]"'
check "redaction, uuid cast: value not shown" bash -c "! grep -qF 'SYNTHETIC-SECRET' <<< \"\$1\"" _ "$out"
check "redaction: no trace" test "$(fingerprint)" == "$before"

# 11e. ssh / connection failure → "could not reach prod", exit 1.
new_migrations
rc=0; out="$(cd "$ROOT" && FAKE_SSH_FAIL=1 PROD_MIGRATION_STATE="1|false" bash "$SCRIPT" 2>&1)" || rc=$?
check "ssh failure during dry-run: exits 1" test "$rc" -eq 1
check "ssh failure during dry-run: says could not reach prod" has "could not reach prod"
check "ssh failure during dry-run: not reported as a migration failure" \
    bash -c "! grep -qF 'migration fails' <<< \"\$1\"" _ "$out"
rc=0; out="$(cd "$ROOT" && FAKE_SSH_FAIL=1 bash "$SCRIPT" 2>&1)" || rc=$?
check "ssh failure on state read: exits 1" test "$rc" -eq 1
check "ssh failure on state read: shows the ssh error" has "Connection refused"

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

# 14. Scanner fixtures: statement-level, not line-level.
mkdir -p "$tmp/scan"
scan_fixture() {  # name, expected output ("" = clean); file content on stdin
    local name="$1" want="$2" got
    cat > "$tmp/scan/$name"
    got="$(cd "$ROOT" && bash "$SCRIPT" --scan "$tmp/scan/$name" 2>&1 || true)"
    got="${got//$tmp\/scan\//}"
    rc=0; out="$got"
    check "scan: $name" test "$got" == "$want"
}
echo "SELECT 1; COMMIT;" | scan_fixture same-line "same-line:1: transaction control"
printf 'SELECT 1;\nCOMMIT\n;\n' | scan_fixture semicolon-next-line "semicolon-next-line:2: transaction control"
printf 'COMMIT -- end\n;\n' | scan_fixture trailing-comment "trailing-comment:1: transaction control"
echo "/* x */ COMMIT;" | scan_fixture block-comment-first "block-comment-first:1: transaction control"
printf 'DO $$\nBEGIN\n    PERFORM 1;\nEND $$; COMMIT;\n' | scan_fixture after-dollar-body "after-dollar-body:4: transaction control"
printf 'SELECT 1; COMMIT' | scan_fixture no-trailing-newline "no-trailing-newline:1: transaction control"
printf "SELECT 'cost \$\$';\nCOMMIT;\n" | scan_fixture dollars-in-string "dollars-in-string:2: transaction control"
printf 'ROLLBACK;\n' | scan_fixture rollback "rollback:1: transaction control"
printf 'START TRANSACTION;\n' | scan_fixture start "start:1: transaction control"
printf 'PREPARE TRANSACTION %s;\n' "'x'" | scan_fixture prepare-tx "prepare-tx:1: transaction control"
printf 'SET statement_timeout = 0;\nSET LOCAL lock_timeout TO %s;\nRESET ALL;\nSET SESSION idle_in_transaction_session_timeout = 0;\n' "'1s'" \
    | scan_fixture timeouts "timeouts:1: changes the dry-run's timeouts
timeouts:2: changes the dry-run's timeouts
timeouts:3: changes the dry-run's timeouts
timeouts:4: changes the dry-run's timeouts"
printf 'SELECT 1 \\gexec\n' | scan_fixture meta "meta:1: psql meta-command"
# Must NOT be flagged:
# shellcheck disable=SC2016 # $a1$ is a literal dollar-quote tag
printf 'DO $a1$\nBEGIN\n    COMMIT;\nEND\n$a1$;\nSELECT $a1$ ; COMMIT; $a1$;\n' | scan_fixture dollar-tag-digits ""
printf '/*\n COMMIT;\n /* nested COMMIT; */ still a comment; COMMIT;\n*/\nSELECT 1;\n-- COMMIT;\n' | scan_fixture comments ""
printf "SELECT E'it\\\\'s; COMMIT;';\nSELECT 'a''b; COMMIT;';\n" | scan_fixture escaped-strings ""
printf 'SELECT 1 AS "x;COMMIT";\n' | scan_fixture quoted-identifier ""
printf 'SAVEPOINT s;\nROLLBACK TO SAVEPOINT s;\nROLLBACK WORK TO s;\nRELEASE SAVEPOINT s;\nSET search_path = public;\nPREPARE q AS SELECT 1;\n' \
    | scan_fixture allowed ""
printf "SELECT '\\\\d+';\nSELECT regexp_replace(x, '\\\\s', '');\n" | scan_fixture backslash-in-string ""
printf 'UPDATE t SET v = CASE WHEN a THEN 1\nELSE 2\nEND;\n' | scan_fixture case-end ""

echo
echo "${pass} passed, ${fail} failed"
(( fail == 0 ))
