# AGENTS.md

This file provides guidance to AI coding agents (Claude Code, Cursor, etc.) when working with code in this repository.

## Documentation map

Docs are organized by topic so you can read just the section you need without loading everything:

| Topic | Entry point |
|-------|-------------|
| Architecture overview | [docs/index.md](docs/index.md) |
| Local setup | [docs/setup.md](docs/setup.md) |
| Contributing workflow | [CONTRIBUTING.md](CONTRIBUTING.md) |
| Configuration / env vars | [docs/configuration.md](docs/configuration.md) |
| Production deploy | [docs/deploy.md](docs/deploy.md) |
| Troubleshooting | [docs/troubleshooting.md](docs/troubleshooting.md) |
| Mail (Stalwart + Bulwark) | [docs/mail/setup.md](docs/mail/setup.md) |
| OpenTelemetry | [docs/otel/index.md](docs/otel/index.md) — instrumentation, app config, local collector, upstream contract, standalone collector |
| 2FA (TOTP) | [docs/security/2fa.md](docs/security/2fa.md) — enrollment, recovery codes, admin reset, all-admins-lost recovery |
| Kubernetes notes | [docs/k8s.md](docs/k8s.md) |

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

For single-test examples and full dev workflow, see [CONTRIBUTING.md](CONTRIBUTING.md).

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
- **TOTP gating (frontend UX)**: action buttons that trigger sensitive operations (anything behind `RequireFreshTOTP` middleware on the server) must call `ensureFreshTotp()` at **click time**, not on form submit. Pattern: prompt the step-up first, then show the confirm dialog (or open the form modal), then run the action. The backend middleware is the hard gate; the frontend prompt is just so users re-verify before they fill in a form, not after

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

## Secrets in `terraform/terraform.tfvars.json`

Tracked as a placeholder; deployers fill it in locally. The file will show as **modified** in `git status` — that's expected. Nix flakes read it directly from the working copy, so techniques that hide local edits from git (`skip-worktree`, `assume-unchanged`) also hide them from nix and silently break deploys with stale placeholder values.

Protection against accidentally committing real secrets is the **pre-commit hook** at `.githooks/pre-commit` (wired via `core.hooksPath = .githooks`, installed automatically the first time you enter `nix develop`). It rejects staged versions of `terraform/terraform.tfvars.json` with a non-empty `hcloud_token` or any `domain` not ending in `example.invalid`. If you accidentally `git add` the file, the hook tells you exactly how to recover.

To update the committed **placeholder** (rare — adding a new field, etc.):
```bash
git stash push -- terraform/terraform.tfvars.json   # set aside your secrets
# edit the file: add only placeholder/empty values
git add terraform/terraform.tfvars.json
git commit
git stash pop                                       # restore your secrets
```

## Env var layering

Each runtime variable Brygge reads lives in **exactly one** of three layers. Keeping them separated avoids the silent-fallback failure mode (env file forgets a non-secret → site quietly defaults) and the secret-in-/nix/store antipattern.

| Layer | What goes here | Examples | Why |
|-------|----------------|----------|-----|
| `clubConfig` (`flake.nix`, sourced from `terraform/terraform.tfvars.json`) | Per-club identity and config known at deploy time, not secret | `domain`, `slug`, `name`, `hostname`, `timezone`, `adminEmail`, `adminSshKeys` | Declarative per club; tfvars-driven; survives redeploy |
| `services.brygge.{environment, extraEnvironment}` (NixOS, in `nix/host.nix`) | Connection wiring + values derived from `clubConfig` | `DATABASE_URL`, `REDIS_URL`, `FRONTEND_URL`, `OTEL_*`, `FEATURE_*`, `DOMAIN` | Single source per host; in source control; survives redeploy |
| `/etc/brygge/env` (root-owned 0400, outside `/nix/store`) | Secrets only | `SMTP_PASSWORD`, `TOTP_ENCRYPTION_KEY`, `S3_SECRET_KEY`, `VAPID_PRIVATE_KEY`, `VIPPS_WEBHOOK_SECRET`, `ANTHROPIC_API_KEY`, `DENDRITE_SERVICE_TOKEN` | World-readable `/nix/store` is the wrong place; rotates independently of the nix config |

**Anti-rules:**

- A var must NOT appear in two layers — drift risk; precedence falls out of systemd unit ordering and isn't intentional.
- The env file must NOT carry non-secret config — when forgotten, the site silently defaults instead of failing loudly. (Symptom: NavBar shows literal "Brygge", magic-link emails say "your club".)
- The systemd unit must NOT carry secrets — anything in `services.X.environment` ends up in `/nix/store`, which is world-readable.

**docker-compose deploys** are a separate path. `deploy/.env` (copied from `.env.example`) carries all three layers' worth of values for that path — there's no nix to inject. The `[nix-injected]` tags in `.env.example` mark which lines exist only for compose deploys.

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
