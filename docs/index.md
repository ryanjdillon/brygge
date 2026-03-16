# Documentation

Welcome to the Brygge documentation. Brygge is an open-source harbour club management platform built with Go and Vue.

## Guides

| Document | Audience | Description |
|----------|----------|-------------|
| [deploy.md](deploy.md) | Operators | Step-by-step deployment guide with provider-specific instructions |
| [setup.md](setup.md) | Club admins | Non-developer guide for initial server setup and configuration |
| [contributing.md](contributing.md) | Developers | Development environment, code style, testing, and PR workflow |
| [k8s.md](k8s.md) | DevOps | Kubernetes migration notes for scaling beyond a single VPS |

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
- **Email** — `RESEND_API_KEY`
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
