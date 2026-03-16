# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Dev Environment

Go, just, golangci-lint, and other tools are provided by the Nix flake. They are **not** in PATH by default. Use `nix develop` to enter the shell, or wrap commands:

```bash
nix develop /home/ryan/code/personal/brygge --command bash -c "cd backend && go test ./..."
```

Node (v22) is available system-wide. Frontend commands (`npm`, `npx`) work directly from `frontend/`.

## Common Commands

All `just` commands run from the repo root (`/home/ryan/code/personal/brygge`). Use `nix develop --command just <target>` if just isn't in PATH.

| Command | What it does |
|---------|-------------|
| `just up` | Start Docker services + Vite dev server |
| `just down` | Stop everything |
| `just setup` | First-time: start DB/Redis, migrate, seed |
| `just test-go` | Go unit tests (no DB needed) |
| `just test-go-integration` | Go tests with real DB/Redis |
| `just test-vue` | Vitest frontend tests |
| `just test-e2e` | Playwright E2E tests |
| `just lint` | Lint all (golangci-lint + eslint) |
| `just fmt` | Format all (gofmt + prettier) |
| `just migrate` | Apply pending DB migrations |
| `just generate` | Regenerate sqlc code |
| `just seed` | Load demo data |
| `just build` | Build production binary with embedded SPA |
| `just openapi-spec` | Generate OpenAPI 3.1 spec (JSON to stdout) |
| `just api-types` | Generate TypeScript types from OpenAPI spec |
| `just deploy` | SSH deploy: pull, build, migrate, restart |
| `just security` | Run all security scans (gosec, govulncheck, npm audit) |

For single-test examples and full dev workflow, see [docs/contributing.md](docs/contributing.md).

## Architecture

**Monorepo** with a Go API backend and Vue 3 SPA frontend. In production, the Vue dist is embedded into the Go binary via `go:embed` and served by chi. See [docs/index.md](docs/index.md) for full architecture overview and data flow.

### Backend (`backend/`)

- **Go 1.25** with **chi/v5** router, **PostgreSQL 16** (pgx/v5 + sqlc), **Redis 7** (cache/sessions)
- Routes in `cmd/api/main.go`, one handler struct per domain, auth via JWT middleware
- Claims: `middleware.GetClaims(ctx)` → `*middleware.Claims` (UserID, ClubID, Roles)
- Pagination: `shared.PaginatedResponse` → `{ items, limit, offset, has_more }`
- Feature flags: `cfg.Features.{Bookings, Projects, Calendar, Commerce, Communications}`
- Rate limiting: strict (5/min), standard (30/min), authed (120/min)
- Migrations: single baseline `000001_init` (consolidated), sqlc queries in `queries/`, generated code in `gen/`

### Frontend (`frontend/`)

- **Vue 3.5** Composition API, **TanStack Vue Query**, **Pinia**, **vue-i18n** (7 locales)
- **Shadcn-vue** + TailwindCSS 4, **lucide-vue-next** icons, **MapLibre GL** for maps
- **openapi-fetch** typed client in `src/lib/apiClient.ts` → `useApiClient()` + `unwrap()`
- Composables in `src/composables/` wrap API calls with TanStack Query
- Routes: public `/`, portal `/portal/` (16 pages), admin `/admin/` (25 pages)
- Legacy `fetchApi` in `useApi.ts` for 2 endpoints not yet in OpenAPI spec

### OpenAPI pipeline

Spec generation (`backend/cmd/openapi/main.go`) → `just api-types` → `frontend/src/types/api.d.ts`. CI freshness check fails if committed types differ from regenerated output.

## Code Conventions

- **Vue**: Composition API with `<script setup>`, destructured imports, all user-facing strings via `t('key')` from vue-i18n
- **Go**: Standard gofmt, errors returned (not panicked), zerolog for structured logging
- **Responses**: `handlers.JSON(w, status, data)` and `handlers.Error(w, status, msg)`
- **i18n**: When adding/modifying locale keys, update all 7 JSON files. Norwegian (nb) has unicode — use jq or Python for safe JSON editing
- **Migrations**: Sequential numbered files (`000002_feature.up.sql` / `.down.sql`)
- **After schema changes**: `just generate` to update sqlc code in `backend/gen/`
- **After OpenAPI changes**: `just api-types` to regenerate types. Register new endpoints in `backend/internal/openapi/register.go`, add wrapper types in `openapi/types.go`
- **API calls in views**: `const client = useApiClient()` + `unwrap(await client.GET(...))`. Only use `fetchApi` for endpoints not in OpenAPI spec

## CI Pipeline

GitHub Actions on push/PR to main: lint (nix + golangci-lint + eslint), test-go (with coverage profiling), test-vue, api-types (spec freshness), build, nix flake check. Security scans: `govulncheck` and `npm audit` block merges; `gosec` and `trivy` run as `continue-on-error`. Dependabot updates Go modules, npm deps, GitHub Actions, and Docker images weekly/monthly.

## Docker / Deployment

Production stack via `deploy/docker-compose.yml` on Hetzner CAX11 (ARM64):

| Service | Image | Purpose |
|---------|-------|---------|
| api | built from source | Go API + embedded SPA (`/brygge` + `/brygge-seed`) |
| db | postgres:16-alpine | Primary database |
| redis | redis:7-alpine | Cache, sessions, rate limiting |
| traefik | traefik:v2.11 | Reverse proxy, auto TLS (Let's Encrypt) |
| vipps-mock | custom build | Vipps OAuth/payment simulator (demo) |
| migrate | migrate/migrate:v4.18.2 | One-shot migration runner |
| dendrite | matrixdotorg/dendrite-monolith | Matrix homeserver (forum) |
| element | vectorim/element-web | Matrix web client |
| uptime-kuma | louislam/uptime-kuma:1 | Status page |

Multi-stage Dockerfile: build frontend → embed in Go binary → distroless runtime. The Go binary serves the SPA with an `index.html` fallback for unmatched routes. CSP headers allow MapLibre, Kartverket, OSM, and Yr.no. See [docs/deploy.md](docs/deploy.md) for full guide.

## Deployment Operations

```bash
# Deploy (pull, build, migrate, restart)
just deploy

# Manual equivalent on server
ssh brygge 'cd /opt/brygge && git pull --ff-only origin main && docker compose -f deploy/docker-compose.yml build api && docker compose -f deploy/docker-compose.yml run --rm migrate && docker compose -f deploy/docker-compose.yml up -d api'

# Seed demo data in production
docker compose -f deploy/docker-compose.yml run --rm --entrypoint /brygge-seed api

# Force-fix dirty migration state (e.g. stuck at version 1 dirty)
docker compose -f deploy/docker-compose.yml run --rm migrate -path=/migrations -database "$DATABASE_URL" force 1

# Nuke DB and re-seed (destructive)
docker compose -f deploy/docker-compose.yml down -v && docker compose -f deploy/docker-compose.yml up -d db redis && docker compose -f deploy/docker-compose.yml run --rm migrate && docker compose -f deploy/docker-compose.yml run --rm --entrypoint /brygge-seed api
```

Vipps mock test users: `admin@test.com` (admin), `slip@test.com` (slip-member), `wl@test.com` (waitlist-member), `member@test.com` (member).

## Testing Tips

- **Go unit tests**: Use nil db/redis — no containers needed. Handler tests use `setupAuthenticatedRouter()` + `generateTestToken()` for JWT mocking
- **Go integration tests**: Real Postgres/Redis via testcontainers. Run with `-count=1` to bypass cache
- **Vue tests**: `mountWithPlugins()` from `src/test/test-utils.ts`, mock lucide-vue-next icons
- **E2E**: Playwright specs in `e2e/`. Not yet in CI (local only)
- **CI**: `api-types` freshness check catches stale OpenAPI types. Security jobs are `continue-on-error` — failures don't block merge
