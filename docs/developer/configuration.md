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
| `TZ`           | No       | the host's zone         | The process's local time zone. It must be the club's: Stripe postings are dated on the local calendar day of the transaction (`created_at` in `time.Local`), and a membership cancelled online ends on 31 December of the local year, with the daily job deactivating from the next local day (BRY-633). NixOS sets it through `time.timeZone = clubConfig.timezone` (no env var needed); the docker-compose deploy sets `TZ` in `deploy/.env` |

In NixOS deploys `CLUB_SLUG`, `CLUB_NAME` and `DOMAIN` come from `clubConfig` (see the env-layering rules in the repo's `AGENTS.md`); don't put them in the secrets env file.

## Authentication

Sessions are server-side rows in Postgres (`auth.SessionService`); there are no JWT variables any more.

| Variable              | Required | Default | Description                                                        |
|----------             |----------|---------|-------------                                                       |
| `TOTP_ENCRYPTION_KEY` | For TOTP | —       | 64 hex chars (32 bytes) for AES-256-GCM encryption of TOTP secrets. The join form also derives its email-HMAC key from it (HKDF, info `brygge/join-email-hmac`); without it the API uses a random per-process key, so the join throttle restarts with the process and audit `email_hmac` values don't match across restarts |
| `JOIN_DAILY_MAIL_CAP` | No       | `100`   | Club-wide cap on emails the public join form sends in a rolling 24 hours, both kinds together (BRY-629). Over it the form still answers 202 and sends nothing; the API logs a warning. Not a secret: set it in the NixOS `environment`, never in `/etc/brygge/env` |
| `KARTVERKET_ADDRESS_URL` | No  | `https://ws.geonorge.no/adresser/v1` | Base of Kartverket's open address API (Adresse-API, CC BY 4.0, no key), which the join form's address search and the join's address re-lookup call (BRY-639). The browser never calls it, so the CSP is unaffected. Override it for tests or a mirror. Not a secret: NixOS `environment` |
| `AUTH_FRESH_TOTP_WINDOW` | No    | `10m`   | Per-action TOTP freshness window for sensitive operations (Go duration; surfaced to the SPA on `/session/me` as `fresh_totp_window_ms` so the in-context countdown stays in sync) |
| `BULK_SEND_THROTTLE`     | No    | `1s`    | Sleep between consecutive sends in the bulk-reminder worker (Go duration). Lower = faster drain, higher = friendlier to Stalwart submission limits and destination MX rate limits. Set `0s` to disable in tests. See [invariants.md](reference/invariants.md#bulk-reminder-queue-is-durable-throttled-and-retried). |

**Generate a TOTP encryption key:**
```bash
openssl rand -hex 32
```

## Email (SMTP)

Used for magic link login, invoice delivery, and broadcasts. By default Brygge talks SMTP to the self-hosted Stalwart mail server on the same host; with `EMAIL_PROVIDER=ses` app mail goes through Amazon SES and the `SMTP_*` relay is kept only as the magic-link fallback ([Sending through SES](#sending-through-amazon-ses)). See [mail/setup.md](mail/setup.md) for the server-side setup.

| Variable        | Required  | Default     | Description                                               |
|-----------------|-----------|-------------|-----------------------------------------------------------|
| `EMAIL_PROVIDER`| No        | `smtp`      | `smtp` (the `SMTP_*` relay) or `ses`. Non-secret: set by `nix/host.nix` to `ses` only when both tfvars `ses_enabled` and `ses_sending` are true. Any other value stops the API at startup |
| `SMTP_HOST`     | For email | —           | SMTP server (usually `mail.<domain>`)                     |
| `SMTP_PORT`     | No        | `587`       | `465` (SMTPS) recommended; `587` (Submission/STARTTLS) supported |
| `SMTP_USERNAME` | For email | —           | Stalwart principal name — `relay` (no `@<domain>` suffix) |
| `SMTP_PASSWORD` | For email | —           | Same value as `/etc/stalwart/relay-password` on the server (provisioned via `stalwart-relay-account` systemd unit) |
| `EMAIL_FROM`    | For email | —           | Header From on outgoing mail (e.g. `<Club Name> <relay@<domain>>`). Stalwart rewrites the envelope sender of the `relay` login to the bounce mailbox (`board_mailboxes` entry with `receives_bounces`, else `postmaster@<domain>`), so bounces never go to the send-only `relay@` — see [mail/setup.md](mail/setup.md#where-bounces-for-app-mail-go) |
| `EMAIL_REPLY_TO`| No        | —           | Reply-To header (e.g. `post@<domain>`) — routes replies to a human-read mailbox when the From address is sending-only |

If `SMTP_HOST` is not set (and `EMAIL_PROVIDER` is not `ses`), the app starts in degraded mode — magic link auth and invoice emails will not work.

Every send, through either relay, is logged in `email_sends` before the relay is contacted ([backend/internal/email/README.md § Send log](../../backend/internal/email/README.md#send-log-email_sends)).

### Sending through Amazon SES

With `EMAIL_PROVIDER=ses` the API sends through the SES SMTP interface: host `email-smtp.<SES_REGION>.amazonaws.com`, port 465 (implicit TLS), username `AWS_ACCESS_KEY_ID`, and a password the API derives from `AWS_SECRET_ACCESS_KEY` with [AWS's documented algorithm](https://docs.aws.amazon.com/ses/latest/dg/smtp-credentials.html#smtp-credentials-convert) — no extra secret. Every message carries `X-SES-CONFIGURATION-SET: $SES_CONFIGURATION_SET` and `X-SES-MESSAGE-TAGS: send_id=<email_sends.id>, kind=<kind>`. `EMAIL_FROM` and `EMAIL_REPLY_TO` apply unchanged; the From domain must be the SES-verified identity.

Required with `ses`: `SES_REGION`, `SES_CONFIGURATION_SET`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `EMAIL_FROM`. Any missing one stops the API at startup with the names listed, rather than falling back to Stalwart unnoticed. The `SMTP_*` relay, when configured, receives a magic link that SES refused outright (not one whose outcome is unknown). Cutover and rollback: [mail/ses.md § Cutover](mail/ses.md#cutover-sending-app-mail-through-ses).

### Contact form recipient

The public contact form (`POST /api/v1/contact`) mails each message to the club's **secretary** address, falling back to the **chairman** address. Both are club settings, edited under Admin > Site settings (`secretary_email`, `chairman_email` on the `clubs` row); nothing is configured through the environment, so Compose and NixOS deploys behave the same. Nothing is hard-coded: a club with neither address set gets no delivery.

- The mail comes from `EMAIL_FROM`; the submitter's address is set as `Reply-To` (this replaces `EMAIL_REPLY_TO` for these messages), so answering from the inbox reaches the visitor.
- With neither address set the form answers 503 `contact_recipient_not_configured`, and without `SMTP_HOST`/`EMAIL_FROM` it answers 503 `mail_not_configured`. A failed send answers 502 `contact_delivery_failed`. The page tells the visitor to retry or use the addresses listed on it; nothing is queued or retried.
- The recipient is meant to move to the board-config function allocation (BRY-482; decision BRD-3 in [../adr/board-workflows.md](../adr/board-workflows.md)). The choice lives in one function, `contactRecipient` in `backend/internal/handlers/contact.go`.

### Board notification address

When a join payment settles, the board gets a "Nytt medlem" email; a refunded or credited join payment sends it an "Innmeldingsbetaling refundert" email, and a paid join whose grant was withheld an "Innmelding må kontrollerast" email (BRY-630). The recipient is resolved when the email is sent (`boardNotificationAddress` in `backend/internal/handlers/membership_notice_sender.go`), first match wins:

1. `clubs.board_notification_email`, the club setting under Admin > Site settings ("Varsel om medlemskap til"; `board_notification_email` on `GET`/`PATCH /admin/settings/site`, a bare mailbox or empty). Empty is the default.
2. The deployment's general shared mailbox: the `board_mailboxes` entry with `from_brand_only` in the board-mailbox spec (`BRYGGE_MAILBOXES_PATH`), conventionally `post@<domain>`. The spec is deployment-wide and a deployment serves one club, so this applies only to the club whose slug is `CLUB_SLUG`.
3. The contact-form recipient: the secretary, else the chairman (as above).

Nothing is hard-coded. With none of them set the email fails at once with the note "no board notification address configured"; the hourly membership sweep puts it back in the queue once the club has an address, so no notice is lost. The grant itself is unaffected. Without mail configured at all, membership emails fail with a note saying so and are never recorded as sent. The welcome email goes to the member's own address.

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
| `MAIL_LOG_SOCKET`         | No            | —       | Unix socket of the host's mail-log helper (`/run/brygge-mail-log.sock` in `nix/host.nix`). The faktura delivery log reads Stalwart's delivery events through it; unset, the log says "unavailable" for sends it cannot answer from `email_sends` ([mail/stalwart-internals.md § Faktura delivery log](mail/stalwart-internals.md#faktura-delivery-log)). Also enables the journal outcome sweep that writes Stalwart sends' outcomes into `email_sends` ([§ Outcome sweep](mail/stalwart-internals.md#outcome-sweep-journalsweep)). Legacy: removed 35 days after the SES cutover (BRY-594) |

The fallback admin has every permission. Any other principal used here needs at least `message-queue-list` for the queue gauges, plus the principal-management permissions provisioning uses.

## Amazon SES delivery events

The API long-polls an SQS queue that the SES configuration set publishes delivery events to (configuration set → SNS → SQS, raw message delivery) and records them in `mail_delivery_events` / `email_sends` (BRY-588). The worker is off unless `SES_EVENTS_QUEUE_NAME` is set; it logs once at info when disabled. Worker internals: [backend/internal/sesevents/README.md](../../backend/internal/sesevents/README.md); setup and runbook: [mail/ses.md](mail/ses.md).

| Variable                | Required       | Default | Description |
|-------------------------|----------------|---------|-------------|
| `SES_REGION`            | For SES events | —       | AWS region of the SES identity and the queues (e.g. `eu-north-1`). Non-secret: `nix/host.nix` `extraEnvironment`, from `clubConfig`. With the queue name set but no region, the worker logs an error and does not start. Also enables the delivery log's "remove from suppression list" action (SESv2 with the same AWS key); unset, that action answers 503 |
| `SES_CONFIGURATION_SET` | For SES        | —       | Configuration set name, `brygge-<slug>`. Non-secret: `nix/host.nix` |
| `SES_EVENTS_QUEUE_NAME` | For SES events | —       | SQS queue name, `brygge-<slug>-ses-events`; the URL is resolved at start with `GetQueueUrl`. Unset disables the worker. Non-secret: `nix/host.nix` |
| `SES_EVENTS_DLQ_NAME`   | No             | —       | Dead-letter queue name, `brygge-<slug>-ses-events-dlq`. Its depth is sampled each minute into `brygge.ses.dlq.messages`; unset skips sampling. Non-secret: `nix/host.nix` |
| `AWS_ACCESS_KEY_ID`     | For SES        | —       | Access key of the `brygge-<slug>-app` IAM user, read by the AWS SDK's default credential chain and used as the SES SMTP username. Secret: `/etc/brygge/env`. Created by hand in the AWS console, never in Terraform state |
| `AWS_SECRET_ACCESS_KEY` | For SES        | —       | Secret of that access key; the SES SMTP password is derived from it. Secret: `/etc/brygge/env` |

## Vipps MobilePay

| Variable                 | Required     | Default | Description                    |
|----------                |----------    |---------|-------------                   |
| `VIPPS_CLIENT_ID`        | For Vipps    | —       | API client ID                  |
| `VIPPS_CLIENT_SECRET`    | For Vipps    | —       | API client secret              |
| `VIPPS_TEST_MODE`        | No           | `true`  | Use Vipps test environment     |
| `VIPPS_MSN`              | For payments | —       | Merchant serial number. A non-empty value also marks Vipps as configured for live payments (`Config.VippsLive()`, with `VIPPS_TEST_MODE` off), which is a condition for the Vipps link on the payment overview; the link itself is the in-app `vipps_portal_url` setting, not built from this number (see [Payments](payments.md#provider-links-on-the-payment-overview)) |
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

The Stripe Dashboard link on the payment overview is shown under the same
condition, and points at test mode (`/test`) for a test key.

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
| `stripe_clearing_account` | Card balance at Stripe until payout; default 1580. Must be an active asset account in the club's chart. Card charges and Stripe fees post to it; payouts clear it (`ClearStripePayouts`) | — |
| `stripe_dispute_account` | Expense account a lost card dispute is booked to (the dispute fee goes to `payment_fee_account`); NULL means 7830 Tap på fordringer, which is in the default kontoplan and was added to existing charts by migration 000120. No settings screen yet: set it in the database. An account missing from the chart or inactive holds disputes as `dispute_account_missing` until fixed | — |
| `stripe_payout_descriptor` | Go regular expression (at most 200 characters) that recognises a Stripe payout in the bank statement text; default `(?i)stripe`. Matched against an incoming bank row's description or counterpart: such a row is matched to a Stripe payout and booked only by its clearing, never by mapping rules, auto-assign or price-item suggestions (once the club uses Stripe) | — |
| `vipps_per_charge_enabled` | Per-charge Vipps model: charges are booked one by one in Tildel and payouts clear by themselves, instead of one bilag per payout (BRY-228) | [faktura.md](../user/faktura.md#vipps-payouts-clear-by-themselves) |
| `online_max_amount` | Default amount cap for online payment (see above) | [faktura.md](../user/faktura.md#sent) |
| `vipps_auto_assign`, `vipps_auto_assign_tie_category` | Book unambiguous Vipps charges without a human; tie category for same-price categories (BRY-340) | [faktura.md](../user/faktura.md#automatic-vipps-booking-per-charge-tildel) |
| `bank_auto_assign` | Book unambiguous KID-less bank deposits to a faktura (BRY-366). While off, the KID cascade keeps its older full-name + amount step | [faktura.md](../user/faktura.md#automatic-booking-of-bank-payments-without-a-kid) |
| `automatic_booking` | Master switch (ACC-13, BRY-554), off by default, "Bokfør og avstem automatisk": the club's system actor books and clears provider payments right after every provider import, at API start and as a nightly safety net. Read and written by `GET`/`PATCH /admin/settings/site` (treasurer/admin, fresh TOTP for the write). **Stored today; the run that reads it comes with a later part of BRY-554, so it has no effect yet, and the settings screen has no toggle yet.** It does not replace the per-rule switches above, which still gate their own rules | [adr/accounting.md](../adr/accounting.md#acc-13-brygge-books-and-clears-unambiguous-provider-payments-automatically-as-a-system-actor) |
| `vipps_portal_url` | Optional address of the club's page in the Vipps MobilePay portal (the one shown after logging in), linked from the Payments page for treasurer and admin while Vipps takes live payments. Must be an `https` address on `portal.vippsmobilepay.com` (at most 200 characters, no userinfo, port or fragment); empty clears it. Not derived from `VIPPS_MSN`: the portal's number is its own | [payments-overview.md](../user/payments-overview.md#links-to-stripe-and-vipps) |
| `price_exemptions` table + `exemption_expense_account` | Role exemptions (BRY-351) | [faktura.md](../user/faktura.md#role-exemptions) |
| `join_price_item_id` | The price item a self-service join charges (BRY-634, "Innmeldingspris"); NULL closes joining. `PATCH /admin/settings/site` takes `join_price_item_id` ("" clears it) and refuses with 400 `invalid_join_price_item` anything but an active, flat price item of the caller's club. `GET` also returns `join_open` and `join_missing` (the `GET /join/status` reasons for the caller's club). Joining also needs the four legal documents published | [membership.md](../user/join.md#opening-joining-innmeldingspris) |

No switch books a payment to an imported faktura; see [invariants.md](reference/invariants.md#imported-fakturas-are-read-only).

## Feature Flags

Feature flags have two layers since migration 000049: env-var defaults (set at deploy time) and per-club DB overrides (toggled at runtime from `Admin → Site → Site content → Modules`). The public `/api/v1/features` endpoint reads the DB row first, falling back to the env default when no row exists or the lookup fails.

| Variable                 | Default | Description                                                                                                                                |
|----------                |---------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `FEATURE_BOOKINGS`       | `true`  | Enable harbor/hoist booking system                                                                                                         |
| `FEATURE_PROJECTS`       | `true`  | Enable project/task management. Also gates the member's slip-issue report (`POST /members/me/slip/issues`, 404 while off; the portal slip page hides its button), since reports are filed as project tasks |
| `FEATURE_CALENDAR`       | `true`  | Enable club calendar                                                                                                                       |
| `FEATURE_COMMERCE`       | `true`  | Enable product catalog and orders                                                                                                          |
| `FEATURE_ACCOUNTING`     | `false` | Enable faktura + GL + bank-imports + Vipps reconciliation                                                                                  |
| `FEATURE_DEMO_AUTH`      | `false` | Mounts the demo login (`/demo/users`, `/demo/login`) for demo deploys. Env-only: no DB override, never toggleable at runtime                |

**Env gates routes, DB gates UI.** The env value decides whether a module's routes are registered in `cmd/api/routes.go` at all; the DB value (`clubs.feature_*`) only decides what the SPA shows. With the env flag on, admins can freely hide or show the module; with it off, the DB switch can be flipped but the routes are unreachable. A club that uses accounting must therefore deploy with `FEATURE_ACCOUNTING=true`. The feedback module (`clubs.feature_feedback`) has no env flag and is DB-only. The slip-issue report follows the split: the endpoint refuses on the env flag, the button hides on the DB flag, so switching Projects off only in Site settings hides the button but leaves the endpoint answering until `FEATURE_PROJECTS=false` is deployed.

## AI (Optional)

| Variable            | Required | Default | Description                              |
|----------           |----------|---------|-------------                             |
| `ANTHROPIC_API_KEY` | No       | —       | Anthropic API key for document summaries |

---

See also: [deploy.md](deploy.md) | [mail/setup.md](mail/setup.md) | [otel/index.md](otel/index.md) | [setup.md](setup.md)
