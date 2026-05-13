# Deploy checklist — bank/Vipps CSV import (DIL-280)

Two new migrations land with this initiative:

| # | File | What |
|---|---|---|
| 37 | `000037_bank_import_dedup.up.sql` | Adds `club_id` + `row_hash` to `bank_import_rows`, backfills, creates unique index. Enables `pgcrypto` (idempotent). |
| 38 | `000038_vipps_imports.up.sql` | Creates `vipps_imports` + `vipps_import_rows` tables with `(club_id, row_hash)` unique index. |

Both apply forward-only on production data. There is no destructive change.

## Pre-flight

1. Confirm the branch is on `main` and recent commits include `affced3 … 853f594`:
   ```bash
   git log --oneline main | head -10
   ```
2. Confirm no pending un-pushed changes on `main`:
   ```bash
   git status -s
   git fetch origin && git log origin/main..main --oneline
   ```
3. (Optional) Snapshot the prod DB before deploy. From the server:
   ```bash
   ssh root@<host> 'sudo -u postgres pg_dump brygge > /var/backups/brygge-pre-dil280.sql'
   ```

## Deploy

Run from the repo on your workstation:

```bash
nix run .#deploy -- <host>
```

`deploy-rs` will:

1. Build the new system closure with `services.brygge` pinned to the current flake.
2. Activate it: `brygge-migrate.service` runs once (oneshot), then `brygge.service` starts.
3. On failure, run `nix run .#deploy -- <host> --rollback` to revert to the prior generation.

## Verify

```bash
# Migrations recorded
ssh root@<host> "sudo -u postgres psql brygge -c 'SELECT version, dirty FROM schema_migrations'"
# Expect: version 38, dirty f

# Tables exist
ssh root@<host> "sudo -u postgres psql brygge -c '\\d vipps_imports'"
ssh root@<host> "sudo -u postgres psql brygge -c '\\d vipps_import_rows'"

# bank_import_rows has the new columns
ssh root@<host> "sudo -u postgres psql brygge -c '\\d bank_import_rows' | grep -E 'club_id|row_hash'"

# pgcrypto present
ssh root@<host> "sudo -u postgres psql brygge -c \"SELECT extname FROM pg_extension WHERE extname = 'pgcrypto'\""

# API up + new endpoints reachable
ssh root@<host> 'systemctl is-active brygge brygge-migrate'
curl -s https://<host>/api/v1/admin/accounting/bank-formats -b /tmp/admin-cookies | head -c 200
```

Expect the bank-formats endpoint to return:

```json
["dnb","sparebank-norge-v1","sparebank1","sparebanken"]
```

## Smoke test (admin browser)

1. Log in as treasurer / admin; complete TOTP.
2. Navigate **Accounting → Bank & Vipps imports** (tile on dashboard).
3. Upload `Transaksjoner-*.csv` from Sparebank Norge with format `sparebank-norge-v1`. Result panel should report `Imported: N · Skipped: 0`.
4. Re-upload the same file. Result panel should report `Imported: 0 · Skipped: N` (dedup verified).
5. Upload `Oppgjørsrapport_*.csv` from Vipps Portal.
6. Find a bank row whose description matches `Utb. <n> Vippsnr <m>` and a corresponding Vipps payout was imported.
7. Click **Reconcile**. Preview modal should show `Balanced — debit = credit` and 3+ lines (bank DR, customer credit(s), Vipps gebyr DR).
8. Click **Create bilag**. Modal closes; row shows "Bilag created".
9. Open **Journal entries**, filter to current period, confirm a `vipps`-sourced draft entry exists.

## Rollback

The migrations are additive — rolling back means running the down migrations only if you want to remove the columns/tables. Otherwise leave them in place and rollback the application via `deploy-rs --rollback`.

If full DB rollback is required:

```bash
ssh root@<host> "sudo -u postgres /usr/local/bin/migrate \
  -path /run/current-system/sw/share/brygge-migrations \
  -database 'postgres:///brygge?host=/run/postgresql&sslmode=disable' \
  down 2"
```

(Adjust path to wherever your nix module installs migrations.)

## Operational notes

- The dedup recipe is enforced in two places (Go and the SQL backfill). They must stay in sync; `TestBankRowHashMatchesSQLBackfill` guards this.
- The Vipps→bank settlement linkage uses `Utb. <n> Vippsnr <m>` parsed from the bank row description. Sparebank Norge writes it in the `Beskrivelse` column. If your bank labels payouts differently, adjust `VippsSettlementPattern` in `vippsreconcile.go`.
- Customer→member resolution is exact (lowercase, trimmed `first_name || ' ' || last_name`). Unresolved customers post to clearing account `2900`. The treasurer should periodically review unresolved entries.
- Migration 000037 uses `public.digest()` (qualified) so it runs cleanly under tightened `search_path` (e.g. integration tests).
