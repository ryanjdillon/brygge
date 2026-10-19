#!/usr/bin/env bash
# deploy-migration-dryrun — check pending migrations against production data
# before a deploy, inside a transaction that always rolls back.
#
# Called by the flake deploy app (`nix run .#deploy`, `just deploy`) after the
# pre-deploy snapshot. Flow:
#
#   1. Read prod's schema_migrations (version, dirty) and the canary. Refuse
#      if dirty, or if this checkout has no file for prod's version (prod is
#      ahead of it). No schema_migrations at all (fresh host) → nothing to
#      check.
#   2. Pending = numbered *.up.sql above prod's version.
#   3. Lex the pending files and repeatable/dev_masked.sql locally, statement
#      by statement, failing closed: transaction control, savepoints, the
#      dry-run's timeouts, COPY FROM STDIN, psql meta-commands, unterminated
#      comments/strings/bodies, and statements that don't start with a
#      keyword. A hit means nothing is sent: fall back to the snapshot check.
#   4. Otherwise stream BEGIN; SET LOCAL timeouts; SAVEPOINT; the canary
#      table; each file between SAVEPOINT/RELEASE guards; dev_masked.sql;
#      ROLLBACK TO SAVEPOINT; ROLLBACK; to psql as the brygge role. Nothing
#      pending → only dev_masked.sql is dry-run.
#   5. Re-read prod. Canary present → PROD WAS MODIFIED, exit 2. A file
#      that ended the transaction (25P01/3B001) → hard abort, exit 2.
#   6. Abort if schema_migrations changed while we ran.
#   7. Classify a failure: ssh/connection failure → error; 55P04 / 25001 →
#      snapshot fallback; 55P03 → prod busy; anything else → the migration
#      fails. Row values in error output are redacted.
#
# The dry-run is not a perfect model of golang-migrate: that runs each file
# in its own implicit transaction, this runs them all in one. The SQLSTATEs
# routed to the snapshot fallback are the ones that difference produces.
#
# Usage:
#   scripts/deploy-migration-dryrun.sh            # run the check
#   scripts/deploy-migration-dryrun.sh --state-sql  # print the state query
#   scripts/deploy-migration-dryrun.sh --scan FILE…  # list statements step 3 rejects
#
# Env:
#   PROD_PSQL            command (shell words) that runs psql against prod as
#                        the brygge role; only simple flags are appended and
#                        all SQL goes over stdin. Required unless skipped.
#   PROD_MIGRATION_STATE "version|dirty|canary" or "none|canary" (the output
#                        of --state-sql), already read by the caller (folds
#                        the read into an earlier ssh). Read via PROD_PSQL
#                        when unset or empty.
#   SNAPSHOT_DUMP        local pg_dump -Fc of prod for the snapshot fallback;
#                        without it migrate-check-prod.sh fetches from S3.
#   SKIP_PROD_CHECK=1    skip everything, loudly.
#   PROD_CHECK=snapshot  skip the dry-run and run the snapshot check.
#   MIGRATIONS_DIR       default backend/migrations
#   DEV_MASKED_SQL       default $MIGRATIONS_DIR/repeatable/dev_masked.sql
#   SNAPSHOT_CHECK_CMD   override the snapshot check (tests); gets the dump
#                        path as its only argument, or none.
#   DRYRUN_TIMEOUT       whole-run timeout in seconds, default 90, minimum 30
#   DRYRUN_STREAM_COPY   tests: save a copy of the SQL stream here
#   DRYRUN_TEST_SKIP_SCAN=1  tests only: disable step 3 to exercise the
#                        in-stream guards; never against a real host
#
# Exit codes: 0 ok · 1 migration failed / check error / unreachable ·
# 2 refused or hard stop (dirty, prod ahead, state changed, canary found,
# a file ended the transaction) · 75 prod busy (lock timeout), retry later.
set -euo pipefail

EXIT_FAIL=1
EXIT_REFUSE=2
EXIT_BUSY=75

MIGRATIONS_DIR="${MIGRATIONS_DIR:-backend/migrations}"
DEV_MASKED_SQL="${DEV_MASKED_SQL:-${MIGRATIONS_DIR}/repeatable/dev_masked.sql}"
DRYRUN_TIMEOUT="${DRYRUN_TIMEOUT:-90}"
# The caller runs psql on the host under DRYRUN_TIMEOUT - 5; keep that sane.
if [[ ! "$DRYRUN_TIMEOUT" =~ ^[0-9]+$ ]] || (( DRYRUN_TIMEOUT < 30 )); then
    DRYRUN_TIMEOUT=30
fi
COMPOSE=(docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml)

# The canary is created first inside the dry-run's transaction. It can only
# exist afterwards if something committed that transaction, so its presence
# is exact proof that prod was modified, whatever the exit code said.
CANARY="public._deploy_dryrun_canary"

# One row, one line: "version|dirty|canary", or "none|canary" when
# golang-migrate has never run. query_to_xml defers parsing the inner query
# to execution time, so the statement is valid even where schema_migrations
# does not exist.
STATE_SQL="SELECT CASE WHEN to_regclass('public.schema_migrations') IS NULL THEN 'none' ELSE coalesce((xpath('/table/row/s/text()', query_to_xml('SELECT version || ''|'' || dirty AS s FROM schema_migrations', false, false, '')))[1]::text, 'none') END || '|' || (to_regclass('${CANARY}') IS NOT NULL)::text;"
STATE_RE='^([0-9]+\|(true|false)|none)\|(true|false)$'

say() { echo "deploy: $*"; }
err() { echo "deploy: $*" >&2; }

# stdout only: ssh may print banners or host-key warnings on stderr.
read_state() {
    local out errf
    errf="$(mktemp)"
    if ! out="$(printf '%s\n' "$STATE_SQL" \
            | timeout 30 bash -c "${PROD_PSQL} -X -q -t -A -v ON_ERROR_STOP=1 -f -" 2>"$errf")"; then
        err "ERROR — could not read schema_migrations on prod:"
        cat "$errf" >&2
        rm -f "$errf"
        return 1
    fi
    rm -f "$errf"
    out="${out//[[:space:]]/}"
    if [[ ! "$out" =~ $STATE_RE ]]; then
        err "ERROR — unexpected schema_migrations state from prod: '${out}'"
        return 1
    fi
    printf '%s' "$out"
}

# A canary on prod means a dry-run's transaction was committed. Report it,
# then remove it so the next run starts clean.
canary_found() {
    err "ABORT — PROD WAS MODIFIED${1:+ ($1)}: the dry-run's canary table ${CANARY} exists,"
    err "so a dry-run transaction was committed and its statements are live on prod."
    err "Restore the pre-deploy snapshot before anything else (docs/developer/deploy.md,"
    err "'Pre-deploy snapshot and restoring from it')."
    if printf '%s\n' "DROP TABLE IF EXISTS ${CANARY};" \
            | timeout 30 bash -c "${PROD_PSQL} -X -q -v ON_ERROR_STOP=1 -f -" >/dev/null 2>&1; then
        err "(dropped ${CANARY})"
    else
        err "WARNING — could not drop ${CANARY}; drop it by hand after restoring."
    fi
    exit "$EXIT_REFUSE"
}

snapshot_check() {
    local dump="${SNAPSHOT_DUMP:-}"
    local args=()
    [[ -n "$dump" ]] && args=("$dump")
    if [[ -n "${SNAPSHOT_CHECK_CMD:-}" ]]; then
        bash -c "${SNAPSHOT_CHECK_CMD} \"\$@\"" snapshot-check "${args[@]}"
        return
    fi
    if ! "${COMPOSE[@]}" exec -T db pg_isready -q >/dev/null 2>&1; then
        err "ERROR — the snapshot check restores prod into the local compose DB, which is not running."
        err "Start it with 'just up' and re-run the deploy."
        return "$EXIT_FAIL"
    fi
    if [[ -z "$dump" ]]; then
        say "no local pre-deploy dump; migrate-check-prod.sh will fetch the newest S3 snapshot"
    fi
    nix develop --command bash scripts/migrate-check-prod.sh "${args[@]}"
}

run_snapshot_fallback() {
    say "falling back to the snapshot check: $1"
    if snapshot_check; then
        say "snapshot check passed"
        return 0
    fi
    err "ERROR — snapshot check failed; aborting the deploy"
    return "$EXIT_FAIL"
}

# A lexer over whole files, not lines: it drops -- and nested /* */
# comments, '…' (with ''), E'…' (with backslash escapes), "identifiers" and
# $tag$…$tag$ bodies, then splits what is left on ';'. It prints
# "file:line: reason" (line = where the statement starts) for every
# statement that could end the dry-run's transaction or loosen its
# timeouts, and for a backslash outside a string (a psql meta-command).
SCAN_AWK=$(cat <<'AWK'
function ident(ch) { return ch ~ /[A-Za-z0-9_$]/ }
function lastch() { return substr(stmt, length(stmt), 1) }
function start_here() { if (sline == 0) sline = FNR }
function report(line, why) { print fname ":" line ": " why }
function is_timeout(name) {
    return name == "STATEMENT_TIMEOUT" || name == "LOCK_TIMEOUT" ||
           name == "IDLE_IN_TRANSACTION_SESSION_TIMEOUT"
}
function mentions_timeout(text) {
    return text ~ /statement_timeout|lock_timeout|idle_in_transaction_session_timeout/
}
# Fails closed: anything it can't positively read as an ordinary statement
# is flagged, since a false positive only costs the slower snapshot check.
function flush(   s, up, n, w, k1, k2, k3, name) {
    s = stmt
    sub(/^[ \t\r\n]+/, "", s)
    if (s != "") {
        up = toupper(s)
        n = split(up, w, /[ \t\r\n(=]+/)
        k1 = w[1]; k2 = (n >= 2) ? w[2] : ""; k3 = (n >= 3) ? w[3] : ""
        name = (k2 == "SESSION" || k2 == "LOCAL") ? k3 : k2
        if (k1 !~ /^[A-Z_][A-Z0-9_]*$/) {
            report(sline, "statement does not start with a keyword")
        } else if (k1 == "BEGIN" || k1 == "START" || k1 == "COMMIT" || k1 == "END" ||
                   k1 == "ABORT" || k1 == "ROLLBACK" || k1 == "SAVEPOINT" || k1 == "RELEASE" ||
                   (k1 == "PREPARE" && k2 == "TRANSACTION")) {
            # SAVEPOINT / RELEASE / ROLLBACK TO can't commit, but they would
            # trip the dry-run's own savepoint guards; no migration needs them.
            report(sline, "transaction control")
        } else if ((k1 == "SET" && is_timeout(name)) ||
                   (k1 == "RESET" && (is_timeout(k2) || k2 == "ALL")) ||
                   mentions_timeout(lits)) {
            report(sline, "changes the dry-run's timeouts")
        } else if (k1 == "COPY" && up ~ /FROM[ \t\r\n]+STDIN/) {
            report(sline, "COPY FROM STDIN would read the rest of the stream as data")
        }
    }
    stmt = ""; sline = 0; lits = ""
}
function endfile() {
    if (fname != "") {
        if (st == "block") report(tokline, "unterminated /* comment at end of file")
        else if (st == "sq") report(tokline, "unterminated string at end of file")
        else if (st == "id") report(tokline, "unterminated quoted identifier at end of file")
        else if (st == "dq") report(tokline, "unterminated " tag " body at end of file")
        flush()
    }
    st = "code"; depth = 0; tag = ""; esc = 0; stmt = ""; sline = 0; lits = ""; sbuf = ""
}
FNR == 1 { endfile(); fname = FILENAME }
{
    line = $0 "\n"
    L = length(line)
    i = 1
    while (i <= L) {
        c = substr(line, i, 1)
        two = substr(line, i, 2)
        if (st == "line") {
            if (c == "\n") { st = "code"; stmt = stmt c }
            i++; continue
        }
        if (st == "block") {
            if (two == "/*") { depth++; i += 2; continue }
            if (two == "*/") { depth--; i += 2; if (depth == 0) { st = "code"; stmt = stmt " " }; continue }
            i++; continue
        }
        # String and body contents go to lits (checked for timeout names);
        # a quoted identifier's text stays in stmt so SET "lock_timeout"
        # reads like SET lock_timeout.
        if (st == "sq") {
            if (esc && c == "\\") { sbuf = sbuf substr(line, i, 2); i += 2; continue }
            if (c == "'") {
                if (substr(line, i + 1, 1) == "'") { sbuf = sbuf "'"; i += 2; continue }
                st = "code"; stmt = stmt "''"; lits = lits " " tolower(sbuf); sbuf = ""
                i++; continue
            }
            sbuf = sbuf c
            i++; continue
        }
        if (st == "id") {
            if (c == "\"") {
                if (substr(line, i + 1, 1) == "\"") { stmt = stmt "\""; i += 2; continue }
                st = "code"
                i++; continue
            }
            stmt = stmt c
            i++; continue
        }
        if (st == "dq") {
            if (substr(line, i, length(tag)) == tag) { st = "code"; stmt = stmt "$$"; i += length(tag); continue }
            lits = lits tolower(c)
            i++; continue
        }
        # st == "code"
        if (two == "--") { st = "line"; i += 2; continue }
        if (two == "/*") { st = "block"; tokline = FNR; depth = 1; i += 2; continue }
        if (c == "'") {
            p = lastch()
            esc = (p == "E" || p == "e") && !ident(substr(stmt, length(stmt) - 1, 1))
            start_here(); tokline = FNR; st = "sq"; sbuf = ""; i++; continue
        }
        if (c == "\"") { start_here(); tokline = FNR; st = "id"; i++; continue }
        if (c == "$" && !ident(lastch()) && match(substr(line, i), /^\$([A-Za-z_][A-Za-z0-9_]*)?\$/)) {
            tag = substr(line, i, RLENGTH)
            start_here(); tokline = FNR; st = "dq"; stmt = stmt "$$"; i += RLENGTH; continue
        }
        if (c == "\\") { report(FNR, "psql meta-command"); i++; continue }
        if (c == ";") { flush(); i++; continue }
        if (c !~ /[ \t\r\n]/) start_here()
        stmt = stmt c
        i++
    }
}
END { endfile() }
AWK
)

scan_unsafe() {
    awk "$SCAN_AWK" "$@"
}

case "${1:-}" in
    --state-sql)
        printf '%s\n' "$STATE_SQL"
        exit 0
        ;;
    --scan)
        shift
        hits="$(scan_unsafe "$@")"
        [[ -z "$hits" ]] && exit 0
        printf '%s\n' "$hits"
        exit "$EXIT_FAIL"
        ;;
esac

if [[ "${SKIP_PROD_CHECK:-0}" == "1" ]]; then
    err "WARNING — SKIP_PROD_CHECK=1: pending migrations are NOT checked against prod data"
    exit 0
fi

: "${PROD_PSQL:?set PROD_PSQL to the command that runs psql against prod}"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

# ── 1. Prod state ──────────────────────────────────────────────
state="${PROD_MIGRATION_STATE:-}"
state="${state//[[:space:]]/}"
if [[ -z "$state" ]]; then
    state="$(read_state)" || exit "$EXIT_FAIL"
elif [[ ! "$state" =~ $STATE_RE ]]; then
    err "ERROR — unexpected PROD_MIGRATION_STATE '${state}'"
    exit "$EXIT_FAIL"
fi
if [[ "${state##*|}" == "true" ]]; then
    canary_found "left by an earlier dry-run"
fi
state="${state%|*}"

if [[ "$state" == "none" ]]; then
    say "prod has no schema_migrations table (fresh host); nothing to check"
    exit 0
fi

prod_version="${state%%|*}"
prod_dirty="${state##*|}"
if [[ "$prod_dirty" == "true" ]]; then
    err "ABORT — prod schema_migrations is DIRTY at version ${prod_version}."
    err "A previous migration failed part-way. Fix it on the host (inspect, then"
    err "'migrate force <version>') before deploying anything on top of it."
    exit "$EXIT_REFUSE"
fi

# ── 2. Pending files ───────────────────────────────────────────
pending=()
have_prod_file=0
shopt -s nullglob
for f in "${MIGRATIONS_DIR}"/[0-9]*_*.up.sql; do
    base="${f##*/}"
    num="${base%%_*}"
    [[ "$num" =~ ^[0-9]+$ ]] || continue
    n=$((10#$num))
    if (( n == prod_version )); then
        have_prod_file=1
    elif (( n > prod_version )); then
        pending+=("$f")
    fi
done
shopt -u nullglob
if (( have_prod_file == 0 )); then
    err "ABORT — prod is at migration ${prod_version}, which this checkout does not have."
    err "Prod is ahead of this checkout; pull/rebase before deploying."
    exit "$EXIT_REFUSE"
fi

if (( ${#pending[@]} == 0 )); then
    say "prod is at migration ${prod_version}; nothing pending — dry-running dev_masked.sql only"
else
    say "prod is at migration ${prod_version}; ${#pending[@]} pending:"
    for f in "${pending[@]}"; do say "  ${f##*/}"; done
fi

if [[ "${PROD_CHECK:-}" == "snapshot" ]]; then
    run_snapshot_fallback "PROD_CHECK=snapshot" || exit "$EXIT_FAIL"
    exit 0
fi

# ── 3. Local safety scan: nothing that could commit is ever sent ──
files=("${pending[@]}")
[[ -f "$DEV_MASKED_SQL" ]] && files+=("$DEV_MASKED_SQL")
hits="$(scan_unsafe "${files[@]}")"
if [[ "${DRYRUN_TEST_SKIP_SCAN:-0}" == "1" ]]; then
    # Exists only so the tests can prove the in-stream guards below catch
    # what the scan would have; never set it against a real host.
    err "WARNING — DRYRUN_TEST_SKIP_SCAN=1: the local safety scan is disabled (tests only)"
    hits=""
fi
if [[ -n "$hits" ]]; then
    say "these statements cannot run inside the dry-run's transaction:"
    while IFS= read -r h; do say "  $h"; done <<< "$hits"
    run_snapshot_fallback "a file controls its own transaction or uses psql meta-commands" \
        || exit "$EXIT_FAIL"
    exit 0
fi

# ── 4. Dry-run ─────────────────────────────────────────────────
# Second line of defence behind the scan: each file runs between
# SAVEPOINT dryrun_file and RELEASE SAVEPOINT dryrun_file. If a file ends
# the transaction, the RELEASE fails at once — 25P01 when no transaction
# is open, 3B001 when the file opened a new one — and ON_ERROR_STOP halts
# before the next file. The closing ROLLBACK TO SAVEPOINT dryrun catches
# the same thing for the stream as a whole.
#
# The map records "start end guard file" so a psql "<stdin>:N" error can be
# traced back to its file and line.
stream="$workdir/stream.sql"
map="$workdir/map"
printf '%s\n' "BEGIN;" \
    "SET LOCAL lock_timeout = '2s';" \
    "SET LOCAL statement_timeout = '15s';" \
    "SET LOCAL idle_in_transaction_session_timeout = '30s';" \
    "SAVEPOINT dryrun;" \
    "CREATE TABLE ${CANARY} ();" > "$stream"
: > "$map"
for f in "${files[@]}"; do
    printf '\\echo %s\n' "'>>> ${f##*/}'" >> "$stream"
    printf '%s\n' "SAVEPOINT dryrun_file;" >> "$stream"
    start=$(( $(wc -l < "$stream") + 1 ))
    cat "$f" >> "$stream"
    # A file without a trailing newline would glue its last line to ours.
    [[ -n "$(tail -c1 "$f")" ]] && echo >> "$stream"
    end=$(wc -l < "$stream")
    printf '%s\n' "RELEASE SAVEPOINT dryrun_file;" >> "$stream"
    printf '%s %s %s %s\n' "$start" "$end" "$(( end + 1 ))" "$f" >> "$map"
done
printf '%s\n' "ROLLBACK TO SAVEPOINT dryrun;" "ROLLBACK;" >> "$stream"
if [[ -n "${DRYRUN_STREAM_COPY:-}" ]]; then
    cp "$stream" "$DRYRUN_STREAM_COPY"
fi

say "dry-running against prod in a rolled-back transaction (timeout ${DRYRUN_TIMEOUT}s)…"
rc=0
out="$(timeout "$DRYRUN_TIMEOUT" bash -c \
    "${PROD_PSQL} -X -q -b -v ON_ERROR_STOP=1 -v VERBOSITY=verbose -f -" \
    < "$stream" 2>&1)" || rc=$?

# Error output can quote row values (Key (email)=(…), Failing row contains,
# invalid input syntax for type uuid: "…", value "…" is out of range).
# Member data must never reach the deployer's terminal or CI logs, so drop
# DETAIL / HINT / CONTEXT / QUERY fields with their continuation lines (a
# value can span lines) up to the next psql:, >>>, FIELD: or LINE n: marker,
# and redact quoted values on ERROR lines.
redact() {
    awk '
        /^(psql:[^ ]*: )?(DETAIL|HINT|CONTEXT|QUERY):/ { skip = 1; next }
        skip && /^(psql:|>>> |[A-Z][A-Z ]*:  |LINE [0-9]+:)/ { skip = 0 }
        !skip { print }
    ' | sed -E \
        -e '/ERROR:/s/value "[^"]*"/value "[redacted]"/g' \
        -e '/ERROR:/s/: ".*"$/: "[redacted]"/' \
        -e 's/Key \(([^)]*)\)=\(.*\)/Key (\1)=([redacted])/' \
        -e 's/(Failing row contains ).*/\1([redacted])/'
}
show_output() { redact <<< "$out" >&2; }

# VERBOSITY=verbose makes psql print "<stdin>:N: ERROR:  SQLSTATE: message".
sqlstate="$(sed -nE 's/.*ERROR:  ([0-9A-Z]{5}): .*/\1/p' <<< "$out" | head -1)"
where=""
ended_by=""
errline="$(sed -nE 's/.*<stdin>:([0-9]+): ERROR:.*/\1/p' <<< "$out" | head -1)"
if [[ -n "$errline" ]]; then
    while read -r s e g f; do
        if (( errline >= s && errline <= e )); then
            where="${f} line $(( errline - s + 1 ))"
            break
        elif (( errline == g )); then
            ended_by="$f"
            break
        fi
    done < "$map"
fi

# ── 5. Re-read prod: the canary settles whether anything committed ──
# Runs before any classification, whatever the exit code was.
after=""
verified=0
if after="$(read_state)"; then
    verified=1
    if [[ "${after##*|}" == "true" ]]; then
        show_output
        canary_found "${ended_by:+ended by ${ended_by}}"
    fi
    after="${after%|*}"
fi

# A file ended the transaction, yet the canary is gone (e.g. it ran
# ROLLBACK and then more statements): those statements autocommitted.
if [[ "$sqlstate" == "25P01" || "$sqlstate" == "3B001" ]] \
        || grep -qE 'no transaction in progress|SET LOCAL can only be used in transaction blocks' <<< "$out"; then
    show_output
    err "ABORT — a migration file ended the dry-run's transaction${ended_by:+ (${ended_by})}."
    err "PROD MAY HAVE BEEN MODIFIED: statements after that point ran outside the rolled-back"
    err "transaction and committed. Restore the pre-deploy snapshot before anything else"
    err "(docs/developer/deploy.md, 'Pre-deploy snapshot and restoring from it'), then"
    err "remove the file's transaction control."
    exit "$EXIT_REFUSE"
fi

if (( verified == 0 )); then
    show_output
    if (( rc == 255 )) || grep -qE 'psql: error:' <<< "$out"; then
        err "ERROR — could not reach prod / psql on the host (ssh or connection failure)."
    else
        err "ERROR — could not re-read prod after the dry-run."
    fi
    err "Prod could not be checked for the canary table ${CANARY}; check for it before"
    err "retrying (it means the dry-run committed). Or deploy with SKIP_PROD_CHECK=1 if prod stays unreachable."
    exit "$EXIT_FAIL"
fi

if (( rc == 255 )) || grep -qE 'psql: error:' <<< "$out"; then
    show_output
    err "ERROR — could not reach prod / psql on the host (ssh or connection failure)."
    err "Nothing was committed (no canary on prod). Check the host and retry."
    exit "$EXIT_FAIL"
fi

# ── 6. Tripwire ────────────────────────────────────────────────
if [[ "$after" != "$state" ]]; then
    err "ABORT — prod schema_migrations changed during the check: '${state}' → '${after}'."
    err "Another deploy or a manual migration ran concurrently. Investigate before deploying."
    exit "$EXIT_REFUSE"
fi

if (( rc == 0 )); then
    say "dry-run passed; prod unchanged (rolled back, no canary on prod)"
    exit 0
fi

# ── 7. Classify the remaining failures (canary verified absent) ──
if (( rc == 124 )); then
    show_output
    err "ERROR — the dry-run did not finish within ${DRYRUN_TIMEOUT}s and was stopped before its ROLLBACK."
    err "Nothing was committed (no canary on prod)."
    err "Use PROD_CHECK=snapshot to check against the pre-deploy dump instead."
    exit "$EXIT_FAIL"
fi

if grep -qE 'FATAL:' <<< "$out"; then
    show_output
    err "ERROR — prod terminated the dry-run's connection (see FATAL above). Nothing was committed (no canary on prod)."
    exit "$EXIT_FAIL"
fi

case "$sqlstate" in
    55P04|25001)
        say "dry-run hit SQLSTATE ${sqlstate}${where:+ in ${where}}, which a single transaction cannot model"
        run_snapshot_fallback "SQLSTATE ${sqlstate}" || exit "$EXIT_FAIL"
        exit 0
        ;;
    55P03)
        show_output
        err "PROD BUSY — a migration could not take its lock within 2s${where:+ (${where})}."
        err "Live traffic holds a conflicting lock. Retry the deploy in a minute."
        exit "$EXIT_BUSY"
        ;;
esac

show_output
err "ABORT — migration fails against prod data${where:+ in ${where}}${sqlstate:+ (SQLSTATE ${sqlstate})}."
if [[ "$sqlstate" == "57014" ]]; then
    err "A statement ran longer than the 15s statement_timeout on prod data."
fi
err "Nothing was committed (no canary on prod). Fix the migration and deploy again."
exit "$EXIT_FAIL"
