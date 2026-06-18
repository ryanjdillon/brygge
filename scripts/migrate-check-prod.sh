#!/usr/bin/env bash
# migrate-check-prod — dry-run pending migrations against a restored copy
# of the latest production snapshot.
#
# Where `just migrate-check` applies every migration to an EMPTY db (cheap,
# catches ~80% of breakage), this restores a real prod snapshot first, so
# data-shape failures that only surface on populated tables (e.g. a
# TEXT/UUID cast on a non-empty column — DIL-363) fail loudly here instead
# of mid-deploy. See DIL-393.
#
# Usage:
#   scripts/migrate-check-prod.sh              # fetch newest snapshot from S3
#   scripts/migrate-check-prod.sh path.dump    # restore a local dump (offline)
#
# PII note (option (a) from DIL-393): the snapshot carries member PII. It is
# fetched to a private tmpdir, restored locally, and shredded on exit — never
# persisted in clear. The S3 bucket is access-controlled; no separate
# decrypt step (the DIL-328 backup stores plain custom-format dumps).
#
# Env (same vars as the DIL-328 backup job; loaded from .env via just):
#   BACKUP_S3_ENDPOINT, BACKUP_S3_BUCKET, BACKUP_S3_ACCESS_KEY, BACKUP_S3_SECRET_KEY
set -euo pipefail

COMPOSE=(docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml)
CHECK_DB="brygge_migrate_check_prod"
LOCAL_DUMP="${1:-}"

workdir="$(mktemp -d)"
cleanup() {
    # shred the snapshot copy so prod PII never lingers on disk
    if [[ -f "$workdir/snap.dump" ]]; then
        shred -u "$workdir/snap.dump" 2>/dev/null || rm -f "$workdir/snap.dump"
    fi
    rm -rf "$workdir"
    "${COMPOSE[@]}" exec -T db psql -U brygge -d postgres -v ON_ERROR_STOP=1 \
        -c "DROP DATABASE IF EXISTS ${CHECK_DB};" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ── 1. Obtain the snapshot ───────────────────────────────────
if [[ -n "$LOCAL_DUMP" ]]; then
    echo "▶ Using local snapshot: $LOCAL_DUMP"
    cp "$LOCAL_DUMP" "$workdir/snap.dump"
else
    : "${BACKUP_S3_ENDPOINT:?set BACKUP_S3_ENDPOINT (or pass a local *.dump)}"
    : "${BACKUP_S3_BUCKET:?set BACKUP_S3_BUCKET}"
    : "${BACKUP_S3_ACCESS_KEY:?set BACKUP_S3_ACCESS_KEY}"
    : "${BACKUP_S3_SECRET_KEY:?set BACKUP_S3_SECRET_KEY}"

    export MC_CONFIG_DIR="$workdir/mc"
    mc alias set bryggesnap "$BACKUP_S3_ENDPOINT" "$BACKUP_S3_ACCESS_KEY" "$BACKUP_S3_SECRET_KEY" >/dev/null

    echo "▶ Finding newest daily snapshot in ${BACKUP_S3_BUCKET}/daily/ ..."
    latest="$(mc ls "bryggesnap/${BACKUP_S3_BUCKET}/daily/" | awk '{print $NF}' | grep '\.dump$' | sort | tail -n1)"
    [[ -n "$latest" ]] || { echo "✗ No snapshot found under ${BACKUP_S3_BUCKET}/daily/" >&2; exit 1; }
    echo "▶ Fetching $latest ..."
    mc cp --quiet "bryggesnap/${BACKUP_S3_BUCKET}/daily/${latest}" "$workdir/snap.dump"
fi

# ── 2. Fresh restore target ──────────────────────────────────
echo "▶ Re-creating ${CHECK_DB} ..."
"${COMPOSE[@]}" exec -T db psql -U brygge -d postgres -v ON_ERROR_STOP=1 \
    -c "DROP DATABASE IF EXISTS ${CHECK_DB};" >/dev/null
"${COMPOSE[@]}" exec -T db psql -U brygge -d postgres -v ON_ERROR_STOP=1 \
    -c "CREATE DATABASE ${CHECK_DB} OWNER brygge;" >/dev/null

# ── 3. Restore the snapshot (custom format) ──────────────────
echo "▶ Restoring snapshot into ${CHECK_DB} ..."
"${COMPOSE[@]}" cp "$workdir/snap.dump" db:/tmp/snap.dump
"${COMPOSE[@]}" exec -T db pg_restore -U brygge -d "${CHECK_DB}" \
    --no-owner --no-privileges --clean --if-exists /tmp/snap.dump
"${COMPOSE[@]}" exec -T db rm -f /tmp/snap.dump

restored_version="$("${COMPOSE[@]}" exec -T db psql -U brygge -d "${CHECK_DB}" -tAc \
    "SELECT version FROM schema_migrations LIMIT 1;" 2>/dev/null | tr -d '[:space:]' || true)"
echo "▶ Snapshot is at migration version: ${restored_version:-unknown}"

# ── 4. Apply pending migrations against real data ────────────
echo "▶ Applying pending migrations (verbose) ..."
migrate -verbose \
    -path backend/migrations \
    -database "postgres://brygge:brygge@localhost:5432/${CHECK_DB}?sslmode=disable" \
    up

echo "✓ migrate-check-prod passed — pending migrations applied cleanly against prod-shaped data."
