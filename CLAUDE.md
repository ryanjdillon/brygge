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

### Running a single test

```bash
# Go - single test function
cd backend && go test ./internal/handlers/ -run TestHandleDataExport -v

# Go - single package
cd backend && go test ./internal/handlers/ -v

# Vue - single test file
cd frontend && npx vitest run src/views/__tests__/PricingView.test.ts

# Vue - watch mode
cd frontend && npx vitest src/views/__tests__/PricingView.test.ts
```

## Architecture

**Monorepo** with a Go API backend and Vue 3 SPA frontend. In production, the Vue dist is embedded into the Go binary via `go:embed` and served by chi.

### Backend (`backend/`)

- **Go 1.25** with **chi/v5** router
- **PostgreSQL 16** via pgx/v5 (no ORM — raw SQL + sqlc code generation)
- **Redis 7** for caching and session storage
- Routes defined in `cmd/api/main.go` — all handler registration lives here
- One handler struct per domain: `handlers.NewBookingsHandler(db, rdb, cfg, log)`
- Auth: JWT tokens via `middleware.Authenticate(jwtService)`, role gates via `middleware.RequireRole("board", "admin")`
- Claims extracted with `middleware.GetClaims(ctx)` returning `*middleware.Claims` (UserID, ClubID, Roles)
- Pagination: `shared.PaginatedResponse` wraps lists as `{ items: [...], limit, offset, has_more }` — frontend must unwrap `.items`
- Feature flags: `cfg.Features.{Bookings, Projects, Calendar, Commerce, Communications}` toggle route groups
- Rate limiting: 3 tiers — strict (5/min IP, auth), standard (30/min IP, public), authed (120/min user)
- Internal packages: `ai/` (Anthropic API), `audit/` (audit trail), `shared/` (pagination, JSON helpers), `testutil/` (DB/Redis test containers)
- Migrations in `backend/migrations/` — single baseline `000001_init` (consolidated)
- sqlc queries in `backend/queries/`, generated code in `backend/gen/` (committed)
- Handler tests use `setupAuthenticatedRouter()` and `setupRoleProtectedRouter()` helpers with `generateTestToken()` for JWT mocking

### Frontend (`frontend/`)

- **Vue 3.5** with Composition API (`<script setup lang="ts">`)
- **TanStack Vue Query** for server state (useQuery/useMutation pattern)
- **Pinia** for client state (auth store)
- **vue-i18n** with 7 locales in `src/locales/` (nb, en, de, fr, it, nl, pl)
- **Shadcn-vue** + Radix-vue + TailwindCSS 4 for UI, **lucide-vue-next** icons, **MapLibre GL** for maps
- **openapi-fetch** typed API client in `src/lib/apiClient.ts` — provides `useApiClient()`, `unwrap()`, and re-exports `ApiError`
- Composables in `src/composables/` wrap API calls with TanStack Query (useBookings, useResources, useEvents, useProjects, useDugnad, useSlipShares, usePricing, useFinancials, useWeather, useMap, useNotifications, useMatrix, useGdpr, useFeatures, useFeatureRequests, useToast)
- Routes in `src/router/index.ts` — public routes at `/`, portal routes under `/portal/` (16 pages), admin routes under `/admin/` (25 pages)
- Portal sidebar nav: `src/views/PortalView.vue`, admin sidebar nav: `src/views/admin/AdminLayout.vue`
- E2E tests: Playwright specs in `e2e/` (directions, accessibility)

### OpenAPI spec & typed client

- **Spec generation**: `backend/cmd/openapi/main.go` dumps the OpenAPI 3.1 JSON spec; `backend/internal/openapi/register.go` registers all operations with huma
- **Type generation**: `just api-types` runs `openapi-typescript` to produce `frontend/src/types/api.d.ts` from `frontend/src/types/openapi.json`
- **Typed client**: `src/lib/apiClient.ts` uses `openapi-fetch` with auth middleware. All composables and most views use `useApiClient()` + `unwrap()` for compile-time type safety on paths, params, bodies, and responses
- **Legacy `fetchApi`**: `src/composables/useApi.ts` still provides `fetchApi()` for 2 views with endpoints not yet in the OpenAPI spec (document comments, waiting-list decline)
- **`ApiError`**: Canonical definition in `src/lib/errors.ts`, re-exported by both `apiClient.ts` and `useApi.ts`
- **CI freshness check**: The `api-types` CI job regenerates the spec and types, failing if they differ from committed versions

### Data flow pattern

Views/composables call `useApiClient()` → `client.GET/POST/PUT/DELETE(path, { params, body })` → `unwrap()` throws `ApiError` on failure → TanStack Query manages caching. Paginated endpoints return `{ items, limit, offset, has_more }`; composables must extract `.items`.

## Code Conventions

- **Vue**: Composition API with `<script setup>`, destructured imports, all user-facing strings via `t('key')` from vue-i18n
- **Go**: Standard gofmt, errors returned (not panicked), zerolog for structured logging
- **Responses**: Backend uses `handlers.JSON(w, status, data)` and `handlers.Error(w, status, msg)` helpers
- **Tests**: Go handler tests mock auth via JWT test tokens (no DB mocking — unit tests use nil db, integration tests use real containers). Vue tests use `mountWithPlugins()` from `src/test/test-utils.ts` and mock lucide-vue-next icons
- **i18n**: When adding/modifying locale keys, update all 7 JSON files. Norwegian (nb) has unicode characters — use jq or Python for safe JSON editing, not raw string replacement
- **Migrations**: Create sequential numbered files (`000002_feature.up.sql` / `.down.sql`)
- **After schema changes**: Run `just generate` to update sqlc-generated code in `backend/gen/`
- **After OpenAPI spec changes**: Run `just api-types` to regenerate `frontend/src/types/api.d.ts`. When adding new endpoints, register them in `backend/internal/openapi/register.go` and add any new response wrapper types to `backend/internal/openapi/types.go`
- **API calls in views**: Use `const client = useApiClient()` + `unwrap(await client.GET(...))` pattern. Only use `fetchApi` from `useApi` for endpoints not in the OpenAPI spec

## CI Pipeline

GitHub Actions runs on push/PR to main: lint (nix + golangci-lint + eslint), test-go (with Postgres/Redis service containers), test-vue, api-types (spec freshness check), build, nix flake check. All must pass.

## Docker / Deployment

- `deploy/docker-compose.yml` + `deploy/docker-compose.dev.yml` for dev overlay
- Services: app (Go binary), db (Postgres 16), redis, traefik, dendrite (Matrix forum integration), element-web, uptime-kuma
- Production: multi-stage Dockerfile builds frontend → embeds in Go binary → distroless runtime
- Target: Hetzner CAX11 (ARM64)
