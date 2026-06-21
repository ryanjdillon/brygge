# Configuration

Brygge is configured entirely via environment variables. In production, set these in `deploy/.env` (see `deploy/.env.example` for a template).

---

## Core

| Variable       | Required | Default                 | Description                                       |
|----------      |----------|---------                |-------------                                      |
| `PORT`         | No       | `8080`                  | HTTP server port                                  |
| `DATABASE_URL` | Yes      | —                       | PostgreSQL connection string                      |
| `REDIS_URL`    | Yes      | —                       | Redis connection string                           |
| `FRONTEND_URL` | No       | `http://localhost:5173` | Base URL for frontend (used in magic link emails) |

## Authentication

| Variable              | Required | Default | Description                                                        |
|----------             |----------|---------|-------------                                                       |
| `JWT_SECRET`          | Yes*     | —       | HMAC-256 signing key for JWT tokens                                |
| `JWT_ACCESS_EXPIRY`   | No       | `15m`   | Access token lifetime (Go duration)                                |
| `JWT_REFRESH_EXPIRY`  | No       | `168h`  | Refresh token lifetime (Go duration)                               |
| `TOTP_ENCRYPTION_KEY` | For TOTP | —       | 64 hex chars (32 bytes) for AES-256-GCM encryption of TOTP secrets |
| `AUTH_FRESH_TOTP_WINDOW` | No    | `10m`   | Per-action TOTP freshness window for sensitive operations (Go duration; surfaced to the SPA on `/session/me` as `fresh_totp_window_ms` so the in-context countdown stays in sync) |
| `BULK_SEND_THROTTLE`     | No    | `1s`    | Sleep between consecutive sends in the bulk-reminder worker (Go duration). Lower = faster drain, higher = friendlier to Stalwart submission limits and destination MX rate limits. Set `0s` to disable in tests. See [invariants.md](reference/invariants.md#bulk-reminder-queue-is-in-process-not-persistent). |

*JWT variables are required while JWT auth remains active. They will be removed once the session migration is complete.

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
| `EMAIL_FROM`    | For email | —           | From-address on outgoing mail (e.g. `<Club Name> <relay@<domain>>`) |
| `EMAIL_REPLY_TO`| No        | —           | Reply-To header (e.g. `post@<domain>`) — routes replies to a human-read mailbox when the From address is sending-only |

If `SMTP_HOST` is not set, the app starts in degraded mode — magic link auth and invoice emails will not work.

## Vipps MobilePay

| Variable                 | Required     | Default | Description                    |
|----------                |----------    |---------|-------------                   |
| `VIPPS_CLIENT_ID`        | For Vipps    | —       | OAuth client ID                |
| `VIPPS_CLIENT_SECRET`    | For Vipps    | —       | OAuth client secret            |
| `VIPPS_CALLBACK_URL`     | For Vipps    | —       | OAuth callback URL             |
| `VIPPS_TEST_MODE`        | No           | `true`  | Use Vipps test environment     |
| `VIPPS_MSN`              | For payments | —       | Merchant serial number         |
| `VIPPS_SUBSCRIPTION_KEY` | For payments | —       | API subscription key           |
| `VIPPS_WEBHOOK_SECRET`   | For payments | —       | Webhook signature verification |

## Object Storage (S3)

| Variable        | Required    | Default  | Description                |
|----------       |----------   |--------- |-------------               |
| `S3_ENDPOINT`   | For uploads | —        | S3-compatible endpoint URL |
| `S3_BUCKET`     | No          | `brygge` | Bucket name                |
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

If the collector is unreachable, the app starts normally with a warning log.

## Feature Flags

Feature flags have two layers since migration 000049: env-var defaults (set at deploy time) and per-club DB overrides (toggled at runtime from `Admin → Site → Site content → Modules`). The public `/api/v1/features` endpoint reads the DB row first, falling back to the env default when no row exists or the lookup fails.

| Variable                 | Default | Description                                                                                                                                |
|----------                |---------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `FEATURE_BOOKINGS`       | `true`  | Enable harbor/hoist booking system                                                                                                         |
| `FEATURE_PROJECTS`       | `true`  | Enable project/task management                                                                                                             |
| `FEATURE_CALENDAR`       | `true`  | Enable club calendar                                                                                                                       |
| `FEATURE_COMMERCE`       | `true`  | Enable product catalog and orders                                                                                                          |
| `FEATURE_COMMUNICATIONS` | `true`  | Enable broadcasts and notifications                                                                                                        |
| `FEATURE_ACCOUNTING`     | `true`  | Enable faktura + GL + bank-imports + Vipps reconciliation. NOTE: this still gates *route registration* in `main.go` — the DB toggle controls UI visibility, the env toggle controls whether the routes exist at all |

**Asymmetry to be aware of**: for everything except accounting, the DB toggle is fully bidirectional. For accounting, the env value gates whether the routes exist; the DB value gates UI visibility. A deploy with `FEATURE_ACCOUNTING=true` (the default) lets admins freely flip the UI on/off; a deploy with `FEATURE_ACCOUNTING=false` means the DB switch can be flipped but the routes are unreachable. Tracked for symmetric handling as a follow-up.

## AI (Optional)

| Variable            | Required | Default | Description                              |
|----------           |----------|---------|-------------                             |
| `ANTHROPIC_API_KEY` | No       | —       | Anthropic API key for document summaries |

---

See also: [deploy.md](deploy.md) | [mail/setup.md](mail/setup.md) | [otel/index.md](otel/index.md) | [setup.md](setup.md)
