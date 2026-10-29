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
the scripts in [`backend/scripts/`](../../backend/scripts/) are written this
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

Located in [`backend/scripts/`](../../backend/scripts/):

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

Schema changes live in [`backend/migrations/`](../../backend/migrations/) as
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
uploads to any S3-compatible store with `rclone`.

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

Download the desired dump from S3 with `rclone` (in the dev shell), then restore with `pg_restore`.

```sh
# 1. Define the remote from the environment (same credentials as environmentFile)
export RCLONE_CONFIG_BRYGGEBACKUP_TYPE=s3
export RCLONE_CONFIG_BRYGGEBACKUP_PROVIDER=Other
export RCLONE_CONFIG_BRYGGEBACKUP_ENDPOINT=<BACKUP_S3_ENDPOINT>
export RCLONE_CONFIG_BRYGGEBACKUP_ACCESS_KEY_ID=<BACKUP_S3_ACCESS_KEY>
export RCLONE_CONFIG_BRYGGEBACKUP_SECRET_ACCESS_KEY=<BACKUP_S3_SECRET_KEY>

# 2. List available dumps (example: daily tier; include the host's s3Prefix)
rclone lsf bryggebackup:<bucket>/<s3Prefix>/daily/

# 3. Download the chosen dump
rclone copyto bryggebackup:<bucket>/<s3Prefix>/daily/brygge-YYYYMMDD-HHMMSS.dump ./brygge-YYYYMMDD.dump

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
- `harbor_payment_targets` (000090) — printable harbor QR targets: a public
  `token` (the QR encodes `/qr/{token}`), label/location, and the linked
  `price_items` row that sets price and revenue category. `short_code`
  (000112) is the typed fallback printed under the QR, served by `GET
  /q/{code}` as a 302 to `/qr/{token}`. It is stored lowercase, unique per
  club (`idx_harbor_targets_club_short_code`) and checked against
  `^[a-z0-9-]{2,24}$`; generated defaults are 5 characters without the
  look-alikes 0/o/1/l/i (BRY-436). `label_translations` (000113) is a JSONB
  map from UI locale code (the `clubs.default_language` set) to the target's
  name in that language, at most 80 characters each; the handler validates
  keys and drops empty values. The print sheet and the public `/qr/{token}`
  page read it (BRY-437).
- `resources`, `bookings` — guest slips, bobil spots, klubbhus, etc.
- `events`, `documents`, `audit_log`, `notifications`, …
- `invoices`, `invoice_lines`, `payments` — faktura issuance and payment
  back-link. `invoices.payment_id` is set by the bank-row KID matcher and
  the Vipps reconciliation cascade; the dashboard's faktura-status widgets
  read from this flag. `invoices.import_source` (with the old number in
  `external_id`, unique per club + source) marks a faktura imported from a
  previous system: it has `kid_number = ''`, `sent_at` NULL and no journal
  entry, and is read-only (see invariants.md, "Imported fakturas are
  read-only"). `clubs.online_max_amount` (000114) is the club's default
  online-payment amount cap; `invoices.online_payment_opt_in` (a nullable
  tri-state: NULL Auto, TRUE On, FALSE Off) overrides it per faktura
  (BRY-283). Both replace the Vipps-named columns from 000093/000094; see
  "Orders and payments" below.
- **Orders and payments.** `orders` is the durable thing a payer pays
  online (faktura pay link via `order_sources`, harbor QR, checkout lines);
  `orders.provider` is the provider of its latest or paying attempt.
  `payment_attempts` (000115) holds one row per payment created at a
  provider: `provider`, `provider_reference` (unique per provider once set;
  the source of truth for references), `status` (`created`, `authorized`,
  `captured`, `failed`, `cancelled`), `amount`, `currency`, and for a
  Stripe card payment the nullable `card_brand` / `card_wallet` (000118;
  Stripe's raw codes, read from the charge for the harbor overview, never
  card digits). The newest
  attempt with a provider reference is the order's current one; older ones keep their rows so a late
  webhook still finds the order. 000115 backfilled one attempt per order with
  a `vipps_reference`. `payment_refunds` (refunds of a captured attempt,
  BRY-446): 000119 adds `provider_payment_ref`, `charge_ref`,
  `failure_reason`, `request_key` (unique per club when set: the key the
  refund button sends in the refund's metadata) and `outcome` (`credit` /
  `recollect`, NULL meaning credit); the Stripe booking pass makes the row
  for a refund that has none (a Dashboard refund). `payment_webhook_events` (raw events of
  providers other than Vipps, unique on `club_id, provider, event_id`) is
  written by the Stripe webhook; Vipps keeps `vipps_webhook_events`. 000114 added the provider-neutral
  `invoices.online_paid_at` / `online_paid_provider` (the "received, awaiting
  settlement" marker, set together, `invoices_online_paid_provider_set`),
  `invoices.online_payment_opt_in`, `clubs.online_max_amount`,
  `clubs.payment_fee_account` (fees for every provider, default 7700) and
  `clubs.stripe_clearing_account` (default 1580, seeded into existing charts),
  backfilled from the Vipps-named columns. Those legacy columns
  (`vipps_paid_at`, `vipps_payment_link_opt_in`, `vipps_max_amount`,
  `vipps_fee_account`) are dual-written by the application and read by
  nothing; BRY-450 drops them. `orders.vipps_reference` is still written on
  every Vipps mint because the settlement reconciliation matches on it
  (BRY-445). See [payments.md](payments.md).
- **Stripe balance.** 000117 mirrors the club's Stripe balance for the
  books. `stripe_balance_transactions` holds one row per Stripe balance
  transaction (unique on `club_id, balance_transaction_id`): the
  `reporting_category` (with Stripe's finer `stripe_type`),
  `amount_ore` / `fee_ore` / `net_ore` (integer øre), `fee_details` (JSONB,
  Stripe's fee breakdown),
  `currency`, Stripe's `created_at` and `available_on`, the source, charge
  and PaymentIntent ids, the matched `attempt_id` / `order_id`, the
  `payout_id` (→ `stripe_payouts`) that paid it out, and the booking state
  (`status`, `review_reason`, `journal_entry_id`, `invoice_id`, the
  `dismissed_*` columns). `stripe_payouts` holds one row per payout (unique
  on `club_id, payout_id`) with amount, `arrival_date`, `status`,
  `automatic`, `statement_descriptor`, `members_listed_at` (set once the
  payout's members were listed: membership known), and the matched `bank_row_id` /
  clearing `journal_entry_id`. The charge object itself is never stored (it
  carries the card holder's billing details). `stripe_sync_state` holds per
  club the `listed_through` watermark of the last complete sync.
  `clubs.stripe_payout_descriptor`
  (default `(?i)stripe`) is the regular expression that recognises a Stripe
  payout in the bank text. 000116 adds `stripe` to `transaction_source` in a
  file of its own. Written by `accounting.SyncStripeBalance`; booked (status,
  `journal_entry_id`) by `accounting.BookStripeBalance`; a payout's
  `bank_row_id` / `journal_entry_id` (and its own `payout` row) set by
  `accounting.ClearStripePayouts`; see
  [backend/internal/accounting/README.md](../../backend/internal/accounting/README.md#the-stripe-balance-pipeline).
- **Stripe refunds.** 000119 (BRY-446) books card refunds.
  `stripe_balance_transactions` gains the `refund_failure` category,
  `origin_txn_id` (a refund's charge row, a refund failure's refund row;
  `order_id` / `attempt_id` stay charge-only), `refunded_ore` on a charge
  (booked refunds less booked refund failures), `recollected_at` on a charge
  (refunded in full to collect again: it no longer settles its fakturas) and
  `refund_request_key` (from the refund's metadata at ingest).
  `refund_documents` holds one row per faktura (or order with no faktura,
  `invoice_id` NULL) a booked refund touched: `refund_txn_id`, the
  `payment_refund_id`, `invoice_id`, `order_id`, `credit_invoice_id` (the
  document the row issued: the credit note, or for `refail_invoice` the debit
  faktura), `amount_ore`, `kind` (`credit` / `recollect` /
  `refail_invoice`), `detached_order_id`; unique on
  `(refund_txn_id, invoice_id)` with NULLs not distinct. It is how a refund
  credit note is linked to its faktura (never `credits_invoice_id`) and what
  refund failures and unassigns read. `clubs.stripe_suspense_account`
  (NULL: 2900) parks a duplicate card capture until its refund books.
  `invoices` gets a unique index on `(club_id, invoice_number)`; every
  creation path numbers through `accounting.NextInvoiceNumberTx`. The
  migration also moves refunds and disputes held as `refund_pending_bry446`
  to `refund_legacy`, other adjustments held that way to
  `unsupported_category`, and refund failures stored as `other_adjustment`
  to `refund_failure`.
- **Stripe disputes.** 000120 (BRY-446) books card disputes.
  `payment_disputes` holds one row per dispute Stripe reports:
  `club_id`, `attempt_id` and `order_id` (NULL when Brygge cannot tie the
  dispute to a payment), `provider` + `provider_dispute_id` (unique),
  `charge_ref`, `provider_payment_ref` (the PaymentIntent), `amount` (kr,
  NULL when the event left it out), `currency`, `reason` (Stripe's code),
  `status` (Stripe's word, free TEXT, see
  [enums.md](reference/enums.md)), `evidence_due_by`, `is_charge_refundable`
  (Stripe's own answer, NULL when absent) and `notified_phase` (the phase the
  treasurer was last emailed about). The Stripe webhook is
  its only writer (`payments.UpsertDispute`, idempotent and forward-only)
  and writes nothing else; the booking pass never reads it. Queried for the
  open disputes of an attempt (`payments.OpenDisputeForAttempt`) and, by a
  later inbox, for what waits for evidence (`status`, `evidence_due_by`).
  `clubs.stripe_dispute_account` (NULL: 7830 Tap på fordringer) is where a
  lost dispute is booked. The migration adds account 7830 to the charts of
  clubs seeded without it (as 000099 did for 3130, 3210 and 3220; new clubs
  get it from `SeedKontoplan`) and hands disputes held as `refund_legacy`
  back to the booking pass when that is safe (NOK, no entry, not dismissed,
  payout not cleared in the books). Read raw by the dev role (ids, an
  amount and codes only).
- `invoice_pdf_archive` (migration 000050) — preserves prior PDF bytes when
  `invoices.pdf_data` is regenerated, so Norwegian bokføringsloven §13's
  5-year retention requirement is satisfied.
- `club_bank_accounts` (migration 000048) — multi-account registry per club
  with semantic `role` (`drift` / `hoyrente` / `other`), GL `gl_code`, and
  an `is_default_for_invoices` flag. The faktura PDF picks the
  default-for-invoices account; bank-statement upload auto-matches
  against `account_number`. Replaces the legacy single `clubs.bank_account`
  column (kept as fallback for one release).
- `bank_imports`, `bank_import_rows` — statement uploads + per-row tagging. `bank_imports.period_from`/`period_to` record the statement's span; `bank_import_rows.reseen_at` marks a bare row a later statement showed again, so it is not provisional (BRY-378)
  (KID auto-match writes `journal_entry_id`; reassign endpoint cascades
  to journal_lines).
- `vipps_import_rows` — Vipps payout CSV ingestion. `journal_entry_id` is
  set by `ReconcileVippsConfirm` after the cascade resolves; unresolved
  belastning lines land in 3900 (Andre inntekter) rather than 2900.
- `clubs.bank_auto_assign` (migration 000110) — per-club opt-in for booking
  unambiguous KID-less bank deposits to a faktura without a human (BRY-366),
  separate from `vipps_auto_assign`. Default off; while off, the bank KID
  cascade keeps its name+amount step.
- `journal_entries`, `journal_lines`, `accounts`, `fiscal_periods` —
  double-entry GL with NS 4102 chart of accounts. New clubs get the default
  chart from `SeedKontoplan`. An account added to the default chart later
  needs a backfill migration for existing clubs, as in 000041 (1925),
  000099 (3130 Strøm, 3210 Bobil/campingplass, 3220 Lokalleie) and 000114
  (1580 Til gode betalingsformidler (Stripe)). Migration 000100 is a one-off data cleanup: it deletes the orphan import drafts that duplicated posted bookings (BRY-324). `journal_entries.source` gained `invoice_resplit` in 000101 for revenue reclassifications (BRY-364). Migration 000104 adds a unique index allowing one `invoice_sync` entry per faktura (BRY-363). 000102 adds `invoices_status_check` (`open`/`voided` only, BRY-325). 000103 adds Vipps auto-assign (BRY-340): `clubs.vipps_auto_assign` (opt-in, default off) and `clubs.vipps_auto_assign_tie_category`, plus `vipps_import_rows.invoice_id` (the faktura a charge settled, for unassign) and `vipps_import_rows.auto_assign_rule`. 000105 adds `invoices.credits_invoice_id` (BRY-342): a credit note's explicit link to the faktura it credits, checked to sit only on negative totals, and unique among live credit notes. 000106 is a data-only backfill (BRY-370): it fills `vipps_import_rows.invoice_id` for charges booked to a faktura before 000103, only where the latest same-club `vipps_charge.assigned_invoice` audit row names the charge, the entry is still linked to it and not voided, and the faktura is still paid by the `KID-avstemming` payment dated on the charge. 000107 adds `opening_balance` to `transaction_source` (BRY-370): the source of the cutover entry that carries a migrating club's balances (and its imported receivables on 1500) into its first Brygge year; nothing writes it yet (BRY-365). 000108 adds role-based price exemptions (BRY-351): `price_exemptions` (club, board role, and exactly one of `category` / `price_item_id`, unique per club+role+target), `clubs.exemption_expense_account` (the expense account code exemptions book to) and `invoice_lines.exemption_role` (set on the offsetting "Fritak" line). 000111 adds `invoice_lines_price_item_category` (BRY-389): `CHECK (price_item_id IS NULL OR category IS NOT NULL)`, so every line with a price item stores the category it was issued under and GL booking never depends on the item's current category.
- `broadcasts`, `broadcast_deliveries` (migration 000069) — bulk member
  sends. A `broadcasts` row is the parent announcement (subject/body, the
  originating shared-mailbox `source_address`, a `status`); each recipient
  gets a `broadcast_deliveries` row the background worker drains (`status`,
  `attempts`, `error`, `sent_at`). Enqueued by the inbox send path when a
  send fans out to a group/BCC; see `backend/internal/broadcast/`.

Every table has `created_at` / `updated_at` `TIMESTAMPTZ` columns and a
`gen_random_uuid()` primary key.

## Migration dry-run against prod data

`just migrate-check` applies every migration to an empty DB. Every deploy
then dry-runs the *pending* migrations on prod itself, inside a
transaction that always rolls back. See [deploy.md](deploy.md#pending-migrations-checked-against-prod-data).
For an ordinary transactional migration that dry-run is the check. You
don't need to run anything by hand.

`just migrate-check-prod` is the snapshot check the deploy falls back to
when a migration can't be dry-run in one transaction: files with their own
`BEGIN`/`COMMIT`, a new enum value used by a later statement, or
`CREATE INDEX CONCURRENTLY`. The deploy runs it with the pre-deploy dump it
just pulled. By hand it restores the **latest daily snapshot** (from the
automated S3 backup, `<bucket>/<s3Prefix>/daily/`, GFS retention daily×7 /
weekly×4 / monthly×12) into a throwaway `brygge_migrate_check_prod` DB,
applies the *pending* migrations against that real data, then drops it.

```bash
just up                              # compose db must be running
just migrate-check-prod              # newest snapshot from S3
just migrate-check-prod ./some.dump  # a local dump (offline)
```

Needs `BACKUP_S3_*` env (same vars as the backup job) in the repo-root `.env`, plus `BACKUP_S3_PREFIX` set to the host's `services.brygge.backup.s3Prefix` (`brygge/backups` on the reference host). A snapshot written by a newer `pg_dump` than the local container is converted through `nixpkgs#postgresql_17` before loading. The snapshot
carries member PII — it is fetched to a tmpdir and `shred`-ded on exit,
never persisted in clear.

## See also

- [deploy.md](deploy.md) — the broader deploy / VM lifecycle
- [rescue-recover-ssh-access.md](rescue-recover-ssh-access.md) — what
  to do when a bad deploy locks you out before you can run `psql`
- [troubleshooting.md](troubleshooting.md) — non-DB ops issues
