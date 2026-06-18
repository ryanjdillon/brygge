# Database operations

PostgreSQL 16 is the only persistent store. In production it runs on the
brygge VM as a NixOS-managed service, listening only on a unix socket
(`/run/postgresql`) — there is **no public TCP port** to attack.

This guide covers everything an operator needs after the initial deploy:
connecting, running ad-hoc SQL, applying migrations, seeding pricing /
catalog data, taking backups, and restoring.

## Conventions used in this guide

Wherever you see a placeholder, substitute the value for your deployment:

| Placeholder    | Meaning                                                      | Example                       |
|----------------|--------------------------------------------------------------|-------------------------------|
| `<host-ip>`    | The brygge server's public IP (from Hetzner panel / DNS).    | `46.225.99.41`                |
| `<key>`        | The SSH private key trusted by `admin_ssh_keys` in tfvars.   | `~/.ssh/brygge_id_ed25519`    |
| `<club_slug>`  | The club's `slug` value in the `clubs` table.                | `kbl`                         |
| `<dbname>`     | The Postgres database name. Always `brygge` unless changed.  | `brygge`                      |

To find the active club slug:

```sh
ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> \
    'sudo -u postgres psql brygge -t -c "SELECT slug FROM clubs;"'
```

The `-o IdentitiesOnly=yes` flag forces ssh to offer only `<key>`. Without
it, ssh-agent will offer every loaded key first and may exceed sshd's
`MaxAuthTries`, locking you out with "Too many authentication failures".

## Connecting

### From a server shell

```sh
ssh -i <key> -o IdentitiesOnly=yes root@<host-ip>

# Interactive REPL on the brygge DB
sudo -u postgres psql brygge

# One-shot query
sudo -u postgres psql brygge -c "SELECT count(*) FROM users;"

# List all databases on the cluster
sudo -u postgres psql -c "\l"
```

`peer` auth is configured for the `postgres` and `brygge` system users, so
no password is needed when running as those users on the host.

### From your laptop (port forward)

When you need a richer client (DataGrip, TablePlus, pgcli) tunnel the unix
socket over SSH:

```sh
ssh -i <key> -o IdentitiesOnly=yes \
    -L 5433:/run/postgresql/.s.PGSQL.5432 \
    root@<host-ip>
```

Then connect locally as the `brygge` role over the tunnel:

```sh
psql "host=localhost port=5433 user=brygge dbname=brygge sslmode=disable"
```

You'll need the `brygge` role's password from `/etc/brygge.env` (or
whatever `EnvironmentFile` your deploy uses). Treat it as a secret.

## Running an SQL script

Two patterns. Both are idempotent if the script itself is idempotent —
the scripts in [`backend/scripts/`](../backend/scripts/) are written this
way (every INSERT is gated on `NOT EXISTS`).

### Pattern A — `scp` then `ssh`

Copy the file once, then run it via psql's `-f`:

```sh
scp -i <key> -o IdentitiesOnly=yes \
    backend/scripts/<script>.sql root@<host-ip>:/tmp/

ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> \
    'sudo -u postgres psql <dbname> -v club_slug=<club_slug> -f /tmp/<script>.sql'
```

`-v club_slug=<value>` sets a psql variable that scripts read with
`:'club_slug'`. The seed scripts use this to scope every INSERT to the
right club without hard-coding a UUID.

### Pattern B — pipe over SSH (no temp file)

```sh
ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> \
    "sudo -u postgres psql <dbname> -v club_slug=<club_slug>" \
    < backend/scripts/<script>.sql
```

Slightly cleaner for scripts you want to run once and never leave on
disk. Loses the `\echo` and other `\` meta-commands' line numbers when
errors happen, which is why the seed scripts default to Pattern A.

## Pre-built scripts

Located in [`backend/scripts/`](../backend/scripts/):

| Script                 | Purpose                                                           | Required vars  |
|------------------------|-------------------------------------------------------------------|----------------|
| `seed_pricing.sql`     | Inserts slip-fee tiers, harbor membership, seasonal/guest/bobil/klubbhus prices, and the matching booking resources. Idempotent — never overwrites existing rows. | `club_slug` |

When adding new operator scripts, follow the same conventions:

- Wrap in `BEGIN; ... COMMIT;` with `\set ON_ERROR_STOP on`.
- Resolve the club via `WITH c AS (SELECT id FROM clubs WHERE slug = :'club_slug')`.
- Guard every INSERT with `WHERE NOT EXISTS (...)` or `ON CONFLICT DO NOTHING`.
- End with a `SELECT` that prints the resulting state so the operator can
  eyeball the change.

## Migrations

Schema changes live in [`backend/migrations/`](../backend/migrations/) as
paired `NNNNNN_name.up.sql` / `NNNNNN_name.down.sql` files. They are run
automatically by the `brygge-migrate` systemd unit on every deploy
(before `brygge.service` starts).

Local dev:

```sh
just migrate         # apply all pending up migrations
just migrate-down 1  # roll back N
```

Manually on the server (almost never needed — the deploy does it):

```sh
ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> 'systemctl start brygge-migrate'
journalctl -u brygge-migrate -e        # check the result
```

### golang-migrate gotchas

- **Every up file needs a matching down file**, even if it's a no-op
  (`-- noop`). The migrator scans the directory and refuses to start if
  any version is missing either side.
- **The version table is `schema_migrations`** with one row tracking the
  current version. Don't `INSERT` extra rows into it manually — golang-
  migrate treats anything but a single row as a corrupted state.

If a deploy fails mid-migration, the table is left in `dirty=true`. Fix
the SQL, then:

```sh
sudo -u postgres psql brygge -c "UPDATE schema_migrations SET dirty = false;"
systemctl restart brygge-migrate
```

## Automated backups

The `services.brygge.backup` NixOS module (implemented in `nix/backup.nix`)
runs `systemd.timers.brygge-backup` on a configurable schedule (default
**02:30 UTC daily**). It uses `pg_dump --format=custom --compress=9` and
uploads to any S3-compatible store via the MinIO client (`mc`).

### GFS retention model

Each successful run writes a **daily** object. On **Monday** it also writes
a **weekly** object. On the **1st of the month** it also writes a **monthly**
object. After uploading, the script prunes each tier to its configured count
(defaults: 7 daily, 4 weekly, 12 monthly).

Object key layout inside the bucket:

```
<bucket>/daily/brygge-YYYYMMDD-HHMMSS.dump
<bucket>/weekly/brygge-YYYYMMDD-HHMMSS.dump
<bucket>/monthly/brygge-YYYYMMDD-HHMMSS.dump
```

### Enabling in host configuration

```nix
services.brygge.backup = {
  enable      = true;
  s3Endpoint  = "https://s3.eu-central-003.backblazeb2.com";
  s3Bucket    = "brygge-backups";

  # Optional: override schedule (systemd OnCalendar syntax)
  schedule    = "*-*-* 02:30:00";

  # Optional: Uptime-Kuma push URL — pinged on success; missed ping = alert
  healthPingUrl = "https://uptime.example.com/api/push/abc123";

  # Optional: tune GFS retention
  retention = {
    daily   = 7;
    weekly  = 4;
    monthly = 12;
  };
};
```

### Required environment-file keys

Add these to `/etc/brygge/env` (the same file used by `services.brygge.environmentFile`,
or a separate path set via `services.brygge.backup.environmentFile`).
The file must be `chmod 0400` and owned by root.

```sh
# Backup bucket credentials — kept separate from app S3 creds
BACKUP_S3_ACCESS_KEY=<your-key-id>
BACKUP_S3_SECRET_KEY=<your-secret-key>

# Optional: override the non-secret options from Nix at runtime
# BACKUP_S3_ENDPOINT=https://...
# BACKUP_S3_BUCKET=brygge-backups
```

### Monitoring

- `systemctl status brygge-backup` — last run result and exit code.
- `journalctl -u brygge-backup -e` — full log of the most recent run.
- If `healthPingUrl` is configured, a missed Uptime-Kuma ping within the
  expected interval means the backup job did not complete successfully.
- A failed run sets the unit to `failed` state; systemd will **not** retry
  automatically (this is intentional — a stuck backup should alert, not loop).

### Restore recipe

Download the desired dump from S3 using `mc`, then restore with `pg_restore`.

```sh
# 1. Configure the mc alias (same credentials as environmentFile)
mc alias set bryggebackup \
    https://s3.eu-central-003.backblazeb2.com \
    <BACKUP_S3_ACCESS_KEY> \
    <BACKUP_S3_SECRET_KEY>

# 2. List available dumps (example: daily tier)
mc ls bryggebackup/brygge-backups/daily/

# 3. Download the chosen dump
mc cp bryggebackup/brygge-backups/daily/brygge-YYYYMMDD-HHMMSS.dump ./brygge-YYYYMMDD.dump

# 4. Restore — stop the app first, then restore
ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> 'systemctl stop brygge'

pg_restore \
    --clean \
    --if-exists \
    --no-owner \
    --no-privileges \
    -d "postgres:///brygge?host=/run/postgresql&sslmode=disable" \
    brygge-YYYYMMDD.dump

# 5. Restart
ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> 'systemctl start brygge'
```

The `--clean --if-exists` flags drop and recreate all objects before
restoring, which is safe for a full-database restore onto an existing
cluster. Omit `--clean` if you are restoring into a brand-new empty
database.

> **Note:** these are brygge-specific application backups (custom-format
> pg_dump of the `brygge` database only). They are distinct from the
> mailbox-level RocksDB snapshots described in DIL-149 for Stalwart.

## Manual on-demand backup

For a quick cluster-wide dump (all databases + roles) before a risky
operation:

```sh
ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> \
    'sudo -u postgres pg_dumpall | gzip' \
    > backup-$(date +%Y%m%d).sql.gz
```

Restore onto a fresh host:

```sh
zcat backup-YYYYMMDD.sql.gz | \
    ssh -i <key> -o IdentitiesOnly=yes root@<new-host-ip> \
    'sudo -u postgres psql'
```

## Schema overview

The full schema is the union of every applied migration; read the files
in `backend/migrations/` for the source of truth. The high-level shape:

- `clubs` — top-level tenant. Every other table FK'd to `club_id`. Carries
  per-club content (harbor / motorhome blocks, board contact emails),
  default-language, the legacy `bank_account` text column (read-only mirror
  pending removal), and six `feature_*` BOOLEAN columns added in migration
  000049 that override env-var defaults at runtime.
- `users`, `user_roles` — accounts and RBAC.
- `slips`, `dock_fingers`, `docks` — harbor map geometry + metadata.
- `slip_assignments` — joins `users` ↔ `slips` (a user can hold multiple
  active assignments; each slip can only be held by one active user, via
  `idx_slip_assignments_active`).
- `boats`, `boat_models` — fleet, with manufacturer/model catalog.
- `price_items`, `slip_fees` — pricing catalog and per-year billing.
- `resources`, `bookings` — guest slips, bobil spots, klubbhus, etc.
- `events`, `documents`, `audit_log`, `notifications`, …
- `invoices`, `invoice_lines`, `payments` — faktura issuance and payment
  back-link. `invoices.payment_id` is set by the bank-row KID matcher and
  the Vipps reconciliation cascade; the dashboard's faktura-status widgets
  read from this flag.
- `invoice_pdf_archive` (migration 000050) — preserves prior PDF bytes when
  `invoices.pdf_data` is regenerated, so Norwegian bokføringsloven §13's
  5-year retention requirement is satisfied.
- `club_bank_accounts` (migration 000048) — multi-account registry per club
  with semantic `role` (`drift` / `hoyrente` / `other`), GL `gl_code`, and
  an `is_default_for_invoices` flag. The faktura PDF picks the
  default-for-invoices account; bank-statement upload auto-matches
  against `account_number`. Replaces the legacy single `clubs.bank_account`
  column (kept as fallback for one release).
- `bank_imports`, `bank_import_rows` — statement uploads + per-row tagging
  (KID auto-match writes `journal_entry_id`; reassign endpoint cascades
  to journal_lines).
- `vipps_import_rows` — Vipps payout CSV ingestion. `journal_entry_id` is
  set by `ReconcileVippsConfirm` after the cascade resolves; unresolved
  belastning lines land in 3900 (Andre inntekter) rather than 2900.
- `journal_entries`, `journal_lines`, `accounts`, `fiscal_periods` —
  double-entry GL with NS 4102 chart of accounts.

Every table has `created_at` / `updated_at` `TIMESTAMPTZ` columns and a
`gen_random_uuid()` primary key.

## See also

- [deploy.md](deploy.md) — the broader deploy / VM lifecycle
- [rescue-recover-ssh-access.md](rescue-recover-ssh-access.md) — what
  to do when a bad deploy locks you out before you can run `psql`
- [troubleshooting.md](troubleshooting.md) — non-DB ops issues
