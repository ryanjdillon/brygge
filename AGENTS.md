<!--
  GENERATED FILE — do not edit.
  Source: <base>/AGENTS.base.md (shared conventions)
  Source: AGENTS.repo.md (this repo)
  Regenerate with: just agents
-->

# Agent conventions

Shared across all my repos. Repo-specific detail lives in the overlay below the
separator. The deeper reasoning behind each rule is in `docs/` — read a doc when
you need the *why* or an edge case, not by default.

## Plan on Linear

Linear is the source of truth for what is being worked on. The repo records how
the code works; Linear records why work is happening and what is left.

- **File, ship, close.** Every non-trivial change has an issue. Move it to
  In Progress when you start and close it when the change ships.
- **Tier the tracking to the size.** A one-line fix needs no issue. A change
  worth reviewing needs an issue. A body of work spanning several changes needs
  a project with a dependency graph.
- **Parents before subs.** Create the parent issue first, then decompose.
- **Close on ship, in the same turn.** An issue left open after its change ships
  makes the whole board untrustworthy. The exception is an explicit "leave it,
  I want to look first".
- **Reference the issue in the commit message** so history and Linear stay linked.
- **Do not invent issues** to look busy, and do not silently work off-issue.

See `docs/planning-on-linear.md`.

## Plan strategically before decomposing

Work descends from an objective, not from whatever is nearest to hand.

1. Start from the **initiative** — the goal in one sentence.
2. Decompose into **projects** with explicit acceptance criteria.
3. Map the **critical path to the objective** and the **path to the target
   state** independently. They are not the same path.
4. Classify every body of work as one of:
   - **strategic-critical** — on the critical path; do it now.
   - **drift-guard** — not on the path, but deferring it causes rework; do it now.
   - **target-state-deferrable** — improves the end state, costs nothing to
     defer; file it and move on.

The rule: no shortsighted shortcuts, and no gold-plating off the critical path.

See `docs/planning-strategically.md`.

## Stay on target

When a fix or a discovery spawns work that is not on the current critical path:
**file it as a Linear issue and continue.** Do not rabbit-hole, and do not
silently expand scope.

- Distinguish must-do-now (on the path, or a drift-guard) from file-for-later.
- Filing is not dropping — Linear is durable, so deferring loses nothing.
- If the new work invalidates the current plan, say so and stop; do not
  improvise a new plan mid-change.

See `docs/staying-on-target.md`.

## Protect the main context

Delegate to a sub-agent whatever produces bulk you do not need to keep: broad
searches, multi-file reads, research spikes, large tool output.

- The sub-agent returns **the conclusion**, not the raw material.
- The main thread keeps the decision and the Linear update, never the dumps.
- State the return contract in the sub-agent's prompt (what shape, what fields).
- Sub-agent context loss is harmless because Linear holds the durable record.

See `docs/subagents.md`.

## Keep modules deep

A small, narrow interface with as much depth behind it as the domain needs —
for source and docs alike. One unit per domain; one topic per doc, standing
alone rather than assembled from fragments across five files.

See `docs/module-organisation.md`.

## Keep docs honest

- **Docs alongside code, in the same commit.** A change that invalidates a doc
  updates that doc in the same commit — never as a follow-up.
- **Write back what you learn.** A non-obvious fact discovered while working
  (an invariant, a gotcha, why an approach failed) goes into the relevant doc.
- Every `docs/` tree has an index; every substantial directory has a README.

See `docs/doc-discipline.md`.

## Code and comments

- Comments explain **why**, not what. No comment narrates a change, references a
  conversation, or notes that something was removed.
- Keep comments general to the code, not to one deployment — describe what the
  code does, not the author's hosts or environment.
- Match the surrounding code's naming, idiom, and comment density.
- Destructure imports where the language supports it
  (`import { foo } from 'bar'`).

## Commits

- Logical, atomic, rebase-able, and succinct. One concern per commit.
- Strip trailing whitespace before staging.
- No references to the tooling or model that produced the change.
- Reference the Linear issue.
- **Never assume git state.** Check `git status` / `git log` before acting —
  do not assume the user has already committed, pushed, or pulled.
- Stage files by name, not by glob.

## Handing off

Work that outlives a session goes in an untracked `<TOPIC>-HANDOFF.md` written
for a reader with no prior context — settled decisions, what is verified (with
SHAs), what remains, and what must not be touched. Never secret values.

See `docs/session-handoff.md`.

## Memory

Record what is durable and not derivable from the repo: user preferences,
guidance you were given and why, project constraints, external references.
Do not record what the code, git history, or this file already says.

See `docs/memory.md`.

---

# Brygge

Harbor club platform: a Go API backend and a Vue 3 SPA frontend in one
monorepo. In production the Vue dist is embedded into the Go binary via
`go:embed` and served by chi.

## Linear

- Workspace `dillonteknisk`, team `brygge`, prefix `BRY-`.
- **Operator/external work** for this repo means DNS changes, Vipps onboarding,
  RBL delisting, and manual production SQL backfills. These stay In Progress
  until confirmed out of band.
- Batch processing recipe (queue format, merge script, when to block instead of
  guess): [docs/developer/batch-loop.md](docs/developer/batch-loop.md).
- Worked example of a mapped project: **Inbox Broadcast & Bulk Send**
  (BRY-162 → BRY-183) — 7 feature parents, 15 task sub-issues, `blocks` edges
  from the data-model root through enqueue/worker to frontend and decommission,
  tests as definition of done on each feature.

## Documentation

All docs are indexed at **[docs/index.md](docs/index.md)**. Each subdirectory has its own README — start at the relevant subdir index, not at the leaves:

- [docs/developer/README.md](docs/developer/README.md) — setup, deploy, configuration, database, enums, invariants, audit actions, change checklists, mail / otel / security subject subdirs
- [docs/user/README.md](docs/user/README.md) — in-app guides (faktura today)
- [docs/developer/checklists/README.md](docs/developer/checklists/README.md) — per-change-type recipes

Source-tree subsystems also carry colocated READMEs that cross-link up:
- `backend/internal/{accounting,handlers,email,middleware,finance}/README.md`
- `frontend/src/views/admin/README.md`

### Drift discipline

The base rule is docs-alongside-code in the same commit. For this repo, that
means specifically:

| When you | Update |
|----------|--------|
| Add an HTTP route | [docs/developer/checklists/add-route.md](docs/developer/checklists/add-route.md) recipe; add `audit-actions.md` row if it logs a new action |
| Add a migration | [docs/developer/checklists/add-migration.md](docs/developer/checklists/add-migration.md); [docs/developer/database.md](docs/developer/database.md); [docs/developer/reference/enums.md](docs/developer/reference/enums.md) if a new enum / TEXT vocabulary value |
| Add a bulk action | [docs/developer/checklists/add-bulk-action.md](docs/developer/checklists/add-bulk-action.md) |
| Add a feature flag | [docs/developer/checklists/add-feature-flag.md](docs/developer/checklists/add-feature-flag.md); [docs/developer/configuration.md](docs/developer/configuration.md) |
| Add an `audit.Action*` constant | [docs/developer/checklists/add-audit-action.md](docs/developer/checklists/add-audit-action.md); [docs/developer/reference/audit-actions.md](docs/developer/reference/audit-actions.md) row |
| Touch an invariant (auth ordering, accounting cascade, retention rules, etc.) | [docs/developer/reference/invariants.md](docs/developer/reference/invariants.md) |
| Change the Vipps reconciliation cascade or KID matching | [backend/internal/accounting/README.md](backend/internal/accounting/README.md) |
| Change handler conventions (response shape, audit pattern, gating) | [backend/internal/handlers/README.md](backend/internal/handlers/README.md) |
| Add an admin nav item / new TOTP-gated path | [frontend/src/views/admin/README.md](frontend/src/views/admin/README.md) |

### Where to write back what you learn

- A rule other code must obey → [docs/developer/reference/invariants.md](docs/developer/reference/invariants.md)
- A subsystem-specific footgun → the "Common misses" section of that subsystem's README
- A change-type-specific trap → the matching checklist's "Common misses"
- A non-blocking issue parked for later → the "Known issues" section below

Call it out in the commit message so a reviewer can verify the capture landed in
the right place.

### Docs categorisation

Docs split by audience — `docs/user/` (in-app site admins, board, members) and
`docs/developer/` (deploy, contribute, low-level troubleshoot, and subject
subdirs like `mail/`, `otel/`, `security/`). Only top-level/cross-audience
material (`architecture.md`, `tech-stack.md`) lives at `docs/`.

## Migrations — applied automatically on deploy

Migrations run automatically as part of every deploy — `brygge-migrate.service` fires before the API starts, so no manual migration step is needed after merging. Do not tell the user to run migrations manually.

`brygge-migrate.service` runs as the `brygge` DB role. golang-migrate (lib/pq `postgres://` driver) sends each file in **one** exec, so a file without its own `BEGIN`/`COMMIT` runs in one implicit transaction: if statement N fails, statements 1..N-1 roll back too and the version is left `dirty` (verified BRY-395). Files apply in separate transactions, and a file that carries its own `BEGIN;`/`COMMIT;` (000001, 000006) commits in pieces — so keep DDL guarded with `IF NOT EXISTS` / `pg_constraint` checks for retries.

**Before every deploy** — use `just deploy <host>`. Gate order: `just migrate-check` → pre-deploy snapshot → pending migrations dry-run on prod (rolled back) → deploy-rs:

```bash
just deploy klokkarvikbaatlag.no    # runs migrate-check first; then nix run .#deploy
```

The deploy target is always `klokkarvikbaatlag.no`. Never ask the user which host to deploy to.

Run `just migrate-check` standalone when you just want to validate a migration in isolation.

Two gates, cheapest first:

- **`just migrate-check`** — applies every migration to an *empty* DB. Fast, catches ~80% of breakage (syntax, ordering, non-idempotent DDL). This is the default `just deploy` gate.
- **Prod dry-run (automatic, in the deploy app)** — `scripts/deploy-migration-dryrun.sh` streams the *pending* files + `dev_masked.sql` to prod's psql inside `BEGIN … ROLLBACK` (lock_timeout 2s, statement_timeout 15s). Catches data-shape failures on populated tables (TEXT/UUID cast, DIL-363; `NOT NULL` / `CHECK` on existing rows). Exit 75 = prod busy (lock timeout), retry. Exit 2 with "PROD MAY HAVE BEEN MODIFIED" = a file ended the transaction: restore the pre-deploy snapshot first. Files the statement-level scan flags (transaction control, timeout SET/RESET, psql meta-commands), and SQLSTATE 55P04/25001, fall back to the snapshot check against the dump the deploy just pulled. `SKIP_PROD_CHECK=1` skips it (loudly), `PROD_CHECK=snapshot` forces the fallback. Tests: `just test-deploy-dryrun`.
- **`just migrate-check-prod`** — the snapshot fallback, also runnable by hand: restores a prod dump (S3 or a local `*.dump`) into the local compose DB, applies pending migrations, drops it. Needs `just up`. The snapshot is shredded on exit (carries member PII).

The recipe spins up a fresh `brygge_migrate_check` DB, applies every migration in `backend/migrations/` end-to-end as the `brygge` role, then drops it. Failures here are deploy-blocking — fix the migration before shipping. Catches:

- Enum casts (PG rejects implicit `text → enum` in INSERT/SELECT)
- Privilege mismatches (e.g. `CREATEROLE` not held by `brygge`)
- Missing `IF NOT EXISTS` guards on `CREATE INDEX` / `ADD COLUMN`
- Schema drift between the migrations and the seed.

When writing a new migration, always:

- Use `CREATE INDEX IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, and wrap `ADD CONSTRAINT` in a `DO` block that queries `pg_constraint`.
- Cast enum literals explicitly (`'asset'::account_type`, `'completed'::payment_status`).
- Run as the `brygge` role's actual privilege level — anything needing `CREATEROLE` or superuser goes in a NixOS systemd-oneshot bootstrap (see `brygge-dev-query-role.service` for the pattern).

## Dev Environment

Go, just, golangci-lint, and other tools are provided by the Nix flake. They are **not** in PATH by default. Use `nix develop` to enter the shell, or wrap commands:

```bash
nix develop /home/ryan/code/personal/brygge --command bash -c "cd backend && go test ./..."
```

Node (v22) is available system-wide. Frontend commands (`npm`, `npx`) work directly from `frontend/`.

### Troubleshooting production data

To inspect production data (e.g. a Vipps payment that didn't reconcile to its
faktura), use `just prod-query "<read-only SQL>"`. It runs as the restricted,
read-only `brygge_dev_ro` role: writes are rejected and member identity columns
(names, emails, addresses) come back **NULL by design** — correlate on
`user_id` / `kid_number` / `vipps_reference` instead. The deny / allow / mask
table matrix is [docs/developer/security/dev-query.md](docs/developer/security/dev-query.md).
Prefer this wrapper over raw `ssh`/`psql` (a permission allowlist should cover
only `just prod-query`), and add your own `LIMIT` — rows are not capped.
Without a usable SSH key (e.g. an agent session), use `just prod-query-http
"<sql>"` instead — same role and masking, authenticated by a short-lived
developer token from `$BRYGGE_DEV_TOKEN` or `~/.config/brygge/dev-token`.

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

Multi-stage Dockerfile: build frontend → embed in Go binary → distroless runtime. The Go binary serves the SPA with an `index.html` fallback for unmatched routes. CSP headers allow MapLibre, Kartverket, OSM, and Yr.no. See [docs/developer/deploy.md](docs/developer/deploy.md) for full guide.

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

# Direct psql on a NixOS production host (peer auth — must run as the brygge OS service user)
# DATABASE_URL uses a Unix socket; connecting as root → "Peer authentication failed for user root"
# 'brygge' is both the database name and the OS service user on any NixOS deployment
sudo -u brygge psql "postgres:///brygge?host=/run/postgresql&sslmode=disable" -c "<SQL>"

# Nuke DB and re-seed (destructive)
docker compose -f deploy/docker-compose.yml down -v && docker compose -f deploy/docker-compose.yml up -d db redis && docker compose -f deploy/docker-compose.yml run --rm migrate && docker compose -f deploy/docker-compose.yml run --rm --entrypoint /brygge-seed api
```

Vipps mock test users: `admin@test.com` (admin), `slip@test.com` (slip-member), `wl@test.com` (waitlist-member), `member@test.com` (member).

## Known issues (non-blocking)

Real issues that have been encountered in passing but are deliberately out of scope for whatever feature work is in flight. Don't re-discover them; fix them when they become the work, then remove the entry.


## Testing Tips

- **Go unit tests**: Use nil db/redis — no containers needed. Handler tests use `setupAuthenticatedRouter()` + `generateTestToken()` for JWT mocking
- **Go integration tests**: Real Postgres/Redis via testcontainers. Run with `-count=1` to bypass cache
- **Vue tests**: `mountWithPlugins()` from `src/test/test-utils.ts`, mock lucide-vue-next icons
- **E2E**: Playwright specs in `e2e/`. Not yet in CI (local only)
- **CI**: `api-types` freshness check catches stale OpenAPI types. Security jobs are `continue-on-error` — failures don't block merge
