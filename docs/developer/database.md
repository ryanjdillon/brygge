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
- `users`, `user_roles` — accounts and RBAC. `users.kind` is `person` or `system`; a club's system actor (000129) is not a member. Harbour share (moloandel) ownership is on the member, not a role (000133, BRD-14): see below.
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
  `recollect`, NULL meaning credit). The rows are written by the refund
  button (`requested`, before Stripe is called, BRY-497), the Stripe refund
  webhook and the refund sweep (their state), and the Stripe booking pass
  (for a refund that has none, a Dashboard refund); no migration. `payment_webhook_events` (raw events of
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
- **Ny faktura after a refund.** 000121 (BRY-498) adds
  `invoices.reissued_as_invoice_id` (nullable, references `invoices(id)` `ON DELETE SET NULL`):
  on a faktura that card refunds credited in full, the faktura the treasurer
  issued to collect again. Set in the same transaction that creates the new
  faktura, under the credited faktura's row lock, so the action is
  once-only (409 `already_reissued`) while the new faktura is not voided
  (a voided one is replaced by the next; deleting a draft clears the link);
  `idx_invoices_reissued_as` (unique, partial) keeps one new faktura from
  standing in for two. The id passes
  through the `dev_masked.invoices` view as it is.
- **Order-lookup indexes.** 000122 (BRY-473, BRY-459) adds five partial
  indexes, no columns. `idx_orders_club_paid` (`club_id, paid_at DESC NULLS
  LAST`, paid or refunded orders) serves the payment overview's
  newest-first list, so it stops after `LIMIT` instead of sorting every paid
  order. `idx_stripe_bt_settling_invoice` / `_order` and
  `idx_vipps_import_rows_booked_invoice` / `_order` serve the "is this
  faktura already booked" check (`invoiceBookedRowSQL`). The planner only
  uses a partial index when the query's `WHERE` implies its predicate, so a
  change to `stripeChargeSettlesSQL`, the booked-row clauses or the overview's
  status filter must change the matching index in a new migration.
- **Payment filter groups.** 000123 (BRY-522) adds `payment_filter_groups`
  (per club: `slug`, `name`, `label_key`, `icon`, `sort_order`, `enabled`,
  `builtin`; `club_id` cascades) and `payment_filter_group_items`
  (`price_item_id` is the primary key, so an item is in at most one group;
  rows cascade with the group and the item). They are the chips of the
  Payments page and decide each order's `kind`; the rule is the
  `paymentfilters.OrderGroupLateralSQL` join, in
  [`backend/internal/paymentfilters`](../../backend/internal/paymentfilters/README.md).
  The migration backfills each club's built-ins (faktura, other) and the guest,
  motorhome and slipping groups only where the club has a price item the old
  rules matched, assigning those items; a club created later has no rows until
  its first write (reads synthesise the built-ins). Both tables hold labels and
  codes only and are readable raw by `brygge_dev_ro`. The same migration adds
  `idx_order_lines_price_item` (partial, `price_item_id IS NOT NULL`) on
  `order_lines`, which had no index on the item: the settings page and the
  Payments page's nudge ask per price item whether an order line has it, and
  the price-item delete's reference count does too. Vocabulary:
  [`enums.md`](reference/enums.md#payment-overview-groups-payment_filter_groups-migration-000123-bry-522).
- **Audit identity scrub.** 000124 (BRY-536) is a data-only migration, no
  schema change: it removes the identity keys (`audit.MemberPIIKeys`) from
  `audit_log.details` of rows about members who are already erased (an
  `@deleted.local` or legacy `deleted@deleted` address, or no users row).
  Idempotent; its down is a no-op. `audit_log` is append-only by convention
  only (no trigger or privilege), and `audit.ScrubMemberPII` is the one
  sanctioned `UPDATE`; see
  [`security/member-lifecycle.md`](security/member-lifecycle.md#the-audit-log-keeps-no-identity).
- **Vipps portal address.** 000125 (BRY-556) adds nullable `clubs.vipps_portal_url`,
  the address of the club's page in the Vipps MobilePay portal, linked from
  the Payments page. NULL means no link. Written only by
  `PATCH /admin/settings/site`, which accepts an `https` address on
  `portal.vippsmobilepay.com` and nothing else; the portal path's number is
  the portal's own, so the link is never derived from `VIPPS_MSN`. Passed
  through `dev_masked.clubs` as is (a club-owned address on a fixed host, no
  person in it). The down drops the `dev_masked.clubs` view first; the
  repeatable file rebuilds it.
- **Settings audit scrub.** 000127 (BRY-556) is a data-only migration, no
  schema change. `PATCH /admin/settings/site` used to log its whole request
  under `new` in `audit_log.details`: the Anthropic API key in clear text and
  the board members' emails, phone and address. It now logs the names of the
  fields set (`{"fields":[...]}`), and 000127 rewrites every existing
  `update_financial_settings` row to that form (the keys of `new`, or of the
  row itself, sorted). **This deliberately rewrites history:** such a row keeps
  who, when and which fields, not the values. Rows already in the new form and
  rows whose details are not an object are left alone, so it is idempotent; its
  down is a no-op.
- **Reversal link.** 000131 (BRY-558) adds `journal_entries.reverses_entry_id`
  (nullable, self-referencing, partial index), set on the reversal bilag a void
  posts (`voidJournalEntryTx`, so the reclassifications' reversals too). The
  ledger void refuses an entry that carries it. The backfill links a reversal
  only when its description names exactly one voided entry of the same club and
  period; older reversals that do not match stay unlinked. Exposed in the
  `dev_masked.journal_entries` view (an id, not free text).
- **Automatic booking schema.** 000129 (BRY-554, BRY-563, ACC-13) carries all of
  the automatic-booking schema, so later changes need no migration.
  `users.kind` (`person` | `system`, default `person`, CHECK `users_kind_check`)
  separates people from the club's system actor, the principal the automatic run
  books as; vocabulary in [`enums.md`](reference/enums.md#userskind-check-since-migration-000129-bry-554).
  The partial unique index `idx_users_one_system_per_club` (`club_id WHERE
  kind = 'system'`) allows one system user per club and is the conflict target of
  `accounting.Service.SystemActor`'s insert. The system user is **not inserted by
  the migration**: `SystemActor` creates it per club on first use (PLAT-1), so
  neither a fresh database nor the deploy dry-run holds one. The trigger
  `user_roles_refuse_system_user` (BEFORE INSERT OR UPDATE on `user_roles`)
  raises `check_violation` for it, so it can never count as an admin or a board
  member. `clubs.automatic_booking` (BOOLEAN, default false) is the club's master
  switch for unattended booking; it is read and written by the
  `/admin/settings/site` GET and PATCH like the other club switches.
  `vipps_import_rows.auto_declined_at` and `bank_import_rows.auto_declined_at`
  (TIMESTAMPTZ, nullable) are for a person's undo taking a row out of every
  automatic pass; **only the columns exist so far**, nothing sets or reads them yet.
  All four new columns are passed through the `dev_masked` views raw (none is
  PII). **The down** drops the four `dev_masked` views (the repeatable file
  rebuilds them), the trigger and function, both `auto_declined_at` columns,
  `clubs.automatic_booking`, the index and the CHECK. It deletes a system user
  only when nothing references it (a journal entry it authored, or an audit row,
  makes the foreign key refuse, and the actor is kept); `users.kind` is dropped only
  when no system user remains, otherwise it stays so that row is still told apart
  from a person. Every query that lists, matches or loads users must filter
  `kind = 'person'`; see the accounting README "System actor".
- **Harbour share ownership.** 000133 (BRY-77, BRD-14) adds
  `users.harbor_share_owner` (BOOLEAN NOT NULL, default false), the only
  is-owner predicate, and `users.harbor_share_paid_on` (DATE, nullable; NULL =
  the paid date is unknown, as for most buy-ins before 2026, and nothing stores
  a placeholder). CHECK `users_harbor_share_paid_on_requires_owner` refuses a
  date without ownership. No index and no backfill: the legacy
  `slip_assignments.harbor_membership_amount` / `harbor_membership_paid_at`
  were only ever written by the seed, so the harbour share import (BRY-139) is
  the source; those two columns are superseded and are to be dropped in a
  follow-up. Both new columns pass through `dev_masked.users` raw. **The down**
  drops `dev_masked.users` first (the repeatable file rebuilds it), then the
  CHECK and both columns.
- **Self-service membership.** 000135 adds the schema for joining online and
  cancelling at year end; the flows that use it come with their own changes.
  On `users`: `member_since` (DATE, when membership was granted),
  `membership_cancelled_at` (TIMESTAMPTZ, when the member cancelled),
  `membership_ends_on` (DATE, the day the cancellation takes effect) and
  `membership_cancel_reason` (TEXT, the member's own words, cleared by every
  erasure path through `accounting.EraseMemberMembershipData`). The table
  `membership_applications` holds a join request until its email link is
  clicked: the form fields (`first_name`, `last_name`, `email`, nullable
  `phone`, `address_line`, `postal_code`, `city`, `is_local`, `newsletter`),
  the SHA-256 `token_hash` of the link token (UNIQUE), `consent_versions`
  (JSONB, consent type to document version), `expires_at` and `consumed_at`
  (single use). `club_id` cascades; `idx_membership_applications_expires`
  serves the hourly purge (`JoinHandler.PurgeExpired`),
  `idx_membership_applications_club_email` (`club_id, lower(email)`) the join
  request replacing an address's unconfirmed one (BRY-629) and erasure, which
  deletes a member's requests by address. The per-address send budget lives
  in Redis, not here. `clubs.join_price_item_id` (UUID,
  `ON DELETE SET NULL`) is the price item a join is charged from; NULL means
  joining is closed. `orders.purpose` and `invoices.purpose` (TEXT, NULL by
  default, CHECKs `orders_purpose_check` / `invoices_purpose_check`) mark what
  settlement must also do; vocabulary in
  [`enums.md`](reference/enums.md#orderspurpose-and-invoicespurpose-check-since-migration-000135).
  `dev_masked` exposes the dates, `join_price_item_id` and `purpose` raw,
  NULLs `membership_cancel_reason`, and fronts `membership_applications` with
  the identity and token hash NULL. **The down** drops the five affected
  `dev_masked` views first, then the CHECKs, columns and table. The current
  repeatable file names what the down drops, so rebuilding the views after a
  down needs the code reverted with it.
- **Legal documents per club.** 000136 (BRY-637) adds
  `idx_legal_documents_club_type_published` on `legal_documents (club_id,
  doc_type, published_at DESC)`, for the per-club lookup of a type's current
  published version (`GET /legal/{docType}`, `POST /members/me/consent`,
  `handlers.LatestLegalVersions`). Index only; **the down** drops it.
- **Rejoin block.** 000137 (BRY-629, BRD-15) adds `users.rejoin_blocked`
  (BOOLEAN NOT NULL DEFAULT false), the board's "can't rejoin" mark that the
  self-service join flow reads. `dev_masked.users` passes it through (a board
  flag, not identity). **The down** drops `dev_masked.users` first (the
  repeatable file rebuilds it), then the column.
- **Membership settlement.** 000138 (BRY-630) widens
  `deliveries_parent_by_kind` with a `membership_*` branch (no broadcast or
  faktura parent; `user_id` is not required, since it is `ON DELETE SET
  NULL`), adds the partial unique index `idx_deliveries_membership_notice_ref`
  on `(kind, payload->>'ref')` for kinds `membership_join_refunded` and
  `membership_grant_withheld` (one board notice of each per join source),
  and adds
  `clubs.board_notification_email` (TEXT NOT NULL DEFAULT '', see
  [configuration.md](configuration.md#board-notification-address)).
  `dev_masked.clubs` NULLs the address. **The down** drops
  `dev_masked.clubs` first, the column and the index, deletes queued
  `membership_*` deliveries and restores the narrower CHECK.
- **Membership cancel notices.** 000139 (BRY-633) adds the partial unique
  index `idx_deliveries_membership_cancel_ref` on `(kind, payload->>'ref')`
  for kinds `membership_cancel_receipt`, `membership_cancel_board_notice`,
  `membership_cancel_undone` and `membership_end_refused`, so each notice of
  one cancellation (`cancel:<user>:<cancelled_at>`) or one refused end
  (`end_refused:<user>:<ends_on>`) is queued once. The kinds need no CHECK
  change: 000138's `membership_*` branch admits them. Index only; **the
  down** drops it.
- **Developer role reads `legal_documents`.** 000141 grants `SELECT` on `legal_documents` to `brygge_dev_ro` (when the role exists). The table is published policy text with no author column. Down revokes. See [security/dev-query.md](security/dev-query.md).
- **Join address verification.** 000140 (BRY-639, BRD-16) adds
  `address_verified BOOLEAN`, `address_kommunenummer TEXT` and
  `address_verified_at TIMESTAMPTZ` to `users` (all nullable: NULL is "never
  checked") and to `membership_applications` (`address_verified NOT NULL
  DEFAULT false`), and the `BEFORE UPDATE` trigger
  `users_clear_stale_address_verification`, which clears a verified user's
  three columns when the street, postal code or city changes without a new
  verification. `is_local` stays NOT NULL; `address_verified = false` means
  it is unknown. Values in [enums.md](reference/enums.md). `dev_masked`
  passes `address_verified` through and NULLs the other two. **The down**
  drops the trigger, its function and the columns.
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
  set by `ReconcileVippsConfirm` after the cascade resolves (which also
  records the settled faktura in `invoice_id`, under the faktura lock);
  unresolved belastning lines land in 3900 (Andre inntekter) rather than 2900.
- `clubs.bank_auto_assign` (migration 000110) — per-club opt-in for booking
  unambiguous KID-less bank deposits to a faktura without a human (BRY-366),
  separate from `vipps_auto_assign`. Default off; while off, the bank KID
  cascade keeps its name+amount step.
- `journal_entries`, `journal_lines`, `accounts`, `fiscal_periods` —
  double-entry GL with NS 4102 chart of accounts. New clubs get the default
  chart from `SeedKontoplan`. An account added to the default chart later
  needs a backfill migration for existing clubs, as in 000041 (1925),
  000099 (3130 Strøm, 3210 Bobil/campingplass, 3220 Lokalleie) and 000114
  (1580 Til gode betalingsformidler (Stripe)). Migration 000100 is a one-off data cleanup: it deletes the orphan import drafts that duplicated posted bookings (BRY-324). `journal_entries.source` gained `invoice_resplit` in 000101 for revenue reclassifications (BRY-364). Migration 000104 adds a unique index allowing one `invoice_sync` entry per faktura (BRY-363). 000102 adds `invoices_status_check` (`open`/`voided` only, BRY-325). 000103 adds Vipps auto-assign (BRY-340): `clubs.vipps_auto_assign` (opt-in, default off) and `clubs.vipps_auto_assign_tie_category`, plus `vipps_import_rows.invoice_id` (the faktura a charge settled, for unassign) and `vipps_import_rows.auto_assign_rule`. 000105 adds `invoices.credits_invoice_id` (BRY-342): a credit note's explicit link to the faktura it credits, checked to sit only on negative totals, and unique among live credit notes. 000106 is a data-only backfill (BRY-370): it fills `vipps_import_rows.invoice_id` for charges booked to a faktura before 000103, only where the latest same-club `vipps_charge.assigned_invoice` audit row names the charge, the entry is still linked to it and not voided, and the faktura is still paid by the `KID-avstemming` payment dated on the charge. 000107 adds `opening_balance` to `transaction_source` (BRY-370): the source of the cutover entry that carries a migrating club's balances (and its imported receivables on 1500) into its first Brygge year; nothing writes it yet (BRY-365). 000108 adds role-based price exemptions (BRY-351): `price_exemptions` (club, board role, and exactly one of `category` / `price_item_id`, unique per club+role+target), `clubs.exemption_expense_account` (the expense account code exemptions book to) and `invoice_lines.exemption_role` (set on the offsetting "Fritak" line). 000111 adds `invoice_lines_price_item_category` (BRY-389): `CHECK (price_item_id IS NULL OR category IS NOT NULL)`, so every line with a price item stores the category it was issued under and GL booking never depends on the item's current category. 000128 adds `fiscal_periods.reference_only` (BRY-418, ACC-12): a boolean, default false, orthogonal to `status`, marking a year whose books of record live in another system. No backfill: the operator marks the year through the UI. It is a column, not a fourth `fiscal_period_status` value, because the year stays open for cost bookings, may be closed later and stays a reference year, and the existing `status != 'open'` checks would refuse the cost bookings with a misleading message.
- `broadcasts`, `broadcast_deliveries` (migration 000069) — bulk member
  sends. A `broadcasts` row is the parent announcement (subject/body, the
  originating shared-mailbox `source_address`, a `status`); each recipient
  gets a `broadcast_deliveries` row the background worker drains (`status`,
  `attempts`, `error`, `sent_at`). Enqueued by the inbox send path when a
  send fans out to a group/BCC; see `backend/internal/broadcast/`.
- `email_sends`, `mail_delivery_events` (migration 000132, BRY-588) — the
  outgoing-mail log and Amazon SES's delivery events for it. One
  `email_sends` row per message sent (`kind`, `recipient`, lower-cased
  `recipient_domain`, optional `related_type`/`related_id` such as
  `invoice`/<invoice id>, `provider` `ses`|`stalwart`, `status`, the SES
  `ses_message_id` once known, unique when set). Its `id` is the `send_id`
  SES message tag, so an event names its send. `mail_delivery_events` holds
  one row per SES event (`event_type`, `event_at`, per-recipient bounce /
  delay / SMTP detail, the full event in `raw`), deduplicated on
  `(ses_message_id, event_type, event_at, recipient)` with `NULLS NOT
  DISTINCT` because SQS delivers at least once; `send_id` is NULL when the
  event has no tag or names no known send. Status and event vocabularies:
  [enums.md](reference/enums.md#email_sendsstatus-check-since-migration-000132).
  Both tables hold recipient addresses (personal data): `club_id` and
  `user_id` cascade on delete, events cascade from their send, member
  erasure deletes them (`accounting.DeleteMemberMailLog`), and rows are kept
  13 months (`email_sends.created_at`, `mail_delivery_events.received_at`),
  see [invariants.md](reference/invariants.md#the-mail-log-names-people-and-lives-13-months).
  Migration 000134 (BRY-615) adds the journal outcome sweep's columns, all
  nullable and never backfilled: `outcome_detail` (the remote server's
  answer, e.g. `550 5.4.1 Recipient address rejected`, at most 500 bytes),
  `outcome_checked_at` (the sweep's last journal read, for back-off) and
  `outcome_final_at` (set once the journal showed a terminal event; the
  sweep stops looking then), plus the partial index
  `idx_email_sends_stalwart_open` for the sweep's candidate scan and
  `idx_email_sends_lower_recipient` (`lower(recipient), created_at`) for the
  sweep's per-recipient read and the member bounce list's address match. See
  [stalwart-internals.md § Outcome sweep](mail/stalwart-internals.md#outcome-sweep-journalsweep).

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
