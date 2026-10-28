# Configuration

Brygge is configured entirely via environment variables. In production, set these in `deploy/.env` (see `deploy/.env.example` for a template).

---

## Core

| Variable       | Required | Default                 | Description                                       |
|----------      |----------|---------                |-------------                                      |
| `PORT`         | No       | `8080`                  | HTTP server port                                  |
| `DATABASE_URL` | Yes      | —                       | PostgreSQL connection string (connects as `brygge`) |
| `DEV_QUERY_DATABASE_URL` | No | —                     | Separate DSN for the `/admin/dev/query` endpoint, connecting as the restricted read-only `brygge_dev_ro` LOGIN role. Kept distinct from `DATABASE_URL` so the role is a real privilege boundary (see [invariants.md](reference/invariants.md#query-role-is-a-real-privilege-boundary-not-set-role)). Unset → the endpoint returns 503. In NixOS deploys it defaults to the `brygge_dev_ro` Unix-socket DSN. |
| `DEV_QUERY_TOKENS_ENABLED` | No | `false`           | Mounts `POST /api/v1/dev/query` for developer bearer tokens and allows admins to mint them (Admin → Developer tokens), so an external tool can query without a session or SSH (BRY-115). Off by default; requires `DEV_QUERY_DATABASE_URL`. See [security/dev-query.md](security/dev-query.md). |
| `REDIS_URL`    | Yes      | —                       | Redis connection string                           |
| `FRONTEND_URL` | No       | `http://localhost:5173` | Base URL for frontend (used in magic link emails) |
| `TRUSTED_PROXY_CIDRS` | No | `127.0.0.0/8,::1/128` | Comma-separated CIDRs of reverse proxies whose `X-Forwarded-For` is believed for the client IP (rate limits, sessions, audit). Replaces the default. Leave unset behind Caddy on the same host; the docker-compose/Traefik deploy sets `172.16.0.0/12`. A malformed entry stops the API at startup. See [invariants.md](reference/invariants.md#the-client-ip-is-rremoteaddr-set-only-from-a-trusted-proxy). |
| `CLUB_SLUG`    | No       | `brygge`                | Selects the club row; per-club DB settings and feature overrides are read by this slug |
| `CLUB_NAME`    | Yes      | —                       | Human-readable club name in the UI and email subjects. Unset → the NavBar says "Brygge" and emails say "your club" |
| `DOMAIN`       | Yes      | `localhost`             | Public domain, used in email footers and canonical URLs |
| `DISABLE_API_DOCS` | No   | —                       | `true` stops mounting the OpenAPI docs at `/api/docs` |

In NixOS deploys `CLUB_SLUG`, `CLUB_NAME` and `DOMAIN` come from `clubConfig` (see the env-layering rules in the repo's `AGENTS.md`); don't put them in the secrets env file.

## Authentication

Sessions are server-side rows in Postgres (`auth.SessionService`); there are no JWT variables any more.

| Variable              | Required | Default | Description                                                        |
|----------             |----------|---------|-------------                                                       |
| `TOTP_ENCRYPTION_KEY` | For TOTP | —       | 64 hex chars (32 bytes) for AES-256-GCM encryption of TOTP secrets |
| `AUTH_FRESH_TOTP_WINDOW` | No    | `10m`   | Per-action TOTP freshness window for sensitive operations (Go duration; surfaced to the SPA on `/session/me` as `fresh_totp_window_ms` so the in-context countdown stays in sync) |
| `BULK_SEND_THROTTLE`     | No    | `1s`    | Sleep between consecutive sends in the bulk-reminder worker (Go duration). Lower = faster drain, higher = friendlier to Stalwart submission limits and destination MX rate limits. Set `0s` to disable in tests. See [invariants.md](reference/invariants.md#bulk-reminder-queue-is-durable-throttled-and-retried). |

**Generate a TOTP encryption key:**
```bash
openssl rand -hex 32
```

## Email (SMTP)

Used for magic link login, invoice delivery, and broadcasts. Brygge talks SMTP to the self-hosted Stalwart mail server on the same host. See [mail/setup.md](mail/setup.md) for the server-side setup.

| Variable        | Required  | Default     | Description                                               |
|-----------------|-----------|-------------|-----------------------------------------------------------|
| `SMTP_HOST`     | For email | —           | SMTP server (usually `mail.<domain>`)                     |
| `SMTP_PORT`     | No        | `587`       | `465` (SMTPS) recommended; `587` (Submission/STARTTLS) supported |
| `SMTP_USERNAME` | For email | —           | Stalwart principal name — `relay` (no `@<domain>` suffix) |
| `SMTP_PASSWORD` | For email | —           | Same value as `/etc/stalwart/relay-password` on the server (provisioned via `stalwart-relay-account` systemd unit) |
| `EMAIL_FROM`    | For email | —           | Header From on outgoing mail (e.g. `<Club Name> <relay@<domain>>`). Stalwart rewrites the envelope sender of the `relay` login to the bounce mailbox (`board_mailboxes` entry with `receives_bounces`, else `postmaster@<domain>`), so bounces never go to the send-only `relay@` — see [mail/setup.md](mail/setup.md#where-bounces-for-app-mail-go) |
| `EMAIL_REPLY_TO`| No        | —           | Reply-To header (e.g. `post@<domain>`) — routes replies to a human-read mailbox when the From address is sending-only |

If `SMTP_HOST` is not set, the app starts in degraded mode — magic link auth and invoice emails will not work.

## Stalwart admin API

The API's handle on the mail server's management REST API (`/api/…`). What it enables depends on what else is set:

- **Outbound-queue gauges** (`brygge.mail.queue.*`, see [otel/instrumentation.md](otel/instrumentation.md#mail-queue-stalwart)) — the URL plus a credential (password or token).
- **Per-user mail provisioning** — additionally needs `TOTP_ENCRYPTION_KEY`.
- **Shared-inbox reconciler** — additionally needs the board-mailbox spec and mailbox passwords.

| Variable                  | Required      | Default | Description |
|---------------------------|---------------|---------|-------------|
| `STALWART_ADMIN_URL`      | For mail admin | —      | Scheme + host of Stalwart's management API (e.g. `http://127.0.0.1:8088`). Unset disables everything above |
| `STALWART_ADMIN_USER`     | No            | `admin` | Principal used with `STALWART_ADMIN_PASSWORD` |
| `STALWART_ADMIN_PASSWORD` | For mail admin | —      | Secret (env file). Either this or `STALWART_ADMIN_TOKEN`; with neither, the queue gauges are not started |
| `STALWART_ADMIN_TOKEN`    | No            | —       | Secret (env file). Pre-encoded `Basic …` credential; overrides user + password |
| `BRYGGE_MAILBOXES_PATH`   | For the inbox | —       | Path to the board-mailbox spec the shared-inbox reconciler applies ([mail/inbox.md](mail/inbox.md)) |
| `STALWART_MAILBOX_PASSWORDS_PATH` | For the inbox | — | Path to the shared mailboxes' password file |
| `BRYGGE_RECONCILER_DRY_RUN` | No          | `false` | Logs what the reconciler would change without writing to Stalwart |

The fallback admin has every permission. Any other principal used here needs at least `message-queue-list` for the queue gauges, plus the principal-management permissions provisioning uses.

## Vipps MobilePay

| Variable                 | Required     | Default | Description                    |
|----------                |----------    |---------|-------------                   |
| `VIPPS_CLIENT_ID`        | For Vipps    | —       | API client ID                  |
| `VIPPS_CLIENT_SECRET`    | For Vipps    | —       | API client secret              |
| `VIPPS_TEST_MODE`        | No           | `true`  | Use Vipps test environment     |
| `VIPPS_MSN`              | For payments | —       | Merchant serial number         |
| `VIPPS_SUBSCRIPTION_KEY` | For payments | —       | API subscription key           |
| `VIPPS_WEBHOOK_SECRET`   | For payments | —       | Webhook signature verification |
| `VIPPS_MOCK_URL`         | No (dev)     | —       | With `VIPPS_TEST_MODE`, sends every server-side Vipps call to this mock instead of the Vipps test API |
| `VIPPS_MOCK_BROWSER_URL` | No (dev)     | `http://localhost:8090` | Browser-facing mock URL for redirects; read only when `VIPPS_MOCK_URL` is set |

After setting the Vipps credentials, run `just vipps-verify` to confirm the
configured merchant authenticates against the Vipps Access Token API
end-to-end before relying on any payment flow.

**Per-club amount cap (runtime, not env).** `clubs.online_max_amount` (set at
`Admin → Economy → Settings`; API field `online_max_amount`) is the
**default** cutoff above which online payment (every provider) is suppressed and
the member pays by bank transfer (KID), so the club avoids provider fees on large
payments. `NULL` (empty field) means no limit. It is a
default, not a hard ceiling: each faktura carries a per-invoice override
(`online_payment_opt_in`: Auto/On/Off, set from the faktura row or the
reminder dialog) that can force online payment on above the cap or off below it.
The legacy `vipps_max_amount` / `vipps_payment_link_opt_in` columns are still
written with the same values until BRY-450 drops them. Enforced
at both link issuance and payment mint — see the invariant in
[reference/invariants.md](reference/invariants.md). Independently of the cap, an
imported faktura gets no pay link until its receivable is in the opening
balance (`imported_no_opening_balance`, BRY-386).

## Stripe

| Variable                 | Required          | Default | Description |
|----------                |----------         |---------|-------------|
| `STRIPE_SECRET_KEY`      | For card payments | —       | Secret API key (`sk_test_…` / `sk_live_…`; test or live mode follows the key). Secret: `/etc/brygge/env` |
| `STRIPE_WEBHOOK_SECRET`  | For card payments | —       | Signing secret of the webhook endpoint (`whsec_…`) for `/api/v1/stripe/webhooks`. Secret: `/etc/brygge/env` |

Card payments are offered only when both are set. One without the other logs a
startup warning and leaves Stripe off: a payment whose webhook Brygge cannot
verify would never be marked paid. See [payments.md](payments.md).

## Object Storage (S3)

| Variable        | Required    | Default  | Description                |
|----------       |----------   |--------- |-------------               |
| `S3_ENDPOINT`   | For uploads | —        | S3-compatible endpoint URL |
| `S3_BUCKET_DOCS`  | No        | `brygge` | Bucket for the document archive and charts |
| `S3_BUCKET_LEGAL` | No        | —        | Bucket for faktura PDFs kept for the bokføringsloven retention period. Unset → PDFs are stored in the database only (logged as a warning at start-up) |
| `S3_ACCESS_KEY` | For uploads | —        | Access key                 |
| `S3_SECRET_KEY` | For uploads | —        | Secret key                 |

## Dendrite (Matrix Forum)

| Variable                 | Required  | Default                | Description                          |
|----------                |---------- |---------               |-------------                         |
| `DENDRITE_INTERNAL_URL`  | No        | `http://dendrite:8008` | Dendrite API URL (Docker network)    |
| `DENDRITE_SERVICE_TOKEN` | For forum | —                      | Service token for Dendrite admin API |

## Web Push (VAPID)

| Variable            | Required | Default | Description       |
|----------           |----------|---------|-------------      |
| `VAPID_PUBLIC_KEY`  | For push | —       | VAPID public key  |
| `VAPID_PRIVATE_KEY` | For push | —       | VAPID private key |

**Generate VAPID keys:**
```bash
npx web-push generate-vapid-keys
```

## Database Pool

| Variable                | Default | Description                       |
|----------               |---------|-------------                      |
| `DB_MAX_CONNS`          | `20`    | Maximum pool connections          |
| `DB_MIN_CONNS`          | `2`     | Minimum idle connections          |
| `DB_MAX_CONN_LIFETIME`  | `30m`   | Max connection age                |
| `DB_MAX_CONN_IDLE_TIME` | `5m`    | Max idle time before closing      |
| `DB_STATEMENT_TIMEOUT`  | `30000` | Statement timeout in milliseconds |

## OpenTelemetry

Metrics and traces are exported via OTLP gRPC. See [otel/index.md](otel/index.md) for collector setup.

Configuration uses standard OTEL environment variables — no Brygge-specific vars needed:

| Variable                      | Default          | Description                                                               |
|----------                     |---------         |-------------                                                              |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector gRPC endpoint                                                   |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc`           | Transport protocol                                                        |
| `OTEL_RESOURCE_ATTRIBUTES`    | —                | Additional resource attributes (e.g. `deployment.environment=production`) |
| `OTEL_TRACES_SAMPLER_ARG`     | code default     | Trace sampling ratio (0.0–1.0); overrides the sampler ratio set in code   |

If the collector is unreachable, the app starts normally with a warning log.

## In-app club settings

Per-club financial settings are stored on `clubs` and edited in **Admin → Økonomiinnstillinger**, not through environment variables. Writes are treasurer/admin with fresh TOTP. None is set by env, and every automatic-booking switch is off until the club turns it on.

| Setting (`clubs` column) | Effect | User guide |
|---|---|---|
| `kid_check_method` | KID check digit for new fakturas: `mod11` (default) or `mod10`. Must match the method registered on the club's OCR/KID agreement, or the payer's bank rejects the KID | — |
| `vipps_clearing_account`, `payment_fee_account` | GL accounts the Vipps clearing postings and every provider's fee postings use (the legacy fee column is dual-written until BRY-450) | — |
| `stripe_clearing_account` | Card balance at Stripe until payout; default 1580. Not editable yet and unused until card payments are booked (BRY-445) | — |
| `vipps_per_charge_enabled` | Per-charge Vipps model: charges are booked one by one in Tildel and payouts clear by themselves, instead of one bilag per payout (BRY-228) | [faktura.md](../user/faktura.md#vipps-payouts-clear-by-themselves) |
| `online_max_amount` | Default amount cap for online payment (see above) | [faktura.md](../user/faktura.md#sent) |
| `vipps_auto_assign`, `vipps_auto_assign_tie_category` | Book unambiguous Vipps charges without a human; tie category for same-price categories (BRY-340) | [faktura.md](../user/faktura.md#automatic-vipps-booking-per-charge-tildel) |
| `bank_auto_assign` | Book unambiguous KID-less bank deposits to a faktura (BRY-366). While off, the KID cascade keeps its older full-name + amount step | [faktura.md](../user/faktura.md#automatic-booking-of-bank-payments-without-a-kid) |
| `price_exemptions` table + `exemption_expense_account` | Role exemptions (BRY-351) | [faktura.md](../user/faktura.md#role-exemptions) |

No switch books a payment to an imported faktura; see [invariants.md](reference/invariants.md#imported-fakturas-are-read-only).

## Feature Flags

Feature flags have two layers since migration 000049: env-var defaults (set at deploy time) and per-club DB overrides (toggled at runtime from `Admin → Site → Site content → Modules`). The public `/api/v1/features` endpoint reads the DB row first, falling back to the env default when no row exists or the lookup fails.

| Variable                 | Default | Description                                                                                                                                |
|----------                |---------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `FEATURE_BOOKINGS`       | `true`  | Enable harbor/hoist booking system                                                                                                         |
| `FEATURE_PROJECTS`       | `true`  | Enable project/task management                                                                                                             |
| `FEATURE_CALENDAR`       | `true`  | Enable club calendar                                                                                                                       |
| `FEATURE_COMMERCE`       | `true`  | Enable product catalog and orders                                                                                                          |
| `FEATURE_ACCOUNTING`     | `false` | Enable faktura + GL + bank-imports + Vipps reconciliation                                                                                  |
| `FEATURE_DEMO_AUTH`      | `false` | Mounts the demo login (`/demo/users`, `/demo/login`) for demo deploys. Env-only: no DB override, never toggleable at runtime                |

**Env gates routes, DB gates UI.** The env value decides whether a module's routes are registered in `main.go` at all; the DB value (`clubs.feature_*`) only decides what the SPA shows. With the env flag on, admins can freely hide or show the module; with it off, the DB switch can be flipped but the routes are unreachable. A club that uses accounting must therefore deploy with `FEATURE_ACCOUNTING=true`. The feedback module (`clubs.feature_feedback`) has no env flag and is DB-only.

## AI (Optional)

| Variable            | Required | Default | Description                              |
|----------           |----------|---------|-------------                             |
| `ANTHROPIC_API_KEY` | No       | —       | Anthropic API key for document summaries |

---

See also: [deploy.md](deploy.md) | [mail/setup.md](mail/setup.md) | [otel/index.md](otel/index.md) | [setup.md](setup.md)
