# Documentation

Welcome to the Brygge documentation. Brygge is an open-source harbour club management platform built with Go and Vue.

Docs are split by audience. Load just the section you need rather than all of it — each entry below is a self-contained module per the deep-module rule in [AGENTS.md](../AGENTS.md).

## Top-level (cross-audience)

| Document | Description |
|----------|-------------|
| [architecture.md](architecture.md) | How the pieces fit together |
| [tech-stack.md](tech-stack.md) | What the moving parts are |
| [../CONTRIBUTING.md](../CONTRIBUTING.md) | Development environment, code style, testing, and PR workflow |

## User docs ([user/](user/))

Audience: site admins (in-app club setup, config, data correction), board members, and members. Operating the running app from the frontend — not deploying or coding it.

| Document | Description |
|----------|-------------|
| [user/faktura.md](user/faktura.md) | Treasurer's guide to the invoicing module (price catalogue, send, reconcile) |
| [user/setting-up-email.md](user/setting-up-email.md) | Member + board guide to reading and sending club mail in Apple Mail, Outlook, Gmail, Thunderbird, etc. |

## Developer docs ([developer/](developer/))

Audience: deployers, contributors, and anyone troubleshooting at the OS/protocol/database layer. Full index at [developer/README.md](developer/README.md).

| Document | Description |
|----------|-------------|
| [developer/quickstart.md](developer/quickstart.md) | Local dev environment in five minutes |
| [developer/setup.md](developer/setup.md) | Single-VPS deployment walkthrough (DNS, SSH, Hetzner) |
| [developer/deploy.md](developer/deploy.md) | Production deploy: Nix flake, deploy-rs, magic-rollback |
| [developer/configuration.md](developer/configuration.md) | Environment variables and feature flags |
| [developer/database.md](developer/database.md) | Postgres ops: connecting, scripts, migrations, backups |
| [developer/reference/enums.md](developer/reference/enums.md) | Postgres enums, TEXT vocabularies, payment / Vipps category → GL maps |
| [developer/reference/invariants.md](developer/reference/invariants.md) | Append-only list of rules other code must respect |
| [developer/reference/audit-actions.md](developer/reference/audit-actions.md) | Every `audit.Action*` constant: string, resource, when, `extra` fields |
| [developer/checklists/](developer/checklists/) | Per-change-type recipes (add route, migration, bulk action, audit action, feature flag) |
| [developer/rescue-recover-ssh-access.md](developer/rescue-recover-ssh-access.md) | Recovering SSH access when locked out |
| [developer/troubleshooting.md](developer/troubleshooting.md) | Common dev + deploy issues |
| [developer/k8s.md](developer/k8s.md) | Kubernetes migration notes for scaling beyond a single VPS |

### Source-tree subsystem READMEs

Colocated with the code they describe; load when you enter the package:

- [`../backend/internal/accounting/README.md`](../backend/internal/accounting/README.md) — Vipps cascade, KID matching, GL constants
- [`../backend/internal/handlers/README.md`](../backend/internal/handlers/README.md) — handler conventions, response shapes, audit pattern, TOTP gating
- [`../backend/internal/email/README.md`](../backend/internal/email/README.md) — outbound senders, templates, `formatNOK`
- [`../backend/internal/middleware/README.md`](../backend/internal/middleware/README.md) — middleware ordering, TOTP gating flavors
- [`../backend/internal/finance/README.md`](../backend/internal/finance/README.md) — invoice data contract, KID generation
- [`../frontend/src/views/admin/README.md`](../frontend/src/views/admin/README.md) — admin sidebar, click-time TOTP gating, `useNavGate`

### Subject subdirs under [developer/](developer/)

Each is a self-contained module covering one subject in depth. Per the deep-module rule, load the whole subdir when you need that subject; entries within it cross-link as needed and don't duplicate content from sibling subjects.

#### [developer/mail/](developer/mail/) — self-hosted Stalwart + Bulwark

| Document | Audience | Description |
|----------|----------|-------------|
| [developer/mail/setup.md](developer/mail/setup.md) | Operators | Initial deploy, DKIM provisioning, role mailboxes, deliverability, day-2 ops |
| [developer/mail/inbox.md](developer/mail/inbox.md) | Operators + Developers | Role-gated shared inbox at `/admin/inbox`: spec format, reconciler, per-user provisioning, send path, verification recipes |
| [developer/mail/stalwart-internals.md](developer/mail/stalwart-internals.md) | Developers | Stalwart 0.15 protocol quirks (admin REST, JMAP, password hashing). Reference for when something at the protocol layer breaks. |
| [developer/mail/bimi.md](developer/mail/bimi.md) | Operators | BIMI: publishing the club logo so it renders next to outbound mail |

#### [developer/otel/](developer/otel/) — OpenTelemetry

| Document | Audience | Description |
|----------|----------|-------------|
| [developer/otel/index.md](developer/otel/index.md) | Operators | Instrumentation, app config, local + upstream collectors |

#### [developer/security/](developer/security/)

| Document | Audience | Description |
|----------|----------|-------------|
| [developer/security/2fa.md](developer/security/2fa.md) | Board members + Operators | Two-factor authentication: enrollment, recovery codes, admin reset |

## Architecture Overview

Brygge is a **monorepo** with a Go API backend and a Vue 3 SPA frontend. In production, the Vue build is embedded into the Go binary via `go:embed` and served by chi alongside the API.

### Backend

- **Go 1.25** with **chi/v5** router and **Huma** for OpenAPI 3.1 spec generation
- **PostgreSQL 16** via pgx/v5 — raw SQL with sqlc code generation (no ORM)
- **Redis 7** for caching, sessions, and rate limiting
- JWT authentication with Vipps Login integration
- Feature flags toggle entire route groups (`FEATURE_BOOKINGS`, `FEATURE_PROJECTS`, etc.)
- Rate limiting in 3 tiers: strict (5/min), standard (30/min), authenticated (120/min)

### Frontend

- **Vue 3.5** with Composition API (`<script setup lang="ts">`)
- **openapi-fetch** typed API client with compile-time type safety
- **TanStack Vue Query** for server state management
- **Pinia** for client state, **vue-i18n** for 7 locales
- **Shadcn-vue** + TailwindCSS 4 for UI components

### Infrastructure

- **Docker Compose** with Traefik v2.11 reverse proxy (auto TLS via Let's Encrypt)
- **Dendrite** (Matrix homeserver) and **Element Web** for integrated forum
- **Uptime Kuma** for status monitoring
- Multi-stage Dockerfile producing a distroless container image
- GitHub Actions CI: lint, test, security scan, build, deploy

### Data Flow

```
View/Composable → useApiClient() → openapi-fetch (typed) → TanStack Query cache → Go handler → PostgreSQL/Redis
```

Paginated endpoints return `{ items, limit, offset, has_more }`. Composables extract `.items` before returning to views.

## Environment Variables

All configuration is done via environment variables. See [deploy/.env.example](../deploy/.env.example) for the full reference with inline comments.

Key variable groups:

- **Domain & club** — `DOMAIN`, `CLUB_SLUG`, `CLUB_NAME`
- **Database** — `DATABASE_URL`, `POSTGRES_*`
- **Auth** — `JWT_SECRET`, `VIPPS_*`
- **Storage** — `S3_*` (documents, charts)
- **Email** — `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `EMAIL_FROM` (self-hosted Stalwart; see [developer/mail/setup.md](developer/mail/setup.md))
- **Inbox / shared mailboxes** — `STALWART_ADMIN_*`, `BRYGGE_MAILBOXES_PATH`, `STALWART_MAILBOX_PASSWORDS_PATH` (see [developer/mail/inbox.md](developer/mail/inbox.md))
- **Features** — `FEATURE_BOOKINGS`, `FEATURE_PROJECTS`, `FEATURE_CALENDAR`, `FEATURE_COMMERCE`, `FEATURE_COMMUNICATIONS`

## Common Tasks

| Task | Command |
|------|---------|
| Start dev environment | `just up` |
| Run all tests | `just test-go && just test-vue` |
| Run a single Go test | `cd backend && go test ./internal/handlers/ -run TestName -v` |
| Run a single Vue test | `cd frontend && npx vitest run src/path/to/test.ts` |
| Apply database migrations | `just migrate` |
| Regenerate sqlc code | `just generate` |
| Regenerate API types | `just api-types` |
| Lint everything | `just lint` |
| Format everything | `just fmt` |
| Build production binary | `just build` |
