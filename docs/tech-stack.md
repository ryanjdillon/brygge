# Tech stack

A high level inventory. For why and how the pieces connect see [architecture.md](architecture.md).

## Backend

- **Go 1.25** with `chi/v5` for routing and `Huma` for OpenAPI 3.1 generation
- **PostgreSQL 16** via `pgx/v5` — raw SQL with `sqlc` codegen, no ORM
- **Redis 7** — sessions, rate limiting, ephemeral state
- **JWT + Vipps Login** for auth (Vipps is Norway's dominant payment + identity provider)
- Three-tier rate limiting: 5/min strict, 30/min standard, 120/min authenticated
- Feature flags gate entire route groups (`FEATURE_BOOKINGS`, `FEATURE_PROJECTS`, `FEATURE_CALENDAR`, `FEATURE_COMMERCE`, `FEATURE_COMMUNICATIONS`, `FEATURE_ACCOUNTING`)

## Frontend

- **Vue 3.5** with Composition API and `<script setup lang="ts">`
- **openapi-fetch** typed client (types regenerated from the live OpenAPI spec)
- **TanStack Vue Query** for server state, **Pinia** for client state
- **vue-i18n** — Norwegian is the primary locale, English is the fallback (German, French, Italian, Dutch, Polish are partial)
- **Shadcn-vue** components in `frontend/src/components/ui/` (source-owned, edit them directly)
- **TailwindCSS 4** + **MapLibre GL** for the harbour map

## Infrastructure

- **Docker Compose** with **Traefik v2.11** as the edge proxy (auto TLS via Let's Encrypt)
- Multi-stage `Dockerfile` produces a distroless image; the production binary embeds the Vue dist via `go:embed`
- **Dendrite** (Matrix) + **Element Web** for the integrated forum
- **Stalwart** for self-hosted SMTP; **MailPit** in dev so nothing ever escapes the host
- **GitHub Actions** for lint, test, security scan, build, deploy
- **Hetzner CAX11 (ARM64, 2 vCPU, 4 GB)** is the recommended target — runs comfortably for a club of a few hundred members
