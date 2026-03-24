# Configuration

Brygge is configured entirely via environment variables. In production, set these in `deploy/.env` (see `deploy/.env.example` for a template).

---

## Core

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PORT` | No | `8080` | HTTP server port |
| `DATABASE_URL` | Yes | — | PostgreSQL connection string |
| `REDIS_URL` | Yes | — | Redis connection string |
| `FRONTEND_URL` | No | `http://localhost:5173` | Base URL for frontend (used in magic link emails) |

## Authentication

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `JWT_SECRET` | Yes* | — | HMAC-256 signing key for JWT tokens |
| `JWT_ACCESS_EXPIRY` | No | `15m` | Access token lifetime (Go duration) |
| `JWT_REFRESH_EXPIRY` | No | `168h` | Refresh token lifetime (Go duration) |
| `TOTP_ENCRYPTION_KEY` | For TOTP | — | 64 hex chars (32 bytes) for AES-256-GCM encryption of TOTP secrets |

*JWT variables are required while JWT auth remains active. They will be removed once the session migration is complete.

**Generate a TOTP encryption key:**
```bash
openssl rand -hex 32
```

## Email (Resend)

Used for magic link login, invoice delivery, and broadcasts. See [resend.md](resend.md) for setup.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `RESEND_API_KEY` | For email | — | Resend API key (`re_...`) |
| `RESEND_FROM_ADDRESS` | No | `noreply@example.com` | Sender address (must use a verified Resend domain) |

If `RESEND_API_KEY` is not set, the app starts in degraded mode — magic link auth and invoice emails will not work.

## Vipps MobilePay

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `VIPPS_CLIENT_ID` | For Vipps | — | OAuth client ID |
| `VIPPS_CLIENT_SECRET` | For Vipps | — | OAuth client secret |
| `VIPPS_CALLBACK_URL` | For Vipps | — | OAuth callback URL |
| `VIPPS_TEST_MODE` | No | `true` | Use Vipps test environment |
| `VIPPS_MSN` | For payments | — | Merchant serial number |
| `VIPPS_SUBSCRIPTION_KEY` | For payments | — | API subscription key |
| `VIPPS_WEBHOOK_SECRET` | For payments | — | Webhook signature verification |

## Object Storage (S3)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `S3_ENDPOINT` | For uploads | — | S3-compatible endpoint URL |
| `S3_BUCKET` | No | `brygge` | Bucket name |
| `S3_ACCESS_KEY` | For uploads | — | Access key |
| `S3_SECRET_KEY` | For uploads | — | Secret key |

## Dendrite (Matrix Forum)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DENDRITE_INTERNAL_URL` | No | `http://dendrite:8008` | Dendrite API URL (Docker network) |
| `DENDRITE_SERVICE_TOKEN` | For forum | — | Service token for Dendrite admin API |

## Web Push (VAPID)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `VAPID_PUBLIC_KEY` | For push | — | VAPID public key |
| `VAPID_PRIVATE_KEY` | For push | — | VAPID private key |

**Generate VAPID keys:**
```bash
npx web-push generate-vapid-keys
```

## Database Pool

| Variable | Default | Description |
|----------|---------|-------------|
| `DB_MAX_CONNS` | `20` | Maximum pool connections |
| `DB_MIN_CONNS` | `2` | Minimum idle connections |
| `DB_MAX_CONN_LIFETIME` | `30m` | Max connection age |
| `DB_MAX_CONN_IDLE_TIME` | `5m` | Max idle time before closing |
| `DB_STATEMENT_TIMEOUT` | `30000` | Statement timeout in milliseconds |

## OpenTelemetry

Metrics and traces are exported via OTLP gRPC. See [otel.md](otel.md) for collector setup.

Configuration uses standard OTEL environment variables — no Brygge-specific vars needed:

| Variable | Default | Description |
|----------|---------|-------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector gRPC endpoint |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` | Transport protocol |
| `OTEL_RESOURCE_ATTRIBUTES` | — | Additional resource attributes (e.g. `deployment.environment=production`) |

If the collector is unreachable, the app starts normally with a warning log.

## Feature Flags

| Variable | Default | Description |
|----------|---------|-------------|
| `FEATURE_BOOKINGS` | `true` | Enable harbor/hoist booking system |
| `FEATURE_PROJECTS` | `true` | Enable project/task management |
| `FEATURE_CALENDAR` | `true` | Enable club calendar |
| `FEATURE_COMMERCE` | `true` | Enable product catalog and orders |
| `FEATURE_COMMUNICATIONS` | `true` | Enable broadcasts and notifications |

## AI (Optional)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | No | — | Anthropic API key for document summaries |

---

See also: [deploy.md](deploy.md) | [resend.md](resend.md) | [otel.md](otel.md) | [setup.md](setup.md)
