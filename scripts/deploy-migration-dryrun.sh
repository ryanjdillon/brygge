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
#   3. Scan the pending files and repeatable/dev_masked.sql locally for
#      top-level transaction control and psql meta-commands. A hit means the
#      file could commit, so nothing is sent: fall back to the snapshot check.
#   4. Otherwise stream BEGIN; SET LOCAL timeouts; each pending file;
#      dev_masked.sql; ROLLBACK; to psql as the brygge role. Nothing pending
#      → only dev_masked.sql is dry-run.
#   5. Classify a failure by SQLSTATE: 55P04 / 25001 → snapshot fallback;
#      55P03 → prod busy; anything else → the migration fails.
#   6. Re-read schema_migrations; abort if it changed while we ran.
#
# The dry-run is not a perfect model of golang-migrate: that runs each file
# in its own implicit transaction, this runs them all in one. The SQLSTATEs
# routed to the snapshot fallback are the ones that difference produces.
#
# Usage:
#   scripts/deploy-migration-dryrun.sh            # run the check
#   scripts/deploy-migration-dryrun.sh --state-sql  # print the state query
#   scripts/deploy-migration-dryrun.sh --scan FILE…  # list lines step 3 rejects
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
#
# Exit codes: 0 ok · 1 migration failed / check error · 2 refused (dirty,
# prod ahead, state changed) · 75 prod busy (lock timeout), retry later.
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

# Prints "file:line: reason" for each line that could end the dry-run's
# transaction or run a psql meta-command. Lines inside dollar-quoted bodies
# (plpgsql BEGIN … END) are skipped; comments are ignored.
scan_unsafe() {
    awk '
    FNR == 1 { indq = 0; tag = "" }
    {
        line = $0
        if (!indq) {
            code = line
            sub(/--.*/, "", code)
            bare = code
            gsub(/'"'"'[^'"'"']*'"'"'/, "", bare)
            up = toupper(code)
            if (bare ~ /\\/) {
                print FILENAME ":" FNR ": psql meta-command"
            } else if (up ~ /^[ \t]*(BEGIN|START[ \t]+TRANSACTION)([ \t][^;]*)?;/ ||
                       up ~ /^[ \t]*(COMMIT|ROLLBACK|END|ABORT)([ \t]+(WORK|TRANSACTION))?([ \t]+AND[ \t]+(NO[ \t]+)?CHAIN)?[ \t]*;/ ||
                       up ~ /^[ \t]*(COMMIT|ROLLBACK)[ \t]+PREPARED/ ||
                       up ~ /^[ \t]*PREPARE[ \t]+TRANSACTION/) {
                print FILENAME ":" FNR ": transaction control"
            }
        }
        rest = line
        if (!indq) sub(/--.*/, "", rest)
        while (1) {
            if (indq) {
                p = index(rest, tag)
                if (p == 0) break
                rest = substr(rest, p + length(tag))
                indq = 0
            } else {
                if (!match(rest, /\$[A-Za-z_]*\$/)) break
                tag = substr(rest, RSTART, RLENGTH)
                rest = substr(rest, RSTART + RLENGTH)
                indq = 1
            }
        }
    }' "$@"
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
if [[ -n "$hits" ]]; then
    say "these lines cannot run inside the dry-run's transaction:"
    while IFS= read -r h; do say "  $h"; done <<< "$hits"
    run_snapshot_fallback "a file controls its own transaction or uses psql meta-commands" \
        || exit "$EXIT_FAIL"
    exit 0
fi

# ── 4. Dry-run ─────────────────────────────────────────────────
# lineno maps a psql "<stdin>:N" error back to its file and line.
stream="$workdir/stream.sql"
map="$workdir/map"
{
    printf '%s\n' "BEGIN;" \
        "SET LOCAL lock_timeout = '2s';" \
        "SET LOCAL statement_timeout = '15s';"
} > "$stream"
: > "$map"
for f in "${files[@]}"; do
    printf '\\echo %s\n' "'>>> ${f##*/}'" >> "$stream"
    start=$(( $(wc -l < "$stream") + 1 ))
    cat "$f" >> "$stream"
    # A file without a trailing newline would glue its last line to ours.
    [[ -n "$(tail -c1 "$f")" ]] && echo >> "$stream"
    end=$(wc -l < "$stream")
    printf '%s %s %s\n' "$start" "$end" "$f" >> "$map"
done
printf '%s\n' "ROLLBACK;" >> "$stream"

say "dry-running against prod in a rolled-back transaction (timeout ${DRYRUN_TIMEOUT}s)…"
rc=0
out="$(timeout "$DRYRUN_TIMEOUT" bash -c \
    "${PROD_PSQL} -X -q -b -v ON_ERROR_STOP=1 -v VERBOSITY=verbose -f -" \
    < "$stream" 2>&1)" || rc=$?

# ── 6. Tripwire (runs whatever the outcome) ────────────────────
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

# ── 5. Classify the failure ────────────────────────────────────
if (( rc == 124 )); then
    printf '%s\n' "$out" >&2
    err "ERROR — dry-run timed out after ${DRYRUN_TIMEOUT}s; the connection was dropped and nothing was committed."
    err "Use PROD_CHECK=snapshot to check against the pre-deploy dump instead."
    exit "$EXIT_FAIL"
fi

# VERBOSITY=verbose makes psql print "<stdin>:N: ERROR:  SQLSTATE: message".
sqlstate="$(sed -nE 's/.*ERROR:  ([0-9A-Z]{5}): .*/\1/p' <<< "$out" | head -1)"
where=""
errline="$(sed -nE 's/.*<stdin>:([0-9]+): ERROR:.*/\1/p' <<< "$out" | head -1)"
if [[ -n "$errline" ]]; then
    while read -r s e f; do
        if (( errline >= s && errline <= e )); then
            where="${f} line $(( errline - s + 1 ))"
            break
        fi
    done < "$map"
fi

case "$sqlstate" in
    55P04|25001)
        say "dry-run hit SQLSTATE ${sqlstate}${where:+ in ${where}}, which a single transaction cannot model"
        run_snapshot_fallback "SQLSTATE ${sqlstate}" || exit "$EXIT_FAIL"
        exit 0
        ;;
    55P03)
        printf '%s\n' "$out" >&2
        err "PROD BUSY — a migration could not take its lock within 2s${where:+ (${where})}."
        err "Live traffic holds a conflicting lock. Retry the deploy in a minute."
        exit "$EXIT_BUSY"
        ;;
esac

printf '%s\n' "$out" >&2
err "ABORT — migration fails against prod data${where:+ in ${where}}${sqlstate:+ (SQLSTATE ${sqlstate})}."
if [[ "$sqlstate" == "57014" ]]; then
    err "A statement ran longer than the 15s statement_timeout on prod data."
fi
err "Nothing was committed. Fix the migration and deploy again."
exit "$EXIT_FAIL"
