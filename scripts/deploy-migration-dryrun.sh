#!/usr/bin/env bash
# deploy-migration-dryrun — check pending migrations against production data
# before a deploy, inside a transaction that always rolls back.
#
# Called by the flake deploy app (`nix run .#deploy`, `just deploy`) after the
# pre-deploy snapshot. Flow:
#
#   1. Read prod's schema_migrations (version, dirty). Refuse if dirty, or if
#      this checkout has no file for prod's version (prod is ahead of it).
#      No schema_migrations at all (fresh host) → nothing to check.
#   2. Pending = numbered *.up.sql above prod's version.
#   3. Lex the pending files and repeatable/dev_masked.sql locally, statement
#      by statement, for transaction control, SET/RESET of the dry-run's
#      timeouts, and psql meta-commands. A hit means the file could commit,
#      so nothing is sent: fall back to the snapshot check.
#   4. Otherwise stream BEGIN; SET LOCAL timeouts; SAVEPOINT; each file
#      between SAVEPOINT/RELEASE guards; dev_masked.sql; ROLLBACK TO
#      SAVEPOINT; ROLLBACK; to psql as the brygge role. Nothing pending →
#      only dev_masked.sql is dry-run.
#   5. Classify a failure: a file that ended the transaction (25P01/3B001)
#      → hard abort, prod may be modified; ssh/connection failure → error;
#      55P04 / 25001 → snapshot fallback; 55P03 → prod busy; anything else
#      → the migration fails. Row values in error output are redacted.
#   6. Re-read schema_migrations; abort if it changed while we ran.
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
#   PROD_MIGRATION_STATE "version|dirty" or "none", already read by the caller
#                        (folds the read into an earlier ssh). Read via
#                        PROD_PSQL when unset or empty.
#   SNAPSHOT_DUMP        local pg_dump -Fc of prod for the snapshot fallback;
#                        without it migrate-check-prod.sh fetches from S3.
#   SKIP_PROD_CHECK=1    skip everything, loudly.
#   PROD_CHECK=snapshot  skip the dry-run and run the snapshot check.
#   MIGRATIONS_DIR       default backend/migrations
#   DEV_MASKED_SQL       default $MIGRATIONS_DIR/repeatable/dev_masked.sql
#   SNAPSHOT_CHECK_CMD   override the snapshot check (tests); gets the dump
#                        path as its only argument, or none.
#   DRYRUN_TIMEOUT       whole-run timeout in seconds, default 90
#   DRYRUN_STREAM_COPY   tests: save a copy of the SQL stream here
#   DRYRUN_TEST_SKIP_SCAN=1  tests only: disable step 3 to exercise the
#                        in-stream guards; never against a real host
#
# Exit codes: 0 ok · 1 migration failed / check error / unreachable ·
# 2 refused or hard stop (dirty, prod ahead, state changed, a file ended
# the transaction) · 75 prod busy (lock timeout), retry later.
set -euo pipefail

EXIT_FAIL=1
EXIT_REFUSE=2
EXIT_BUSY=75

MIGRATIONS_DIR="${MIGRATIONS_DIR:-backend/migrations}"
DEV_MASKED_SQL="${DEV_MASKED_SQL:-${MIGRATIONS_DIR}/repeatable/dev_masked.sql}"
DRYRUN_TIMEOUT="${DRYRUN_TIMEOUT:-90}"
COMPOSE=(docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml)

# One row, one line: "version|dirty", or "none" when golang-migrate has never
# run. query_to_xml defers parsing the inner query to execution time, so the
# statement is valid even where schema_migrations does not exist.
STATE_SQL="SELECT CASE WHEN to_regclass('public.schema_migrations') IS NULL THEN 'none' ELSE coalesce((xpath('/table/row/s/text()', query_to_xml('SELECT version || ''|'' || dirty AS s FROM schema_migrations', false, false, '')))[1]::text, 'none') END;"

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
    if [[ "$out" != "none" && ! "$out" =~ ^[0-9]+\|(true|false)$ ]]; then
        err "ERROR — unexpected schema_migrations state from prod: '${out}'"
        return 1
    fi
    printf '%s' "$out"
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
function flush(   s, n, w, k1, k2, k3, name) {
    s = stmt
    sub(/^[ \t\r\n]+/, "", s)
    if (s != "") {
        n = split(toupper(s), w, /[ \t\r\n(=]+/)
        k1 = w[1]; k2 = (n >= 2) ? w[2] : ""; k3 = (n >= 3) ? w[3] : ""
        if (k1 == "BEGIN" || k1 == "START" || k1 == "COMMIT" || k1 == "END" || k1 == "ABORT") {
            report(sline, "transaction control")
        } else if (k1 == "ROLLBACK" && k2 != "TO" &&
                   !((k2 == "WORK" || k2 == "TRANSACTION") && k3 == "TO")) {
            report(sline, "transaction control")
        } else if (k1 == "PREPARE" && k2 == "TRANSACTION") {
            report(sline, "transaction control")
        } else if (k1 == "SET") {
            name = (k2 == "SESSION" || k2 == "LOCAL") ? k3 : k2
            if (is_timeout(name)) report(sline, "changes the dry-run's timeouts")
        } else if (k1 == "RESET" && (is_timeout(k2) || k2 == "ALL")) {
            report(sline, "changes the dry-run's timeouts")
        }
    }
    stmt = ""; sline = 0
}
function endfile() {
    if (fname != "") flush()
    st = "code"; depth = 0; tag = ""; esc = 0; stmt = ""; sline = 0
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
        if (st == "sq") {
            if (esc && c == "\\") { i += 2; continue }
            if (c == "'") {
                if (substr(line, i + 1, 1) == "'") { i += 2; continue }
                st = "code"; stmt = stmt "''"
            }
            i++; continue
        }
        if (st == "id") {
            if (c == "\"") {
                if (substr(line, i + 1, 1) == "\"") { i += 2; continue }
                st = "code"; stmt = stmt "\"\""
            }
            i++; continue
        }
        if (st == "dq") {
            if (substr(line, i, length(tag)) == tag) { st = "code"; stmt = stmt "$$"; i += length(tag); continue }
            i++; continue
        }
        # st == "code"
        if (two == "--") { st = "line"; i += 2; continue }
        if (two == "/*") { st = "block"; depth = 1; i += 2; continue }
        if (c == "'") {
            p = lastch()
            esc = (p == "E" || p == "e") && !ident(substr(stmt, length(stmt) - 1, 1))
            start_here(); st = "sq"; i++; continue
        }
        if (c == "\"") { start_here(); st = "id"; i++; continue }
        if (c == "$" && !ident(lastch()) && match(substr(line, i), /^\$([A-Za-z_][A-Za-z0-9_]*)?\$/)) {
            tag = substr(line, i, RLENGTH)
            start_here(); st = "dq"; stmt = stmt "$$"; i += RLENGTH; continue
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
elif [[ "$state" != "none" && ! "$state" =~ ^[0-9]+\|(true|false)$ ]]; then
    err "ERROR — unexpected PROD_MIGRATION_STATE '${state}'"
    exit "$EXIT_FAIL"
fi

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
    "SAVEPOINT dryrun;" > "$stream"
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
# invalid input syntax for type uuid: "…"). Member data must never reach
# the deployer's terminal or CI logs, so drop the detail lines and redact
# the quoted values in what is left.
redact() {
    sed -E \
        -e '/^(psql:[^ ]*: )?(DETAIL|HINT|CONTEXT|QUERY):/d' \
        -e 's/(invalid input (syntax|value) for [^:]*): ".*"/\1: "[redacted]"/' \
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

# ── 5a. A file ended the transaction: hard stop, before anything else ──
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

# ── 5b. Could not reach prod at all ────────────────────────────
if (( rc == 255 )) || grep -qE 'psql: error:' <<< "$out"; then
    show_output
    err "ERROR — could not reach prod / psql on the host (ssh or connection failure)."
    err "Nothing ran. Check the host and retry, or deploy with SKIP_PROD_CHECK=1 if it stays unreachable."
    exit "$EXIT_FAIL"
fi

# ── 6. Tripwire ────────────────────────────────────────────────
after="$(read_state)" || {
    err "ERROR — could not re-read schema_migrations after the dry-run; aborting"
    exit "$EXIT_FAIL"
}
if [[ "$after" != "$state" ]]; then
    err "ABORT — prod schema_migrations changed during the check: '${state}' → '${after}'."
    err "Another deploy or a manual migration ran concurrently. Investigate before deploying."
    exit "$EXIT_REFUSE"
fi

if (( rc == 0 )); then
    say "dry-run passed; prod unchanged (rolled back)"
    exit 0
fi

# ── 5c. Classify the remaining failures ────────────────────────
if (( rc == 124 )); then
    show_output
    err "ERROR — the dry-run did not finish within ${DRYRUN_TIMEOUT}s and was stopped before its ROLLBACK."
    err "The server rolls back a transaction whose session ends, so nothing should have been committed."
    err "Use PROD_CHECK=snapshot to check against the pre-deploy dump instead."
    exit "$EXIT_FAIL"
fi

if grep -qE 'FATAL:' <<< "$out"; then
    show_output
    err "ERROR — prod terminated the dry-run's connection (see FATAL above); its transaction was rolled back."
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
err "Nothing was committed. Fix the migration and deploy again."
exit "$EXIT_FAIL"
