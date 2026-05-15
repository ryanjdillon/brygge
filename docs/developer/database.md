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

## Backups

The deploy provisions a nightly `pg_dumpall` cron. Manual on-demand:

```sh
ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> \
    'sudo -u postgres pg_dumpall | gzip' \
    > backup-$(date +%Y%m%d).sql.gz
```

This dumps the **whole cluster** (brygge + dendrite + roles) — handy
before risky migrations, before promoting a staging snapshot, or before
swapping hosts.

Restore onto a fresh host:

```sh
zcat backup-YYYYMMDD.sql.gz | \
    ssh -i <key> -o IdentitiesOnly=yes root@<new-host-ip> \
    'sudo -u postgres psql'
```

A single-database restore (useful when only `brygge` is corrupted):

```sh
ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> \
    'sudo -u postgres pg_dump brygge | gzip' \
    > brygge-only-$(date +%Y%m%d).sql.gz

zcat brygge-only-YYYYMMDD.sql.gz | \
    ssh -i <key> -o IdentitiesOnly=yes root@<host-ip> \
    'sudo -u postgres psql brygge'
```

## Schema overview

The full schema is the union of every applied migration; read the files
in `backend/migrations/` for the source of truth. The high-level shape:

- `clubs` — top-level tenant. Every other table FK'd to `club_id`.
- `users`, `user_roles` — accounts and RBAC.
- `slips`, `dock_fingers`, `docks` — harbor map geometry + metadata.
- `slip_assignments` — joins `users` ↔ `slips` (a user can hold multiple
  active assignments; each slip can only be held by one active user, via
  `idx_slip_assignments_active`).
- `boats`, `boat_models` — fleet, with manufacturer/model catalog.
- `price_items`, `slip_fees` — pricing catalog and per-year billing.
- `resources`, `bookings` — guest slips, bobil spots, klubbhus, etc.
- `events`, `documents`, `audit_log`, `notifications`, …

Every table has `created_at` / `updated_at` `TIMESTAMPTZ` columns and a
`gen_random_uuid()` primary key.

## See also

- [deploy.md](deploy.md) — the broader deploy / VM lifecycle
- [rescue-recover-ssh-access.md](rescue-recover-ssh-access.md) — what
  to do when a bad deploy locks you out before you can run `psql`
- [troubleshooting.md](troubleshooting.md) — non-DB ops issues
